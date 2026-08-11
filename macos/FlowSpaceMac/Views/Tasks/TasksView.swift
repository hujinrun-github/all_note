import AppKit
import SwiftUI

struct TasksView: View {
    @Environment(AppSession.self) private var session
    @Environment(\.openWindow) private var openWindow
    let store: WorkspaceStore
    @Binding var selectedOccurrenceID: String
    @Binding var selectedTaskID: String
    @Binding var workspaceView: TaskWorkspaceView
    let initialTaskID: String?
    let initialOccurrenceID: String?
    @State private var projectFilter = ""
    @State private var priorityFilter: Int?
    @State private var statusFilter: ExecutionStatus?
    @State private var dateFilter: TaskDateFilter = .current
    @State private var loadedView: TaskWorkspaceView?
    @State private var batchSelectionMode = false
    @State private var batchSelectedOccurrenceIDs: Set<String> = []
    @State private var batchSelectionAnchorID: String?
    @State private var taskSheet: TaskListSheetDestination?
    @State private var didApplyInitialSelection = false

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
            if workspaceView != .draft {
                TaskBatchSelectionHeader(
                    enabled: batchSelectionMode,
                    selectedCount: batchSelectedOccurrenceIDs.count,
                    resultCount: filteredOccurrences.count,
                    begin: { batchSelectionMode = true },
                    selectAll: selectAllFilteredOccurrences,
                    finish: finishBatchSelection
                )
                Divider()
            }
            content
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .task(id: workspaceView) {
            let requestedView = workspaceView
            selectedOccurrenceID = ""
            selectedTaskID = ""
            finishBatchSelection()
            await store.loadTaskWorkspace(requestedView)
            guard !Task.isCancelled, requestedView == workspaceView else { return }
            loadedView = requestedView
            applyInitialTaskSelection()
            applyPendingTaskSelection()
        }
        .onChange(of: filter) {
            normalizeSelection()
        }
        .onChange(of: session.pendingWorkspaceEntitySelection) {
            applyPendingTaskSelection()
        }
        .sheet(item: $taskSheet) { destination in
            Group {
                switch destination {
                case .batch(let request):
                    TaskBatchOperationSheet(
                        request: request,
                        projects: store.projects,
                        tasksByID: store.tasksByID
                    ) { action, occurrences in
                        let report = await store.performTaskBatch(action, occurrences: occurrences)
                        await store.loadTaskWorkspace(workspaceView)
                        if report.isCompleteSuccess {
                            finishBatchSelection()
                        } else {
                            normalizeBatchSelection()
                        }
                        return report
                    }
                case .edit(let task):
                    TaskDefinitionEditorSheet(task: task, store: store) {
                        await store.loadTaskWorkspace(workspaceView)
                    }
                case .reschedule(let entry):
                    let timezone = TimeZone(identifier: entry.timezone) ?? .current
                    CalendarEntryEditorSheet(entry: entry, timezone: timezone, store: store) {
                        await store.loadTaskWorkspace(workspaceView)
                    }
                    .environment(\.timeZone, timezone)
                case .block(let occurrence):
                    OccurrenceBlockSheet(occurrence: occurrence, store: store) {
                        await store.loadTaskWorkspace(workspaceView)
                    }
                case .move(let task):
                    TaskMoveSheet(task: task, store: store) {
                        await store.loadTaskWorkspace(workspaceView)
                    }
                case .lifecycle(let request):
                    TaskLifecycleConfirmationSheet(request: request, store: store) {
                        await store.loadTaskWorkspace(workspaceView)
                    }
                }
            }
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
                    selected: selectedTaskID == task.id,
                    select: {
                        selectedTaskID = task.id
                        selectedOccurrenceID = ""
                    },
                    open: { openTask(task) },
                    openInNewWorkspace: { openInNewWorkspace(task, view: .draft) }
                )
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
                ForEach(collections, id: \.id) { collection in
                    if collection.occurrences.count > 1 {
                        OccurrenceCollectionRow(
                            collection: collection,
                            projects: store.projectsByID,
                            selectedOccurrenceID: selectedOccurrenceID,
                            selectionMode: batchSelectionMode,
                            batchSelectedOccurrenceIDs: batchSelectedOccurrenceIDs,
                            isMutating: store.mutationsDisabled,
                            select: selectForCurrentMode,
                            toggleBatchSelection: toggleBatchSelection,
                            toggle: toggle,
                            open: { occurrence in
                                openOccurrence(occurrence)
                            },
                            contextAction: { occurrence, action in
                                handleContextAction(occurrence, action: action)
                            }
                        )
                    } else if let occurrence = collection.occurrences.first {
                        OccurrenceRow(
                            occurrence: occurrence,
                            task: collection.task,
                            project: store.projectsByID[occurrence.projectID ?? collection.task?.projectID ?? ""],
                            selected: occurrence.id == selectedOccurrenceID,
                            selectionMode: batchSelectionMode,
                            batchSelected: batchSelectedOccurrenceIDs.contains(occurrence.id),
                            isMutating: store.mutationsDisabled,
                            select: { selectForCurrentMode(occurrence, extending: $0) },
                            toggleBatchSelection: { toggleBatchSelection(occurrence) },
                            toggle: { await toggle(occurrence) },
                            open: { openOccurrence(occurrence) },
                            contextAction: { handleContextAction(occurrence, action: $0) }
                        )
                    }
                }
            }
            .listStyle(.inset)
            .focusable()
            .onMoveCommand(perform: moveSelection)
            .onKeyPress(.space) {
                guard let selectedOccurrence else { return .ignored }
                if batchSelectionMode {
                    toggleBatchSelection(selectedOccurrence)
                } else {
                    Task { await toggle(selectedOccurrence) }
                }
                return .handled
            }
            .onKeyPress("a", phases: .down) { press in
                guard batchSelectionMode, press.modifiers.contains(.command) else { return .ignored }
                selectAllFilteredOccurrences()
                return .handled
            }
            if batchSelectionMode && batchSelectedOccurrenceIDs.count >= 2 {
                Divider()
                TaskBatchActionBar(
                    selectedCount: batchSelectedOccurrenceIDs.count,
                    isDisabled: store.mutationsDisabled,
                    choose: presentBatchOperation
                )
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
        normalizeBatchSelection()
    }

    private func applyPendingTaskSelection() {
        guard let pendingSelection = session.pendingWorkspaceEntitySelection else { return }
        guard !store.isLoading else { return }
        let didApply = switch pendingSelection {
        case .task(let taskID):
            applyTaskSelection(taskID: taskID)
        case .occurrence(let occurrenceID):
            applyOccurrenceSelection(occurrenceID: occurrenceID)
        case .project, .note:
            false
        }
        guard didApply else { return }
        session.consumeWorkspaceEntitySelection()
    }

    private func applyInitialTaskSelection() {
        guard !didApplyInitialSelection, !store.isLoading else { return }
        if let initialOccurrenceID,
           applyOccurrenceSelection(occurrenceID: initialOccurrenceID) {
            didApplyInitialSelection = true
        } else if let initialTaskID,
                  applyTaskSelection(taskID: initialTaskID) {
            didApplyInitialSelection = true
        }
    }

    @discardableResult
    private func applyOccurrenceSelection(occurrenceID: String) -> Bool {
        clearFilters()
        guard let occurrence = store.occurrences.first(where: { $0.id == occurrenceID }) else {
            return false
        }
        select(occurrence)
        return true
    }

    @discardableResult
    private func applyTaskSelection(taskID: String) -> Bool {
        clearFilters()
        if let occurrence = store.occurrences.first(where: { $0.taskID == taskID && !$0.executionStatus.isTerminal })
            ?? store.occurrences.first(where: { $0.taskID == taskID }) {
            select(occurrence)
            return true
        } else if let task = store.tasks.first(where: { $0.id == taskID }) {
            if task.lifecycleStatus == .draft, workspaceView != .draft {
                workspaceView = .draft
                return false
            }
            selectedTaskID = task.id
            selectedOccurrenceID = ""
            return true
        }
        return false
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
        let occurrence = filteredOccurrences[nextIndex]
        if batchSelectionMode, NSEvent.modifierFlags.contains(.shift) {
            extendBatchSelection(to: occurrence)
            selectedOccurrenceID = occurrence.id
            selectedTaskID = ""
        } else {
            select(occurrence)
        }
    }

    private func selectForCurrentMode(_ occurrence: OccurrenceV2, extending: Bool) {
        if batchSelectionMode {
            if extending {
                extendBatchSelection(to: occurrence)
            } else {
                toggleBatchSelection(occurrence)
            }
        }
        select(occurrence)
    }

    private func toggleBatchSelection(_ occurrence: OccurrenceV2) {
        if batchSelectedOccurrenceIDs.contains(occurrence.id) {
            batchSelectedOccurrenceIDs.remove(occurrence.id)
        } else {
            batchSelectedOccurrenceIDs.insert(occurrence.id)
        }
        batchSelectionAnchorID = occurrence.id
        selectedOccurrenceID = occurrence.id
        selectedTaskID = ""
    }

    private func extendBatchSelection(to occurrence: OccurrenceV2) {
        let orderedIDs = filteredOccurrences.map(\.id)
        let anchorID = batchSelectionAnchorID ?? selectedOccurrenceID
        guard !anchorID.isEmpty else {
            toggleBatchSelection(occurrence)
            return
        }
        batchSelectedOccurrenceIDs.formUnion(
            TaskBatchPlanner.selectionRange(from: anchorID, through: occurrence.id, in: orderedIDs)
        )
    }

    private func selectAllFilteredOccurrences() {
        batchSelectionMode = true
        batchSelectedOccurrenceIDs = Set(filteredOccurrences.map(\.id))
        batchSelectionAnchorID = filteredOccurrences.first?.id
    }

    private func finishBatchSelection() {
        batchSelectionMode = false
        batchSelectedOccurrenceIDs.removeAll()
        batchSelectionAnchorID = nil
    }

    private func normalizeBatchSelection() {
        let visibleIDs = Set(filteredOccurrences.map(\.id))
        batchSelectedOccurrenceIDs.formIntersection(visibleIDs)
        if let anchor = batchSelectionAnchorID, !visibleIDs.contains(anchor) {
            batchSelectionAnchorID = nil
        }
    }

    private func presentBatchOperation(_ kind: TaskBatchOperationKind) {
        let selected = filteredOccurrences.filter { batchSelectedOccurrenceIDs.contains($0.id) }
        guard selected.count >= 2 else { return }
        taskSheet = .batch(TaskBatchRequest(kind: kind, occurrences: selected))
    }

    private func openOccurrence(_ occurrence: OccurrenceV2) {
        guard let task = store.tasksByID[occurrence.taskID] else {
            store.errorMessage = "找不到这次执行对应的任务定义。"
            return
        }
        if NSEvent.modifierFlags.contains(.option) {
            openInNewWorkspace(task, view: workspaceView)
        } else {
            openTask(task)
        }
    }

    private func openTask(_ task: TaskV2) {
        taskSheet = .edit(task)
    }

    private func openInNewWorkspace(_ task: TaskV2, view: TaskWorkspaceView) {
        openWindow(value: WorkspaceWindowRoute.task(task.id, view: view))
    }

    private func handleContextAction(_ occurrence: OccurrenceV2, action: OccurrenceRowContextAction) {
        switch action {
        case .start:
            Task { await start(occurrence) }
        case .block:
            taskSheet = .block(occurrence)
        case .unblock:
            Task { await unblock(occurrence) }
        case .toggleCompletion:
            Task { await toggle(occurrence) }
        case .editTask:
            openOccurrenceForEditing(occurrence)
        case .reschedule:
            presentReschedule(occurrence)
        case .copyLink:
            copyTaskLink(occurrence)
        case .moveProject:
            withTask(for: occurrence) { taskSheet = .move($0) }
        case .cancelTask:
            withTask(for: occurrence) {
                taskSheet = .lifecycle(TaskLifecycleRequest(task: $0, command: .cancel))
            }
        case .archiveTask:
            withTask(for: occurrence) {
                taskSheet = .lifecycle(TaskLifecycleRequest(task: $0, command: .archive))
            }
        case .openInNewWorkspace:
            withTask(for: occurrence) { openInNewWorkspace($0, view: workspaceView) }
        }
    }

    private func withTask(for occurrence: OccurrenceV2, perform: (TaskV2) -> Void) {
        guard let task = store.tasksByID[occurrence.taskID] else {
            store.errorMessage = "找不到这次执行对应的任务定义。"
            return
        }
        perform(task)
    }

    private func copyTaskLink(_ occurrence: OccurrenceV2) {
        guard let baseURL = session.baseURL,
              let url = WorkspaceWebLink.task(
                baseURL: baseURL,
                taskID: occurrence.taskID,
                view: workspaceView
              ) else {
            store.errorMessage = "无法生成任务链接。"
            return
        }
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.setString(url.absoluteString, forType: .string)
    }

    private func openOccurrenceForEditing(_ occurrence: OccurrenceV2) {
        guard let task = store.tasksByID[occurrence.taskID] else {
            store.errorMessage = "找不到这次执行对应的任务定义。"
            return
        }
        openTask(task)
    }

    private func presentReschedule(_ occurrence: OccurrenceV2) {
        guard let task = store.tasksByID[occurrence.taskID],
              let entry = CalendarEntryV2(
                occurrence: occurrence,
                task: task,
                project: store.projectsByID[occurrence.projectID ?? task.projectID]
              ) else {
            store.errorMessage = "无法读取这次执行的安排信息。"
            return
        }
        taskSheet = .reschedule(entry)
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

    private func unblock(_ occurrence: OccurrenceV2) async {
        do {
            try await store.unblock(occurrence)
            await store.loadTaskWorkspace(workspaceView)
        } catch {
            store.errorMessage = error.localizedDescription
            if let apiError = error as? APIError, apiError.isRevisionConflict {
                await store.loadTaskWorkspace(workspaceView)
            }
        }
    }
}

private enum TaskListSheetDestination: Identifiable {
    case batch(TaskBatchRequest)
    case edit(TaskV2)
    case reschedule(CalendarEntryV2)
    case block(OccurrenceV2)
    case move(TaskV2)
    case lifecycle(TaskLifecycleRequest)

    var id: String {
        switch self {
        case .batch(let request): "batch-\(request.id)"
        case .edit(let task): "edit-\(task.id)"
        case .reschedule(let entry): "reschedule-\(entry.occurrenceID)"
        case .block(let occurrence): "block-\(occurrence.id)"
        case .move(let task): "move-\(task.id)"
        case .lifecycle(let request): request.id
        }
    }
}

private struct TaskLifecycleRequest: Identifiable {
    let task: TaskV2
    let command: TaskLifecycleCommand

    var id: String { "\(command.rawValue)-\(task.id)" }

    var title: String {
        command == .cancel ? "取消这个任务？" : "归档这个任务？"
    }

    var message: String {
        if command == .cancel {
            return "任务定义及其未结束执行会按照服务端规则取消；已有历史记录会保留。"
        }
        return "归档后任务会从普通视图隐藏，执行历史仍由服务端保留。"
    }

    var confirmationTitle: String { command == .cancel ? "取消任务" : "归档" }
}

private struct TaskMoveSheet: View {
    @Environment(\.dismiss) private var dismiss
    let task: TaskV2
    let store: WorkspaceStore
    let onSaved: () async -> Void
    @State private var projectID: String
    @State private var isSaving = false
    @State private var errorMessage: String?

    init(task: TaskV2, store: WorkspaceStore, onSaved: @escaping () async -> Void) {
        self.task = task
        self.store = store
        self.onSaved = onSaved
        _projectID = State(initialValue: task.projectID)
    }

    var body: some View {
        NavigationStack {
            Form {
                Picker("目标项目", selection: $projectID) {
                    ForEach(store.projects) { project in
                        Text(project.name).tag(project.id)
                    }
                }
                if let errorMessage {
                    Label(errorMessage, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.red)
                }
            }
            .formStyle(.grouped)
            .navigationTitle("移动任务")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("取消") { dismiss() }.disabled(isSaving)
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("移动") { Task { await save() } }
                        .buttonStyle(.borderedProminent)
                        .disabled(
                            isSaving || store.mutationsDisabled
                                || projectID.isEmpty || projectID == task.projectID
                        )
                }
            }
        }
        .frame(width: 480, height: 260)
        .interactiveDismissDisabled(isSaving)
    }

    private func save() async {
        isSaving = true
        errorMessage = nil
        defer { isSaving = false }
        do {
            try await store.moveTask(task, to: projectID)
            await onSaved()
            dismiss()
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}

private struct TaskLifecycleConfirmationSheet: View {
    @Environment(\.dismiss) private var dismiss
    let request: TaskLifecycleRequest
    let store: WorkspaceStore
    let onSaved: () async -> Void
    @State private var isSaving = false
    @State private var errorMessage: String?

    var body: some View {
        NavigationStack {
            VStack(alignment: .leading, spacing: 18) {
                Label(request.title, systemImage: "exclamationmark.triangle")
                    .font(.title3.weight(.semibold))
                    .foregroundStyle(.red)
                Text(request.message).foregroundStyle(.secondary)
                if let errorMessage {
                    Label(errorMessage, systemImage: "xmark.octagon")
                        .foregroundStyle(.red)
                }
                Spacer()
            }
            .padding(24)
            .navigationTitle(request.task.title)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("再想想") { dismiss() }.disabled(isSaving)
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button(request.confirmationTitle, role: .destructive) {
                        Task { await save() }
                    }
                    .disabled(isSaving || store.mutationsDisabled)
                }
            }
        }
        .frame(width: 500, height: 280)
        .interactiveDismissDisabled(isSaving)
    }

    private func save() async {
        isSaving = true
        errorMessage = nil
        defer { isSaving = false }
        do {
            try await store.executeTaskLifecycle(request.task, command: request.command)
            await onSaved()
            dismiss()
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}

private struct TaskBatchSelectionHeader: View {
    let enabled: Bool
    let selectedCount: Int
    let resultCount: Int
    let begin: () -> Void
    let selectAll: () -> Void
    let finish: () -> Void

    var body: some View {
        HStack(spacing: 10) {
            if enabled {
                Label("已选择 \(selectedCount) 项", systemImage: "checkmark.square")
                    .fontWeight(.medium)
                Text("Shift 点按或 Shift ↑/↓ 连续选择")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Spacer()
                Button(selectedCount == resultCount ? "已全选" : "选择当前筛选结果", action: selectAll)
                    .disabled(resultCount == 0 || selectedCount == resultCount)
                    .keyboardShortcut("a", modifiers: .command)
                Button("完成选择", action: finish)
            } else {
                Spacer()
                Button("批量选择", systemImage: "checkmark.square", action: begin)
                    .disabled(resultCount == 0)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 7)
        .background(enabled ? Color.accentColor.opacity(0.055) : .clear)
    }
}

private struct TaskBatchActionBar: View {
    let selectedCount: Int
    let isDisabled: Bool
    let choose: (TaskBatchOperationKind) -> Void

    var body: some View {
        HStack(spacing: 8) {
            Text("\(selectedCount) 项")
                .font(.callout.monospacedDigit().weight(.semibold))
            Divider().frame(height: 18)
            Button("完成", systemImage: "checkmark.circle") { choose(.complete) }
            Button("移动", systemImage: "folder") { choose(.move) }
            Button("优先级", systemImage: "flag") { choose(.priority) }
            Button("改期", systemImage: "calendar.badge.clock") { choose(.reschedule) }
            Spacer()
            Button("取消任务", systemImage: "xmark.circle") { choose(.cancel) }
                .tint(.red)
        }
        .buttonStyle(.bordered)
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
        .background(.bar)
        .disabled(isDisabled)
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
                            .lineLimit(1)
                            .fixedSize(horizontal: true, vertical: false)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 6)
                            .background(
                                selection == item ? Color.accentColor.opacity(0.14) : .clear,
                                in: .capsule
                            )
                            .contentShape(.capsule)
                    }
                    .buttonStyle(.plain)
                    .fixedSize(horizontal: true, vertical: false)
                    .accessibilityIdentifier("task-workspace-view-\(item.rawValue)")
                    .accessibilityAddTraits(selection == item ? .isSelected : [])
                }
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
        }
        .scrollIndicators(.hidden)
        .frame(height: 52)
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
    let open: () -> Void
    let openInNewWorkspace: () -> Void

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
        .simultaneousGesture(TapGesture(count: 2).onEnded { open() })
        .contextMenu {
            Button("编辑任务", systemImage: "pencil", action: open)
            Divider()
            Button("在新工作区打开", systemImage: "macwindow.badge.plus", action: openInNewWorkspace)
        }
    }
}
