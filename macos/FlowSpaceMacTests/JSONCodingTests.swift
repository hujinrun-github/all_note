import Foundation
import XCTest
@testable import FlowSpaceMac

final class JSONCodingTests: XCTestCase {
    func testDecodesBackendIDAcronyms() throws {
        let data = Data(#"""
        {
          "id": "user-1",
          "email": "user@example.com",
          "display_name": "User",
          "must_change_password": false,
          "default_workspace_id": "workspace-1",
          "role": "user",
          "status": "active"
        }
        """#.utf8)

        let user = try JSONDecoder.flowSpace().decode(AccountUser.self, from: data)
        XCTAssertEqual(user.defaultWorkspaceID, "workspace-1")
        XCTAssertEqual(user.displayName, "User")
    }

    func testEncodesTaskCreationUsingBackendSnakeCaseKeys() throws {
        let input = CreateTaskInput(
            projectID: "project-1",
            roadmapNodeID: "roadmap-node-1",
            title: "单词学习",
            description: nil,
            priority: 0,
            sortOrder: 0,
            schedule: ScheduleInput(
                recurrenceType: .daily,
                timingType: .timeBlock,
                timezone: "Asia/Shanghai",
                startsOn: "2026-08-08",
                endsOn: nil,
                localStartTime: "11:00",
                durationMinutes: 30,
                rule: RecurrenceRule(interval: 1, weekdays: nil, monthDays: nil)
            )
        )

        let data = try JSONEncoder.flowSpace().encode(input)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let schedule = try XCTUnwrap(object["schedule"] as? [String: Any])

        XCTAssertEqual(object["project_id"] as? String, "project-1")
        XCTAssertEqual(object["roadmap_node_id"] as? String, "roadmap-node-1")
        XCTAssertEqual(schedule["timing_type"] as? String, "time_block")
        XCTAssertEqual(schedule["local_start_time"] as? String, "11:00")
    }

    func testTaskPriorityLevelsMatchTaskDomainRange() {
        XCTAssertEqual(TaskPriorityLevel.allCases.map(\.rawValue), [0, 1, 2, 3])
        XCTAssertEqual(TaskPriorityLevel.urgent.title, "紧急")
        XCTAssertNil(TaskPriorityLevel(rawValue: -1))
        XCTAssertNil(TaskPriorityLevel(rawValue: 4))
    }

    func testEncodesOccurrenceRescheduleWithRevisionsAndDSTOffset() throws {
        let input = RescheduleOccurrenceInput(
            expectedTaskRevision: 2,
            expectedScheduleRevision: 3,
            expectedOccurrenceRevision: 4,
            timing: OccurrenceTimingInput(
                timingType: .timeBlock,
                timezone: "America/New_York",
                plannedDate: "2026-11-01",
                allDayEndDate: nil,
                localStartTime: "01:30",
                durationMinutes: 30
            ),
            selectedOffsets: ["2026-11-01": -14_400]
        )

        let data = try JSONEncoder.flowSpace().encode(input)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let timing = try XCTUnwrap(object["timing"] as? [String: Any])
        let offsets = try XCTUnwrap(object["selected_offsets"] as? [String: Any])

        XCTAssertEqual(object["expected_task_revision"] as? Int, 2)
        XCTAssertEqual(object["expected_schedule_revision"] as? Int, 3)
        XCTAssertEqual(object["expected_occurrence_revision"] as? Int, 4)
        XCTAssertEqual(timing["planned_date"] as? String, "2026-11-01")
        XCTAssertEqual(timing["local_start_time"] as? String, "01:30")
        XCTAssertEqual(offsets["2026-11-01"] as? Int, -14_400)
    }

    func testEncodesProjectCASAndTaskMigrationContracts() throws {
        let update = UpdateProjectInput(
            name: "日语日常学习",
            kind: .learning,
            horizon: .long,
            expectedProjectRevision: 7
        )
        let updateData = try JSONEncoder.flowSpace().encode(update)
        let updateObject = try XCTUnwrap(JSONSerialization.jsonObject(with: updateData) as? [String: Any])
        XCTAssertEqual(updateObject["expected_project_revision"] as? Int, 7)
        XCTAssertEqual(updateObject["kind"] as? String, "learning")
        XCTAssertNil(updateObject["status"])

        let move = MoveTaskInput(
            projectID: "project-target",
            expectedTaskRevision: 3,
            expectedScheduleRevision: 5
        )
        let moveData = try JSONEncoder.flowSpace().encode(move)
        let moveObject = try XCTUnwrap(JSONSerialization.jsonObject(with: moveData) as? [String: Any])
        XCTAssertEqual(moveObject["project_id"] as? String, "project-target")
        XCTAssertEqual(moveObject["expected_task_revision"] as? Int, 3)
        XCTAssertEqual(moveObject["expected_schedule_revision"] as? Int, 5)
    }

    func testEncodesTaskDefinitionUpdateAndBlockMetadata() throws {
        let update = UpdateTaskDefinitionInput(
            title: "复习单词",
            description: "第二课",
            priority: 2,
            projectID: "project-japanese",
            taskNoteID: "note-lesson-2",
            expectedTaskRevision: 4,
            expectedScheduleRevision: 6
        )
        let updateData = try JSONEncoder.flowSpace().encode(update)
        let updateObject = try XCTUnwrap(JSONSerialization.jsonObject(with: updateData) as? [String: Any])
        XCTAssertEqual(updateObject["title"] as? String, "复习单词")
        XCTAssertEqual(updateObject["priority"] as? Int, 2)
        XCTAssertEqual(updateObject["project_id"] as? String, "project-japanese")
        XCTAssertEqual(updateObject["task_note_id"] as? String, "note-lesson-2")
        XCTAssertEqual(updateObject["expected_task_revision"] as? Int, 4)
        XCTAssertEqual(updateObject["expected_schedule_revision"] as? Int, 6)

        let block = BlockOccurrenceInput(
            expectedTaskRevision: 4,
            expectedScheduleRevision: 6,
            expectedOccurrenceRevisions: ["occurrence-1": 8],
            blockedReason: "等待教材",
            nextAction: "教材到达后完成第二课"
        )
        let blockData = try JSONEncoder.flowSpace().encode(block)
        let blockObject = try XCTUnwrap(JSONSerialization.jsonObject(with: blockData) as? [String: Any])
        let revisions = try XCTUnwrap(blockObject["expected_occurrence_revisions"] as? [String: Any])
        XCTAssertEqual(revisions["occurrence-1"] as? Int, 8)
        XCTAssertEqual(blockObject["blocked_reason"] as? String, "等待教材")
        XCTAssertEqual(blockObject["next_action"] as? String, "教材到达后完成第二课")
    }
}
