import SwiftUI

struct SyncSettingsView: View {
    let store: WorkspaceStore
    @State private var draft: SyncTargetDraft?
    @State private var deletingTarget: SyncTarget?
    @State private var deletionTarget: SyncTarget?
    @State private var notice: SyncNotice?

    private var groupedTargets: [(SyncTargetType, [SyncTarget])] {
        SyncTargetType.allCases.map { type in
            (type, store.syncTargets.filter { $0.type == type })
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                VStack(alignment: .leading, spacing: 3) {
                    Text("同步目标").font(.title2.weight(.semibold))
                    Text("管理 Notion 与 Obsidian 目标；单篇笔记需在编辑器检查器中绑定后才会同步。")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Menu("添加同步目标", systemImage: "plus") {
                    Button("Notion") { draft = SyncTargetDraft(type: .notion) }
                    Button("Obsidian") { draft = SyncTargetDraft(type: .obsidian) }
                }
                .buttonStyle(.borderedProminent)
            }
            .padding(18)

            Divider()

            if store.isLoadingSync && store.syncTargets.isEmpty {
                ProgressView("正在读取同步目标…")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollView {
                    VStack(alignment: .leading, spacing: 18) {
                        if let notice {
                            Label(notice.message, systemImage: notice.isError ? "exclamationmark.triangle.fill" : "checkmark.circle.fill")
                                .foregroundStyle(notice.isError ? .red : .green)
                                .padding(10)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .background((notice.isError ? Color.red : Color.green).opacity(0.07), in: .rect(cornerRadius: 9))
                        }
                        ForEach(groupedTargets, id: \.0) { type, targets in
                            GroupBox {
                                if targets.isEmpty {
                                    ContentUnavailableView(
                                        "还没有 \(type.title) 目标",
                                        systemImage: type.systemImage,
                                        description: Text("创建目标后，可在笔记检查器中选择它。")
                                    )
                                    .frame(maxWidth: .infinity, minHeight: 110)
                                } else {
                                    VStack(spacing: 0) {
                                        ForEach(Array(targets.enumerated()), id: \.element.id) { index, target in
                                            if index > 0 { Divider() }
                                            SyncTargetRow(
                                                target: target,
                                                isMutating: store.mutationsDisabled,
                                                edit: { draft = SyncTargetDraft(target: target) },
                                                push: { await push(target) },
                                                pull: { await pull(target) },
                                                reviewDeletions: { deletionTarget = target },
                                                delete: { deletingTarget = target }
                                            )
                                            .padding(.vertical, 9)
                                        }
                                    }
                                }
                            } label: {
                                Label(type.title, systemImage: type.systemImage)
                            }
                        }
                    }
                    .padding(18)
                }
            }
        }
        .task { await store.loadSyncTargets() }
        .sheet(item: $draft) { draft in
            SyncTargetEditorSheet(
                draft: draft,
                store: store,
                save: { edited in await save(edited) }
            )
        }
        .sheet(item: $deletionTarget) { target in
            SyncDeletionReviewSheet(target: target, store: store)
        }
        .confirmationDialog(
            "删除“\(deletingTarget?.name ?? "")”？",
            isPresented: Binding(
                get: { deletingTarget != nil },
                set: { if !$0 { deletingTarget = nil } }
            )
        ) {
            Button("删除同步目标", role: .destructive) {
                guard let target = deletingTarget else { return }
                Task { await delete(target) }
            }
        } message: {
            Text("已经使用的目标可能无法删除。删除目标不会删除外部文稿。")
        }
    }

    private func save(_ draft: SyncTargetDraft) async -> Bool {
        do {
            _ = try await store.saveSyncTarget(draft)
            self.draft = nil
            notice = SyncNotice(message: draft.targetID == nil ? "同步目标已创建" : "同步设置已保存")
            return true
        } catch {
            notice = SyncNotice(message: error.localizedDescription, isError: true)
            return false
        }
    }

    private func push(_ target: SyncTarget) async {
        do {
            let result = try await store.pushSyncTarget(target)
            notice = SyncNotice(message: "已推送到 \(target.name)：成功 \(result.synced)，失败 \(result.failed)")
        } catch {
            notice = SyncNotice(message: error.localizedDescription, isError: true)
        }
    }

    private func pull(_ target: SyncTarget) async {
        do {
            let result = try await store.pullSyncTarget(target)
            let pending = result.externalDeleted > 0 ? "，待确认外部删除 \(result.externalDeleted)" : ""
            notice = SyncNotice(
                message: "已从 \(target.name) 拉取：更新 \(result.pulled)，导入 \(result.imported)，失败 \(result.failed)\(pending)"
            )
            if result.externalDeleted > 0 { deletionTarget = target }
        } catch {
            notice = SyncNotice(message: error.localizedDescription, isError: true)
        }
    }

    private func delete(_ target: SyncTarget) async {
        do {
            try await store.deleteSyncTarget(target)
            deletingTarget = nil
            notice = SyncNotice(message: "同步目标已删除；外部文稿未被删除")
        } catch {
            notice = SyncNotice(message: error.localizedDescription, isError: true)
        }
    }
}

private struct SyncTargetRow: View {
    let target: SyncTarget
    let isMutating: Bool
    let edit: () -> Void
    let push: () async -> Void
    let pull: () async -> Void
    let reviewDeletions: () -> Void
    let delete: () -> Void

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: target.type.systemImage)
                .font(.title2)
                .foregroundStyle(.tint)
                .frame(width: 32)
            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text(target.name).fontWeight(.semibold)
                    if target.isDefault == true {
                        Text("默认").font(.caption2).padding(.horizontal, 6).padding(.vertical, 2).background(.tint.opacity(0.12), in: .capsule)
                    }
                    if target.autoSync {
                        Label("自动", systemImage: "arrow.triangle.2.circlepath").font(.caption2).foregroundStyle(.secondary)
                    }
                }
                Text(detailText).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer()
            Button("FlowSpace → \(target.type.title)") { Task { await push() } }
                .disabled(isMutating)
            Button("\(target.type.title) → FlowSpace") { Task { await pull() } }
                .disabled(isMutating)
            Menu("更多", systemImage: "ellipsis") {
                Button("处理外部删除", systemImage: "trash.slash", action: reviewDeletions)
                Button("编辑", action: edit)
                Divider()
                Button("删除", role: .destructive, action: delete)
            }
            .menuStyle(.borderlessButton)
            .fixedSize()
            .disabled(isMutating)
        }
    }

    private var detailText: String {
        if target.type == .obsidian {
            return "\(target.vaultPath) / \(target.baseFolder)"
        }
        return target.parsedConfig["data_source_id"] as? String ?? "Notion Data Source"
    }
}

private struct SyncDeletionReviewSheet: View {
    @Environment(\.dismiss) private var dismiss
    let target: SyncTarget
    let store: WorkspaceStore
    @State private var items: [ExternalDeletedNote] = []
    @State private var isLoading = true
    @State private var errorMessage: String?
    @State private var confirmingItem: ExternalDeletedNote?

    var body: some View {
        NavigationStack {
            Group {
                if isLoading && items.isEmpty {
                    ProgressView("正在读取外部删除记录…")
                } else if items.isEmpty {
                    ContentUnavailableView(
                        "没有待处理的外部删除",
                        systemImage: "checkmark.circle",
                        description: Text("\(target.name) 的本地笔记与外部文稿已对齐。")
                    )
                } else {
                    List(items) { item in
                        VStack(alignment: .leading, spacing: 8) {
                            HStack(alignment: .top) {
                                VStack(alignment: .leading, spacing: 3) {
                                    Text(item.title.isEmpty ? "未命名笔记" : item.title)
                                        .font(.headline)
                                    Text(item.externalPath)
                                        .font(.caption.monospaced())
                                        .foregroundStyle(.secondary)
                                        .textSelection(.enabled)
                                    if let timestamp = item.lastSyncedAt {
                                        Text("上次同步：\(Date(timeIntervalSince1970: TimeInterval(timestamp)).formatted(date: .abbreviated, time: .shortened))")
                                            .font(.caption)
                                            .foregroundStyle(.secondary)
                                    }
                                }
                                Spacer()
                                Button("恢复外部文稿", systemImage: "arrow.uturn.backward") {
                                    Task { await restore(item) }
                                }
                                .disabled(store.mutationsDisabled)
                                Button("确认删除本地笔记", systemImage: "trash", role: .destructive) {
                                    confirmingItem = item
                                }
                                .disabled(store.mutationsDisabled)
                            }
                        }
                        .padding(.vertical, 6)
                    }
                }
            }
            .navigationTitle("处理外部删除 · \(target.name)")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("关闭") { dismiss() }
                }
                ToolbarItem {
                    Button("刷新", systemImage: "arrow.clockwise") { Task { await load() } }
                        .disabled(isLoading)
                }
            }
        }
        .frame(width: 760, height: 520)
        .task(id: target.id) { await load() }
        .confirmationDialog(
            "确认删除 FlowSpace 中的这篇笔记？",
            isPresented: Binding(
                get: { confirmingItem != nil },
                set: { if !$0 { confirmingItem = nil } }
            ),
            titleVisibility: .visible
        ) {
            Button("删除本地笔记", role: .destructive) {
                guard let item = confirmingItem else { return }
                Task { await confirm(item) }
            }
            Button("返回", role: .cancel) {}
        } message: {
            Text("外部文稿已经不存在。确认后会删除 FlowSpace 中对应的笔记，此操作无法撤销。")
        }
        .overlay(alignment: .bottom) {
            if let errorMessage {
                ErrorBanner(message: errorMessage) { self.errorMessage = nil }.padding()
            }
        }
    }

    private func load() async {
        isLoading = true
        errorMessage = nil
        defer { isLoading = false }
        do {
            items = try await store.externalDeletedNotes(for: target)
        } catch is CancellationError {
            return
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func restore(_ item: ExternalDeletedNote) async {
        do {
            _ = try await store.restoreExternalDeletion(item, target: target)
            items.removeAll { $0.noteID == item.noteID }
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func confirm(_ item: ExternalDeletedNote) async {
        do {
            try await store.confirmExternalDeletion(item, target: target)
            confirmingItem = nil
            items.removeAll { $0.noteID == item.noteID }
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}

private struct SyncTargetEditorSheet: View {
    @Environment(\.dismiss) private var dismiss
    @State private var draft: SyncTargetDraft
    @State private var localNotice: SyncNotice?
    let store: WorkspaceStore
    let save: (SyncTargetDraft) async -> Bool

    init(draft: SyncTargetDraft, store: WorkspaceStore, save: @escaping (SyncTargetDraft) async -> Bool) {
        _draft = State(initialValue: draft)
        self.store = store
        self.save = save
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack {
                Label(draft.targetID == nil ? "添加 \(draft.type.title) 目标" : "编辑 \(draft.type.title) 目标", systemImage: draft.type.systemImage)
                    .font(.title2.weight(.semibold))
                Spacer()
                if draft.targetID == nil {
                    Picker("类型", selection: $draft.type) {
                        ForEach(SyncTargetType.allCases) { type in Text(type.title).tag(type) }
                    }
                    .frame(width: 150)
                }
            }

            Form {
                TextField("目标名称", text: $draft.name)
                if draft.type == .obsidian {
                    TextField("服务端 Vault 路径", text: $draft.vaultPath)
                    TextField("同步目录", text: $draft.baseFolder)
                    Text("路径必须由 FlowSpace 服务端访问；远程连接时不要选择本机路径。")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                } else {
                    TextField("数据库链接或 Data Source ID", text: $draft.dataSourceID)
                    SecureField(draft.tokenConfigured ? "Notion Token（已设置，留空保持）" : "Notion Token", text: $draft.token)
                    TextField("标题属性", text: $draft.titleProperty)
                }
                TextField("同步标签（逗号分隔）", text: $draft.tags)
                Toggle("自动同步", isOn: $draft.autoSync)
                Toggle("设为默认目标", isOn: $draft.isDefault)
            }
            .formStyle(.grouped)

            if let localNotice {
                Label(localNotice.message, systemImage: localNotice.isError ? "exclamationmark.triangle.fill" : "checkmark.circle.fill")
                    .font(.callout)
                    .foregroundStyle(localNotice.isError ? .red : .green)
            }

            HStack {
                Button("测试连接") {
                    Task {
                        do {
                            try await store.testSyncTarget(draft)
                            localNotice = SyncNotice(message: "连接测试成功")
                        } catch {
                            localNotice = SyncNotice(message: error.localizedDescription, isError: true)
                        }
                    }
                }
                .disabled(store.mutationsDisabled)
                Spacer()
                Button("取消") { dismiss() }.disabled(store.isMutating)
                Button("保存") {
                    Task { if await save(draft) { dismiss() } }
                }
                .buttonStyle(.borderedProminent)
                .disabled(store.mutationsDisabled)
            }
        }
        .padding(22)
        .frame(width: 570, height: draft.type == .notion ? 580 : 540)
        .interactiveDismissDisabled(store.isMutating)
    }
}

private struct SyncNotice: Equatable {
    let message: String
    var isError = false
}
