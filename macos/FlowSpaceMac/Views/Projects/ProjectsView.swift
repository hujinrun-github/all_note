import SwiftUI

struct ProjectsView: View {
    enum Section: String, CaseIterable, Identifiable {
        case overview
        case tasks
        case schedule
        case notes
        case roadmap
        case history

        var id: String { rawValue }
        var title: String {
            switch self {
            case .overview: "概览"
            case .tasks: "任务"
            case .schedule: "日程"
            case .notes: "笔记"
            case .roadmap: "学习路线"
            case .history: "历史"
            }
        }
    }

    @Environment(AppSession.self) private var session
    @Environment(\.openWindow) private var openWindow
    let store: WorkspaceStore
    @Binding var selectedOccurrenceID: String
    @State private var selectedProjectID = ""
    @State private var section: Section = .overview
    @State private var query = ""
    @State private var statusFilter: ProjectStatus?
    @State private var editorContext: ProjectEditorContext?
    @State private var completionProject: ProjectV2?
    @State private var deleteProject: ProjectV2?

    private var selectedProject: ProjectV2? {
        store.projects.first { $0.id == selectedProjectID }
    }

    private var projectTasks: [TaskV2] {
        store.tasks.filter { $0.projectID == selectedProjectID }
    }

    private var projectOccurrences: [OccurrenceV2] {
        store.occurrences.filter {
            $0.projectID == selectedProjectID || store.tasksByID[$0.taskID]?.projectID == selectedProjectID
        }
    }

    private var projectNotes: [FlowNote] {
        store.notes.filter { note in note.projects.contains { $0.id == selectedProjectID } }
    }

    private var visibleSections: [Section] {
        Section.allCases.filter { $0 != .roadmap || selectedProject?.kind == .learning }
    }

    private var visibleProjects: [ProjectV2] {
        let keyword = query.trimmingCharacters(in: .whitespacesAndNewlines)
        return store.projects.filter { project in
            let statusMatches = statusFilter.map { project.status == $0 }
                ?? (project.status != .archived)
            return statusMatches && (keyword.isEmpty || project.name.localizedCaseInsensitiveContains(keyword))
        }
    }

    var body: some View {
        HSplitView {
            VStack(spacing: 8) {
                HStack(spacing: 6) {
                    TextField("搜索项目", text: $query)
                        .textFieldStyle(.roundedBorder)
                    Button("新建项目", systemImage: "plus") {
                        editorContext = ProjectEditorContext(project: nil)
                    }
                    .labelStyle(.iconOnly)
                    .help("新建项目")
                }

                Picker("项目状态", selection: $statusFilter) {
                    Text("进行中的项目").tag(Optional<ProjectStatus>.none)
                    ForEach(ProjectStatus.allCases, id: \.self) { status in
                        Text(statusLabel(status)).tag(Optional(status))
                    }
                }
                .labelsHidden()

                List(visibleProjects, selection: $selectedProjectID) { project in
                    VStack(alignment: .leading, spacing: 4) {
                        Text(project.name).fontWeight(.medium)
                        HStack {
                            Text(project.kind == .learning ? "学习项目" : "标准项目")
                            Text("·")
                            Text(statusLabel(project.status))
                        }
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    }
                    .tag(project.id)
                    .padding(.vertical, 4)
                    .contextMenu {
                        if canEdit(project) {
                            Button("编辑项目") { editorContext = ProjectEditorContext(project: project) }
                        }
                        if canDelete(project) {
                            Button("删除项目", role: .destructive) { deleteProject = project }
                        }
                    }
                }
            }
            .padding(10)
            .frame(minWidth: 210, idealWidth: 250)

            if let selectedProject {
                VStack(alignment: .leading, spacing: 0) {
                    projectHeader(selectedProject)
                    Divider()
                    sectionContent(selectedProject)
                }
                .frame(minWidth: 440)
            } else {
                ContentUnavailableView("选择一个项目", systemImage: "folder")
                    .frame(minWidth: 420)
            }
        }
        .task {
            await store.loadProjects()
            applyPendingProjectSelection()
            if selectedProjectID.isEmpty { selectedProjectID = store.projects.first?.id ?? "" }
        }
        .onChange(of: session.pendingWorkspaceEntitySelection) {
            applyPendingProjectSelection()
        }
        .onChange(of: selectedProjectID) {
            section = .overview
            selectedOccurrenceID = ""
        }
        .sheet(item: $editorContext) { context in
            ProjectEditorSheet(project: context.project, store: store) { projectID in
                selectedProjectID = projectID
                await store.loadProjects()
            }
        }
        .sheet(item: $completionProject) { project in
            ProjectCompletionSheet(project: project, store: store) {
                await store.loadProjects()
            }
        }
        .sheet(item: $deleteProject) { project in
            ProjectDeletionSheet(project: project, store: store) {
                selectedProjectID = ""
                await store.loadProjects()
                selectedProjectID = visibleProjects.first?.id ?? ""
            }
        }
    }

    private func projectHeader(_ project: ProjectV2) -> some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack {
                VStack(alignment: .leading, spacing: 5) {
                    Text(project.name)
                        .font(.largeTitle.weight(.semibold))
                    HStack {
                        Label(project.kind == .learning ? "学习项目" : "标准项目", systemImage: "folder")
                        Text(statusLabel(project.status))
                        Text("revision \(project.revision)")
                    }
                    .foregroundStyle(.secondary)
                }
                Spacer()
                VStack(alignment: .trailing, spacing: 8) {
                    Text("\(projectTasks.count) 个任务")
                        .font(.headline)
                        .foregroundStyle(.secondary)
                    projectActions(project)
                }
            }

            ScrollView(.horizontal) {
                HStack(spacing: 6) {
                    ForEach(visibleSections) { item in
                        Button {
                            section = item
                        } label: {
                            Text(item.title)
                                .padding(.horizontal, 12)
                                .padding(.vertical, 6)
                                .background(section == item ? Color.accentColor.opacity(0.15) : Color.clear, in: .capsule)
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
            .scrollIndicators(.hidden)
        }
        .padding(20)
    }

    @ViewBuilder
    private func projectActions(_ project: ProjectV2) -> some View {
        if project.systemRole == nil || project.systemRole?.isEmpty == true {
            HStack(spacing: 7) {
                if canEdit(project) {
                    Button("编辑") { editorContext = ProjectEditorContext(project: project) }
                }
                switch project.status {
                case .planning:
                    Button("开始项目") { run(project, command: .activate) }
                        .buttonStyle(.borderedProminent)
                case .active:
                    Button("完成项目") { beginCompletion(project) }
                        .buttonStyle(.borderedProminent)
                    Menu("更多") {
                        Button("暂停项目") { run(project, command: .pause) }
                        Button("归档项目") { run(project, command: .archive) }
                        Divider()
                        Button("删除项目", role: .destructive) { deleteProject = project }
                    }
                case .paused:
                    Button("继续项目") { run(project, command: .resume) }
                        .buttonStyle(.borderedProminent)
                    Menu("更多") {
                        Button("归档项目") { run(project, command: .archive) }
                        Button("删除项目", role: .destructive) { deleteProject = project }
                    }
                case .completed:
                    Button("恢复为进行中") { run(project, command: .restore, restoreTo: .active) }
                    Menu("更多") {
                        Button("归档项目") { run(project, command: .archive) }
                        Button("删除项目", role: .destructive) { deleteProject = project }
                    }
                case .archived:
                    Button("恢复项目") { run(project, command: .restore, restoreTo: .active) }
                    Button("删除项目", role: .destructive) { deleteProject = project }
                }
            }
            .disabled(store.isMutating)
        }
    }

    @ViewBuilder
    private func sectionContent(_ project: ProjectV2) -> some View {
        switch section {
        case .overview:
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    HStack(spacing: 12) {
                        ProjectMetric(title: "任务定义", value: projectTasks.count, color: .blue)
                        ProjectMetric(title: "待执行", value: projectOccurrences.count { !$0.executionStatus.isTerminal }, color: .orange)
                        ProjectMetric(title: "已完成", value: projectOccurrences.count { $0.executionStatus == .done }, color: .green)
                        ProjectMetric(title: "项目笔记", value: projectNotes.count, color: .purple)
                    }
                    GroupBox("正在推进") {
                        taskList(Array(projectTasks.prefix(6)))
                    }
                    GroupBox("近期日程") {
                        occurrenceList(projectOccurrences.filter { !$0.executionStatus.isTerminal }.prefixArray(6))
                    }
                }
                .padding(18)
            }
        case .tasks:
            taskList(projectTasks)
        case .schedule:
            occurrenceList(projectOccurrences.filter { !$0.executionStatus.isTerminal })
        case .notes:
            if projectNotes.isEmpty {
                ContentUnavailableView(
                    "还没有项目笔记",
                    systemImage: "note.text",
                    description: Text("在笔记编辑窗口中选择本项目即可建立关联。")
                )
            } else {
                List(projectNotes) { note in
                    Button {
                        openWindow(value: note.id)
                    } label: {
                        VStack(alignment: .leading, spacing: 4) {
                            Text(note.title).fontWeight(.medium)
                            Text(note.plainTextPreview).font(.caption).foregroundStyle(.secondary).lineLimit(2)
                        }
                    }
                    .buttonStyle(.plain)
                }
                .listStyle(.inset)
            }
        case .roadmap:
            RoadmapProjectView(
                project: project,
                store: store
            )
        case .history:
            occurrenceList(projectOccurrences.filter { $0.executionStatus.isTerminal })
        }
    }

    @ViewBuilder
    private func taskList(_ tasks: [TaskV2]) -> some View {
        if tasks.isEmpty {
            ContentUnavailableView("项目中还没有任务", systemImage: "checklist")
                .frame(minHeight: 180)
        } else {
            List(tasks) { task in
                VStack(alignment: .leading, spacing: 5) {
                    Text(task.title).fontWeight(.medium)
                    HStack {
                        Text(lifecycleLabel(task.lifecycleStatus))
                        Text("优先级 \(task.priority)")
                        Text("\(projectOccurrences.count { $0.taskID == task.id }) 次执行")
                    }
                    .font(.caption)
                    .foregroundStyle(.secondary)
                }
                .padding(.vertical, 4)
            }
            .listStyle(.inset)
            .frame(minHeight: 180)
        }
    }

    @ViewBuilder
    private func occurrenceList(_ occurrences: [OccurrenceV2]) -> some View {
        if occurrences.isEmpty {
            ContentUnavailableView("没有执行记录", systemImage: "calendar.badge.clock")
                .frame(minHeight: 180)
        } else {
            List(occurrences) { occurrence in
                OccurrenceRow(
                    occurrence: occurrence,
                    task: store.tasksByID[occurrence.taskID],
                    project: selectedProject,
                    selected: selectedOccurrenceID == occurrence.id,
                    isMutating: store.isMutating,
                    select: { selectedOccurrenceID = occurrence.id },
                    toggle: { await toggle(occurrence) }
                )
            }
            .listStyle(.inset)
            .frame(minHeight: 180)
        }
    }

    private func toggle(_ occurrence: OccurrenceV2) async {
        do {
            try await store.toggle(occurrence)
            await store.loadProjects()
        } catch {
            store.errorMessage = error.localizedDescription
        }
    }

    private func beginCompletion(_ project: ProjectV2) {
        let unfinished = projectOccurrences.filter { !$0.executionStatus.isTerminal }
        if unfinished.isEmpty {
            run(project, command: .complete)
        } else {
            completionProject = project
        }
    }

    private func run(
        _ project: ProjectV2,
        command: ProjectLifecycleCommand,
        restoreTo: ProjectStatus? = nil
    ) {
        Task {
            do {
                try await store.executeProjectCommand(project, command: command, restoreTo: restoreTo)
                await store.loadProjects()
            } catch {
                store.errorMessage = error.localizedDescription
            }
        }
    }

    private func canEdit(_ project: ProjectV2) -> Bool {
        (project.systemRole == nil || project.systemRole?.isEmpty == true)
            && project.status != .completed
            && project.status != .archived
    }

    private func canDelete(_ project: ProjectV2) -> Bool {
        project.systemRole == nil || project.systemRole?.isEmpty == true
    }

    private func applyPendingProjectSelection() {
        guard case .project(let projectID) = session.pendingWorkspaceEntitySelection else { return }
        guard !store.isLoading else { return }
        if store.projects.contains(where: { $0.id == projectID }) {
            selectedProjectID = projectID
        }
        session.consumeWorkspaceEntitySelection()
    }

    private func statusLabel(_ status: ProjectStatus) -> String {
        switch status {
        case .planning: "规划中"
        case .active: "进行中"
        case .paused: "已暂停"
        case .completed: "已完成"
        case .archived: "已归档"
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
}

private struct ProjectEditorContext: Identifiable {
    let project: ProjectV2?
    var id: String { project?.id ?? "new-project" }
}

private struct ProjectEditorSheet: View {
    @Environment(\.dismiss) private var dismiss
    let project: ProjectV2?
    let store: WorkspaceStore
    let onSaved: (String) async -> Void
    @State private var name: String
    @State private var kind: ProjectKind
    @State private var horizon: ProjectHorizon
    @State private var initialStatus: ProjectStatus
    @State private var errorMessage: String?

    init(project: ProjectV2?, store: WorkspaceStore, onSaved: @escaping (String) async -> Void) {
        self.project = project
        self.store = store
        self.onSaved = onSaved
        _name = State(initialValue: project?.name ?? "")
        _kind = State(initialValue: project?.kind ?? .standard)
        _horizon = State(initialValue: project?.horizon ?? .short)
        _initialStatus = State(initialValue: project?.status == .active ? .active : .planning)
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("项目") {
                    TextField("项目名称", text: $name)
                    Picker("类型", selection: $kind) {
                        Text("标准项目").tag(ProjectKind.standard)
                        Text("学习项目").tag(ProjectKind.learning)
                    }
                    Picker("周期", selection: $horizon) {
                        Text("短期").tag(ProjectHorizon.short)
                        Text("长期").tag(ProjectHorizon.long)
                    }
                    if project == nil {
                        Picker("初始状态", selection: $initialStatus) {
                            Text("规划中").tag(ProjectStatus.planning)
                            Text("进行中").tag(ProjectStatus.active)
                        }
                    }
                }

                if kind == .learning {
                    Section {
                        Label("学习项目可以创建阶段 Roadmap 和节点脑图。", systemImage: "point.3.connected.trianglepath.dotted")
                            .foregroundStyle(.secondary)
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
            .navigationTitle(project == nil ? "新建项目" : "编辑项目")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("取消") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("保存") { Task { await save() } }
                        .buttonStyle(.borderedProminent)
                        .disabled(name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || store.isMutating)
                }
            }
        }
        .frame(width: 500, height: 430)
    }

    private func save() async {
        errorMessage = nil
        do {
            let saved: ProjectV2
            if let project {
                saved = try await store.updateProject(
                    project,
                    name: name,
                    kind: kind,
                    horizon: horizon
                )
            } else {
                saved = try await store.createProject(
                    name: name,
                    kind: kind,
                    horizon: horizon,
                    status: initialStatus
                )
            }
            await onSaved(saved.id)
            dismiss()
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}

private struct ProjectCompletionSheet: View {
    private enum Decision { case choose, move }

    @Environment(\.dismiss) private var dismiss
    let project: ProjectV2
    let store: WorkspaceStore
    let onCompleted: () async -> Void
    @State private var decision: Decision = .choose
    @State private var targetProjectID = ""
    @State private var errorMessage: String?

    private var openOccurrences: [OccurrenceV2] {
        store.occurrences.filter { occurrence in
            let projectID = occurrence.projectID ?? store.tasksByID[occurrence.taskID]?.projectID
            return projectID == project.id && !occurrence.executionStatus.isTerminal
        }
    }

    private var affectedTasks: [TaskV2] {
        let taskIDs = Set(openOccurrences.map(\.taskID))
        return taskIDs.compactMap { store.tasksByID[$0] }
    }

    private var targetProjects: [ProjectV2] {
        store.projects.filter {
            $0.id != project.id && $0.status != .completed && $0.status != .archived
        }
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("完成项目前") {
                    LabeledContent("未完成执行", value: "\(openOccurrences.count)")
                    LabeledContent("受影响任务", value: "\(affectedTasks.count)")
                    Text("需要明确取消这些任务，或把对应任务迁移到其他项目。")
                        .foregroundStyle(.secondary)
                }

                if decision == .choose {
                    Section("处理方式") {
                        Button("迁移任务到其他项目", systemImage: "arrow.right.folder") {
                            decision = .move
                        }
                        Button("取消未完成任务并完成项目", systemImage: "xmark.circle") {
                            Task { await cancelAndComplete() }
                        }
                        .disabled(store.isMutating)
                    }
                } else {
                    Section("迁移任务") {
                        Picker("目标项目", selection: $targetProjectID) {
                            Text("请选择").tag("")
                            ForEach(targetProjects) { candidate in
                                Text(candidate.name).tag(candidate.id)
                            }
                        }
                        Text("迁移的是任务定义，因此该任务的后续执行也会归入目标项目。")
                            .foregroundStyle(.secondary)
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
            .navigationTitle("完成“\(project.name)”")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button(decision == .move ? "返回" : "暂不处理") {
                        if decision == .move {
                            decision = .choose
                        } else {
                            dismiss()
                        }
                    }
                }
                if decision == .move {
                    ToolbarItem(placement: .confirmationAction) {
                        Button("迁移并完成") { Task { await moveAndComplete() } }
                            .buttonStyle(.borderedProminent)
                            .disabled(targetProjectID.isEmpty || store.isMutating)
                    }
                }
            }
        }
        .frame(width: 540, height: 470)
    }

    private func cancelAndComplete() async {
        errorMessage = nil
        do {
            for task in affectedTasks {
                try await store.executeTaskLifecycle(task, command: .cancel)
            }
            try await store.executeProjectCommand(project, command: .complete)
            await onCompleted()
            dismiss()
        } catch let error as APIError where error.isRevisionConflict {
            errorMessage = "任务或项目已更新，请刷新并重新确认未完成执行。"
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func moveAndComplete() async {
        errorMessage = nil
        do {
            for task in affectedTasks {
                try await store.moveTask(task, to: targetProjectID)
            }
            try await store.executeProjectCommand(project, command: .complete)
            await onCompleted()
            dismiss()
        } catch let error as APIError where error.isRevisionConflict {
            errorMessage = "任务或项目已更新，目标项目选择已保留，请刷新后重试。"
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}

private struct ProjectDeletionSheet: View {
    @Environment(\.dismiss) private var dismiss
    let project: ProjectV2
    let store: WorkspaceStore
    let onDeleted: () async -> Void
    @State private var errorMessage: String?

    private var tasks: [TaskV2] { store.tasks.filter { $0.projectID == project.id } }
    private var openOccurrences: [OccurrenceV2] {
        store.occurrences.filter { occurrence in
            let projectID = occurrence.projectID ?? store.tasksByID[occurrence.taskID]?.projectID
            return projectID == project.id && !occurrence.executionStatus.isTerminal
        }
    }
    private var notes: [FlowNote] {
        store.notes.filter { note in note.projects.contains { $0.id == project.id } }
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("删除影响") {
                    LabeledContent("任务定义", value: "\(tasks.count)")
                    LabeledContent("未完成执行", value: "\(openOccurrences.count)")
                    LabeledContent("关联笔记", value: "\(notes.count)")
                    LabeledContent(
                        "Roadmap",
                        value: store.roadmapsByProjectID[project.id] == nil ? "无" : "存在"
                    )
                }

                if !openOccurrences.isEmpty {
                    Section {
                        Label("项目仍有受保护的未完成执行，请先完成、取消或迁移任务。", systemImage: "lock.trianglebadge.exclamationmark")
                            .foregroundStyle(.orange)
                    }
                } else {
                    Section {
                        Label("删除后不可恢复，关联内容可能同时受到影响。", systemImage: "exclamationmark.triangle")
                            .foregroundStyle(.red)
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
            .navigationTitle("删除“\(project.name)”？")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("取消") { dismiss() }
                }
                ToolbarItem(placement: .destructiveAction) {
                    Button("永久删除", role: .destructive) { Task { await remove() } }
                        .disabled(!openOccurrences.isEmpty || store.isMutating)
                }
            }
        }
        .frame(width: 520, height: 440)
        .task {
            if project.kind == .learning {
                await store.loadRoadmap(projectID: project.id)
            }
        }
    }

    private func remove() async {
        errorMessage = nil
        do {
            try await store.deleteProject(project)
            await onDeleted()
            dismiss()
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}

private struct ProjectMetric: View {
    let title: String
    let value: Int
    let color: Color

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            Text(title).font(.caption).foregroundStyle(.secondary)
            Text("\(value)").font(.title2.weight(.semibold)).foregroundStyle(color)
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.regularMaterial, in: .rect(cornerRadius: 10))
    }
}

private extension Collection {
    func prefixArray(_ maxLength: Int) -> [Element] {
        Array(prefix(maxLength))
    }
}
