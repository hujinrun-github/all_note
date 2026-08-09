import Foundation
import XCTest
@testable import FlowSpaceMac

final class TaskBatchModelsTests: XCTestCase {
    func testUniqueTasksPreservesFirstSelectedOrderAndDeduplicatesRecurringExecutions() throws {
        let decoder = JSONDecoder.flowSpace()
        let tasks = try decoder.decode([TaskV2].self, from: Data(tasksJSON.utf8))
        let occurrences = try decoder.decode([OccurrenceV2].self, from: Data(occurrencesJSON.utf8))

        let result = TaskBatchPlanner.uniqueTasks(
            for: occurrences,
            tasksByID: Dictionary(uniqueKeysWithValues: tasks.map { ($0.id, $0) })
        )

        XCTAssertEqual(result.map(\.id), ["task-1", "task-2"])
    }

    func testSelectionRangeWorksInBothDirections() {
        let ids = ["a", "b", "c", "d"]
        XCTAssertEqual(TaskBatchPlanner.selectionRange(from: "b", through: "d", in: ids), ["b", "c", "d"])
        XCTAssertEqual(TaskBatchPlanner.selectionRange(from: "d", through: "b", in: ids), ["b", "c", "d"])
    }

    func testPartialReportNeverClaimsCompleteSuccess() {
        let report = TaskBatchReport(
            action: .complete,
            selectedOccurrenceCount: 2,
            affectedTaskCount: 2,
            results: [
                .init(id: "1", title: "成功", succeeded: true, detail: "已完成"),
                .init(id: "2", title: "失败", succeeded: false, detail: "版本冲突"),
            ]
        )

        XCTAssertFalse(report.isCompleteSuccess)
        XCTAssertEqual(report.successCount, 1)
        XCTAssertEqual(report.failureCount, 1)
        XCTAssertEqual(report.summary, "部分完成：1 项成功，1 项失败")
    }

    private let tasksJSON = #"""
    [
      {"id":"task-1","project_id":"project-1","title":"重复任务","priority":0,"sort_order":0,"lifecycle_status":"active","revision":3,"schedule_revision":4},
      {"id":"task-2","project_id":"project-1","title":"单次任务","priority":1,"sort_order":1,"lifecycle_status":"active","revision":1,"schedule_revision":1}
    ]
    """#

    private let occurrencesJSON = #"""
    [
      {"id":"occ-1","task_id":"task-1","occurrence_key":"2026-08-08","execution_status":"open","revision":1,"generated_schedule_revision":4},
      {"id":"occ-2","task_id":"task-1","occurrence_key":"2026-08-09","execution_status":"open","revision":1,"generated_schedule_revision":4},
      {"id":"occ-3","task_id":"task-2","occurrence_key":"2026-08-09","execution_status":"open","revision":1,"generated_schedule_revision":1}
    ]
    """#
}
