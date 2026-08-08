import Foundation

enum MenuBarPreferences {
    static let enabledKey = "menuBarExtra.enabled"
}

struct MenuBarTaskSummary: Equatable, Sendable {
    let occurrenceID: String
    let title: String
    let schedule: String

    var shortTitle: String {
        Self.truncate(title, limit: 30)
    }

    static func truncate(_ value: String, limit: Int) -> String {
        guard value.count > limit else { return value }
        return String(value.prefix(limit)) + "…"
    }
}

enum MenuBarTaskSummaryBuilder {
    static func next(
        occurrences: [OccurrenceV2],
        tasks: [TaskV2],
        calendar: Calendar = .current
    ) -> MenuBarTaskSummary? {
        let tasksByID = Dictionary(uniqueKeysWithValues: tasks.map { ($0.id, $0) })
        guard let occurrence = occurrences
            .filter({ !$0.executionStatus.isTerminal })
            .sorted(by: menuOrder)
            .first else {
            return nil
        }

        return MenuBarTaskSummary(
            occurrenceID: occurrence.id,
            title: tasksByID[occurrence.taskID]?.title ?? occurrence.title ?? "未命名任务",
            schedule: scheduleLabel(for: occurrence, calendar: calendar)
        )
    }

    private static func menuOrder(_ lhs: OccurrenceV2, _ rhs: OccurrenceV2) -> Bool {
        if (lhs.executionStatus == .active) != (rhs.executionStatus == .active) {
            return lhs.executionStatus == .active
        }
        return OccurrenceV2.scheduleAscending(lhs, rhs)
    }

    private static func scheduleLabel(for occurrence: OccurrenceV2, calendar: Calendar) -> String {
        if let raw = occurrence.plannedStartAt,
           let date = Date.flowSpaceISO8601(raw) {
            if calendar.isDateInToday(date) {
                return date.formatted(date: .omitted, time: .shortened)
            }
            return date.formatted(date: .abbreviated, time: .shortened)
        }
        if occurrence.plannedDate != nil {
            return "全天"
        }
        return "未安排"
    }
}
