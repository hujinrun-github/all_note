import Foundation
import XCTest
@testable import FlowSpaceMac

final class KnowledgeCodingTests: XCTestCase {
    func testDecodesNoteProjectAndFolderIDs() throws {
        let note = try JSONDecoder.flowSpace().decode(FlowNote.self, from: Data(noteJSON.utf8))

        XCTAssertEqual(note.folderID, "folder-1")
        XCTAssertEqual(note.projects.first?.id, "project-1")
        XCTAssertEqual(note.parsedTags, ["日语", "学习"])
    }

    func testDecodesSummaryNoteAndOccurrenceReferences() throws {
        let summary = try JSONDecoder.flowSpace().decode(SummaryData.self, from: Data(summaryJSON.utf8))

        XCTAssertEqual(summary.activeDays, 1)
        XCTAssertEqual(summary.groups[0].tasks[0].noteID, "note-1")
        XCTAssertEqual(summary.groups[0].tasks[0].occurrenceDate, "2026-08-08")
    }

    func testEncodesNoteProjectIDsForBackend() throws {
        let input = UpdateNoteInput(
            title: "单词学习",
            body: "正文",
            folderID: nil,
            tags: nil,
            projectIDs: ["project-1"]
        )
        let data = try JSONEncoder.flowSpace().encode(input)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(object["project_ids"] as? [String], ["project-1"])
    }

    func testNormalizesAndEncodesNoteTags() throws {
        let tags = FlowNote.normalizedTags(from: "#学习， 日语\n学习, #JLPT")

        XCTAssertEqual(tags, ["学习", "日语", "JLPT"])
        let data = try XCTUnwrap(FlowNote.tagsJSON(tags).data(using: .utf8))
        XCTAssertEqual(try JSONDecoder().decode([String].self, from: data), tags)
    }

    private let noteJSON = #"""
    {
      "id": "note-1",
      "title": "单词学习",
      "body": "# 今日\n复习单词",
      "folder_id": "folder-1",
      "tags": "[\"日语\",\"学习\"]",
      "projects": [{"id":"project-1","name":"日语日常学习","type":"regular"}],
      "created_at": 1786170000,
      "updated_at": 1786173000
    }
    """#

    private let summaryJSON = #"""
    {
      "groups": [{
        "date": "2026-08-08",
        "count": 1,
        "tasks": [{
          "id": "task-1",
          "title": "单词学习",
          "done": 1,
          "note_id": "note-1",
          "execution_type": "occurrence",
          "occurrence_date": "2026-08-08",
          "project": {"id":"project-1","name":"日语日常学习","type":"regular"}
        }]
      }],
      "active_days": 1,
      "project_count": 1
    }
    """#
}
