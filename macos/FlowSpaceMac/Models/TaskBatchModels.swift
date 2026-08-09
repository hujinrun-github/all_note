import Foundation

enum TaskBatchAction: Sendable, Equatable {
    case complete
    case move(projectID: String, projectName: String)
    case priority(TaskPriorityLevel)
    case reschedule(Date)
    case cancel

    var title: String {
        switch self {
        case .complete: "批量完成"
        case .move: "批量移动"
        case .priority: "批量设置优先级"
        case .reschedule: "批量改期"
        case .cancel: "批量取消任务"
        }
    }

    var operatesOnTaskDefinitions: Bool {
        switch self {
        case .move, .priority, .cancel: true
        case .complete, .reschedule: false
        }
    }
}

struct TaskBatchItemResult: Identifiable, Sendable, Equatable {
    let id: String
    let title: String
    let succeeded: Bool
    let detail: String
}

struct TaskBatchReport: Sendable, Equatable {
    let action: TaskBatchAction
    let selectedOccurrenceCount: Int
    let affectedTaskCount: Int
    let results: [TaskBatchItemResult]

    var successCount: Int { results.count { $0.succeeded } }
    var failureCount: Int { results.count - successCount }
    var isCompleteSuccess: Bool { !results.isEmpty && failureCount == 0 }

    var summary: String {
        if isCompleteSuccess {
            return "已成功处理全部 \(successCount) 项"
        }
        if successCount == 0 {
            return "没有项目处理成功，\(failureCount) 项失败"
        }
        return "部分完成：\(successCount) 项成功，\(failureCount) 项失败"
    }
}

enum TaskBatchPlanner {
    static func uniqueTasks(
        for occurrences: [OccurrenceV2],
        tasksByID: [String: TaskV2]
    ) -> [TaskV2] {
        var seen = Set<String>()
        return occurrences.compactMap { occurrence in
            guard seen.insert(occurrence.taskID).inserted else { return nil }
            return tasksByID[occurrence.taskID]
        }
    }

    static func selectionRange(
        from anchorID: String,
        through targetID: String,
        in orderedIDs: [String]
    ) -> Set<String> {
        guard let anchor = orderedIDs.firstIndex(of: anchorID),
              let target = orderedIDs.firstIndex(of: targetID) else {
            return [targetID]
        }
        return Set(orderedIDs[min(anchor, target)...max(anchor, target)])
    }
}
