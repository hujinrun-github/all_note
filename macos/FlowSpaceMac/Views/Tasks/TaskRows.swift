import AppKit
import SwiftUI

enum OccurrenceRowContextAction {
    case start
    case block
    case unblock
    case toggleCompletion
    case editTask
    case reschedule
    case copyLink
    case moveProject
    case cancelTask
    case archiveTask
    case openInNewWorkspace
}

struct OccurrenceRow: View {
    let occurrence: OccurrenceV2
    let task: TaskV2?
    let project: ProjectV2?
    let selected: Bool
    let selectionMode: Bool
    let batchSelected: Bool
    let isMutating: Bool
    let select: (_ extending: Bool) -> Void
    let toggleBatchSelection: () -> Void
    let toggle: () async -> Void
    let open: (() -> Void)?
    let contextAction: ((OccurrenceRowContextAction) -> Void)?

    init(
        occurrence: OccurrenceV2,
        task: TaskV2?,
        project: ProjectV2?,
        selected: Bool,
        selectionMode: Bool,
        batchSelected: Bool,
        isMutating: Bool,
        select: @escaping (_ extending: Bool) -> Void,
        toggleBatchSelection: @escaping () -> Void,
        toggle: @escaping () async -> Void,
        open: (() -> Void)? = nil,
        contextAction: ((OccurrenceRowContextAction) -> Void)? = nil
    ) {
        self.occurrence = occurrence
        self.task = task
        self.project = project
        self.selected = selected
        self.selectionMode = selectionMode
        self.batchSelected = batchSelected
        self.isMutating = isMutating
        self.select = select
        self.toggleBatchSelection = toggleBatchSelection
        self.toggle = toggle
        self.open = open
        self.contextAction = contextAction
    }

    var body: some View {
        HStack(spacing: 12) {
            Button {
                if selectionMode {
                    toggleBatchSelection()
                } else {
                    Task { await toggle() }
                }
            } label: {
                Image(systemName: leadingIcon)
                    .font(.title3)
                    .foregroundStyle(leadingColor)
            }
            .buttonStyle(.plain)
            .disabled(!selectionMode && (isMutating || (occurrence.executionStatus.isTerminal && occurrence.executionStatus != .done)))
            .accessibilityLabel(selectionMode ? (batchSelected ? "取消选择" : "选择") : (occurrence.executionStatus == .done ? "重新打开" : "完成"))

            Button {
                select(NSEvent.modifierFlags.contains(.shift))
            } label: {
                VStack(alignment: .leading, spacing: 4) {
                    Text(task?.title ?? occurrence.title ?? "未命名任务")
                        .fontWeight(.medium)
                        .foregroundStyle(occurrence.executionStatus == .done ? .secondary : .primary)
                        .strikethrough(occurrence.executionStatus == .done)
                    HStack(spacing: 8) {
                        if let project {
                            Label(project.name, systemImage: "folder")
                        }
                        Text(scheduleLabel)
                        StatusBadge(status: occurrence.executionStatus)
                    }
                    .font(.caption)
                    .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .contentShape(.rect)
            }
            .buttonStyle(.plain)
        }
        .padding(.vertical, 7)
        .padding(.horizontal, 10)
        .background(
            batchSelected ? Color.accentColor.opacity(0.18) : (selected ? Color.accentColor.opacity(0.12) : .clear),
            in: .rect(cornerRadius: 9)
        )
        .accessibilityIdentifier("occurrence-row-\(occurrence.id)")
        .simultaneousGesture(TapGesture(count: 2).onEnded { open?() })
        .contextMenu {
            if let contextAction {
                Button("编辑任务", systemImage: "pencil") { contextAction(.editTask) }
                if !occurrence.executionStatus.isTerminal {
                    Button("改期", systemImage: "calendar.badge.clock") { contextAction(.reschedule) }
                        .disabled(isMutating)
                }
                if occurrence.executionStatus == .open {
                    Button("开始", systemImage: "play.fill") { contextAction(.start) }
                        .disabled(isMutating)
                }
                if occurrence.executionStatus == .blocked {
                    Button("解除阻塞", systemImage: "lock.open") { contextAction(.unblock) }
                        .disabled(isMutating)
                } else if occurrence.executionStatus == .open || occurrence.executionStatus == .active {
                    Button("设为阻塞", systemImage: "exclamationmark.octagon") { contextAction(.block) }
                        .disabled(isMutating)
                }
                Button(
                    occurrence.executionStatus == .done ? "重新打开" : "完成",
                    systemImage: occurrence.executionStatus == .done ? "arrow.uturn.backward" : "checkmark"
                ) {
                    contextAction(.toggleCompletion)
                }
                .disabled(isMutating || (occurrence.executionStatus.isTerminal && occurrence.executionStatus != .done))
                Divider()
                Button("复制链接", systemImage: "link") { contextAction(.copyLink) }
                Button("移动到项目", systemImage: "folder") { contextAction(.moveProject) }
                    .disabled(isMutating || task == nil || task?.lifecycleStatus == .archived)
                if task?.lifecycleStatus == .draft
                    || task?.lifecycleStatus == .active
                    || task?.lifecycleStatus == .paused {
                    Button("取消任务", systemImage: "xmark.circle", role: .destructive) {
                        contextAction(.cancelTask)
                    }
                    .disabled(isMutating)
                }
                if task?.lifecycleStatus != .archived {
                    Button("归档任务", systemImage: "archivebox") { contextAction(.archiveTask) }
                        .disabled(isMutating || task == nil)
                }
                Divider()
                Button("在新工作区打开", systemImage: "macwindow.badge.plus") {
                    contextAction(.openInNewWorkspace)
                }
            }
        }
    }

    private var leadingIcon: String {
        if selectionMode { return batchSelected ? "checkmark.square.fill" : "square" }
        return occurrence.executionStatus == .done ? "checkmark.circle.fill" : "circle"
    }

    private var leadingColor: Color {
        if selectionMode { return batchSelected ? .accentColor : .secondary }
        return occurrence.executionStatus == .done ? .green : .secondary
    }

    private var scheduleLabel: String {
        if let start = occurrence.plannedStartAt, let date = Date.flowSpaceISO8601(start) {
            return date.formatted(date: .abbreviated, time: .shortened)
        }
        if let date = occurrence.plannedDate { return date }
        return "未安排"
    }
}

struct OccurrenceCollectionRow: View {
    let collection: OccurrenceCollection
    let projects: [String: ProjectV2]
    let selectedOccurrenceID: String
    let selectionMode: Bool
    let batchSelectedOccurrenceIDs: Set<String>
    let isMutating: Bool
    let select: (OccurrenceV2, Bool) -> Void
    let toggleBatchSelection: (OccurrenceV2) -> Void
    let toggle: (OccurrenceV2) async -> Void
    let open: ((OccurrenceV2) -> Void)?
    let contextAction: ((OccurrenceV2, OccurrenceRowContextAction) -> Void)?
    @State private var expanded = false

    init(
        collection: OccurrenceCollection,
        projects: [String: ProjectV2],
        selectedOccurrenceID: String,
        selectionMode: Bool,
        batchSelectedOccurrenceIDs: Set<String>,
        isMutating: Bool,
        select: @escaping (OccurrenceV2, Bool) -> Void,
        toggleBatchSelection: @escaping (OccurrenceV2) -> Void,
        toggle: @escaping (OccurrenceV2) async -> Void,
        open: ((OccurrenceV2) -> Void)? = nil,
        contextAction: ((OccurrenceV2, OccurrenceRowContextAction) -> Void)? = nil
    ) {
        self.collection = collection
        self.projects = projects
        self.selectedOccurrenceID = selectedOccurrenceID
        self.selectionMode = selectionMode
        self.batchSelectedOccurrenceIDs = batchSelectedOccurrenceIDs
        self.isMutating = isMutating
        self.select = select
        self.toggleBatchSelection = toggleBatchSelection
        self.toggle = toggle
        self.open = open
        self.contextAction = contextAction
    }

    var body: some View {
        DisclosureGroup(isExpanded: $expanded) {
            VStack(spacing: 2) {
                ForEach(collection.occurrences) { occurrence in
                    occurrenceRow(occurrence)
                }
            }
            .padding(.leading, 6)
        } label: {
            collectionLabel
        }
        .onAppear {
            expanded = selectionMode || collection.occurrences.contains { $0.id == selectedOccurrenceID }
        }
        .onChange(of: selectionMode) {
            if selectionMode { expanded = true }
        }
    }

    private func occurrenceRow(_ occurrence: OccurrenceV2) -> OccurrenceRow {
        let projectID = occurrence.projectID ?? collection.task?.projectID ?? ""
        return OccurrenceRow(
            occurrence: occurrence,
            task: collection.task,
            project: projects[projectID],
            selected: occurrence.id == selectedOccurrenceID,
            selectionMode: selectionMode,
            batchSelected: batchSelectedOccurrenceIDs.contains(occurrence.id),
            isMutating: isMutating,
            select: { extending in select(occurrence, extending) },
            toggleBatchSelection: { toggleBatchSelection(occurrence) },
            toggle: { await toggle(occurrence) },
            open: { open?(occurrence) },
            contextAction: { action in contextAction?(occurrence, action) }
        )
    }

    private var collectionLabel: some View {
        HStack {
            VStack(alignment: .leading, spacing: 4) {
                Text(collection.title).fontWeight(.semibold)
                Text(collectionSummary)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Text("\(collection.occurrences.count) 次执行")
                .font(.caption.weight(.medium))
                .foregroundStyle(.secondary)
                .padding(.horizontal, 8)
                .padding(.vertical, 4)
                .background(.quaternary, in: .capsule)
        }
        .padding(.vertical, 8)
    }

    private var collectionSummary: String {
        let done = collection.occurrences.count { $0.executionStatus == .done }
        let first = collection.occurrences.first.flatMap { occurrence -> String? in
            if let start = occurrence.plannedStartAt, let date = Date.flowSpaceISO8601(start) {
                return date.formatted(date: .abbreviated, time: .omitted)
            }
            return occurrence.plannedDate
        } ?? "未安排"
        let last = collection.occurrences.last.flatMap { occurrence -> String? in
            if let start = occurrence.plannedStartAt, let date = Date.flowSpaceISO8601(start) {
                return date.formatted(date: .abbreviated, time: .omitted)
            }
            return occurrence.plannedDate
        } ?? first
        return "\(first == last ? first : "\(first) – \(last)") · 已完成 \(done)/\(collection.occurrences.count)"
    }
}

struct StatusBadge: View {
    let status: ExecutionStatus

    var body: some View {
        Text(label)
            .font(.caption2.weight(.semibold))
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .foregroundStyle(color)
            .background(color.opacity(0.12), in: .capsule)
    }

    private var label: String {
        switch status {
        case .open: "未开始"
        case .active: "进行中"
        case .blocked: "已阻塞"
        case .done: "已完成"
        case .skipped: "已跳过"
        case .cancelled: "已取消"
        }
    }

    private var color: Color {
        switch status {
        case .open: .secondary
        case .active: .blue
        case .blocked: .orange
        case .done: .green
        case .skipped: .purple
        case .cancelled: .red
        }
    }
}
