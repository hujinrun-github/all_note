import SwiftUI

struct NoteMetadataInspector: View {
    let noteID: String
    let store: WorkspaceStore
    @Binding var selectedProjectIDs: Set<String>
    @Binding var tagsText: String
    let wordCount: Int
    let createdAt: Int64
    let updatedAt: Int64
    let markDirty: () -> Void

    @State private var taskProjectID = ""
    @State private var taskToLinkID = ""
    @State private var associationError: String?

    private var editableProjects: [ProjectV2] {
        store.projects.filter { $0.status != .archived }
    }

    private var linkedTasks: [TaskV2] {
        store.tasks
            .filter { $0.taskNoteID == noteID }
            .sorted { $0.title.localizedStandardCompare($1.title) == .orderedAscending }
    }

    private var availableTasks: [TaskV2] {
        store.tasks.filter { task in
            task.projectID == taskProjectID
                && (task.taskNoteID == nil || task.taskNoteID?.isEmpty == true)
                && task.lifecycleStatus != .cancelled
                && task.lifecycleStatus != .archived
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            projectSection
            Divider()
            taskSection
            Divider()
            tagSection
            Divider()
            documentSection
        }
        .padding(16)
        .task {
            if store.tasks.isEmpty { await store.loadAllTasks() }
            chooseTaskProjectIfNeeded()
        }
        .onChange(of: selectedProjectIDs) {
            chooseTaskProjectIfNeeded()
        }
    }

    private var projectSection: some View {
        VStack(alignment: .leading, spacing: 9) {
            Label("关联项目", systemImage: "folder")
                .font(.headline)

            if editableProjects.isEmpty {
                Text("暂无可关联项目")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                Menu("管理项目", systemImage: "folder.badge.plus") {
                    ForEach(editableProjects) { project in
                        Toggle(
                            project.name,
                            isOn: Binding(
                                get: { selectedProjectIDs.contains(project.id) },
                                set: { selected in setProject(project.id, selected: selected) }
                            )
                        )
                    }
                }

                if selectedProjectIDs.isEmpty {
                    Text("未归属")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                } else {
                    VStack(alignment: .leading, spacing: 5) {
                        ForEach(editableProjects.filter { selectedProjectIDs.contains($0.id) }) { project in
                            Label(project.name, systemImage: "folder.fill")
                                .font(.caption)
                        }
                    }
                }
            }
        }
    }

    private var taskSection: some View {
        VStack(alignment: .leading, spacing: 9) {
            Label("关联任务", systemImage: "checklist")
                .font(.headline)

            if linkedTasks.isEmpty {
                Text("还没有关联任务")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                VStack(spacing: 6) {
                    ForEach(linkedTasks) { task in
                        HStack(spacing: 8) {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(task.title).font(.caption.weight(.medium))
                                Text(store.projectsByID[task.projectID]?.name ?? "未知项目")
                                    .font(.caption2)
                                    .foregroundStyle(.secondary)
                            }
                            Spacer()
                            Button("取消关联", systemImage: "xmark") {
                                Task { await unlink(task) }
                            }
                            .labelStyle(.iconOnly)
                            .buttonStyle(.borderless)
                            .disabled(store.isMutating)
                        }
                    }
                }
            }

            Picker("筛选项目", selection: $taskProjectID) {
                Text("请选择项目").tag("")
                ForEach(editableProjects) { project in
                    Text(project.name).tag(project.id)
                }
            }

            Picker("添加任务", selection: $taskToLinkID) {
                Text(taskProjectID.isEmpty ? "请先选择项目" : "选择未关联任务").tag("")
                ForEach(availableTasks) { task in
                    Text(task.title).tag(task.id)
                }
            }
            .disabled(taskProjectID.isEmpty || availableTasks.isEmpty || store.isMutating)
            .onChange(of: taskToLinkID) {
                guard !taskToLinkID.isEmpty else { return }
                let taskID = taskToLinkID
                taskToLinkID = ""
                Task { await link(taskID: taskID) }
            }

            if !taskProjectID.isEmpty && availableTasks.isEmpty {
                Text("该项目没有可关联的任务。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if let associationError {
                Label(associationError, systemImage: "exclamationmark.triangle")
                    .font(.caption)
                    .foregroundStyle(.red)
            }
        }
    }

    private var tagSection: some View {
        VStack(alignment: .leading, spacing: 7) {
            Label("标签", systemImage: "tag")
                .font(.headline)
            TextField("用逗号分隔标签", text: $tagsText)
                .textFieldStyle(.roundedBorder)
                .onChange(of: tagsText) { markDirty() }
            let tags = FlowNote.normalizedTags(from: tagsText)
            if !tags.isEmpty {
                Text(tags.map { "#\($0)" }.joined(separator: "  "))
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var documentSection: some View {
        VStack(alignment: .leading, spacing: 7) {
            Label("文稿信息", systemImage: "doc.text")
                .font(.headline)
            LabeledContent("字数", value: "\(wordCount)")
            LabeledContent("创建时间", value: formatted(createdAt))
            LabeledContent("更新时间", value: formatted(updatedAt))
            Text("笔记正文当前没有 revision / CAS；保存失败时会保留本地内容，但不会声称已进入离线队列。")
                .font(.caption2)
                .foregroundStyle(.secondary)
        }
    }

    private func setProject(_ projectID: String, selected: Bool) {
        if selected {
            selectedProjectIDs.insert(projectID)
        } else {
            selectedProjectIDs.remove(projectID)
        }
        markDirty()
        chooseTaskProjectIfNeeded()
    }

    private func chooseTaskProjectIfNeeded() {
        guard taskProjectID.isEmpty || !editableProjects.contains(where: { $0.id == taskProjectID }) else { return }
        taskProjectID = editableProjects.first(where: { selectedProjectIDs.contains($0.id) })?.id
            ?? editableProjects.first?.id
            ?? ""
    }

    private func link(taskID: String) async {
        associationError = nil
        guard let task = store.tasksByID[taskID] else {
            associationError = "任务已更新，请刷新后重试。"
            return
        }
        do {
            _ = try await store.setTaskNote(task, noteID: noteID)
            if selectedProjectIDs.insert(task.projectID).inserted { markDirty() }
        } catch let error as APIError where error.isRevisionConflict {
            associationError = "任务已在其他窗口更新，列表已刷新，请重新选择。"
            await store.loadAllTasks()
        } catch {
            associationError = error.localizedDescription
        }
    }

    private func unlink(_ task: TaskV2) async {
        associationError = nil
        do {
            _ = try await store.setTaskNote(task, noteID: nil)
        } catch let error as APIError where error.isRevisionConflict {
            associationError = "任务已在其他窗口更新，列表已刷新，请重试。"
            await store.loadAllTasks()
        } catch {
            associationError = error.localizedDescription
        }
    }

    private func formatted(_ timestamp: Int64) -> String {
        Date(timeIntervalSince1970: TimeInterval(timestamp))
            .formatted(date: .abbreviated, time: .shortened)
    }
}
