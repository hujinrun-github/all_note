import SwiftUI

struct TasksView: View {
    @Environment(AppSession.self) private var session
    let store: WorkspaceStore
    @Binding var selectedOccurrenceID: String
    @Binding var selectedTaskID: String
    @Binding var workspaceView: TaskWorkspaceView
    @State private var projectFilter = ""
    @State private var priorityFilter: Int?
    @State private var statusFilter: ExecutionStatus?
    @State private var dateFilter: TaskDateFilter = .current
    @State private var loadedView: TaskWorkspaceView?

    private var filter: TaskWorkspaceFilter {
        TaskWorkspaceFilter(
            projectID: projectFilter,
            priority: priorityFilter,
            executionStatus: statusFilter,
            date: dateFilter
        )
    }

    private var filteredOccurrences: [OccurrenceV2] {
        filter.occurrences(store.occurrences, tasks: store.tasksByID)
    }

    private var filteredDrafts: [TaskV2] {
        filter.drafts(store.tasks)
    }

    private var collections: [OccurrenceCollection] {
        OccurrenceCollection.group(filteredOccurrences, tasks: store.tasksByID)
    }

    private var selectedOccurrence: OccurrenceV2? {
        filteredOccurrences.first { $0.id == selectedOccurrenceID }
    }

    var body: some View {
        VStack(spacing: 0) {
            TaskFixedViewSelector(selection: $workspaceView)
            Divider()
            TaskWorkspaceFilterBar(
                store: store,
                projectID: $projectFilter,
                priority: $priorityFilter,
                status: $statusFilter,
                date: $dateFilter,
                filtersEnabled: workspaceView != .draft,
                resultCount: workspaceView == .draft ? filteredDrafts.count : filteredOccurrences.count,
                clear: clearFilters
            )
            Divider()
            content
        }
        .task(id: workspaceView) {
            let requestedView = workspaceView
            selectedOccurrenceID = ""
            selectedTaskID = ""
            await store.loadTaskWorkspace(requestedView)
            guard !Task.isCancelled, requestedView == workspaceView else { return }
            loadedView = requestedView
            applyPendingTaskSelection()
        }
        .onChange(of: filter) {
            normalizeSelection()
        }
        .onChange(of: session.pendingWorkspaceEntitySelection) {
            applyPendingTaskSelection()
        }
    }

    @ViewBuilder
    private var content: some View {
        if store.isLoading && loadedView != workspaceView {
            ProgressView("正在加载任务…")
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if workspaceView == .draft {
            draftList
        } else {
            occurrenceList
        }
    }

    @ViewBuilder
    private var draftList: some View {
        if filteredDrafts.isEmpty {
            ContentUnavailableView(
                filter.isActive ? "没有匹配的草稿" : "没有任务草稿",
                systemImage: "doc.badge.ellipsis",
                description: Text("未发布的任务定义会显示在这里。")
            )
        } else {
            List(filteredDrafts) { task in
                TaskDefinitionRow(
                    task: task,
                    project: store.projectsByID[task.projectID],
                    selected: selectedTaskID == task.id
                ) {
                    selectedTaskID = task.id
                    selectedOccurrenceID = ""
                }
                .tag(task.id)
            }
            .listStyle(.inset)
        }
    }

    @ViewBuilder
    private var occurrenceList: some View {
        if filteredOccurrences.isEmpty {
            ContentUnavailableView(
                filter.isActive ? "没有匹配的任务" : emptyTitle,
                systemImage: workspaceView.systemImage,
                description: Text(emptyDescription)
            )
        } else {
            List {
                ForEach(collections) { collection in
                    if collection.occurrences.count > 1 {
                        OccurrenceCollectionRow(
                            collection: collection,
                            projects: store.projectsByID,
                            selectedOccurrenceID: selectedOccurrenceID,
                            isMutating: store.isMutating,
                            select: select,
                            toggle: toggle
                        )
                    } else if let occurrence = collection.occurrences.first {
                        OccurrenceRow(
                            occurrence: occurrence,
                            task: collection.task,
                            project: store.projectsByID[occurrence.projectID ?? collection.task?.projectID ?? ""],
                            selected: occurrence.id == selectedOccurrenceID,
                            isMutating: store.isMutating,
                            select: { select(occurrence) },
                            toggle: { await toggle(occurrence) }
                        )
                        .contextMenu {
                            if occurrence.executionStatus == .open {
                                Button("开始", systemImage: "play.fill") {
                                    Task { await start(occurrence) }
                                }
                            }
                            Button(
                                occurrence.executionStatus == .done ? "重新打开" : "完成",
                                systemImage: occurrence.executionStatus == .done ? "arrow.uturn.backward" : "checkmark"
                            ) {
                                Task { await toggle(occurrence) }
                            }
                            .disabled(occurrence.executionStatus.isTerminal && occurrence.executionStatus != .done)
                        }
                    }
                }
            }
            .listStyle(.inset)
            .focusable()
            .onMoveCommand(perform: moveSelection)
            .onKeyPress(.space) {
                guard let selectedOccurrence else { return .ignored }
                Task { await toggle(selectedOccurrence) }
                return .handled
            }
        }
    }

    private var emptyTitle: String {
        switch workspaceView {
        case .inbox: "任务收件箱已清空"
        case .today: "今天没有任务"
        case .upcoming: "接下来 7 天没有任务"
        case .overdue: "没有逾期任务"
        case .unscheduled: "没有未安排任务"
        case .recurring: "没有重复任务"
        case .completed: "最近 30 天没有完成记录"
        case .draft: "没有任务草稿"
        }
    }

    private var emptyDescription: String {
        workspaceView == .completed ? "完成任务后会在这里保留最近记录。" : "可以使用右上角的新建按钮添加任务。"
    }

    private func select(_ occurrence: OccurrenceV2) {
        selectedOccurrenceID = occurrence.id
        selectedTaskID = ""
    }

    private func clearFilters() {
        projectFilter = ""
        priorityFilter = nil
        statusFilter = nil
        dateFilter = .current
    }

    private func normalizeSelection() {
        if !selectedOccurrenceID.isEmpty,
           !filteredOccurrences.contains(where: { $0.id == selectedOccurrenceID }) {
            selectedOccurrenceID = ""
        }
        if !selectedTaskID.isEmpty,
           !filteredDrafts.contains(where: { $0.id == selectedTaskID }),
           workspaceView == .draft {
            selectedTaskID = ""
        }
    }

    private func applyPendingTaskSelection() {
        guard case .task(let taskID) = session.pendingWorkspaceEntitySelection else { return }
        guard !store.isLoading else { return }
        clearFilters()
        if let occurrence = store.occurrences.first(where: { $0.taskID == taskID && !$0.executionStatus.isTerminal })
            ?? store.occurrences.first(where: { $0.taskID == taskID }) {
            select(occurrence)
        } else if let task = store.tasks.first(where: { $0.id == taskID }) {
            if task.lifecycleStatus == .draft, workspaceView != .draft {
                workspaceView = .draft
                return
            }
            selectedTaskID = task.id
            selectedOccurrenceID = ""
        }
        session.consumeWorkspaceEntitySelection()
    }

    private func moveSelection(_ direction: MoveCommandDirection) {
        guard direction == .up || direction == .down, !filteredOccurrences.isEmpty else { return }
        let currentIndex = filteredOccurrences.firstIndex { $0.id == selectedOccurrenceID }
        let nextIndex: Int
        if let currentIndex {
            nextIndex = direction == .up
                ? max(filteredOccurrences.startIndex, currentIndex - 1)
                : min(filteredOccurrences.index(before: filteredOccurrences.endIndex), currentIndex + 1)
        } else {
            nextIndex = filteredOccurrences.startIndex
        }
        select(filteredOccurrences[nextIndex])
    }

    private func toggle(_ occurrence: OccurrenceV2) async {
        do {
            try await store.toggle(occurrence)
            await store.loadTaskWorkspace(workspaceView)
        } catch {
            store.errorMessage = error.localizedDescription
            if let apiError = error as? APIError, apiError.isRevisionConflict {
                await store.loadTaskWorkspace(workspaceView)
            }
        }
    }

    private func start(_ occurrence: OccurrenceV2) async {
        do {
            try await store.start(occurrence)
            await store.loadTaskWorkspace(workspaceView)
        } catch {
            store.errorMessage = error.localizedDescription
            if let apiError = error as? APIError, apiError.isRevisionConflict {
                await store.loadTaskWorkspace(workspaceView)
            }
        }
    }
}

private struct TaskFixedViewSelector: View {
    @Binding var selection: TaskWorkspaceView

    var body: some View {
        ScrollView(.horizontal) {
            HStack(spacing: 6) {
                ForEach(TaskWorkspaceView.allCases) { item in
                    Button {
                        selection = item
                    } label: {
                        Label(item.title, systemImage: item.systemImage)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 6)
                            .background(selection == item ? Color.accentColor.opacity(0.14) : .clear, in: .capsule)
                    }
                    .buttonStyle(.plain)
                    .accessibilityAddTraits(selection == item ? .isSelected : [])
                }
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
        }
        .scrollIndicators(.hidden)
    }
}

private struct TaskWorkspaceFilterBar: View {
    let store: WorkspaceStore
    @Binding var projectID: String
    @Binding var priority: Int?
    @Binding var status: ExecutionStatus?
    @Binding var date: TaskDateFilter
    let filtersEnabled: Bool
    let resultCount: Int
    let clear: () -> Void

    var body: some View {
        HStack(spacing: 10) {
            Picker("项目", selection: $projectID) {
                Text("全部项目").tag("")
                ForEach(store.projects) { project in Text(project.name).tag(project.id) }
            }
            .frame(maxWidth: 190)

            Picker("优先级", selection: $priority) {
                Text("全部优先级").tag(Optional<Int>.none)
                Text("紧急").tag(Optional(3))
                Text("高").tag(Optional(2))
                Text("中").tag(Optional(1))
                Text("普通").tag(Optional(0))
            }
            .frame(maxWidth: 150)

            Picker("状态", selection: $status) {
                Text("全部状态").tag(Optional<ExecutionStatus>.none)
                ForEach(ExecutionStatus.allCases, id: \.self) { item in
                    Text(executionStatusLabel(item)).tag(Optional(item))
                }
            }
            .frame(maxWidth: 145)
            .disabled(!filtersEnabled)

            Picker("日期", selection: $date) {
                ForEach(TaskDateFilter.allCases) { item in Text(item.title).tag(item) }
            }
            .frame(maxWidth: 145)
            .disabled(!filtersEnabled)

            if projectID != "" || priority != nil || status != nil || date != .current {
                Button("清除筛选", systemImage: "xmark.circle", action: clear)
                    .labelStyle(.iconOnly)
                    .help("清除筛选")
            }
            Spacer()
            Text("\(resultCount) 个结果")
                .foregroundStyle(.secondary)
                .monospacedDigit()
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
    }

    private func executionStatusLabel(_ status: ExecutionStatus) -> String {
        switch status {
        case .open: "未开始"
        case .active: "进行中"
        case .blocked: "已阻塞"
        case .done: "已完成"
        case .skipped: "已跳过"
        case .cancelled: "已取消"
        }
    }
}

private struct TaskDefinitionRow: View {
    let task: TaskV2
    let project: ProjectV2?
    let selected: Bool
    let select: () -> Void

    var body: some View {
        Button(action: select) {
            HStack(spacing: 12) {
                Image(systemName: "doc.badge.ellipsis")
                    .foregroundStyle(.secondary)
                VStack(alignment: .leading, spacing: 4) {
                    Text(task.title).fontWeight(.medium)
                    HStack(spacing: 8) {
                        if let project { Label(project.name, systemImage: "folder") }
                        Text("优先级 \(task.priority)")
                        Text("草稿")
                    }
                    .font(.caption)
                    .foregroundStyle(.secondary)
                }
                Spacer()
            }
            .padding(.vertical, 7)
            .padding(.horizontal, 10)
            .background(selected ? Color.accentColor.opacity(0.12) : .clear, in: .rect(cornerRadius: 9))
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("task-definition-row-\(task.id)")
    }
}
