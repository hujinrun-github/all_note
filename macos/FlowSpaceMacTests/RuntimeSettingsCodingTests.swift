import Foundation
import XCTest
@testable import FlowSpaceMac

final class RuntimeSettingsCodingTests: XCTestCase {
    func testDecodesRuntimeBindingsAndRevisions() throws {
        let runtime = try JSONDecoder.flowSpace().decode(RuntimeSettings.self, from: Data(runtimeJSON.utf8))

        XCTAssertEqual(runtime.workspaceID, "workspace-1")
        XCTAssertEqual(runtime.bindingRevision, 7)
        XCTAssertEqual(runtime.binding(.objectS3)?.endpointID, "endpoint-1")
        XCTAssertEqual(runtime.binding(.llmTranscription)?.mode, .reuseChat)
    }

    func testEncodesProfileAndBindingCASPayloads() throws {
        var draft = RuntimeProfileDraft(kind: .objectS3)
        draft.name = "MinIO"
        draft.endpoint = "http://192.168.1.13:19000"
        draft.namespace = "flowspace-test"
        draft.accessKey = "tylerhu"
        draft.objectSecretKey = "secret"
        let input = draft.saveInput(versionID: "version-1", familyID: "family-1")
        let data = try JSONEncoder.flowSpace().encode(input)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        XCTAssertEqual(object["family_id"] as? String, "family-1")
        XCTAssertEqual(object["kind"] as? String, "object_s3")
        XCTAssertEqual((object["config"] as? [String: String])?["bucket"], "flowspace-test")
        XCTAssertEqual(object["secret"] as? String, #"{"access_key":"tylerhu","secret_key":"secret"}"#)

        let binding = SetServiceBindingInput(
            mode: .custom,
            endpointID: "endpoint-1",
            expectedRevision: 3,
            expectedRuntimeRevision: 7
        )
        let bindingData = try JSONEncoder.flowSpace().encode(binding)
        let bindingObject = try XCTUnwrap(JSONSerialization.jsonObject(with: bindingData) as? [String: Any])
        XCTAssertEqual(bindingObject["endpoint_id"] as? String, "endpoint-1")
        XCTAssertEqual(bindingObject["expected_runtime_revision"] as? Int, 7)
    }

    func testDecodesCodexDeviceAuthorization() throws {
        let authorization = try JSONDecoder.flowSpace().decode(
            CodexDeviceAuthorization.self,
            from: Data(codexJSON.utf8)
        )
        XCTAssertEqual(authorization.flowID, "flow-1")
        XCTAssertEqual(authorization.userCode, "ABCD-EFGH")
        XCTAssertEqual(authorization.verificationURL, "https://auth.openai.com/device")
    }

    private let runtimeJSON = #"""
    {
      "workspace_id": "workspace-1",
      "mode": "active",
      "epoch": 4,
      "binding_revision": 7,
      "bindings": [
        {
          "kind":"object_s3","mode":"custom","endpoint_id":"endpoint-1","endpoint_name":"MinIO",
          "provider":"minio","profile_version_id":"version-1","has_credentials":true,"revision":3
        },
        {
          "kind":"llm_transcription","mode":"reuse_chat","has_credentials":false,"revision":2
        }
      ]
    }
    """#

    private let codexJSON = #"""
    {
      "flow_id":"flow-1","user_code":"ABCD-EFGH",
      "verification_url":"https://auth.openai.com/device",
      "interval_seconds":5,"expires_at":"2026-08-08T18:00:00Z"
    }
    """#
}
