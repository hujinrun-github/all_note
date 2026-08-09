import Foundation
import XCTest
@testable import FlowSpaceMac

final class ContentImportCodingTests: XCTestCase {
    func testDecodesResolvedPodcastEpisodeURLs() throws {
        let episode = try JSONDecoder.flowSpace().decode(
            ResolvedPodcastEpisode.self,
            from: Data(episodeJSON.utf8)
        )

        XCTAssertEqual(episode.sourceType, "apple")
        XCTAssertEqual(episode.submittedURL, "https://podcasts.apple.com/example")
        XCTAssertEqual(episode.externalID, "episode-1")
        XCTAssertTrue(episode.hasPublicTranscript)
    }

    func testDecodesContentImportAcronymsAndResultNote() throws {
        let item = try JSONDecoder.flowSpace().decode(ContentImport.self, from: Data(importJSON.utf8))

        XCTAssertEqual(item.projectIDs, ["project-1"])
        XCTAssertTrue(item.summarizeWithAI)
        XCTAssertEqual(item.resultNoteID, "note-1")
        XCTAssertEqual(item.status, .completed)
    }

    func testEncodesCreateImportUsingBackendKeys() throws {
        let input = CreateContentImportInput(
            sourceURL: "https://podcasts.apple.com/example",
            summarizeWithAI: true,
            summaryPrompt: "提炼行动项",
            includeTranscript: false,
            language: "auto",
            folderID: nil,
            projectIDs: ["project-1"],
            tags: ["播客"]
        )
        let data = try JSONEncoder.flowSpace().encode(input)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        XCTAssertEqual(object["source_url"] as? String, "https://podcasts.apple.com/example")
        XCTAssertEqual(object["summarize_with_ai"] as? Bool, true)
        XCTAssertEqual(object["project_ids"] as? [String], ["project-1"])
        XCTAssertEqual(object["summary_prompt"] as? String, "提炼行动项")
    }

    private let episodeJSON = #"""
    {
      "source_type": "apple",
      "submitted_url": "https://podcasts.apple.com/example",
      "canonical_url": "https://podcasts.apple.com/example?id=1",
      "external_id": "episode-1",
      "title": "一次测试节目",
      "podcast_title": "测试播客",
      "duration_seconds": 1800,
      "has_public_transcript": true
    }
    """#

    private let importJSON = #"""
    {
      "id": "import-1",
      "source_url": "https://podcasts.apple.com/example",
      "source_type": "apple",
      "title": "一次测试节目",
      "status": "completed",
      "stage": "completed",
      "progress": 100,
      "summarize_with_ai": true,
      "summary_prompt": "提炼行动项",
      "include_transcript": false,
      "language": "auto",
      "project_ids": ["project-1"],
      "tags": ["播客"],
      "result_note_id": "note-1",
      "result_note_available": true,
      "retryable": false,
      "revision": 5,
      "created_at": 1786170000,
      "updated_at": 1786173000
    }
    """#
}
