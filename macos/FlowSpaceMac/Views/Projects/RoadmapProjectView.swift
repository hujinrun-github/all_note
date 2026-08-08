import SwiftUI

struct RoadmapProjectView: View {
    struct GenerationRequest: Identifiable {
        let id = UUID()
        var prompt = ""
    }

    @Environment(\.openWindow) private var openWindow
    let project: ProjectV2
    let store: WorkspaceStore
    @State private var selectedStageID = ""
    @State private var nodeDraft: RoadmapNodeDraft?
    @State private var generationRequest: GenerationRequest?
    @State private var deletingNode: RoadmapNodeV2?
    @State private var taskDraft: TaskDraft?
    @State private var selectedTask: TaskV2?
    @State private var roadmapActionError: String?

    private var roadmap: RoadmapV2? { store.roadmapsByProjectID[project.id] }
    private var stages: [RoadmapNodeV2] { roadmap?.stages ?? [] }
    private var selectedStage: RoadmapNodeV2? {
        stages.first { $0.id == selectedStageID } ?? stages.first
    }
    private var visibleNodes: [RoadmapNodeV2] {
        guard let roadmap, let selectedStage else { return [] }
        return roadmap.nodes
            .filter { $0.parentID == selectedStage.id }
            .sorted(by: RoadmapNodeV2.positionAscending)
    }
    private var roadmapTasks: [TaskV2] {
        guard let roadmap else { return [] }
        let nodeIDs = Set(roadmap.nodes.map(\.id))
        return store.tasks.filter { task in
            task.roadmapNodeID.map(nodeIDs.contains) == true
        }
    }
    private var selectedStageTasks: [TaskV2] {
        guard let selectedStage else { return [] }
        let nodeIDs = Set([selectedStage.id] + visibleNodes.map(\.id))
        return store.tasks.filter { task in
            task.roadmapNodeID.map(nodeIDs.contains) == true
        }
    }
    private var firstProtectedNode: RoadmapNodeV2? {
        guard let roadmap else { return nil }
        let protectedNodeIDs = Set(roadmapTasks.compactMap(\.roadmapNodeID))
        return roadmap.nodes.first { protectedNodeIDs.contains($0.id) }
    }

    var body: some View {
        Group {
            if store.loadingRoadmapProjectIDs.contains(project.id) {
                ProgressView("正在读取学习路线…")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if let roadmap {
                roadmapContent(roadmap)
            } else {
                emptyState
            }
        }
        .task(id: project.id) {
            await store.loadRoadmap(projectID: project.id)
            selectFirstStageIfNeeded()
        }
        .onChange(of: stages) { selectFirstStageIfNeeded() }
        .sheet(item: $nodeDraft) { draft in
            RoadmapNodeEditor(
                draft: draft,
                stages: stages,
                isSaving: store.isMutating,
                save: { updated in await save(updated) }
            )
        }
        .sheet(item: $generationRequest) { request in
            RoadmapGenerationSheet(
                request: request,
                projectName: project.name,
                isGenerating: store.isMutating,
                generate: { prompt in await generate(prompt) }
            )
        }
        .sheet(item: $taskDraft) { draft in
            TaskEditorSheet(draft: draft, store: store) {
                await refreshRoadmapContext()
            }
        }
        .sheet(item: $selectedTask) { task in
            TaskDefinitionEditorSheet(
                task: task,
                store: store,
                allowsProjectChange: false
            ) {
                await refreshRoadmapContext()
            }
        }
        .confirmationDialog(
            "删除“\(deletingNode?.title ?? "")”？",
            isPresented: Binding(
                get: { deletingNode != nil },
                set: { if !$0 { deletingNode = nil } }
            )
        ) {
            Button("删除节点", role: .destructive) {
                guard let deletingNode, let roadmap else { return }
                Task {
                    do {
                        try await store.deleteRoadmapNode(
                            projectID: project.id,
                            roadmapID: roadmap.id,
                            node: deletingNode
                        )
                        self.deletingNode = nil
                    } catch {
                        store.errorMessage = error.localizedDescription
                    }
                }
            }
        } message: {
            Text("节点下的结构可能同时被移除；关联任务仍由任务域管理。")
        }
    }

    private var emptyState: some View {
        ContentUnavailableView {
            Label("还没有学习路线", systemImage: "point.3.connected.trianglepath.dotted")
        } description: {
            Text("创建空白路线自行规划，或让 AI 根据学习目标生成阶段。")
        } actions: {
            HStack {
                Button("创建空白路线") {
                    Task {
                        do { try await store.createRoadmap(project: project) }
                        catch { store.errorMessage = error.localizedDescription }
                    }
                }
                .disabled(store.isMutating)
                Button("AI 生成", systemImage: "sparkles") {
                    generationRequest = GenerationRequest()
                }
                .buttonStyle(.borderedProminent)
                .disabled(store.isMutating)
            }
        }
    }

    private func roadmapContent(_ roadmap: RoadmapV2) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                VStack(alignment: .leading, spacing: 3) {
                    Text(roadmap.title).font(.title2.weight(.semibold))
                    if !roadmap.description.isEmpty {
                        Text(roadmap.description).foregroundStyle(.secondary).lineLimit(2)
                    }
                }
                Spacer()
                Button("重新生成", systemImage: "sparkles") {
                    if roadmapTasks.isEmpty {
                        roadmapActionError = nil
                        generationRequest = GenerationRequest()
                    } else {
                        roadmapActionError = "当前路线已有 \(roadmapTasks.count) 个关联任务。为保护执行记录，请先迁移或解绑任务，再重新生成路线。"
                    }
                }
                .help(roadmapTasks.isEmpty ? "根据项目目标重新生成节点" : "路线已有任务，不能替换节点")
                Button("添加阶段", systemImage: "plus") {
                    nodeDraft = RoadmapNodeDraft(parentID: nil, nodeType: .stage)
                }
            }
            .padding(18)

            if let roadmapActionError {
                HStack(spacing: 12) {
                    Label(roadmapActionError, systemImage: "lock.trianglebadge.exclamationmark")
                        .foregroundStyle(.orange)
                    Spacer()
                    if let firstProtectedNode {
                        Button("查看关联任务") {
                            selectedStageID = stageID(containing: firstProtectedNode, in: roadmap) ?? firstProtectedNode.id
                            openWindow(value: RoadmapMindMapRoute(projectID: project.id, nodeID: firstProtectedNode.id))
                        }
                    }
                    Button("关闭", systemImage: "xmark") { self.roadmapActionError = nil }
                        .labelStyle(.iconOnly)
                }
                .padding(.horizontal, 18)
                .padding(.bottom, 14)
            }

            Divider()

            if stages.isEmpty {
                ContentUnavailableView {
                    Label("路线中还没有阶段", systemImage: "flag.checkered")
                } actions: {
                    Button("添加第一个阶段") {
                        nodeDraft = RoadmapNodeDraft(parentID: nil, nodeType: .stage)
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                stageRail
                Divider()
                stageDetail(roadmap)
            }
        }
    }

    private var stageRail: some View {
        ScrollView(.horizontal) {
            HStack(spacing: 0) {
                ForEach(Array(stages.enumerated()), id: \.element.id) { index, stage in
                    if index > 0 {
                        Rectangle()
                            .fill(.tertiary)
                            .frame(width: 34, height: 2)
                    }
                    Button {
                        selectedStageID = stage.id
                    } label: {
                        VStack(spacing: 6) {
                            ZStack {
                                Circle()
                                    .fill(selectedStage?.id == stage.id ? Color.accentColor : Color.secondary.opacity(0.18))
                                    .frame(width: 30, height: 30)
                                Text("\(index + 1)")
                                    .font(.caption.weight(.bold))
                                    .foregroundStyle(selectedStage?.id == stage.id ? .white : .primary)
                            }
                            Text(stage.title)
                                .font(.caption.weight(.medium))
                                .lineLimit(1)
                                .frame(maxWidth: 120)
                            ProgressView(value: stage.progress.completionFraction)
                                .frame(width: 92)
                        }
                        .padding(.vertical, 12)
                    }
                    .buttonStyle(.plain)
                    .contextMenu {
                        Button("编辑") { nodeDraft = RoadmapNodeDraft(node: stage) }
                        Button("删除", role: .destructive) { deletingNode = stage }
                    }
                }
            }
            .padding(.horizontal, 22)
        }
        .scrollIndicators(.hidden)
    }

    private func stageDetail(_ roadmap: RoadmapV2) -> some View {
        HStack(spacing: 0) {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    if let stage = selectedStage {
                        stageWorkspaceHeader(stage)

                        if visibleNodes.isEmpty {
                            ContentUnavailableView(
                                "这个阶段还没有主题",
                                systemImage: "book.pages",
                                description: Text("添加主题或里程碑来拆分学习目标。")
                            )
                            .frame(maxWidth: .infinity, minHeight: 180)
                        } else {
                            LazyVGrid(columns: [GridItem(.adaptive(minimum: 230), spacing: 12)], spacing: 12) {
                                ForEach(visibleNodes) { node in
                                    RoadmapNodeCard(
                                        node: node,
                                        taskCount: store.tasks.count { $0.roadmapNodeID == node.id },
                                        openMindMap: {
                                            openWindow(value: RoadmapMindMapRoute(projectID: project.id, nodeID: node.id))
                                        },
                                        edit: { nodeDraft = RoadmapNodeDraft(node: node) },
                                        delete: { deletingNode = node }
                                    )
                                }
                            }
                        }

                        stageTaskList(stage)
                    }
                }
                .padding(18)
            }
            .frame(maxWidth: .infinity)

            Divider()

            RoadmapLedger(
                roadmap: roadmap,
                selectedNode: selectedStage,
                taskCount: roadmapTasks.count,
                openMindMap: {
                    guard let selectedStage else { return }
                    openWindow(value: RoadmapMindMapRoute(projectID: project.id, nodeID: selectedStage.id))
                }
            )
            .frame(width: 270)
        }
    }

    private func stageWorkspaceHeader(_ stage: RoadmapNodeV2) -> some View {
        HStack(alignment: .top) {
            VStack(alignment: .leading, spacing: 4) {
                Text(stage.title).font(.title3.weight(.semibold))
                if !stage.description.isEmpty {
                    Text(stage.description).foregroundStyle(.secondary)
                }
            }
            Spacer()
            RoadmapProgressBadge(progress: stage.progress)
            Button("脑图", systemImage: "point.3.filled.connected.trianglepath.dotted") {
                openWindow(value: RoadmapMindMapRoute(projectID: project.id, nodeID: stage.id))
            }
            Button("添加任务", systemImage: "checklist") {
                taskDraft = makeTaskDraft(nodeID: stage.id)
            }
            Button("添加节点", systemImage: "plus") {
                nodeDraft = RoadmapNodeDraft(parentID: stage.id, nodeType: .topic)
            }
        }
    }

    private func stageTaskList(_ stage: RoadmapNodeV2) -> some View {
        GroupBox("关联任务") {
            if selectedStageTasks.isEmpty {
                VStack(spacing: 10) {
                    Text("当前阶段还没有关联任务")
                        .foregroundStyle(.secondary)
                    Button("创建第一项任务") {
                        taskDraft = makeTaskDraft(nodeID: stage.id)
                    }
                }
                .frame(maxWidth: .infinity, minHeight: 90)
            } else {
                VStack(spacing: 0) {
                    ForEach(selectedStageTasks) { task in
                        Button {
                            selectedTask = task
                        } label: {
                            HStack(spacing: 10) {
                                Image(systemName: task.lifecycleStatus == .completed ? "checkmark.circle.fill" : "circle")
                                    .foregroundStyle(task.lifecycleStatus == .completed ? .green : .secondary)
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(task.title).fontWeight(.medium)
                                    Text(taskDetail(task))
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                }
                                Spacer()
                                Image(systemName: "chevron.right")
                                    .foregroundStyle(.tertiary)
                            }
                            .contentShape(.rect)
                            .padding(.vertical, 9)
                        }
                        .buttonStyle(.plain)
                        if task.id != selectedStageTasks.last?.id { Divider() }
                    }
                }
            }
        }
    }

    private func selectFirstStageIfNeeded() {
        if !stages.contains(where: { $0.id == selectedStageID }) {
            selectedStageID = stages.first?.id ?? ""
        }
    }

    private func makeTaskDraft(nodeID: String) -> TaskDraft {
        var draft = TaskDraft()
        draft.projectID = project.id
        draft.roadmapNodeID = nodeID
        return draft
    }

    private func taskDetail(_ task: TaskV2) -> String {
        let priority = TaskPriorityLevel(rawValue: task.priority)?.title ?? "普通"
        return "\(task.lifecycleStatus.title) · \(priority)优先级"
    }

    private func stageID(containing node: RoadmapNodeV2, in roadmap: RoadmapV2) -> String? {
        if node.nodeType == .stage { return node.id }
        var parentID = node.parentID
        while let currentParentID = parentID,
              let parent = roadmap.nodes.first(where: { $0.id == currentParentID }) {
            if parent.nodeType == .stage { return parent.id }
            parentID = parent.parentID
        }
        return nil
    }

    private func refreshRoadmapContext() async {
        await store.loadAllTasks()
        await store.loadRoadmap(projectID: project.id, force: true)
        selectFirstStageIfNeeded()
    }

    private func save(_ draft: RoadmapNodeDraft) async -> Bool {
        guard let roadmap else { return false }
        do {
            try await store.saveRoadmapNode(projectID: project.id, roadmapID: roadmap.id, draft: draft)
            nodeDraft = nil
            return true
        } catch {
            store.errorMessage = error.localizedDescription
            return false
        }
    }

    private func generate(_ prompt: String) async -> Bool {
        roadmapActionError = nil
        do {
            try await store.generateRoadmap(projectID: project.id, prompt: prompt)
            generationRequest = nil
            selectFirstStageIfNeeded()
            return true
        } catch let error as APIError where error.code == "roadmap_node_has_tasks" {
            roadmapActionError = "当前路线已有任务。为保护执行记录，请先迁移或解绑任务，再重新生成路线。"
            generationRequest = nil
            return false
        } catch {
            store.errorMessage = error.localizedDescription
            return false
        }
    }
}

private struct RoadmapNodeCard: View {
    let node: RoadmapNodeV2
    let taskCount: Int
    let openMindMap: () -> Void
    let edit: () -> Void
    let delete: () -> Void

    var body: some View {
        GroupBox {
            VStack(alignment: .leading, spacing: 10) {
                HStack {
                    Label(node.nodeType.title, systemImage: node.nodeType.systemImage)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Spacer()
                    Menu("更多", systemImage: "ellipsis") {
                        Button("在脑图中打开", action: openMindMap)
                        Button("编辑", action: edit)
                        Divider()
                        Button("删除", role: .destructive, action: delete)
                    }
                    .menuStyle(.borderlessButton)
                    .fixedSize()
                }
                Text(node.title).font(.headline).lineLimit(2)
                Text(node.description.isEmpty ? "暂无说明" : node.description)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                ProgressView(value: node.progress.completionFraction)
                HStack {
                    Text("\(taskCount) 个任务")
                    Spacer()
                    Text("完成 \(node.progress.done)/\(node.progress.total)")
                }
                .font(.caption)
                .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}

struct RoadmapProgressBadge: View {
    let progress: RoadmapNodeProgress

    var body: some View {
        HStack(spacing: 5) {
            Image(systemName: "checkmark.circle")
            Text("\(progress.done)/\(progress.total)")
        }
        .font(.caption.weight(.medium))
        .padding(.horizontal, 9)
        .padding(.vertical, 5)
        .background(.green.opacity(0.12), in: .capsule)
        .foregroundStyle(.green)
        .help("已完成 / 全部执行")
    }
}

private struct RoadmapLedger: View {
    let roadmap: RoadmapV2
    let selectedNode: RoadmapNodeV2?
    let taskCount: Int
    let openMindMap: () -> Void

    private var totalOccurrences: Int { roadmap.nodes.reduce(0) { $0 + $1.progress.total } }
    private var doneOccurrences: Int { roadmap.nodes.reduce(0) { $0 + $1.progress.done } }
    private var blockedOccurrences: Int { roadmap.nodes.reduce(0) { $0 + $1.progress.blocked } }
    private var openOccurrences: Int { roadmap.nodes.reduce(0) { $0 + $1.progress.open } }
    private var completionFraction: Double {
        guard totalOccurrences > 0 else { return 0 }
        return Double(doneOccurrences) / Double(totalOccurrences)
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                VStack(alignment: .leading, spacing: 9) {
                    HStack {
                        Text("总进度").font(.headline)
                        Spacer()
                        Text(completionFraction, format: .percent.precision(.fractionLength(0)))
                            .font(.title3.monospacedDigit().weight(.semibold))
                    }
                    ProgressView(value: completionFraction)
                    LabeledContent("已完成执行", value: "\(doneOccurrences) / \(totalOccurrences)")
                    LabeledContent("任务定义", value: "\(taskCount)")
                    LabeledContent("待开始", value: "\(openOccurrences)")
                    LabeledContent("被阻塞", value: "\(blockedOccurrences)")
                }

                Divider()

                VStack(alignment: .leading, spacing: 8) {
                    Label("下一步", systemImage: "flag.fill")
                        .font(.headline)
                    Text(selectedNode.map { "继续推进“\($0.title)”" } ?? "选择一个阶段")
                        .fontWeight(.semibold)
                    Text(nextStepDescription)
                        .font(.callout)
                        .foregroundStyle(.secondary)
                    Button("打开脑图", systemImage: "point.3.filled.connected.trianglepath.dotted", action: openMindMap)
                        .disabled(selectedNode == nil)
                }

                Divider()

                Label {
                    Text("路线负责学习结构，任务负责实际执行；节点进度由执行记录自动汇总。")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                } icon: {
                    Image(systemName: "list.bullet.clipboard")
                }
            }
            .padding(18)
        }
        .background(Color(nsColor: .controlBackgroundColor).opacity(0.45))
    }

    private var nextStepDescription: String {
        guard let selectedNode else { return "从上方阶段 rail 选择当前要推进的学习阶段。" }
        if selectedNode.progress.blocked > 0 {
            return "先处理 \(selectedNode.progress.blocked) 个阻塞执行，再安排新的行动。"
        }
        if selectedNode.progress.tasks > 0 {
            return "已有 \(selectedNode.progress.tasks) 个关联任务，可以在脑图中集中处理。"
        }
        return "先为该阶段建立一项清晰、可验证的关联任务。"
    }
}

struct RoadmapNodeEditor: View {
    @Environment(\.dismiss) private var dismiss
    @State private var draft: RoadmapNodeDraft
    let stages: [RoadmapNodeV2]
    let isSaving: Bool
    let save: (RoadmapNodeDraft) async -> Bool

    init(
        draft: RoadmapNodeDraft,
        stages: [RoadmapNodeV2],
        isSaving: Bool,
        save: @escaping (RoadmapNodeDraft) async -> Bool
    ) {
        _draft = State(initialValue: draft)
        self.stages = stages
        self.isSaving = isSaving
        self.save = save
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(draft.editingNodeID == nil ? "添加路线节点" : "编辑路线节点")
                .font(.title2.weight(.semibold))
            Form {
                TextField("名称", text: $draft.title)
                Picker("类型", selection: $draft.nodeType) {
                    ForEach(RoadmapNodeType.allCases) { type in Text(type.title).tag(type) }
                }
                Picker("所属阶段", selection: $draft.parentID) {
                    Text("顶层节点").tag(Optional<String>.none)
                    ForEach(stages) { stage in Text(stage.title).tag(Optional(stage.id)) }
                }
                TextField("说明", text: $draft.description, axis: .vertical)
                    .lineLimit(3...6)
            }
            HStack {
                Spacer()
                Button("取消") { dismiss() }
                Button("保存") {
                    Task { if await save(draft) { dismiss() } }
                }
                .buttonStyle(.borderedProminent)
                .disabled(isSaving || draft.title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
        .padding(22)
        .frame(width: 480)
    }
}

private struct RoadmapGenerationSheet: View {
    @Environment(\.dismiss) private var dismiss
    @State private var prompt: String
    let projectName: String
    let isGenerating: Bool
    let generate: (String) async -> Bool

    init(
        request: RoadmapProjectView.GenerationRequest,
        projectName: String,
        isGenerating: Bool,
        generate: @escaping (String) async -> Bool
    ) {
        _prompt = State(initialValue: request.prompt)
        self.projectName = projectName
        self.isGenerating = isGenerating
        self.generate = generate
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Label("生成学习路线", systemImage: "sparkles")
                .font(.title2.weight(.semibold))
            Text("留空会根据“\(projectName)”生成通用路线；也可以补充目标、基础和时间要求。")
                .foregroundStyle(.secondary)
            TextEditor(text: $prompt)
                .font(.body)
                .frame(height: 150)
                .padding(8)
                .background(.quaternary.opacity(0.45), in: .rect(cornerRadius: 8))
            HStack {
                if isGenerating { ProgressView("正在生成，可能需要一些时间…") }
                Spacer()
                Button("取消") { dismiss() }.disabled(isGenerating)
                Button("生成") {
                    Task { if await generate(prompt) { dismiss() } }
                }
                .buttonStyle(.borderedProminent)
                .disabled(isGenerating)
            }
        }
        .padding(22)
        .frame(width: 520)
        .interactiveDismissDisabled(isGenerating)
    }
}
