import Foundation

enum TaskWorkspaceView: String, CaseIterable, Identifiable, Sendable {
    case inbox
    case today
    case upcoming
    case overdue
    case unscheduled
    case recurring
    case completed
    case draft

    var id: String { rawValue }

    var title: String {
        switch self {
        case .inbox: "任务收件箱"
        case .today: "今天"
        case .upcoming: "接下来"
        case .overdue: "已逾期"
        case .unscheduled: "无日期"
        case .recurring: "重复"
        case .completed: "已完成"
        case .draft: "草稿"
        }
    }

    var systemImage: String {
        switch self {
        case .inbox: "tray"
        case .today: "sun.max"
        case .upcoming: "calendar.badge.clock"
        case .overdue: "exclamationmark.clock"
        case .unscheduled: "calendar.badge.minus"
        case .recurring: "repeat"
        case .completed: "checkmark.circle"
        case .draft: "doc.badge.ellipsis"
        }
    }

    func occurrenceQuery(
        inboxProjectID: String?,
        now: Date = Date(),
        calendar: Calendar = .current,
        timezone: TimeZone = .current
    ) -> [URLQueryItem]? {
        guard self != .draft else { return nil }
        var query = [
            URLQueryItem(name: "timezone", value: timezone.identifier),
            URLQueryItem(name: "scope", value: occurrenceScope),
        ]
        let startOfToday = calendar.startOfDay(for: now)
        let startOfTomorrow = calendar.date(byAdding: .day, value: 1, to: startOfToday) ?? now

        switch self {
        case .inbox:
            guard let inboxProjectID, !inboxProjectID.isEmpty else { return [] }
            query.append(URLQueryItem(name: "project_id", value: inboxProjectID))
        case .today:
            query += range(startOfToday, startOfTomorrow)
        case .upcoming:
            let end = calendar.date(byAdding: .day, value: 7, to: startOfTomorrow) ?? startOfTomorrow
            query += range(startOfTomorrow, end)
        case .overdue:
            query.append(URLQueryItem(name: "from", value: APIClient.iso8601String(now)))
        case .completed:
            let start = calendar.date(byAdding: .day, value: -30, to: startOfToday) ?? startOfToday
            query += range(start, startOfTomorrow)
        case .recurring:
            query.append(URLQueryItem(name: "recurring", value: "true"))
        case .unscheduled, .draft:
            break
        }
        return query
    }

    private var occurrenceScope: String {
        switch self {
        case .inbox, .recurring: "all"
        case .today: "today"
        case .upcoming: "upcoming"
        case .overdue: "overdue"
        case .unscheduled: "unscheduled"
        case .completed: "completed"
        case .draft: "all"
        }
    }

    private func range(_ start: Date, _ end: Date) -> [URLQueryItem] {
        [
            URLQueryItem(name: "from", value: APIClient.iso8601String(start)),
            URLQueryItem(name: "to", value: APIClient.iso8601String(end)),
        ]
    }
}

enum TaskDateFilter: String, CaseIterable, Identifiable, Sendable {
    case current
    case today
    case nextSevenDays = "next-7-days"
    case unscheduled

    var id: String { rawValue }

    var title: String {
        switch self {
        case .current: "当前视图"
        case .today: "今天"
        case .nextSevenDays: "未来 7 天"
        case .unscheduled: "无日期"
        }
    }
}

struct TaskWorkspaceFilter: Equatable, Sendable {
    var projectID = ""
    var priority: Int?
    var executionStatus: ExecutionStatus?
    var date: TaskDateFilter = .current

    var isActive: Bool {
        !projectID.isEmpty || priority != nil || executionStatus != nil || date != .current
    }

    func occurrences(
        _ occurrences: [OccurrenceV2],
        tasks: [String: TaskV2],
        now: Date = Date(),
        calendar: Calendar = .current
    ) -> [OccurrenceV2] {
        occurrences.filter { occurrence in
            let task = tasks[occurrence.taskID]
            let occurrenceProjectID = occurrence.projectID ?? task?.projectID ?? ""
            guard projectID.isEmpty || occurrenceProjectID == projectID else { return false }
            guard priority == nil || task?.priority == priority else { return false }
            guard executionStatus == nil || occurrence.executionStatus == executionStatus else { return false }
            return matchesDate(occurrence, now: now, calendar: calendar)
        }
    }

    func drafts(_ tasks: [TaskV2]) -> [TaskV2] {
        tasks.filter { task in
            task.lifecycleStatus == .draft &&
                (projectID.isEmpty || task.projectID == projectID) &&
                (priority == nil || task.priority == priority)
        }
    }

    private func matchesDate(_ occurrence: OccurrenceV2, now: Date, calendar: Calendar) -> Bool {
        guard date != .current else { return true }
        guard let scheduledDate = occurrenceDate(occurrence, calendar: calendar) else {
            return date == .unscheduled
        }
        guard date != .unscheduled else { return false }
        let start = calendar.startOfDay(for: now)
        let next = calendar.date(byAdding: .day, value: 1, to: start) ?? start
        if date == .today { return scheduledDate >= start && scheduledDate < next }
        let end = calendar.date(byAdding: .day, value: 7, to: start) ?? next
        return scheduledDate >= start && scheduledDate < end
    }

    private func occurrenceDate(_ occurrence: OccurrenceV2, calendar: Calendar) -> Date? {
        if let start = occurrence.plannedStartAt.flatMap(Date.flowSpaceISO8601) {
            return start
        }
        guard let plannedDate = occurrence.plannedDate else { return nil }
        let parts = plannedDate.split(separator: "-").compactMap { Int($0) }
        guard parts.count == 3 else { return nil }
        return calendar.date(from: DateComponents(year: parts[0], month: parts[1], day: parts[2]))
    }
}

enum TaskLifecycleCommand: String, Sendable {
    case publish
    case pause
    case resume
    case cancel
    case restore
    case archive
}

struct TaskDeleteResponse: Codable, Sendable {
    let taskID: String
    let deleted: Bool
    let taskRevision: Int
}
