import SwiftUI

struct TaskDefinitionInspector: View {
    private enum DestructiveAction: String, Identifiable {
        case cancel
        case archive
        case delete

        var id: String { rawValue }
    }

    let task: TaskV2
    let store: WorkspaceStore
    let refresh: () async -> Void
    let close: () -> Void
    @State private var localError: String?
    @State private var destructiveAction: DestructiveAction?
    @State private var editingTask = false

    private var project: ProjectV2? { store.projectsByID[task.projectID] }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                VStack(alignment: .leading, spacing: 8) {
                    Text("任务定义")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Text(task.title)
                        .font(.title2.weight(.semibold))
                    TaskLifecycleBadge(status: task.lifecycleStatus)
                }

                lifecycleActions
                    .disabled(store.isMutating)

                if task.lifecycleStatus != .completed && task.lifecycleStatus != .archived {
                    Button("编辑任务定义", systemImage: "pencil") {
                        editingTask = true
                    }
                    .disabled(store.isMutating)
                }

                if let localError {
                    Label(localError, systemImage: "exclamationmark.triangle")
                        .font(.callout)
                        .foregroundStyle(.red)
                }

                Divider()
                InspectorField(label: "所属项目", value: project?.name ?? "未归属")
                InspectorField(label: "任务状态", value: lifecycleLabel(task.lifecycleStatus))
                InspectorField(label: "优先级", value: priorityLabel(task.priority))
                if let description = task.description, !description.isEmpty {
                    InspectorField(label: "描述", value: description)
                }

                Divider()
                VStack(alignment: .leading, spacing: 6) {
                    Text("并发版本")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Text("任务 r\(task.revision) · 安排 r\(task.scheduleRevision)")
                        .font(.caption.monospacedDigit())
                        .textSelection(.enabled)
                }

                Divider()
                VStack(alignment: .leading, spacing: 10) {
                    Text("任务管理").font(.headline)
                    HStack {
                        Button("归档", systemImage: "archivebox") {
                            destructiveAction = .archive
                        }
                        Button("永久删除", systemImage: "trash", role: .destructive) {
                            destructiveAction = .delete
                        }
                    }
                    .disabled(store.isMutating)
                    Text("服务端会根据执行历史和 revision 判断当前操作是否允许。")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            .padding(22)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .confirmationDialog(
            confirmationTitle,
            isPresented: Binding(
                get: { destructiveAction != nil },
                set: { if !$0 { destructiveAction = nil } }
            ),
            titleVisibility: .visible
        ) {
            if let destructiveAction {
                Button(confirmationButtonTitle, role: .destructive) {
                    let action = destructiveAction
                    self.destructiveAction = nil
                    Task { await perform(action) }
                }
            }
            Button("取消", role: .cancel) { destructiveAction = nil }
        } message: {
            Text(confirmationMessage)
        }
        .sheet(isPresented: $editingTask) {
            TaskDefinitionEditorSheet(task: task, store: store) {
                await refresh()
            }
        }
    }

    @ViewBuilder
    private var lifecycleActions: some View {
        switch task.lifecycleStatus {
        case .draft:
            HStack {
                Button("发布", systemImage: "paperplane.fill") {
                    Task { await perform(.publish) }
                }
                .buttonStyle(.borderedProminent)
                Button("取消任务", systemImage: "xmark.circle") {
                    destructiveAction = .cancel
                }
            }
        case .active:
            HStack {
                Button("暂停", systemImage: "pause.fill") {
                    Task { await perform(.pause) }
                }
                Button("取消任务", systemImage: "xmark.circle") {
                    destructiveAction = .cancel
                }
            }
        case .paused:
            HStack {
                Button("恢复", systemImage: "play.fill") {
                    Task { await perform(.resume) }
                }
                .buttonStyle(.borderedProminent)
                Button("取消任务", systemImage: "xmark.circle") {
                    destructiveAction = .cancel
                }
            }
        case .cancelled, .archived:
            Button("恢复任务", systemImage: "arrow.uturn.backward") {
                Task { await perform(.restore) }
            }
            .buttonStyle(.borderedProminent)
        case .completed:
            Text("任务定义已完成，可归档保留历史记录。")
                .font(.callout)
                .foregroundStyle(.secondary)
        }
    }

    private var confirmationTitle: String {
        switch destructiveAction {
        case .cancel: "取消这个任务？"
        case .archive: "归档这个任务？"
        case .delete: "永久删除这个任务？"
        case nil: "确认操作"
        }
    }

    private var confirmationButtonTitle: String {
        switch destructiveAction {
        case .cancel: "取消任务"
        case .archive: "归档"
        case .delete: "永久删除"
        case nil: "确认"
        }
    }

    private var confirmationMessage: String {
        switch destructiveAction {
        case .cancel: "任务定义及其未结束执行会按照服务端规则取消；已有历史记录不会伪装成删除。"
        case .archive: "归档后任务将从普通视图隐藏，执行历史仍由服务端保留。"
        case .delete: "任务定义和允许删除的执行记录会被永久删除，此操作无法撤销。"
        case nil: ""
        }
    }

    private func perform(_ command: TaskLifecycleCommand) async {
        localError = nil
        do {
            try await store.executeTaskLifecycle(task, command: command)
            await refresh()
            close()
        } catch {
            localError = error.localizedDescription
            if let apiError = error as? APIError, apiError.isRevisionConflict {
                await refresh()
            }
        }
    }

    private func perform(_ action: DestructiveAction) async {
        localError = nil
        do {
            switch action {
            case .cancel:
                try await store.executeTaskLifecycle(task, command: .cancel)
            case .archive:
                try await store.executeTaskLifecycle(task, command: .archive)
            case .delete:
                try await store.deleteTaskDefinition(task)
            }
            await refresh()
            close()
        } catch {
            localError = error.localizedDescription
            if let apiError = error as? APIError, apiError.isRevisionConflict {
                await refresh()
            }
        }
    }

    private func lifecycleLabel(_ status: TaskLifecycleStatus) -> String {
        switch status {
        case .draft: "草稿"
        case .active: "已发布"
        case .paused: "已暂停"
        case .completed: "已完成"
        case .cancelled: "已取消"
        case .archived: "已归档"
        }
    }

    private func priorityLabel(_ priority: Int) -> String {
        switch priority {
        case 3...: "紧急"
        case 2: "高"
        case 1: "中"
        default: "普通"
        }
    }
}

struct TaskDefinitionEditorSheet: View {
    @Environment(\.dismiss) private var dismiss
    let task: TaskV2
    let store: WorkspaceStore
    let allowsProjectChange: Bool
    let onSaved: () async -> Void
    @State private var title: String
    @State private var description: String
    @State private var priority: Int
    @State private var projectID: String
    @State private var errorMessage: String?

    init(
        task: TaskV2,
        store: WorkspaceStore,
        allowsProjectChange: Bool = true,
        onSaved: @escaping () async -> Void
    ) {
        self.task = task
        self.store = store
        self.allowsProjectChange = allowsProjectChange
        self.onSaved = onSaved
        _title = State(initialValue: task.title)
        _description = State(initialValue: task.description ?? "")
        _priority = State(initialValue: task.priority)
        _projectID = State(initialValue: task.projectID)
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("任务定义") {
                    TextField("任务标题", text: $title)
                    TextField("描述", text: $description, axis: .vertical)
                        .lineLimit(3...8)
                    Picker("优先级", selection: $priority) {
                        ForEach(TaskPriorityLevel.allCases) { level in
                            Text(level.title).tag(level.rawValue)
                        }
                    }
                    if allowsProjectChange {
                        Picker("所属项目", selection: $projectID) {
                            ForEach(editableProjects) { project in
                                Text(project.name).tag(project.id)
                            }
                        }
                    } else {
                        LabeledContent("所属项目", value: store.projectsByID[projectID]?.name ?? "当前学习项目")
                    }
                }

                Section {
                    Text(allowsProjectChange
                         ? "这里修改的是任务定义；重复任务的所有执行都会继续引用新的标题、优先级和项目。"
                         : "这里修改的是任务定义；Roadmap 关联任务需留在当前学习项目中。重复执行会继续引用新的标题和优先级。")
                        .foregroundStyle(.secondary)
                }

                if let errorMessage {
                    Section {
                        Label(errorMessage, systemImage: "exclamationmark.triangle")
                            .foregroundStyle(.red)
                    }
                }
            }
            .formStyle(.grouped)
            .navigationTitle("编辑任务")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("取消") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("保存") { Task { await save() } }
                        .buttonStyle(.borderedProminent)
                        .disabled(
                            title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                                || projectID.isEmpty
                                || store.isMutating
                        )
                }
            }
        }
        .frame(width: 520, height: 500)
    }

    private var editableProjects: [ProjectV2] {
        store.projects.filter { $0.status != .completed && $0.status != .archived }
    }

    private func save() async {
        errorMessage = nil
        do {
            _ = try await store.updateTaskDefinition(
                task,
                title: title,
                description: description,
                priority: priority,
                projectID: projectID
            )
            await onSaved()
            dismiss()
        } catch let error as APIError where error.isRevisionConflict {
            errorMessage = "任务已在其他窗口中更新，请关闭编辑器后重新打开。"
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}

private struct TaskLifecycleBadge: View {
    let status: TaskLifecycleStatus

    var body: some View {
        Text(label)
            .font(.caption2.weight(.semibold))
            .padding(.horizontal, 7)
            .padding(.vertical, 3)
            .foregroundStyle(color)
            .background(color.opacity(0.12), in: .capsule)
    }

    private var label: String {
        switch status {
        case .draft: "草稿"
        case .active: "已发布"
        case .paused: "已暂停"
        case .completed: "已完成"
        case .cancelled: "已取消"
        case .archived: "已归档"
        }
    }

    private var color: Color {
        switch status {
        case .draft: .secondary
        case .active: .blue
        case .paused: .orange
        case .completed: .green
        case .cancelled: .red
        case .archived: .purple
        }
    }
}
