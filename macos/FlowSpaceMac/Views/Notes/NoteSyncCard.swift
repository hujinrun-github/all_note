import SwiftUI

struct NoteSyncCard: View {
    private struct PendingChange: Identifiable {
        enum Action { case unbind; case change(SyncTarget) }
        let id = UUID()
        let action: Action
    }

    let noteID: String
    let store: WorkspaceStore
    @State private var pendingChange: PendingChange?
    @State private var notice: String?
    @State private var noticeIsError = false

    private var response: NoteSyncBindingResponse? { store.noteSyncBindings[noteID] }
    private var binding: NoteSyncBinding? { response?.binding }
    private var currentTarget: SyncTarget? {
        response?.target ?? store.syncTargets.first { $0.id == binding?.targetID }
    }
    private var availableTargets: [SyncTarget] {
        var byID = Dictionary(uniqueKeysWithValues: store.syncTargets.filter(\.enabled).map { ($0.id, $0) })
        response?.candidates?.forEach { candidate in
            if candidate.target.enabled { byID[candidate.target.id] = candidate.target }
        }
        if let currentTarget { byID[currentTarget.id] = currentTarget }
        return byID.values.sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
    }

    private var selection: Binding<String> {
        Binding(
            get: { binding?.targetID ?? "__none__" },
            set: { selectTarget($0) }
        )
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 11) {
            HStack {
                Label("笔记同步", systemImage: "arrow.triangle.2.circlepath")
                    .font(.headline)
                Spacer()
                Text(statusLabel)
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(statusColor)
            }

            Picker("同步目标", selection: selection) {
                Text("不同步").tag("__none__")
                ForEach(availableTargets) { target in
                    Text("\(target.name)（\(target.type.title)）").tag(target.id)
                }
            }
            .disabled(store.isMutating)

            if let currentTarget {
                Text("当前目标：\(currentTarget.name)（\(currentTarget.type.title)）")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                Text("默认不同步；选择目标并保存绑定后才能执行同步。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            if let state = response?.state {
                if !state.externalPath.isEmpty {
                    Text(state.externalPath)
                        .font(.caption.monospaced())
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                        .lineLimit(2)
                }
                if let url = URL(string: state.externalURL), !state.externalURL.isEmpty {
                    Link("打开外部文稿", destination: url)
                        .font(.caption)
                }
                if let error = state.errorMessage, !error.isEmpty {
                    Text(error).font(.caption).foregroundStyle(.red)
                }
            }

            if let compatibilityMessage {
                Text(compatibilityMessage).font(.caption).foregroundStyle(.orange)
            }
            if let notice {
                Text(notice)
                    .font(.caption)
                    .foregroundStyle(noticeIsError ? .red : .green)
            }

            Button(store.isMutating ? "同步中…" : "同步此笔记", systemImage: "arrow.triangle.2.circlepath") {
                Task { await syncNow() }
            }
            .disabled(binding == nil || store.isMutating)
        }
        .padding(16)
        .task(id: noteID) { await store.loadNoteSync(noteID: noteID) }
        .confirmationDialog(
            confirmationTitle,
            isPresented: Binding(
                get: { pendingChange != nil },
                set: { if !$0 { pendingChange = nil } }
            )
        ) {
            Button(confirmationButtonTitle, role: confirmationRole) {
                guard let pendingChange else { return }
                Task { await confirm(pendingChange) }
            }
        } message: {
            Text(confirmationMessage)
        }
    }

    private var statusLabel: String {
        guard binding != nil else { return "未同步" }
        switch response?.state?.status {
        case .synced: return "已同步"
        case .pending: return "待同步"
        case .failed: return "同步失败"
        case .externalDeleted: return "外部已删除"
        case nil: return "已绑定"
        }
    }

    private var statusColor: Color {
        switch response?.state?.status {
        case .synced: .green
        case .failed, .externalDeleted: .orange
        case .pending: .blue
        case nil: .secondary
        }
    }

    private var compatibilityMessage: String? {
        guard let response else { return nil }
        if response.bindingMismatch == true {
            return response.boundTargetName.map { "这篇笔记已经绑定到 \($0)，请刷新后再试" } ?? "同步绑定已变化，请刷新后再试"
        }
        if response.defaultTargetMissing == true { return "还没有可用的默认同步目标" }
        if response.bindingRequired == true { return "这篇笔记当前设置为不同步，需要重新选择同步目标" }
        return nil
    }

    private func selectTarget(_ targetID: String) {
        notice = nil
        if targetID == "__none__" {
            if binding != nil { pendingChange = PendingChange(action: .unbind) }
            return
        }
        guard let next = availableTargets.first(where: { $0.id == targetID }) else { return }
        if let binding, binding.targetID != targetID {
            pendingChange = PendingChange(action: .change(next))
        } else if binding == nil {
            Task { await bind(to: next, confirmChange: false) }
        }
    }

    private func bind(to target: SyncTarget, confirmChange: Bool) async {
        do {
            try await store.bindNote(noteID: noteID, targetID: target.id, confirmChange: confirmChange)
            noticeIsError = false
            notice = "同步目标已更新；现在可以同步此笔记"
        } catch {
            noticeIsError = true
            notice = syncErrorMessage(error)
        }
    }

    private func confirm(_ change: PendingChange) async {
        pendingChange = nil
        switch change.action {
        case .unbind:
            do {
                try await store.unbindNote(noteID: noteID)
                noticeIsError = false
                notice = "已设为不同步；外部文稿未被删除"
            } catch {
                noticeIsError = true
                notice = syncErrorMessage(error)
            }
        case .change(let target):
            await bind(to: target, confirmChange: true)
        }
    }

    private func syncNow() async {
        notice = nil
        do {
            let result = try await store.syncNote(noteID: noteID)
            noticeIsError = result.status == "failed"
            notice = result.errorMessage ?? (noticeIsError ? "同步失败" : "同步完成")
        } catch {
            noticeIsError = true
            notice = syncErrorMessage(error)
        }
    }

    private func syncErrorMessage(_ error: Error) -> String {
        guard let apiError = error as? APIError else { return error.localizedDescription }
        return switch apiError.code {
        case "sync_binding_conflict": "同步绑定已被其他窗口修改，请刷新后重试"
        case "target_change_requires_confirm": "更换同步目标需要再次确认"
        case "binding_required": "请先选择同步目标"
        default: apiError.message
        }
    }

    private var confirmationTitle: String {
        guard let pendingChange else { return "确认同步变更" }
        return switch pendingChange.action {
        case .unbind: "停止同步这篇笔记？"
        case .change(let target): "改为同步到 \(target.name)？"
        }
    }
    private var confirmationButtonTitle: String {
        guard let pendingChange else { return "确认" }
        return switch pendingChange.action {
        case .unbind: "停止同步"
        case .change: "更换目标"
        }
    }
    private var confirmationRole: ButtonRole? {
        guard let pendingChange else { return nil }
        if case .unbind = pendingChange.action { return .destructive }
        return nil
    }
    private var confirmationMessage: String {
        guard let pendingChange else { return "" }
        return switch pendingChange.action {
        case .unbind:
            "解除绑定只停止后续同步，不会删除 Notion 页面或 Obsidian 文稿。"
        case .change:
            "现有外部文稿会保留；下次同步将使用新的目标。"
        }
    }
}
