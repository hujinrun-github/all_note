import Foundation
import XCTest
@testable import FlowSpaceMac

final class TaskWorkspaceModelsTests: XCTestCase {
    func testUpcomingQueryUsesTomorrowThroughFollowingSevenDays() throws {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = try XCTUnwrap(TimeZone(identifier: "Asia/Shanghai"))
        let now = try XCTUnwrap(Date.flowSpaceISO8601("2026-08-08T12:00:00+08:00"))

        let query = try XCTUnwrap(
            TaskWorkspaceView.upcoming.occurrenceQuery(
                inboxProjectID: nil,
                now: now,
                calendar: calendar,
                timezone: calendar.timeZone
            )
        )
        let values = Dictionary(uniqueKeysWithValues: query.compactMap { item in
            item.value.map { (item.name, $0) }
        })

        XCTAssertEqual(values["scope"], "upcoming")
        XCTAssertEqual(values["timezone"], "Asia/Shanghai")
        XCTAssertEqual(values["from"], "2026-08-08T16:00:00Z")
        XCTAssertEqual(values["to"], "2026-08-15T16:00:00Z")
    }

    func testInboxAndRecurringQueriesCarryServerAuthorityFilters() throws {
        let inbox = try XCTUnwrap(TaskWorkspaceView.inbox.occurrenceQuery(inboxProjectID: "inbox-1"))
        let recurring = try XCTUnwrap(TaskWorkspaceView.recurring.occurrenceQuery(inboxProjectID: nil))
        let inboxValues = Dictionary(uniqueKeysWithValues: inbox.compactMap { item in
            item.value.map { (item.name, $0) }
        })
        let recurringValues = Dictionary(uniqueKeysWithValues: recurring.compactMap { item in
            item.value.map { (item.name, $0) }
        })

        XCTAssertEqual(inboxValues["scope"], "all")
        XCTAssertEqual(inboxValues["project_id"], "inbox-1")
        XCTAssertEqual(recurringValues["scope"], "all")
        XCTAssertEqual(recurringValues["recurring"], "true")
        XCTAssertNil(TaskWorkspaceView.draft.occurrenceQuery(inboxProjectID: nil))
    }

    func testOccurrenceFiltersCombineProjectPriorityStatusAndDate() throws {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = try XCTUnwrap(TimeZone(identifier: "Asia/Shanghai"))
        let now = try XCTUnwrap(Date.flowSpaceISO8601("2026-08-08T12:00:00+08:00"))
        let primary = makeTask(id: "task-primary", projectID: "project-1", priority: 2, status: .active)
        let secondary = makeTask(id: "task-secondary", projectID: "project-2", priority: 1, status: .active)
        let occurrences = [
            makeOccurrence(id: "today", taskID: primary.id, projectID: primary.projectID, date: "2026-08-08"),
            makeOccurrence(id: "future", taskID: primary.id, projectID: primary.projectID, date: "2026-08-10"),
            makeOccurrence(id: "other-project", taskID: secondary.id, projectID: secondary.projectID, date: "2026-08-08"),
            makeOccurrence(id: "unscheduled", taskID: primary.id, projectID: primary.projectID, date: nil),
        ]
        let tasks = [primary.id: primary, secondary.id: secondary]
        let filter = TaskWorkspaceFilter(
            projectID: "project-1",
            priority: 2,
            executionStatus: .open,
            date: .today
        )

        XCTAssertEqual(
            filter.occurrences(occurrences, tasks: tasks, now: now, calendar: calendar).map(\.id),
            ["today"]
        )

        var unscheduledFilter = filter
        unscheduledFilter.date = .unscheduled
        XCTAssertEqual(
            unscheduledFilter.occurrences(occurrences, tasks: tasks, now: now, calendar: calendar).map(\.id),
            ["unscheduled"]
        )
    }

    func testDraftFilterNeverIncludesPublishedDefinitions() {
        let draft = makeTask(id: "draft", projectID: "project-1", priority: 3, status: .draft)
        let active = makeTask(id: "active", projectID: "project-1", priority: 3, status: .active)
        let filter = TaskWorkspaceFilter(projectID: "project-1", priority: 3)

        XCTAssertEqual(filter.drafts([draft, active]).map(\.id), ["draft"])
    }

    func testTaskWorkspaceWindowRouteRoundTripsThroughCodable() throws {
        let route = WorkspaceWindowRoute.task("task-42", view: .completed)

        let encoded = try JSONEncoder().encode(route)
        let decoded = try JSONDecoder().decode(WorkspaceWindowRoute.self, from: encoded)

        XCTAssertEqual(decoded, route)
        XCTAssertEqual(decoded.destination, .tasks)
        XCTAssertEqual(decoded.selection, .task("task-42"))
        XCTAssertEqual(decoded.taskView, .completed)
    }

    func testOccurrenceWorkspaceRouteAndNoteSelectionRoundTripThroughCodable() throws {
        let route = WorkspaceWindowRoute.occurrence("occurrence-42", view: .overdue)
        let decodedRoute = try JSONDecoder().decode(
            WorkspaceWindowRoute.self,
            from: JSONEncoder().encode(route)
        )
        XCTAssertEqual(decodedRoute, route)
        XCTAssertEqual(decodedRoute.destination, .tasks)
        XCTAssertEqual(decodedRoute.selection, .occurrence("occurrence-42"))
        XCTAssertEqual(decodedRoute.taskView, .overdue)

        let noteSelection = WorkspaceEntitySelection.note("note-7")
        let decodedSelection = try JSONDecoder().decode(
            WorkspaceEntitySelection.self,
            from: JSONEncoder().encode(noteSelection)
        )
        XCTAssertEqual(decodedSelection, noteSelection)
    }

    func testTaskWebLinkPreservesServicePathAndEncodesSelection() throws {
        let baseURL = try XCTUnwrap(URL(string: "https://flow.example.com/workspace"))

        let url = try XCTUnwrap(
            WorkspaceWebLink.task(baseURL: baseURL, taskID: "task/中文 42", view: .recurring)
        )
        let components = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false))
        let queryItems = try XCTUnwrap(components.queryItems)
        let query = Dictionary(uniqueKeysWithValues: queryItems.map { ($0.name, $0.value ?? "") })

        XCTAssertEqual(components.path, "/workspace/tasks")
        XCTAssertEqual(query["view"], "recurring")
        XCTAssertEqual(query["task_id"], "task/中文 42")
    }

    func testProjectWorkspaceRouteAndWebLinkPreserveSelection() throws {
        let route = WorkspaceWindowRoute.project("project/中文 7")
        let decoded = try JSONDecoder().decode(
            WorkspaceWindowRoute.self,
            from: JSONEncoder().encode(route)
        )
        XCTAssertEqual(decoded, route)
        XCTAssertEqual(decoded.destination, .projects)
        XCTAssertEqual(decoded.selection, .project("project/中文 7"))

        let baseURL = try XCTUnwrap(URL(string: "https://flow.example.com/workspace"))
        let url = try XCTUnwrap(
            WorkspaceWebLink.project(baseURL: baseURL, projectID: "project/中文 7")
        )
        let components = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false))
        XCTAssertEqual(
            components.percentEncodedPath,
            "/workspace/projects/project%2F%E4%B8%AD%E6%96%87%207"
        )
    }

    private func makeTask(
        id: String,
        projectID: String,
        priority: Int,
        status: TaskLifecycleStatus
    ) -> TaskV2 {
        TaskV2(
            id: id,
            projectID: projectID,
            roadmapNodeID: nil,
            taskNoteID: nil,
            title: id,
            description: nil,
            priority: priority,
            sortOrder: 0,
            lifecycleStatus: status,
            revision: 1,
            scheduleRevision: 1
        )
    }

    private func makeOccurrence(
        id: String,
        taskID: String,
        projectID: String,
        date: String?
    ) -> OccurrenceV2 {
        OccurrenceV2(
            id: id,
            taskID: taskID,
            projectID: projectID,
            title: id,
            occurrenceKey: date ?? "unscheduled",
            executionStatus: .open,
            revision: 1,
            generatedScheduleRevision: 1,
            plannedDate: date,
            allDayEndDate: nil,
            plannedStartAt: nil,
            plannedEndAt: nil,
            dueAt: nil,
            blockedReason: nil,
            nextAction: nil,
            location: nil,
            recurrenceType: RecurrenceType.none,
            recurring: false,
            taskRevision: 1,
            scheduleRevision: 1,
            timingType: date == nil ? .unscheduled : .date,
            timezone: "Asia/Shanghai"
        )
    }
}
