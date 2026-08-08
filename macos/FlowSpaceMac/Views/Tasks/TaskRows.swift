import SwiftUI

struct OccurrenceRow: View {
    let occurrence: OccurrenceV2
    let task: TaskV2?
    let project: ProjectV2?
    let selected: Bool
    let isMutating: Bool
    let select: () -> Void
    let toggle: () async -> Void

    var body: some View {
        HStack(spacing: 12) {
            Button {
                Task { await toggle() }
            } label: {
                Image(systemName: occurrence.executionStatus == .done ? "checkmark.circle.fill" : "circle")
                    .font(.title3)
                    .foregroundStyle(occurrence.executionStatus == .done ? .green : .secondary)
            }
            .buttonStyle(.plain)
            .disabled(isMutating || (occurrence.executionStatus.isTerminal && occurrence.executionStatus != .done))
            .accessibilityLabel(occurrence.executionStatus == .done ? "重新打开" : "完成")

            Button(action: select) {
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
        .background(selected ? Color.accentColor.opacity(0.12) : .clear, in: .rect(cornerRadius: 9))
        .accessibilityIdentifier("occurrence-row-\(occurrence.id)")
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
    let isMutating: Bool
    let select: (OccurrenceV2) -> Void
    let toggle: (OccurrenceV2) async -> Void
    @State private var expanded = false

    var body: some View {
        DisclosureGroup(isExpanded: $expanded) {
            VStack(spacing: 2) {
                ForEach(collection.occurrences) { occurrence in
                    OccurrenceRow(
                        occurrence: occurrence,
                        task: collection.task,
                        project: projects[occurrence.projectID ?? collection.task?.projectID ?? ""],
                        selected: occurrence.id == selectedOccurrenceID,
                        isMutating: isMutating,
                        select: { select(occurrence) },
                        toggle: { await toggle(occurrence) }
                    )
                }
            }
            .padding(.leading, 6)
        } label: {
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
        .onAppear {
            expanded = collection.occurrences.contains { $0.id == selectedOccurrenceID }
        }
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

