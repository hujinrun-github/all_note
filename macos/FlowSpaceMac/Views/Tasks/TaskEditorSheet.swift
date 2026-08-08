import SwiftUI

struct TaskEditorSheet: View {
    @Environment(\.dismiss) private var dismiss
    @State private var draft: TaskDraft
    @State private var errorMessage: String?
    let store: WorkspaceStore
    let onCreated: () async -> Void

    init(draft: TaskDraft, store: WorkspaceStore, onCreated: @escaping () async -> Void) {
        _draft = State(initialValue: draft)
        self.store = store
        self.onCreated = onCreated
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("任务") {
                    TextField("任务标题", text: $draft.title)
                        .accessibilityIdentifier("task-title")
                    if draft.roadmapNodeID == nil {
                        Picker("所属项目", selection: $draft.projectID) {
                            if store.projects.isEmpty {
                                Text("暂无项目").tag("")
                            }
                            ForEach(store.projects) { project in
                                Text(project.name).tag(project.id)
                            }
                        }
                    } else {
                        LabeledContent("所属项目", value: store.projectsByID[draft.projectID]?.name ?? "当前学习项目")
                        Text("Roadmap 关联任务会固定在当前学习项目和节点中。")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    Picker("优先级", selection: $draft.priority) {
                        ForEach(TaskPriorityLevel.allCases) { level in
                            Text(level.title).tag(level.rawValue)
                        }
                    }
                }

                Section("安排") {
                    Picker("安排方式", selection: $draft.timingType) {
                        Text("无日期").tag(TimingType.unscheduled)
                        Text("指定日期").tag(TimingType.date)
                        Text("指定时间").tag(TimingType.timeBlock)
                    }

                    if draft.timingType != .unscheduled {
                        DatePicker(
                            "日期",
                            selection: $draft.date,
                            displayedComponents: .date
                        )
                    }
                    if draft.timingType == .timeBlock {
                        DatePicker(
                            "开始时间",
                            selection: $draft.date,
                            displayedComponents: .hourAndMinute
                        )
                        Stepper("时长：\(draft.durationMinutes) 分钟", value: $draft.durationMinutes, in: 15...480, step: 15)
                    }

                    Picker("重复", selection: $draft.recurrenceType) {
                        Text("不重复").tag(RecurrenceType.none)
                        Text("每天").tag(RecurrenceType.daily)
                        Text("每周").tag(RecurrenceType.weekly)
                        Text("每月").tag(RecurrenceType.monthly)
                    }
                }

                if let errorMessage {
                    Section {
                        Label(errorMessage, systemImage: "exclamationmark.triangle")
                            .foregroundStyle(.red)
                    }
                }
            }
            .formStyle(.grouped)
            .navigationTitle("新建任务")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("取消") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("创建") { Task { await create() } }
                        .buttonStyle(.borderedProminent)
                        .disabled(store.isMutating || draft.title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || draft.projectID.isEmpty)
                }
            }
        }
        .frame(width: 520, height: 520)
        .task {
            if store.projects.isEmpty { await store.loadProjects() }
            if draft.projectID.isEmpty { draft.projectID = store.defaultProjectID }
        }
    }

    private func create() async {
        errorMessage = nil
        do {
            try await store.createTask(from: draft)
            await onCreated()
            dismiss()
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}
