import SwiftUI

struct OccurrenceInspector: View {
    let occurrence: OccurrenceV2
    let store: WorkspaceStore
    let refresh: () async -> Void
    @State private var localError: String?
    @State private var editingTask = false
    @State private var blockingOccurrence = false
    @State private var scheduleEntry: CalendarEntryV2?

    private var task: TaskV2? { store.tasksByID[occurrence.taskID] }
    private var project: ProjectV2? {
        store.projectsByID[occurrence.projectID ?? task?.projectID ?? ""]
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                VStack(alignment: .leading, spacing: 8) {
                    Text(task?.title ?? occurrence.title ?? "未命名任务")
                        .font(.title2.weight(.semibold))
                    StatusBadge(status: occurrence.executionStatus)
                }

                HStack {
                    if occurrence.executionStatus == .done {
                        Button("重新打开", systemImage: "arrow.uturn.backward") {
                            Task { await toggle() }
                        }
                    } else {
                        Button("完成", systemImage: "checkmark") {
                            Task { await toggle() }
                        }
                        .buttonStyle(.borderedProminent)

                        if occurrence.executionStatus == .open {
                            Button("开始", systemImage: "play.fill") {
                                Task { await start() }
                            }
                        }
                    }
                }
                .disabled(store.isMutating)

                if !occurrence.executionStatus.isTerminal {
                    HStack {
                        Button("编辑任务", systemImage: "pencil") {
                            editingTask = true
                        }
                        Button("改期", systemImage: "calendar.badge.clock") {
                            if let task {
                                scheduleEntry = CalendarEntryV2(
                                    occurrence: occurrence,
                                    task: task,
                                    project: project
                                )
                            }
                        }
                        if occurrence.executionStatus == .blocked {
                            Button("解除阻塞", systemImage: "lock.open") {
                                Task { await unblock() }
                            }
                        } else if occurrence.executionStatus == .open || occurrence.executionStatus == .active {
                            Button("设为阻塞", systemImage: "exclamationmark.octagon") {
                                blockingOccurrence = true
                            }
                        }
                    }
                    .disabled(store.isMutating)
                }

                if let localError {
                    Label(localError, systemImage: "exclamationmark.triangle")
                        .font(.callout)
                        .foregroundStyle(.red)
                }

                Divider()

                InspectorField(label: "所属项目", value: project?.name ?? "未归属")
                InspectorField(label: "本次安排", value: scheduleLabel)
                InspectorField(label: "执行状态", value: occurrence.executionStatus.rawValue)
                InspectorField(label: "任务状态", value: task?.lifecycleStatus.rawValue ?? "—")
                InspectorField(label: "优先级", value: task.map { String($0.priority) } ?? "—")
                InspectorField(label: "重复", value: occurrence.recurring == true ? "重复任务实例" : "单次")

                if let reason = occurrence.blockedReason, !reason.isEmpty {
                    InspectorField(label: "阻塞原因", value: reason)
                }
                if let next = occurrence.nextAction, !next.isEmpty {
                    InspectorField(label: "下一步", value: next)
                }

                Divider()
                VStack(alignment: .leading, spacing: 6) {
                    Text("并发版本")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Text("任务 r\(occurrence.taskRevision ?? task?.revision ?? 0) · 安排 r\(occurrence.scheduleRevision ?? task?.scheduleRevision ?? 0) · 执行 r\(occurrence.revision)")
                        .font(.caption.monospacedDigit())
                        .textSelection(.enabled)
                }
            }
            .padding(22)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .sheet(isPresented: $editingTask) {
            if let task {
                TaskDefinitionEditorSheet(task: task, store: store) {
                    await refresh()
                }
            }
        }
        .sheet(isPresented: $blockingOccurrence) {
            OccurrenceBlockSheet(occurrence: occurrence, store: store) {
                await refresh()
            }
        }
        .sheet(item: $scheduleEntry) { entry in
            let timezone = TimeZone(identifier: entry.timezone) ?? .current
            CalendarEntryEditorSheet(entry: entry, timezone: timezone, store: store) {
                await refresh()
            }
            .environment(\.timeZone, timezone)
        }
    }

    private var scheduleLabel: String {
        if let start = occurrence.plannedStartAt, let date = Date.flowSpaceISO8601(start) {
            let end = occurrence.plannedEndAt.flatMap(Date.flowSpaceISO8601)
            if let end {
                return "\(date.formatted(date: .abbreviated, time: .shortened))–\(end.formatted(date: .omitted, time: .shortened))"
            }
            return date.formatted(date: .abbreviated, time: .shortened)
        }
        return occurrence.plannedDate ?? "未安排"
    }

    private func toggle() async {
        localError = nil
        do {
            try await store.toggle(occurrence)
            await refresh()
        } catch {
            localError = error.localizedDescription
            if let apiError = error as? APIError, apiError.isRevisionConflict {
                await refresh()
            }
        }
    }

    private func start() async {
        localError = nil
        do {
            try await store.start(occurrence)
            await refresh()
        } catch {
            localError = error.localizedDescription
            if let apiError = error as? APIError, apiError.isRevisionConflict {
                await refresh()
            }
        }
    }

    private func unblock() async {
        localError = nil
        do {
            try await store.unblock(occurrence)
            await refresh()
        } catch {
            localError = error.localizedDescription
            if let apiError = error as? APIError, apiError.isRevisionConflict {
                await refresh()
            }
        }
    }
}

private struct OccurrenceBlockSheet: View {
    @Environment(\.dismiss) private var dismiss
    let occurrence: OccurrenceV2
    let store: WorkspaceStore
    let onSaved: () async -> Void
    @State private var reason = ""
    @State private var nextAction = ""
    @State private var errorMessage: String?

    var body: some View {
        NavigationStack {
            Form {
                Section("阻塞信息") {
                    TextField("阻塞原因", text: $reason, axis: .vertical)
                        .lineLimit(2...5)
                    TextField("解除阻塞需要采取的下一步", text: $nextAction, axis: .vertical)
                        .lineLimit(2...5)
                }
                Section {
                    Text("阻塞原因和下一步会显示在任务详情中，便于之后继续推进。")
                        .foregroundStyle(.secondary)
                }
                if let errorMessage {
                    Section {
                        Label(errorMessage, systemImage: "exclamationmark.triangle")
                            .foregroundStyle(.red)
                    }
                }
            }
            .formStyle(.grouped)
            .navigationTitle("阻塞本次执行")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("取消") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("设为阻塞") { Task { await save() } }
                        .buttonStyle(.borderedProminent)
                        .disabled(
                            reason.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                                || nextAction.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                                || store.isMutating
                        )
                }
            }
        }
        .frame(width: 500, height: 390)
    }

    private func save() async {
        errorMessage = nil
        do {
            try await store.block(occurrence, reason: reason, nextAction: nextAction)
            await onSaved()
            dismiss()
        } catch let error as APIError where error.isRevisionConflict {
            errorMessage = "执行状态已更新，请关闭后重新打开。"
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}

struct InspectorField: View {
    let label: String
    let value: String

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            Text(label)
                .foregroundStyle(.secondary)
                .frame(width: 72, alignment: .leading)
            Text(value)
                .frame(maxWidth: .infinity, alignment: .leading)
                .textSelection(.enabled)
        }
        .font(.callout)
    }
}
