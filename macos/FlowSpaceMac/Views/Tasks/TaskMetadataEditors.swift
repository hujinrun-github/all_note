import SwiftUI

struct TaskCompletionRequirementsEditor: View {
    @Binding var requirements: [TaskCompletionRequirement]
    @State private var draft: RequirementDraft?
    @State private var errorMessage: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if requirements.isEmpty {
                Text("未设置必选项，可直接完成任务。")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            } else {
                ForEach(requirements) { requirement in
                    HStack(spacing: 9) {
                        Image(systemName: requirement.kind.systemImage)
                            .foregroundStyle(.secondary)
                            .frame(width: 18)
                        VStack(alignment: .leading, spacing: 2) {
                            Text(requirement.title)
                            Text(requirement.kind.title)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        Button("编辑", systemImage: "pencil") {
                            draft = RequirementDraft(requirement: requirement)
                        }
                        .labelStyle(.iconOnly)
                        Button("删除", systemImage: "trash", role: .destructive) {
                            requirements.removeAll { $0.id == requirement.id }
                        }
                        .labelStyle(.iconOnly)
                    }
                }
            }

            Button(requirements.count >= 20 ? "最多添加 20 项" : "添加必选项", systemImage: "plus") {
                draft = RequirementDraft()
            }
            .disabled(requirements.count >= 20)

            if let errorMessage {
                Label(errorMessage, systemImage: "exclamationmark.triangle")
                    .font(.caption)
                    .foregroundStyle(.red)
            }
        }
        .sheet(item: $draft) { draft in
            RequirementEditorSheet(draft: draft) { requirement in
                save(requirement)
            }
        }
    }

    private func save(_ requirement: TaskCompletionRequirement) -> Bool {
        errorMessage = nil
        if let index = requirements.firstIndex(where: { $0.id == requirement.id }) {
            requirements[index] = requirement
        } else {
            guard requirements.count < 20 else {
                errorMessage = "完成条件最多添加 20 项"
                return false
            }
            requirements.append(requirement)
        }
        draft = nil
        return true
    }
}

struct TaskAttachmentLinksEditor: View {
    @Binding var links: [TaskAttachmentLink]
    @State private var draft: AttachmentLinkDraft?
    @State private var errorMessage: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if links.isEmpty {
                Text("还没有附件链接。")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            } else {
                ForEach(links) { link in
                    HStack(spacing: 9) {
                        Image(systemName: "link")
                            .foregroundStyle(.secondary)
                        VStack(alignment: .leading, spacing: 2) {
                            Text(link.name)
                            Text(link.url)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                        }
                        Spacer()
                        Button("编辑", systemImage: "pencil") {
                            draft = AttachmentLinkDraft(link: link)
                        }
                        .labelStyle(.iconOnly)
                        Button("删除", systemImage: "trash", role: .destructive) {
                            links.removeAll { $0.url == link.url }
                        }
                        .labelStyle(.iconOnly)
                    }
                }
            }

            Button(links.count >= 20 ? "最多添加 20 条" : "添加附件链接", systemImage: "plus") {
                draft = AttachmentLinkDraft()
            }
            .disabled(links.count >= 20)

            if let errorMessage {
                Label(errorMessage, systemImage: "exclamationmark.triangle")
                    .font(.caption)
                    .foregroundStyle(.red)
            }
        }
        .sheet(item: $draft) { draft in
            AttachmentLinkEditorSheet(draft: draft) { originalURL, link in
                save(originalURL: originalURL, link: link)
            }
        }
    }

    private func save(originalURL: String?, link: TaskAttachmentLink) -> Bool {
        errorMessage = nil
        if links.contains(where: { $0.url == link.url && $0.url != originalURL }) {
            errorMessage = "附件链接不能重复"
            return false
        }
        if let originalURL, let index = links.firstIndex(where: { $0.url == originalURL }) {
            links[index] = link
        } else {
            guard links.count < 20 else {
                errorMessage = "附件链接最多添加 20 条"
                return false
            }
            links.append(link)
        }
        draft = nil
        return true
    }
}

struct TaskCompletionGateView: View {
    let task: TaskV2
    let store: WorkspaceStore
    let allowsToggle: Bool
    let onChanged: () async -> Void
    @State private var isSaving = false
    @State private var errorMessage: String?

    private var completedCount: Int { task.completionRequirements.count { $0.completed } }

    var body: some View {
        VStack(alignment: .leading, spacing: 11) {
            HStack {
                Label("完成门槛", systemImage: "checkmark.circle")
                    .font(.headline)
                Spacer()
                Text("\(completedCount) / \(task.completionRequirements.count)")
                    .font(.callout.monospacedDigit().weight(.semibold))
            }

            if task.completionRequirements.isEmpty {
                Text("未设置必选项，可直接完成任务。")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            } else {
                ProgressView(value: Double(completedCount), total: Double(task.completionRequirements.count))
                ForEach(task.completionRequirements) { requirement in
                    HStack(alignment: .top, spacing: 9) {
                        Button {
                            Task { await toggle(requirement) }
                        } label: {
                            Image(systemName: requirement.completed ? "checkmark.circle.fill" : "circle")
                                .foregroundStyle(requirement.completed ? .green : .secondary)
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel(requirement.completed ? "取消完成：\(requirement.title)" : "标记完成：\(requirement.title)")
                        .frame(width: 18, height: 18)
                        .disabled(!allowsToggle || isSaving || store.mutationsDisabled)

                        Image(systemName: requirement.kind.systemImage)
                            .foregroundStyle(.secondary)
                            .frame(width: 18)
                        VStack(alignment: .leading, spacing: 2) {
                            if let rawURL = requirement.url,
                               let url = URL(string: rawURL) {
                                Link(requirement.title, destination: url)
                            } else {
                                Text(requirement.title)
                            }
                            Text(requirement.kind.title)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }
                }
                Text(completedCount == task.completionRequirements.count
                     ? "完成门槛已满足，可以完成任务。"
                     : "所有必选项完成后，才能完成任务。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            if let errorMessage {
                Label(errorMessage, systemImage: "exclamationmark.triangle")
                    .font(.caption)
                    .foregroundStyle(.red)
            }
        }
        .padding(12)
        .background(.quaternary.opacity(0.35), in: .rect(cornerRadius: 10))
    }

    private func toggle(_ requirement: TaskCompletionRequirement) async {
        isSaving = true
        errorMessage = nil
        defer { isSaving = false }
        var next = task.completionRequirements
        guard let index = next.firstIndex(where: { $0.id == requirement.id }) else { return }
        next[index].completed.toggle()
        do {
            _ = try await store.updateTaskCompletionRequirements(task, requirements: next)
            await onChanged()
        } catch {
            errorMessage = error.localizedDescription
            if let apiError = error as? APIError, apiError.isRevisionConflict {
                await onChanged()
            }
        }
    }
}

struct TaskAttachmentLinksView: View {
    let links: [TaskAttachmentLink]

    var body: some View {
        if !links.isEmpty {
            VStack(alignment: .leading, spacing: 8) {
                Text("附件链接").font(.headline)
                ForEach(links) { link in
                    if let url = URL(string: link.url) {
                        Link(destination: url) {
                            Label(link.name, systemImage: "link")
                        }
                    }
                }
            }
        }
    }
}

private struct RequirementDraft: Identifiable {
    let id = UUID()
    let requirementID: String
    var kind: TaskCompletionRequirementKind
    var title: String
    var url: String
    let completed: Bool

    init() {
        requirementID = "requirement-\(UUID().uuidString.lowercased())"
        kind = .article
        title = ""
        url = ""
        completed = false
    }

    init(requirement: TaskCompletionRequirement) {
        requirementID = requirement.id
        kind = requirement.kind
        title = requirement.title
        url = requirement.url ?? ""
        completed = requirement.completed
    }
}

private struct RequirementEditorSheet: View {
    @Environment(\.dismiss) private var dismiss
    @State private var draft: RequirementDraft
    @State private var errorMessage: String?
    let save: (TaskCompletionRequirement) -> Bool

    init(draft: RequirementDraft, save: @escaping (TaskCompletionRequirement) -> Bool) {
        _draft = State(initialValue: draft)
        self.save = save
    }

    var body: some View {
        NavigationStack {
            Form {
                Picker("类型", selection: $draft.kind) {
                    ForEach(TaskCompletionRequirementKind.allCases) { kind in
                        Label(kind.title, systemImage: kind.systemImage).tag(kind)
                    }
                }
                TextField("必选项名称", text: $draft.title)
                TextField("http(s) 链接（可选）", text: $draft.url)
                if let errorMessage {
                    Label(errorMessage, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.red)
                }
            }
            .formStyle(.grouped)
            .navigationTitle("完成条件")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("取消") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("保存") { submit() }.buttonStyle(.borderedProminent)
                }
            }
        }
        .frame(width: 460, height: 310)
    }

    private func submit() {
        let title = draft.title.trimmingCharacters(in: .whitespacesAndNewlines)
        let rawURL = draft.url.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !title.isEmpty, title.count <= 200 else {
            errorMessage = "请输入不超过 200 个字符的必选项名称"
            return
        }
        guard rawURL.isEmpty || validHTTPURL(rawURL) else {
            errorMessage = "请输入有效的 http(s) 链接"
            return
        }
        if save(TaskCompletionRequirement(
            id: draft.requirementID,
            kind: draft.kind,
            title: title,
            url: rawURL.isEmpty ? nil : rawURL,
            completed: draft.completed
        )) { dismiss() }
    }
}

private struct AttachmentLinkDraft: Identifiable {
    let id = UUID()
    let originalURL: String?
    var name: String
    var url: String

    init() {
        originalURL = nil
        name = ""
        url = ""
    }

    init(link: TaskAttachmentLink) {
        originalURL = link.url
        name = link.name
        url = link.url
    }
}

private struct AttachmentLinkEditorSheet: View {
    @Environment(\.dismiss) private var dismiss
    @State private var draft: AttachmentLinkDraft
    @State private var errorMessage: String?
    let save: (String?, TaskAttachmentLink) -> Bool

    init(draft: AttachmentLinkDraft, save: @escaping (String?, TaskAttachmentLink) -> Bool) {
        _draft = State(initialValue: draft)
        self.save = save
    }

    var body: some View {
        NavigationStack {
            Form {
                TextField("链接名称", text: $draft.name)
                TextField("https://…", text: $draft.url)
                if let errorMessage {
                    Label(errorMessage, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.red)
                }
            }
            .formStyle(.grouped)
            .navigationTitle("附件链接")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("取消") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("保存") { submit() }.buttonStyle(.borderedProminent)
                }
            }
        }
        .frame(width: 460, height: 270)
    }

    private func submit() {
        let name = draft.name.trimmingCharacters(in: .whitespacesAndNewlines)
        let rawURL = draft.url.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty, name.count <= 120 else {
            errorMessage = "请输入不超过 120 个字符的链接名称"
            return
        }
        guard validHTTPURL(rawURL) else {
            errorMessage = "请输入有效的 http(s) 链接"
            return
        }
        if save(draft.originalURL, TaskAttachmentLink(name: name, url: rawURL)) { dismiss() }
    }
}

private func validHTTPURL(_ value: String) -> Bool {
    guard let components = URLComponents(string: value),
          let scheme = components.scheme?.lowercased(),
          (scheme == "http" || scheme == "https"),
          components.host?.isEmpty == false else { return false }
    return true
}
