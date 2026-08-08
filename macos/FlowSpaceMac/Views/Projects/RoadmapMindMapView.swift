import SwiftUI

struct RoadmapMindMapView: View {
    let route: RoadmapMindMapRoute
    let store: WorkspaceStore
    @State private var selectedNodeID: String
    @State private var nodeDraft: RoadmapNodeDraft?
    @State private var taskDraft: TaskDraft?
    @State private var selectedTask: TaskV2?
    @State private var selectedOccurrence: OccurrenceV2?
    @State private var zoomScale = 1.0

    init(route: RoadmapMindMapRoute, store: WorkspaceStore) {
        self.route = route
        self.store = store
        _selectedNodeID = State(initialValue: route.nodeID)
    }

    private var project: ProjectV2? { store.projectsByID[route.projectID] }
    private var roadmap: RoadmapV2? { store.roadmapsByProjectID[route.projectID] }
    private var selectedNode: RoadmapNodeV2? {
        roadmap?.nodes.first { $0.id == selectedNodeID }
    }
    private var children: [RoadmapNodeV2] {
        roadmap?.nodes
            .filter { $0.parentID == selectedNodeID }
            .sorted(by: RoadmapNodeV2.positionAscending) ?? []
    }
    private var linkedTasks: [TaskV2] {
        store.tasks.filter { $0.roadmapNodeID == selectedNodeID }
    }

    var body: some View {
        Group {
            if store.loadingRoadmapProjectIDs.contains(route.projectID) {
                ProgressView("正在读取脑图…")
            } else if let roadmap, let selectedNode {
                VStack(spacing: 0) {
                    mapHeader(roadmap, selectedNode: selectedNode)
                    Divider()
                    mindMap(selectedNode)
                }
            } else {
                ContentUnavailableView(
                    "脑图节点不可用",
                    systemImage: "point.3.connected.trianglepath.dotted",
                    description: Text("路线可能已更新或节点已被删除。")
                )
            }
        }
        .task {
            if store.projects.isEmpty { await store.loadProjects() }
            await store.loadRoadmap(projectID: route.projectID)
            if selectedNode == nil { selectedNodeID = roadmap?.stages.first?.id ?? "" }
        }
        .sheet(item: $nodeDraft) { draft in
            RoadmapNodeEditor(
                draft: draft,
                stages: roadmap?.stages ?? [],
                isSaving: store.isMutating,
                save: { updated in await save(updated) }
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
        .sheet(item: $selectedOccurrence) { occurrence in
            OccurrenceInspector(occurrence: occurrence, store: store) {
                await refreshRoadmapContext()
            }
            .frame(minWidth: 440, minHeight: 620)
        }
        .navigationTitle(selectedNode?.title ?? project?.name ?? "学习脑图")
    }

    private func mapHeader(_ roadmap: RoadmapV2, selectedNode: RoadmapNodeV2) -> some View {
        HStack(spacing: 14) {
            VStack(alignment: .leading, spacing: 3) {
                Text(project?.name ?? roadmap.title)
                    .font(.title2.weight(.semibold))
                Text("聚焦：\(selectedNode.title)")
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Picker("聚焦节点", selection: $selectedNodeID) {
                ForEach(roadmap.nodes.sorted(by: RoadmapNodeV2.positionAscending)) { node in
                    Label(node.title, systemImage: node.nodeType.systemImage).tag(node.id)
                }
            }
            .frame(width: 260)
            Button("编辑节点", systemImage: "pencil") {
                nodeDraft = RoadmapNodeDraft(node: selectedNode)
            }
            Button("添加子节点", systemImage: "plus") {
                nodeDraft = RoadmapNodeDraft(parentID: selectedNode.id, nodeType: .topic)
            }
            Button("添加任务", systemImage: "checklist") {
                taskDraft = makeTaskDraft()
            }
            Divider().frame(height: 20)
            Button("缩小", systemImage: "minus.magnifyingglass") {
                zoomScale = max(0.6, zoomScale - 0.1)
            }
            .labelStyle(.iconOnly)
            .keyboardShortcut("-", modifiers: .command)
            Button("适应画布", systemImage: "arrow.up.left.and.arrow.down.right") {
                zoomScale = 0.85
            }
            .labelStyle(.iconOnly)
            .keyboardShortcut("0", modifiers: .command)
            Button("放大", systemImage: "plus.magnifyingglass") {
                zoomScale = min(1.6, zoomScale + 0.1)
            }
            .labelStyle(.iconOnly)
            .keyboardShortcut("+", modifiers: .command)
            Text(zoomScale, format: .percent.precision(.fractionLength(0)))
                .font(.caption.monospacedDigit())
                .frame(width: 42, alignment: .trailing)
        }
        .padding(18)
    }

    private func mindMap(_ root: RoadmapNodeV2) -> some View {
        ScrollView([.horizontal, .vertical]) {
            HStack(alignment: .center, spacing: 22) {
                MindMapNodeCard(node: root, isRoot: true) {
                    selectedNodeID = root.id
                }

                Image(systemName: "arrow.right")
                    .font(.title2)
                    .foregroundStyle(.tertiary)

                VStack(spacing: 12) {
                    if children.isEmpty {
                        MindMapEmptyCard(title: "没有子节点", systemImage: "plus") {
                            nodeDraft = RoadmapNodeDraft(parentID: root.id, nodeType: .topic)
                        }
                    } else {
                        ForEach(children) { child in
                            MindMapNodeCard(node: child, isRoot: false) {
                                selectedNodeID = child.id
                            }
                        }
                    }
                }

                Image(systemName: "arrow.right")
                    .font(.title2)
                    .foregroundStyle(.tertiary)

                VStack(alignment: .leading, spacing: 12) {
                    Text("关联任务").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                    ForEach(linkedTasks) { task in
                        linkedTaskCard(task)
                    }
                    Button("创建关联任务", systemImage: "plus") {
                        taskDraft = makeTaskDraft()
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(store.isMutating)
                    .frame(width: 270)
                }
            }
            .padding(36)
            .frame(minWidth: 920, minHeight: 520)
            .scaleEffect(zoomScale)
            .animation(.snappy(duration: 0.2), value: zoomScale)
        }
        .background(Color(nsColor: .underPageBackgroundColor))
    }

    private func linkedTaskCard(_ task: TaskV2) -> some View {
        let taskOccurrences = store.occurrences
            .filter { $0.taskID == task.id }
            .sorted(by: OccurrenceV2.scheduleAscending)
        return VStack(alignment: .leading, spacing: 9) {
            Button {
                selectedTask = task
            } label: {
                HStack(spacing: 10) {
                    Image(systemName: task.lifecycleStatus == .completed ? "checkmark.circle.fill" : "circle")
                        .foregroundStyle(task.lifecycleStatus == .completed ? .green : .secondary)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(task.title).fontWeight(.medium)
                        Text(taskStatus(task.lifecycleStatus)).font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer()
                    Image(systemName: "pencil")
                        .foregroundStyle(.tertiary)
                }
                .contentShape(.rect)
            }
            .buttonStyle(.plain)

            if taskOccurrences.isEmpty {
                Text("暂无执行实例")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                ForEach(taskOccurrences.prefix(4)) { occurrence in
                    Button {
                        selectedOccurrence = occurrence
                    } label: {
                        HStack(spacing: 7) {
                            StatusBadge(status: occurrence.executionStatus)
                            Text(occurrenceSchedule(occurrence))
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                            Spacer()
                            Image(systemName: "chevron.right")
                                .font(.caption2)
                                .foregroundStyle(.tertiary)
                        }
                    }
                    .buttonStyle(.plain)
                }
                if taskOccurrences.count > 4 {
                    Text("另有 \(taskOccurrences.count - 4) 次执行")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .padding(12)
        .frame(width: 270, alignment: .leading)
        .background(.background, in: .rect(cornerRadius: 10))
        .overlay { RoundedRectangle(cornerRadius: 10).stroke(.separator) }
    }

    private func makeTaskDraft() -> TaskDraft {
        var draft = TaskDraft()
        draft.projectID = route.projectID
        draft.roadmapNodeID = selectedNodeID
        return draft
    }

    private func occurrenceSchedule(_ occurrence: OccurrenceV2) -> String {
        if let start = occurrence.plannedStartAt, let date = Date.flowSpaceISO8601(start) {
            return date.formatted(date: .abbreviated, time: .shortened)
        }
        if let date = occurrence.plannedDate { return date }
        return "无日期"
    }

    private func refreshRoadmapContext() async {
        await store.loadAllTasks()
        await store.loadRoadmap(projectID: route.projectID, force: true)
        if selectedNode == nil { selectedNodeID = roadmap?.stages.first?.id ?? "" }
    }

    private func save(_ draft: RoadmapNodeDraft) async -> Bool {
        guard let roadmap else { return false }
        do {
            try await store.saveRoadmapNode(
                projectID: route.projectID,
                roadmapID: roadmap.id,
                draft: draft
            )
            nodeDraft = nil
            return true
        } catch {
            store.errorMessage = error.localizedDescription
            return false
        }
    }

    private func taskStatus(_ status: TaskLifecycleStatus) -> String {
        switch status {
        case .draft: "草稿"
        case .active: "进行中"
        case .paused: "暂停"
        case .completed: "已完成"
        case .cancelled: "已取消"
        case .archived: "已归档"
        }
    }
}

private struct MindMapNodeCard: View {
    let node: RoadmapNodeV2
    let isRoot: Bool
    let select: () -> Void

    var body: some View {
        Button(action: select) {
            VStack(alignment: .leading, spacing: 10) {
                Label(node.nodeType.title, systemImage: node.nodeType.systemImage)
                    .font(.caption)
                    .foregroundStyle(isRoot ? Color.accentColor : .secondary)
                Text(node.title)
                    .font(isRoot ? .title3.weight(.semibold) : .headline)
                    .multilineTextAlignment(.leading)
                if !node.description.isEmpty {
                    Text(node.description)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(3)
                        .multilineTextAlignment(.leading)
                }
                ProgressView(value: node.progress.completionFraction)
                Text("\(node.progress.done)/\(node.progress.total) 已完成")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
            .padding(16)
            .frame(width: isRoot ? 260 : 230, alignment: .leading)
            .background(isRoot ? Color.accentColor.opacity(0.08) : Color(nsColor: .controlBackgroundColor), in: .rect(cornerRadius: 13))
            .overlay {
                RoundedRectangle(cornerRadius: 13)
                    .stroke(isRoot ? Color.accentColor.opacity(0.5) : Color.secondary.opacity(0.2), lineWidth: isRoot ? 2 : 1)
            }
        }
        .buttonStyle(.plain)
    }
}

private struct MindMapEmptyCard: View {
    let title: String
    let systemImage: String
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Label(title, systemImage: systemImage)
                .foregroundStyle(.secondary)
                .frame(width: 200, height: 72)
                .background(.quaternary.opacity(0.4), in: .rect(cornerRadius: 12))
        }
        .buttonStyle(.plain)
    }
}
