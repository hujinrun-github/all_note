import SwiftUI

struct WorkspaceView: View {
    @Environment(AppSession.self) private var session
    @Environment(\.openWindow) private var openWindow
    @SceneStorage("workspace.destination") private var destinationRaw = WorkspaceDestination.today.rawValue
    @SceneStorage("workspace.selectedOccurrence") private var selectedOccurrenceID = ""
    @SceneStorage("workspace.selectedTaskDefinition") private var selectedTaskDefinitionID = ""
    @SceneStorage("workspace.taskView") private var taskViewRaw = TaskWorkspaceView.upcoming.rawValue
    @SceneStorage("workspace.columnVisibility") private var columnVisibilityRaw = "all"
    @State private var taskDraft: TaskDraft?
    @State private var didApplyInitialRoute = false

    let store: WorkspaceStore
    let initialRoute: WorkspaceWindowRoute?

    init(store: WorkspaceStore, initialRoute: WorkspaceWindowRoute? = nil) {
        self.store = store
        self.initialRoute = initialRoute
    }

    private var destination: Binding<WorkspaceDestination?> {
        Binding(
            get: { WorkspaceDestination(rawValue: destinationRaw) ?? .today },
            set: {
                destinationRaw = ($0 ?? .today).rawValue
                selectedOccurrenceID = ""
                selectedTaskDefinitionID = ""
            }
        )
    }

    private var taskWorkspaceView: Binding<TaskWorkspaceView> {
        Binding(
            get: { TaskWorkspaceView(rawValue: taskViewRaw) ?? .upcoming },
            set: {
                taskViewRaw = $0.rawValue
                selectedOccurrenceID = ""
                selectedTaskDefinitionID = ""
            }
        )
    }

    private var columnVisibility: Binding<NavigationSplitViewVisibility> {
        Binding(
            get: {
                switch columnVisibilityRaw {
                case "detailOnly": .detailOnly
                case "doubleColumn": .doubleColumn
                default: .all
                }
            },
            set: { value in
                if value == .detailOnly {
                    columnVisibilityRaw = "detailOnly"
                } else if value == .doubleColumn {
                    columnVisibilityRaw = "doubleColumn"
                } else {
                    columnVisibilityRaw = "all"
                }
            }
        )
    }

    private var selectedOccurrence: OccurrenceV2? {
        store.occurrences.first { $0.id == selectedOccurrenceID }
    }

    private var selectedTaskDefinition: TaskV2? {
        store.tasks.first { $0.id == selectedTaskDefinitionID }
    }

    var body: some View {
        NavigationSplitView(columnVisibility: columnVisibility) {
            WorkspaceSidebar(selection: destination)
                .navigationSplitViewColumnWidth(min: 180, ideal: 220, max: 280)
        } content: {
            destinationContent
                .navigationSplitViewColumnWidth(min: 480, ideal: 700)
        } detail: {
            if let selectedOccurrence {
                OccurrenceInspector(
                    occurrence: selectedOccurrence,
                    store: store,
                    refresh: refreshCurrentDestination
                )
            } else if let selectedTaskDefinition {
                TaskDefinitionInspector(
                    task: selectedTaskDefinition,
                    store: store,
                    refresh: refreshCurrentDestination,
                    close: { selectedTaskDefinitionID = "" }
                )
            } else {
                ContentUnavailableView(
                    "选择一项查看详情",
                    systemImage: "sidebar.right",
                    description: Text("任务定义、执行状态和安排会显示在这里。")
                )
            }
        }
        .navigationTitle((WorkspaceDestination(rawValue: destinationRaw) ?? .today).title)
        .toolbar {
            ToolbarItemGroup(placement: .primaryAction) {
                Button {
                    Task { await refreshCurrentDestination() }
                } label: {
                    Label("刷新", systemImage: "arrow.clockwise")
                }
                .disabled(store.isLoading)

                Button {
                    openWindow(id: "global-search")
                } label: {
                    Label("全局搜索", systemImage: "magnifyingglass")
                }

                Button {
                    var draft = TaskDraft()
                    draft.projectID = store.defaultProjectID
                    taskDraft = draft
                } label: {
                    Label("新建任务", systemImage: "plus")
                }
            }
        }
        .safeAreaInset(edge: .top, spacing: 0) {
            if session.connectivity.isOffline {
                OfflineReadOnlyBanner(
                    lastSuccessfulResponseAt: session.connectivity.lastSuccessfulResponseAt,
                    retry: { Task { await session.retryConnection() } }
                )
            }
        }
        .sheet(item: $taskDraft) { draft in
            TaskEditorSheet(draft: draft, store: store) {
                await refreshCurrentDestination()
            }
        }
        .overlay(alignment: .bottom) {
            if let error = store.errorMessage {
                ErrorBanner(message: error) { store.errorMessage = nil }
                    .padding()
            }
        }
        .task {
            applyInitialWorkspaceRoute()
            applyPendingWorkspaceDestination()
        }
        .onChange(of: session.pendingWorkspaceDestination) {
            applyPendingWorkspaceDestination()
        }
    }

    @ViewBuilder
    private var destinationContent: some View {
        switch WorkspaceDestination(rawValue: destinationRaw) ?? .today {
        case .today:
            TodayView(store: store, selectedOccurrenceID: $selectedOccurrenceID)
        case .tasks:
            TasksView(
                store: store,
                selectedOccurrenceID: $selectedOccurrenceID,
                selectedTaskID: $selectedTaskDefinitionID,
                workspaceView: taskWorkspaceView,
                initialTaskID: initialTaskID,
                initialOccurrenceID: initialOccurrenceID
            )
        case .inbox:
            InboxView(store: store, selectedOccurrenceID: $selectedOccurrenceID)
        case .projects:
            ProjectsView(
                store: store,
                selectedOccurrenceID: $selectedOccurrenceID,
                initialProjectID: initialProjectID
            )
        case .calendar:
            CalendarWorkspaceView(store: store)
        case .notes:
            NotesLibraryView(store: store)
        case .review:
            DailySummaryView(
                store: store,
                navigateToOccurrence: navigateToSummaryOccurrence,
                navigateToNote: navigateToSummaryNote
            )
        }
    }

    private func refreshCurrentDestination() async {
        switch WorkspaceDestination(rawValue: destinationRaw) ?? .today {
        case .today:
            await store.load(scope: .today)
        case .tasks, .inbox:
            if WorkspaceDestination(rawValue: destinationRaw) == .tasks {
                await store.loadTaskWorkspace(TaskWorkspaceView(rawValue: taskViewRaw) ?? .upcoming)
            } else {
                await store.loadAllTasks()
            }
        case .projects:
            await store.loadProjects()
        case .calendar:
            await store.loadCalendar(containing: Date())
        case .notes, .review:
            if WorkspaceDestination(rawValue: destinationRaw) == .notes {
                await store.loadNotes()
            } else {
                await store.loadSummary(period: .week)
            }
        }
    }

    private func applyPendingWorkspaceDestination() {
        guard let requested = session.pendingWorkspaceDestination else { return }
        destinationRaw = requested.rawValue
        selectedOccurrenceID = ""
        selectedTaskDefinitionID = ""
        session.consumeWorkspaceDestination()
    }

    private var initialTaskID: String? {
        guard let selection = initialRoute?.selection,
              case .task(let taskID) = selection else { return nil }
        return taskID
    }

    private var initialProjectID: String? {
        guard let selection = initialRoute?.selection,
              case .project(let projectID) = selection else { return nil }
        return projectID
    }

    private var initialOccurrenceID: String? {
        guard let selection = initialRoute?.selection,
              case .occurrence(let occurrenceID) = selection else { return nil }
        return occurrenceID
    }

    private func navigateToSummaryOccurrence(_ occurrenceID: String, view: TaskWorkspaceView) {
        taskViewRaw = view.rawValue
        selectedOccurrenceID = ""
        selectedTaskDefinitionID = ""
        session.requestWorkspaceEntitySelection(.occurrence(occurrenceID))
        destinationRaw = WorkspaceDestination.tasks.rawValue
    }

    private func navigateToSummaryNote(_ noteID: String) {
        selectedOccurrenceID = ""
        selectedTaskDefinitionID = ""
        session.requestWorkspaceEntitySelection(.note(noteID))
        destinationRaw = WorkspaceDestination.notes.rawValue
    }

    private func applyInitialWorkspaceRoute() {
        guard !didApplyInitialRoute, let initialRoute else { return }
        didApplyInitialRoute = true
        destinationRaw = initialRoute.destination.rawValue
        if let taskView = initialRoute.taskView {
            taskViewRaw = taskView.rawValue
        }
        selectedOccurrenceID = ""
        selectedTaskDefinitionID = ""
    }
}

private struct WorkspaceSidebar: View {
    @Environment(AppSession.self) private var session
    @Environment(\.openWindow) private var openWindow
    @Binding var selection: WorkspaceDestination?

    var body: some View {
        List(selection: $selection) {
            Section("聚焦") {
                SidebarRow(.today)
                SidebarRow(.tasks)
                SidebarRow(.inbox)
            }
            Section("计划") {
                SidebarRow(.projects)
                SidebarRow(.calendar)
            }
            Section("知识") {
                SidebarRow(.notes)
            }
            Section("回顾") {
                SidebarRow(.review)
            }
        }
        .listStyle(.sidebar)
        .safeAreaInset(edge: .bottom) {
            Menu {
                SettingsLink { Label("设置", systemImage: "gear") }
                if session.isAdmin {
                    Button("账号管理") { openWindow(id: "account-admin") }
                }
                Divider()
                Button("退出登录") { Task { await session.logout() } }
            } label: {
                HStack(spacing: 10) {
                    Image(systemName: "person.crop.circle.fill")
                        .font(.title2)
                    VStack(alignment: .leading, spacing: 1) {
                        Text(session.currentUser?.user.displayName ?? "FlowSpace")
                            .lineLimit(1)
                        connectionLabel
                    }
                    Spacer()
                    Image(systemName: "chevron.up.chevron.down")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                .contentShape(.rect)
                .padding(.horizontal, 12)
                .padding(.vertical, 10)
            }
            .menuStyle(.borderlessButton)
            .padding(8)
            .background(.bar)
        }
    }

    @ViewBuilder
    private var connectionLabel: some View {
        if session.connectivity.isOffline {
            Label("离线 · 只读", systemImage: "wifi.slash")
                .foregroundStyle(.orange)
        } else if case .checking = session.connectivity.state {
            Label("正在检查连接", systemImage: "circle.dotted")
                .foregroundStyle(.secondary)
        } else {
            Label("在线", systemImage: "circle.fill")
                .foregroundStyle(.green)
        }
    }
}

struct OfflineReadOnlyBanner: View {
    let lastSuccessfulResponseAt: Date?
    let retry: () -> Void

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: "wifi.slash")
            VStack(alignment: .leading, spacing: 2) {
                Text("离线 · 只读")
                    .fontWeight(.semibold)
                if let lastSuccessfulResponseAt {
                    Text("正在显示最近加载的内容 · 上次连接于 \(lastSuccessfulResponseAt.formatted(date: .omitted, time: .shortened))")
                        .font(.caption)
                } else {
                    Text("恢复连接后才能保存更改")
                        .font(.caption)
                }
            }
            Spacer()
            Button("重试连接", systemImage: "arrow.clockwise", action: retry)
        }
        .foregroundStyle(.orange)
        .padding(.horizontal, 16)
        .padding(.vertical, 9)
        .background(.orange.opacity(0.1))
        .overlay(alignment: .bottom) { Divider() }
    }
}

private struct SidebarRow: View {
    private let destination: WorkspaceDestination

    init(_ destination: WorkspaceDestination) {
        self.destination = destination
    }

    var body: some View {
        Label(destination.title, systemImage: destination.systemImage)
            .tag(destination)
    }
}

struct ErrorBanner: View {
    let message: String
    let dismiss: () -> Void

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(.orange)
            Text(message)
                .lineLimit(2)
            Spacer()
            Button("关闭", systemImage: "xmark", action: dismiss)
                .labelStyle(.iconOnly)
                .buttonStyle(.plain)
        }
        .padding(12)
        .background(.regularMaterial, in: .rect(cornerRadius: 12))
        .shadow(radius: 8, y: 3)
        .frame(maxWidth: 560)
        .accessibilityElement(children: .combine)
    }
}

struct ModulePlaceholder: View {
    let title: String
    let message: String
    let systemImage: String

    var body: some View {
        ContentUnavailableView(title, systemImage: systemImage, description: Text(message))
    }
}
