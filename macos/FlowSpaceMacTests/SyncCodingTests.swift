import Foundation
import XCTest
@testable import FlowSpaceMac

final class SyncCodingTests: XCTestCase {
    func testDecodesSyncTargetAcronymsAndConfig() throws {
        let target = try JSONDecoder.flowSpace().decode(SyncTarget.self, from: Data(targetJSON.utf8))

        XCTAssertEqual(target.type, .notion)
        XCTAssertEqual(target.configJSON, #"{"data_source_id":"ds-1","token_set":true}"#)
        XCTAssertTrue(target.autoSync)
        XCTAssertEqual(target.parsedConfig["data_source_id"] as? String, "ds-1")
    }

    func testDecodesNoteBindingAndExternalState() throws {
        let response = try JSONDecoder.flowSpace().decode(
            NoteSyncBindingResponse.self,
            from: Data(bindingJSON.utf8)
        )

        XCTAssertEqual(response.binding?.targetID, "target-1")
        XCTAssertEqual(response.state?.externalURL, "https://notion.so/page-1")
        XCTAssertEqual(response.state?.status, .synced)
        XCTAssertEqual(response.candidates?.first?.target.id, "target-1")
    }

    func testEncodesTargetAndBindingCASKeys() throws {
        var draft = SyncTargetDraft(type: .notion)
        draft.name = "Personal Notion"
        draft.dataSourceID = "ds-1"
        draft.token = "secret-token"
        draft.tags = "sync, 日语"
        draft.autoSync = true
        let targetData = try JSONEncoder.flowSpace().encode(draft.input())
        let targetObject = try XCTUnwrap(JSONSerialization.jsonObject(with: targetData) as? [String: Any])

        XCTAssertEqual(targetObject["auto_sync"] as? Bool, true)
        XCTAssertEqual(targetObject["config_json"] as? String, #"{"data_source_id":"ds-1","required_tags":["sync","日语"],"title_property":"Name","token":"secret-token"}"#)

        let deleteInput = DeleteNoteSyncBindingInput(expectedTargetID: "target-1", expectedUpdatedAt: 123)
        let bindingData = try JSONEncoder.flowSpace().encode(deleteInput)
        let bindingObject = try XCTUnwrap(JSONSerialization.jsonObject(with: bindingData) as? [String: Any])
        XCTAssertEqual(bindingObject["expected_target_id"] as? String, "target-1")
        XCTAssertEqual(bindingObject["expected_updated_at"] as? Int, 123)
    }

    private let targetJSON = #"""
    {
      "id": "target-1", "type": "notion", "name": "Personal Notion",
      "vault_path": "", "base_folder": "",
      "config_json": "{\"data_source_id\":\"ds-1\",\"token_set\":true}",
      "enabled": true, "auto_sync": true, "is_default": true,
      "created_at": 1786170000, "updated_at": 1786173000
    }
    """#

    private let bindingJSON = #"""
    {
      "binding": {"note_id":"note-1","target_id":"target-1","created_at":100,"updated_at":123},
      "target": {
        "id":"target-1","type":"notion","name":"Personal Notion","vault_path":"","base_folder":"",
        "config_json":"{}","enabled":true,"auto_sync":false,"created_at":100,"updated_at":123
      },
      "state": {
        "note_id":"note-1","target_id":"target-1","external_path":"page-1","external_id":"page-1",
        "external_url":"https://notion.so/page-1","content_hash":"a","external_hash":"a",
        "last_direction":"push","last_synced_at":123,"status":"synced"
      },
      "candidates": [{
        "target": {
          "id":"target-1","type":"notion","name":"Personal Notion","vault_path":"","base_folder":"",
          "config_json":"{}","enabled":true,"auto_sync":false,"created_at":100,"updated_at":123
        }
      }]
    }
    """#
}
