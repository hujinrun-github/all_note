import Foundation
import XCTest
@testable import FlowSpaceMac

final class OccurrenceCollectionTests: XCTestCase {
    func testGroupsRepeatedExecutionsByTaskAndKeepsScheduleOrder() throws {
        let decoder = JSONDecoder.flowSpace()
        let task = try decoder.decode(TaskV2.self, from: Data(taskJSON.utf8))
        let occurrences = try decoder.decode([OccurrenceV2].self, from: Data(occurrencesJSON.utf8))

        let groups = OccurrenceCollection.group(occurrences, tasks: [task.id: task])

        XCTAssertEqual(groups.count, 1)
        XCTAssertEqual(groups[0].title, "单词学习")
        XCTAssertEqual(groups[0].occurrences.map(\.id), ["occurrence-early", "occurrence-late"])
    }

    private let taskJSON = #"""
    {
      "id": "task-1",
      "project_id": "project-1",
      "title": "单词学习",
      "priority": 0,
      "sort_order": 0,
      "lifecycle_status": "active",
      "revision": 3,
      "schedule_revision": 4
    }
    """#

    private let occurrencesJSON = #"""
    [
      {
        "id": "occurrence-late",
        "task_id": "task-1",
        "occurrence_key": "2026-08-09",
        "execution_status": "open",
        "revision": 2,
        "generated_schedule_revision": 4,
        "planned_start_at": "2026-08-09T11:00:00+08:00"
      },
      {
        "id": "occurrence-early",
        "task_id": "task-1",
        "occurrence_key": "2026-08-08",
        "execution_status": "done",
        "revision": 5,
        "generated_schedule_revision": 4,
        "planned_start_at": "2026-08-08T11:00:00+08:00"
      }
    ]
    """#
}
