import SwiftUI

struct InboxView: View {
    let store: WorkspaceStore
    @Binding var selectedOccurrenceID: String
    @State private var title = ""
    @State private var priority = TaskPriorityLevel.normal.rawValue
    @State private var targetProjectID = ""
    @State private var organizerError: String?

    private var inboxOccurrences: [OccurrenceV2] {
        guard let inboxID = store.inboxProject?.id else { return [] }
        return store.occurrences.filter {
            $0.projectID == inboxID || store.tasksByID[$0.taskID]?.projectID == inboxID
        }
    }

    private var selectedOccurrence: OccurrenceV2? {
        inboxOccurrences.first { $0.id == selectedOccurrenceID }
    }

    private var selectedTask: TaskV2? {
        selectedOccurrence.flatMap { store.tasksByID[$0.taskID] }
    }

    private var organizeTargets: [ProjectV2] {
        store.projects.filter {
            $0.id != store.inboxProject?.id && $0.status != .completed && $0.status != .archived
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            Group {
                if store.isLoading && store.occurrences.isEmpty {
                    ProgressView("正在加载未整理任务…")
                } else if inboxOccurrences.isEmpty {
                    ContentUnavailableView(
                        "未整理已经清空",
                        systemImage: "tray",
                        description: Text("快速捕获但未归入项目的任务会出现在这里。")
                    )
                } else {
                    List(inboxOccurrences) { occurrence in
                        OccurrenceRow(
                            occurrence: occurrence,
                            task: store.tasksByID[occurrence.taskID],
                            project: store.inboxProject,
                            selected: occurrence.id == selectedOccurrenceID,
                            isMutating: store.isMutating,
                            select: {
                                selectedOccurrenceID = occurrence.id
                                configureOrganizer()
                            },
                            toggle: { await toggle(occurrence) }
                        )
                    }
                    .listStyle(.inset)
                }
            }

            if selectedTask != nil {
                Divider()
                organizer
            }
        }
        .task {
            await store.loadAllTasks()
            configureOrganizer()
        }
        .onChange(of: selectedOccurrenceID) {
            configureOrganizer()
        }
    }

    private var organizer: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Label("整理任务", systemImage: "tray.and.arrow.up")
                    .font(.headline)
                Spacer()
                Text(store.inboxProject?.name ?? "系统收件箱")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            HStack(spacing: 10) {
                TextField("任务标题", text: $title)
                    .textFieldStyle(.roundedBorder)
                    .frame(minWidth: 180)

                Picker("优先级", selection: $priority) {
                    ForEach(TaskPriorityLevel.allCases) { level in
                        Text(level.title).tag(level.rawValue)
                    }
                }
                .frame(maxWidth: 120)

                Picker("归入项目", selection: $targetProjectID) {
                    if organizeTargets.isEmpty {
                        Text("暂无可用项目").tag("")
                    }
                    ForEach(organizeTargets) { project in
                        Text(project.name).tag(project.id)
                    }
                }
                .frame(minWidth: 160, maxWidth: 220)

                Button("保存并归类", systemImage: "arrow.right.folder") {
                    Task { await organize() }
                }
                .buttonStyle(.borderedProminent)
                .disabled(
                    title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                        || targetProjectID.isEmpty
                        || store.isMutating
                )
            }

            if let organizerError {
                Label(organizerError, systemImage: "exclamationmark.triangle")
                    .font(.callout)
                    .foregroundStyle(.red)
            } else {
                Text("标题、优先级和项目会作为同一次 revision 更新保存。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .padding(14)
    }

    private func toggle(_ occurrence: OccurrenceV2) async {
        do {
            try await store.toggle(occurrence)
            await store.loadAllTasks()
        } catch {
            store.errorMessage = error.localizedDescription
        }
    }

    private func configureOrganizer() {
        guard let task = selectedTask else {
            title = ""
            priority = TaskPriorityLevel.normal.rawValue
            targetProjectID = organizeTargets.first?.id ?? ""
            organizerError = nil
            return
        }
        title = task.title
        priority = task.priority
        if !organizeTargets.contains(where: { $0.id == targetProjectID }) {
            targetProjectID = organizeTargets.first?.id ?? ""
        }
        organizerError = nil
    }

    private func organize() async {
        guard let task = selectedTask else { return }
        organizerError = nil
        do {
            _ = try await store.updateTaskDefinition(
                task,
                title: title,
                description: task.description ?? "",
                priority: priority,
                projectID: targetProjectID
            )
            selectedOccurrenceID = ""
            await store.loadAllTasks()
        } catch let error as APIError where error.isRevisionConflict {
            organizerError = "任务已在其他窗口中更新，请刷新后重新整理。"
            await store.loadAllTasks()
            configureOrganizer()
        } catch {
            organizerError = error.localizedDescription
        }
    }
}
