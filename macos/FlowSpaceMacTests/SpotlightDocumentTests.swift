import XCTest
@testable import FlowSpaceMac

final class SpotlightDocumentTests: XCTestCase {
    func testIdentifierRoundTripsUnicodeAndSeparatorCharacters() {
        let identifier = FlowSpotlightIdentifier.make(
            entity: .note,
            workspaceID: "工作区.alpha",
            id: "note/日语:1"
        )

        XCTAssertEqual(
            FlowSpotlightIdentifier.parse(identifier),
            .note(workspaceID: "工作区.alpha", id: "note/日语:1")
        )
        XCTAssertNil(FlowSpotlightIdentifier.parse("flowspace.note.invalid"))
    }

    func testTaskDocumentsIncludeProjectContextAndExcludeHiddenTasks() {
        let project = makeProject()
        let active = makeTask(id: "task-active", status: .active)
        let archived = makeTask(id: "task-archived", status: .archived)
        let cancelled = makeTask(id: "task-cancelled", status: .cancelled)

        let documents = FlowSpotlightDocumentBuilder.tasks(
            [active, archived, cancelled],
            projects: [project.id: project],
            workspaceID: "workspace-1"
        )

        XCTAssertEqual(documents.map(\.title), ["单词学习"])
        XCTAssertTrue(documents[0].contentDescription.contains("日语日常学习"))
        XCTAssertTrue(documents[0].keywords.contains("任务"))
        XCTAssertEqual(documents[0].route, .task(workspaceID: "workspace-1", id: "task-active"))
    }

    func testProjectDocumentsExcludeArchivedProjects() {
        let active = makeProject()
        let archived = ProjectV2(
            id: "project-archived",
            name: "旧项目",
            kind: .standard,
            horizon: .short,
            status: .archived,
            systemRole: nil,
            revision: 1
        )

        let documents = FlowSpotlightDocumentBuilder.projects([active, archived], workspaceID: "workspace-1")

        XCTAssertEqual(documents.map(\.title), ["日语日常学习"])
        XCTAssertEqual(documents[0].route, .project(workspaceID: "workspace-1", id: active.id))
    }

    func testNoteDocumentIncludesTagsProjectsAndBoundedPreview() {
        let note = FlowNote(
            id: "note-1",
            title: "语法复习",
            body: "# 今日\n" + String(repeating: "复习句型 ", count: 80),
            folderID: "folder-1",
            tags: #"["日语","学习"]"#,
            projects: [NoteProject(id: "project-1", name: "日语日常学习", type: "regular")],
            createdAt: 1,
            updatedAt: 2
        )

        let document = FlowSpotlightDocumentBuilder.notes([note], workspaceID: "workspace-1")[0]

        XCTAssertTrue(document.contentDescription.count <= 281)
        XCTAssertTrue(document.keywords.contains("日语"))
        XCTAssertTrue(document.keywords.contains("日语日常学习"))
        XCTAssertEqual(document.route, .note(workspaceID: "workspace-1", id: note.id))
    }

    private func makeProject() -> ProjectV2 {
        ProjectV2(
            id: "project-1",
            name: "日语日常学习",
            kind: .learning,
            horizon: .long,
            status: .active,
            systemRole: nil,
            revision: 2
        )
    }

    private func makeTask(id: String, status: TaskLifecycleStatus) -> TaskV2 {
        TaskV2(
            id: id,
            projectID: "project-1",
            roadmapNodeID: nil,
            taskNoteID: nil,
            title: "单词学习",
            description: "复习 N2 单词",
            priority: 2,
            sortOrder: 0,
            lifecycleStatus: status,
            revision: 3,
            scheduleRevision: 4
        )
    }
}
