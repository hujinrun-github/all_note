import SwiftUI

struct QuickCaptureWindowView: View {
    @Environment(AppSession.self) private var session
    @Environment(\.dismissWindow) private var dismissWindow
    @State private var draft = TaskDraft()
    @State private var errorMessage: String?
    @FocusState private var titleFocused: Bool

    var body: some View {
        if session.phase != .ready || session.workspaceStore == nil {
            ContentUnavailableView(
                "请先登录 FlowSpace",
                systemImage: "person.crop.circle.badge.exclamationmark",
                description: Text("快速捕获使用当前工作空间的 v2 数据。")
            )
            .padding()
        } else if let store = session.workspaceStore {
            VStack(alignment: .leading, spacing: 16) {
                HStack {
                    Label("快速捕获", systemImage: "bolt.fill")
                        .font(.title2.weight(.semibold))
                    Spacer()
                    Text("任务")
                        .foregroundStyle(.secondary)
                }

                TextField("想要推进什么？", text: $draft.title)
                    .textFieldStyle(.plain)
                    .font(.title3)
                    .focused($titleFocused)
                    .onSubmit { Task { await create(using: store, closeAfterSave: false) } }
                    .accessibilityIdentifier("quick-capture-title")

                HStack {
                    Picker("项目", selection: $draft.projectID) {
                        ForEach(store.projects) { project in
                            Text(project.name).tag(project.id)
                        }
                    }
                    .labelsHidden()
                    .frame(maxWidth: 190)

                    Picker("安排", selection: $draft.timingType) {
                        Text("无日期").tag(TimingType.unscheduled)
                        Text("指定日期").tag(TimingType.date)
                        Text("指定时间").tag(TimingType.timeBlock)
                    }
                    .labelsHidden()
                    .frame(maxWidth: 150)
                }

                HStack {
                    Picker("优先级", selection: $draft.priority) {
                        ForEach(TaskPriorityLevel.allCases) { level in
                            Text("\(level.title)优先级").tag(level.rawValue)
                        }
                    }
                    .labelsHidden()
                    .frame(maxWidth: 150)

                    Picker("重复", selection: $draft.recurrenceType) {
                        Text("不重复").tag(RecurrenceType.none)
                        Text("每天").tag(RecurrenceType.daily)
                        Text("每周").tag(RecurrenceType.weekly)
                        Text("每月").tag(RecurrenceType.monthly)
                    }
                    .labelsHidden()
                    .frame(maxWidth: 130)
                }

                if draft.timingType != .unscheduled {
                    HStack {
                        DatePicker(
                            draft.timingType == .timeBlock ? "开始" : "日期",
                            selection: $draft.date,
                            displayedComponents: draft.timingType == .timeBlock ? [.date, .hourAndMinute] : [.date]
                        )
                        if draft.timingType == .timeBlock {
                            Stepper("\(draft.durationMinutes) 分钟", value: $draft.durationMinutes, in: 15...480, step: 15)
                        }
                    }
                }

                if let errorMessage {
                    Label(errorMessage, systemImage: "exclamationmark.triangle")
                        .font(.callout)
                        .foregroundStyle(.red)
                }

                HStack {
                    Spacer()
                    Button("保存并关闭") {
                        Task { await create(using: store, closeAfterSave: true) }
                    }
                    .keyboardShortcut(.return, modifiers: [.command, .option])
                    .disabled(cannotSave(using: store))

                    Button("保存并继续") {
                        Task { await create(using: store, closeAfterSave: false) }
                    }
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.return, modifiers: .command)
                    .disabled(cannotSave(using: store))
                }
            }
            .padding(22)
            .task {
                if store.projects.isEmpty { await store.loadProjects() }
                if draft.projectID.isEmpty { draft.projectID = store.defaultProjectID }
                titleFocused = true
            }
            .onChange(of: draft.timingType) {
                if draft.timingType == .unscheduled {
                    draft.recurrenceType = .none
                }
            }
            .onChange(of: draft.recurrenceType) {
                if draft.recurrenceType != .none, draft.timingType == .unscheduled {
                    draft.timingType = .date
                    draft.date = Date()
                }
            }
        }
    }

    private func create(using store: WorkspaceStore, closeAfterSave: Bool) async {
        errorMessage = nil
        do {
            try await store.createTask(from: draft)
            draft = TaskDraft()
            draft.projectID = store.defaultProjectID
            await session.refreshMenuBarSummary()
            if closeAfterSave {
                dismissWindow(id: "quick-capture")
            } else {
                titleFocused = true
            }
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func cannotSave(using store: WorkspaceStore) -> Bool {
        draft.title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            || draft.projectID.isEmpty
            || store.isMutating
    }
}
