import Foundation
import XCTest
@testable import FlowSpaceMac

final class RoadmapAndAttachmentCodingTests: XCTestCase {
    func testDecodesRoadmapIDsAndProgress() throws {
        let roadmap = try JSONDecoder.flowSpace().decode(RoadmapV2.self, from: Data(roadmapJSON.utf8))

        XCTAssertEqual(roadmap.projectID, "project-1")
        XCTAssertEqual(roadmap.nodes[0].roadmapID, "roadmap-1")
        XCTAssertEqual(roadmap.nodes[1].parentID, "stage-1")
        XCTAssertEqual(roadmap.nodes[0].progress.completionFraction, 0.5)
        XCTAssertEqual(roadmap.edges[0].fromNodeID, "stage-1")
    }

    func testEncodesRoadmapNodeRevisionAndParent() throws {
        let input = RoadmapNodeInput(
            parentID: "stage-1",
            title: "语法",
            description: nil,
            nodeType: .topic,
            position: 2,
            expectedRevision: 7
        )
        let data = try JSONEncoder.flowSpace().encode(input)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        XCTAssertEqual(object["parent_id"] as? String, "stage-1")
        XCTAssertEqual(object["node_type"] as? String, "topic")
        XCTAssertEqual(object["expected_revision"] as? Int, 7)
    }

    func testRoadmapTaskDraftKeepsProjectAndNodeContext() {
        var draft = TaskDraft()
        draft.projectID = "project-1"
        draft.roadmapNodeID = "topic-1"
        draft.recurrenceType = .weekly

        XCTAssertEqual(draft.projectID, "project-1")
        XCTAssertEqual(draft.roadmapNodeID, "topic-1")
        XCTAssertEqual(draft.recurrenceType, .weekly)
        XCTAssertEqual(TaskLifecycleStatus.active.title, "进行中")
    }

    func testDecodesAttachmentAndBuildsAozoraFurigana() throws {
        let attachment = try JSONDecoder.flowSpace().decode(NoteAttachment.self, from: Data(attachmentJSON.utf8))
        XCTAssertEqual(attachment.noteID, "note-1")
        XCTAssertEqual(attachment.contentURL, "/api/notes/note-1/attachments/file-1/content")

        let result = FuriganaResult(
            segments: [
                FuriganaSegment(text: "日本語", reading: "にほんご"),
                FuriganaSegment(text: "を学ぶ", reading: nil),
            ],
            source: "local"
        )
        XCTAssertEqual(result.markdown, "｜日本語《にほんご》を学ぶ")
    }

    private let roadmapJSON = #"""
    {
      "id": "roadmap-1",
      "project_id": "project-1",
      "title": "日语路线",
      "description": "",
      "status": "active",
      "revision": 2,
      "nodes": [
        {
          "id": "stage-1", "project_id": "project-1", "roadmap_id": "roadmap-1",
          "title": "入门", "description": "", "node_type": "stage", "position": 1, "revision": 1,
          "progress": {"tasks": 2, "total": 2, "open": 1, "active": 0, "blocked": 0, "done": 1, "skipped": 0, "cancelled": 0}
        },
        {
          "id": "topic-1", "project_id": "project-1", "roadmap_id": "roadmap-1", "parent_id": "stage-1",
          "title": "假名", "description": "", "node_type": "topic", "position": 1, "revision": 1,
          "progress": {"tasks": 0, "total": 0, "open": 0, "active": 0, "blocked": 0, "done": 0, "skipped": 0, "cancelled": 0}
        }
      ],
      "edges": [{"id":"edge-1","from_node_id":"stage-1","to_node_id":"topic-1","edge_type":"suggested_order","revision":1}]
    }
    """#

    private let attachmentJSON = #"""
    {
      "id": "file-1", "note_id": "note-1", "kind": "image",
      "original_name": "kana.png", "mime_type": "image/png", "size_bytes": 1024,
      "sha256": "abc", "source": "upload", "deletable": true, "created_at": 1786173000,
      "content_url": "/api/notes/note-1/attachments/file-1/content"
    }
    """#
}
