import XCTest
@testable import FlowSpaceMac

final class MenuBarTaskSummaryTests: XCTestCase {
    func testActiveOccurrenceWinsAndUsesTaskDefinitionTitle() throws {
        let open = occurrence(
            id: "open",
            taskID: "task-open",
            status: .open,
            plannedStartAt: "2026-08-08T09:00:00+08:00"
        )
        let active = occurrence(
            id: "active",
            taskID: "task-active",
            status: .active,
            plannedStartAt: "2026-08-08T10:00:00+08:00"
        )

        let summary = try XCTUnwrap(MenuBarTaskSummaryBuilder.next(
            occurrences: [open, active],
            tasks: [task(id: "task-open", title: "普通任务"), task(id: "task-active", title: "正在推进的任务")]
        ))

        XCTAssertEqual(summary.occurrenceID, "active")
        XCTAssertEqual(summary.title, "正在推进的任务")
    }

    func testTerminalOccurrencesAreExcluded() {
        let summary = MenuBarTaskSummaryBuilder.next(
            occurrences: [occurrence(id: "done", taskID: "task", status: .done)],
            tasks: [task(id: "task", title: "已完成任务")]
        )
        XCTAssertNil(summary)
    }

    func testMenuTitleTruncatesAtThirtyCharacters() {
        let title = String(repeating: "任", count: 31)
        let truncated = MenuBarTaskSummary.truncate(title, limit: 30)
        XCTAssertEqual(truncated, String(repeating: "任", count: 30) + "…")
    }

    private func task(id: String, title: String) -> TaskV2 {
        TaskV2(
            id: id,
            projectID: "project",
            roadmapNodeID: nil,
            taskNoteID: nil,
            title: title,
            description: nil,
            priority: 0,
            sortOrder: 0,
            lifecycleStatus: .active,
            revision: 1,
            scheduleRevision: 1
        )
    }

    private func occurrence(
        id: String,
        taskID: String,
        status: ExecutionStatus,
        plannedStartAt: String? = nil
    ) -> OccurrenceV2 {
        OccurrenceV2(
            id: id,
            taskID: taskID,
            projectID: "project",
            title: nil,
            occurrenceKey: id,
            executionStatus: status,
            revision: 1,
            generatedScheduleRevision: 1,
            plannedDate: "2026-08-08",
            allDayEndDate: nil,
            plannedStartAt: plannedStartAt,
            plannedEndAt: nil,
            dueAt: nil,
            blockedReason: nil,
            nextAction: nil,
            location: nil,
            recurrenceType: RecurrenceType.none,
            recurring: false,
            taskRevision: 1,
            scheduleRevision: 1,
            timingType: plannedStartAt == nil ? .date : .timeBlock,
            timezone: "Asia/Shanghai"
        )
    }
}
