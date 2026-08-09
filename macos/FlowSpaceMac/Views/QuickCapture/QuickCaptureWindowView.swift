import SwiftUI

struct QuickCaptureWindowView: View {
    private enum CaptureKind: String, CaseIterable, Identifiable {
        case task
        case note
        case event

        var id: String { rawValue }
        var title: String {
            switch self {
            case .task: "任务"
            case .note: "笔记"
            case .event: "日程"
            }
        }
        var systemImage: String {
            switch self {
            case .task: "checkmark.circle"
            case .note: "note.text"
            case .event: "calendar"
            }
        }
    }

    private struct NoteCaptureDraft {
        var title = ""
        var body = ""
        var projectID = ""
        var tags = ""

        var hasInput: Bool {
            !title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                || !body.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                || !tags.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        }
    }

    @Environment(AppSession.self) private var session
    @Environment(\.dismissWindow) private var dismissWindow
    @State private var kind: CaptureKind = .task
    @State private var taskDraft = TaskDraft()
    @State private var noteDraft = NoteCaptureDraft()
    @State private var errorMessage: String?
    @State private var confirmDiscard = false
    @FocusState private var titleFocused: Bool

    private var hasInput: Bool {
        switch kind {
        case .task, .event:
            !taskDraft.title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        case .note:
            noteDraft.hasInput
        }
    }

    var body: some View {
        Group {
            if session.phase != .ready || session.workspaceStore == nil {
                ContentUnavailableView(
                    "请先登录 FlowSpace",
                    systemImage: "person.crop.circle.badge.exclamationmark",
                    description: Text("快速捕获使用当前工作空间的 v2 数据。")
                )
                .padding()
            } else if let store = session.workspaceStore {
                captureForm(store: store)
            }
        }
        .windowDismissBehavior(hasInput ? .disabled : .enabled)
        .alert("放弃未保存的内容？", isPresented: $confirmDiscard) {
            Button("继续编辑", role: .cancel) {}
            Button("放弃并关闭", role: .destructive) {
                withTransaction(\.dismissBehavior, .destructive) {
                    dismissWindow(id: "quick-capture")
                }
            }
        } message: {
            Text("快速捕获中的内容尚未保存，关闭后无法恢复。")
        }
    }

    private func captureForm(store: WorkspaceStore) -> some View {
        VStack(alignment: .leading, spacing: 16) {
            if store.isReadOnly {
                Label("服务离线，当前内容只保留在此窗口内", systemImage: "wifi.slash")
                    .font(.callout)
                    .foregroundStyle(.orange)
            }

            HStack {
                Label("快速捕获", systemImage: "bolt.fill")
                    .font(.title2.weight(.semibold))
                Spacer()
                Button("关闭", systemImage: "xmark") { requestClose() }
                    .labelStyle(.iconOnly)
                    .buttonStyle(.plain)
            }

            Picker("类型", selection: $kind) {
                ForEach(CaptureKind.allCases) { value in
                    Label(value.title, systemImage: value.systemImage).tag(value)
                }
            }
            .pickerStyle(.segmented)

            switch kind {
            case .task:
                taskFields(store: store, eventMode: false)
            case .note:
                noteFields(store: store)
            case .event:
                taskFields(store: store, eventMode: true)
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
            applyProjectDefaults(store: store)
            configureKind()
            titleFocused = true
        }
        .onChange(of: kind) {
            errorMessage = nil
            configureKind()
            titleFocused = true
        }
        .onChange(of: taskDraft.timingType) {
            if taskDraft.timingType == .unscheduled {
                taskDraft.recurrenceType = .none
            }
        }
        .onChange(of: taskDraft.recurrenceType) {
            if taskDraft.recurrenceType != .none, taskDraft.timingType == .unscheduled {
                taskDraft.timingType = .date
                taskDraft.date = Date()
            }
        }
    }

    @ViewBuilder
    private func taskFields(store: WorkspaceStore, eventMode: Bool) -> some View {
        TextField(eventMode ? "日程标题" : "想要推进什么？", text: $taskDraft.title)
            .textFieldStyle(.plain)
            .font(.title3)
            .focused($titleFocused)
            .onSubmit { Task { await create(using: store, closeAfterSave: false) } }
            .accessibilityIdentifier("quick-capture-title")

        HStack {
            projectPicker(store: store, selection: $taskDraft.projectID)

            Picker("安排", selection: $taskDraft.timingType) {
                if !eventMode { Text("无日期").tag(TimingType.unscheduled) }
                Text("指定日期").tag(TimingType.date)
                Text("指定时间").tag(TimingType.timeBlock)
            }
            .labelsHidden()
            .frame(maxWidth: 150)
        }

        HStack {
            Picker("优先级", selection: $taskDraft.priority) {
                ForEach(TaskPriorityLevel.allCases) { level in
                    Text("\(level.title)优先级").tag(level.rawValue)
                }
            }
            .labelsHidden()
            .frame(maxWidth: 150)

            Picker("重复", selection: $taskDraft.recurrenceType) {
                Text("不重复").tag(RecurrenceType.none)
                Text("每天").tag(RecurrenceType.daily)
                Text("每周").tag(RecurrenceType.weekly)
                Text("每月").tag(RecurrenceType.monthly)
            }
            .labelsHidden()
            .frame(maxWidth: 130)
        }

        if taskDraft.timingType != .unscheduled {
            HStack {
                DatePicker(
                    taskDraft.timingType == .timeBlock ? "开始" : "日期",
                    selection: $taskDraft.date,
                    displayedComponents: taskDraft.timingType == .timeBlock ? [.date, .hourAndMinute] : [.date]
                )
                if taskDraft.timingType == .timeBlock {
                    Stepper("\(taskDraft.durationMinutes) 分钟", value: $taskDraft.durationMinutes, in: 15...480, step: 15)
                }
            }
        }
    }

    @ViewBuilder
    private func noteFields(store: WorkspaceStore) -> some View {
        TextField("笔记标题", text: $noteDraft.title)
            .textFieldStyle(.plain)
            .font(.title3)
            .focused($titleFocused)
            .accessibilityIdentifier("quick-capture-note-title")

        TextEditor(text: $noteDraft.body)
            .font(.body)
            .scrollContentBackground(.hidden)
            .padding(8)
            .frame(minHeight: 110)
            .background(.quaternary.opacity(0.35), in: .rect(cornerRadius: 8))
            .overlay(alignment: .topLeading) {
                if noteDraft.body.isEmpty {
                    Text("记录正文…")
                        .foregroundStyle(.tertiary)
                        .padding(.horizontal, 13)
                        .padding(.vertical, 16)
                        .allowsHitTesting(false)
                }
            }

        HStack {
            projectPicker(store: store, selection: $noteDraft.projectID, allowsNone: true)
            TextField("标签，用逗号分隔", text: $noteDraft.tags)
                .textFieldStyle(.roundedBorder)
        }
    }

    private func projectPicker(
        store: WorkspaceStore,
        selection: Binding<String>,
        allowsNone: Bool = false
    ) -> some View {
        Picker("项目", selection: selection) {
            if allowsNone { Text("未归属项目").tag("") }
            ForEach(store.projects) { project in
                Text(project.name).tag(project.id)
            }
        }
        .labelsHidden()
        .frame(maxWidth: 190)
    }

    private func create(using store: WorkspaceStore, closeAfterSave: Bool) async {
        errorMessage = nil
        do {
            switch kind {
            case .task, .event:
                try await store.createTask(from: taskDraft)
            case .note:
                _ = try await store.createNote(
                    title: noteDraft.title.trimmingCharacters(in: .whitespacesAndNewlines),
                    body: noteDraft.body,
                    projectID: noteDraft.projectID.isEmpty ? nil : noteDraft.projectID,
                    tags: FlowNote.normalizedTags(from: noteDraft.tags)
                )
            }
            clearCurrentDraft(store: store)
            await session.refreshMenuBarSummary()
            if closeAfterSave {
                withTransaction(\.dismissBehavior, .destructive) {
                    dismissWindow(id: "quick-capture")
                }
            } else {
                titleFocused = true
            }
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func cannotSave(using store: WorkspaceStore) -> Bool {
        let missingRequiredFields: Bool
        switch kind {
        case .task:
            missingRequiredFields = taskDraft.title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                || taskDraft.projectID.isEmpty
        case .event:
            missingRequiredFields = taskDraft.title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                || taskDraft.projectID.isEmpty
                || taskDraft.timingType == .unscheduled
        case .note:
            missingRequiredFields = noteDraft.title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        }
        return missingRequiredFields || store.mutationsDisabled
    }

    private func configureKind() {
        if kind == .event, taskDraft.timingType == .unscheduled {
            taskDraft.timingType = .date
            taskDraft.date = Date()
        }
    }

    private func applyProjectDefaults(store: WorkspaceStore) {
        if taskDraft.projectID.isEmpty { taskDraft.projectID = store.defaultProjectID }
    }

    private func clearCurrentDraft(store: WorkspaceStore) {
        switch kind {
        case .task:
            taskDraft = TaskDraft()
            taskDraft.projectID = store.defaultProjectID
        case .event:
            taskDraft = TaskDraft()
            taskDraft.projectID = store.defaultProjectID
            taskDraft.timingType = .date
            taskDraft.date = Date()
        case .note:
            noteDraft = NoteCaptureDraft()
        }
    }

    private func requestClose() {
        if hasInput {
            confirmDiscard = true
        } else {
            dismissWindow(id: "quick-capture")
        }
    }
}
