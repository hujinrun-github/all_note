import SwiftUI

enum TaskBatchOperationKind: String, Identifiable {
    case complete
    case move
    case priority
    case reschedule
    case cancel

    var id: String { rawValue }
}

struct TaskBatchRequest: Identifiable {
    let id = UUID()
    let kind: TaskBatchOperationKind
    let occurrences: [OccurrenceV2]
}

struct TaskBatchOperationSheet: View {
    @Environment(\.dismiss) private var dismiss
    let request: TaskBatchRequest
    let projects: [ProjectV2]
    let tasksByID: [String: TaskV2]
    let perform: (TaskBatchAction, [OccurrenceV2]) async -> TaskBatchReport

    @State private var projectID = ""
    @State private var priority: TaskPriorityLevel = .normal
    @State private var targetDate = Calendar.current.date(byAdding: .day, value: 1, to: Date()) ?? Date()
    @State private var isApplying = false
    @State private var report: TaskBatchReport?

    private var uniqueTaskCount: Int {
        TaskBatchPlanner.uniqueTasks(for: request.occurrences, tasksByID: tasksByID).count
    }

    var body: some View {
        NavigationStack {
            Group {
                if let report {
                    resultView(report)
                } else {
                    confirmationView
                }
            }
            .navigationTitle(title)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button(report == nil ? "取消" : "关闭") { dismiss() }
                        .disabled(isApplying)
                }
                if report == nil {
                    ToolbarItem(placement: .confirmationAction) {
                        Button(confirmTitle) { Task { await apply() } }
                            .buttonStyle(.borderedProminent)
                            .tint(request.kind == .cancel ? .red : .accentColor)
                            .disabled(isApplying || !configurationIsValid)
                    }
                }
            }
        }
        .frame(width: 560, height: report == nil ? 400 : 520)
        .onAppear {
            if projectID.isEmpty { projectID = projects.first?.id ?? "" }
        }
        .interactiveDismissDisabled(isApplying)
    }

    private var confirmationView: some View {
        Form {
            Section("选择范围") {
                LabeledContent("所选执行", value: "\(request.occurrences.count) 项")
                LabeledContent("涉及任务定义", value: "\(uniqueTaskCount) 个")
                if uniqueTaskCount < request.occurrences.count {
                    Label(
                        "同一重复任务的多次执行已归为一个任务集合；移动、优先级和取消只会对任务定义执行一次。",
                        systemImage: "rectangle.stack"
                    )
                    .font(.callout)
                    .foregroundStyle(.secondary)
                }
            }

            switch request.kind {
            case .complete:
                Section("操作") {
                    Text("逐项完成所选执行。已结束的执行会保留为失败项，不会影响其他执行。")
                }
            case .move:
                Section("移动到项目") {
                    Picker("项目", selection: $projectID) {
                        ForEach(projects) { project in Text(project.name).tag(project.id) }
                    }
                }
            case .priority:
                Section("新的优先级") {
                    Picker("优先级", selection: $priority) {
                        ForEach(TaskPriorityLevel.allCases) { level in Text(level.title).tag(level) }
                    }
                    .pickerStyle(.segmented)
                }
            case .reschedule:
                Section("新的日期") {
                    DatePicker("日期", selection: $targetDate, displayedComponents: .date)
                    Text("这是可调整的默认日期。原有具体开始时间和时长会保留；未指定时间的执行仍为全天任务。")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                }
            case .cancel:
                Section("确认取消") {
                    Label(
                        "取消按任务定义执行，会影响这些任务的后续安排；这不是删除，历史记录仍会保留。",
                        systemImage: "exclamationmark.triangle"
                    )
                    .foregroundStyle(.red)
                }
            }

            if isApplying {
                Section {
                    HStack {
                        ProgressView().controlSize(.small)
                        Text("正在逐项提交并校验版本…")
                    }
                }
            }
        }
        .formStyle(.grouped)
    }

    private func resultView(_ report: TaskBatchReport) -> some View {
        VStack(alignment: .leading, spacing: 14) {
            Label(
                report.summary,
                systemImage: report.isCompleteSuccess
                    ? "checkmark.circle.fill"
                    : (report.successCount == 0 ? "xmark.octagon.fill" : "exclamationmark.triangle.fill")
            )
            .font(.title3.weight(.semibold))
            .foregroundStyle(report.isCompleteSuccess ? .green : (report.successCount == 0 ? .red : .orange))

            Text("所选 \(report.selectedOccurrenceCount) 次执行，涉及 \(report.affectedTaskCount) 个任务定义。")
                .font(.callout)
                .foregroundStyle(.secondary)

            List {
                ForEach(report.results) { result in
                    TaskBatchResultRow(result: result)
                }
            }
            .listStyle(.inset)
        }
        .padding(22)
    }

    private var title: String {
        switch request.kind {
        case .complete: "批量完成"
        case .move: "批量移动"
        case .priority: "批量设置优先级"
        case .reschedule: "批量改期"
        case .cancel: "批量取消任务"
        }
    }

    private var confirmTitle: String {
        request.kind == .cancel ? "确认取消" : "开始处理"
    }

    private var configurationIsValid: Bool {
        request.kind != .move || !projectID.isEmpty
    }

    private func apply() async {
        isApplying = true
        defer { isApplying = false }
        let action: TaskBatchAction
        switch request.kind {
        case .complete:
            action = .complete
        case .move:
            let projectName = projects.first(where: { $0.id == projectID })?.name ?? "目标项目"
            action = .move(projectID: projectID, projectName: projectName)
        case .priority:
            action = .priority(priority)
        case .reschedule:
            action = .reschedule(targetDate)
        case .cancel:
            action = .cancel
        }
        report = await perform(action, request.occurrences)
    }
}

private struct TaskBatchResultRow: View {
    let result: TaskBatchItemResult

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: result.succeeded ? "checkmark.circle.fill" : "xmark.circle.fill")
                .foregroundStyle(result.succeeded ? Color.green : Color.red)
            VStack(alignment: .leading, spacing: 3) {
                Text(result.title).fontWeight(.medium)
                Text(result.detail)
                    .font(.caption)
                    .foregroundStyle(result.succeeded ? Color.secondary : Color.red)
            }
        }
        .padding(.vertical, 3)
    }
}
