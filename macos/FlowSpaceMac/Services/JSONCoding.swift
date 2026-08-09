import Foundation

extension JSONDecoder {
    static func flowSpace() -> JSONDecoder {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .custom { codingPath in
            let raw = codingPath.last?.stringValue ?? ""
            return FlowSpaceCodingKey(stringValue: FlowSpaceJSONKeys.decode(raw))!
        }
        return decoder
    }
}

extension JSONEncoder {
    static func flowSpace() -> JSONEncoder {
        let encoder = JSONEncoder()
        encoder.keyEncodingStrategy = .convertToSnakeCase
        return encoder
    }
}

private enum FlowSpaceJSONKeys {
    private static let acronymOverrides: [String: String] = [
        "avatar_url": "avatarURL",
        "user_id": "userID",
        "default_workspace_id": "defaultWorkspaceID",
        "folder_id": "folderID",
        "note_id": "noteID",
        "occurrence_id": "occurrenceID",
        "owner_user_id": "ownerUserID",
        "project_id": "projectID",
        "project_ids": "projectIDs",
        "roadmap_id": "roadmapID",
        "roadmap_node_id": "roadmapNodeID",
        "parent_id": "parentID",
        "from_node_id": "fromNodeID",
        "to_node_id": "toNodeID",
        "task_id": "taskID",
        "task_note_id": "taskNoteID",
        "content_url": "contentURL",
        "attachment_id": "attachmentID",
        "client_id": "clientID",
        "source_url": "sourceURL",
        "submitted_url": "submittedURL",
        "canonical_url": "canonicalURL",
        "external_id": "externalID",
        "feed_url": "feedURL",
        "cover_url": "coverURL",
        "result_note_id": "resultNoteID",
        "summarize_with_ai": "summarizeWithAI",
        "target_id": "targetID",
        "bound_target_id": "boundTargetID",
        "external_url": "externalURL",
        "config_json": "configJSON",
        "workspace_id": "workspaceID",
        "endpoint_id": "endpointID",
        "profile_version_id": "profileVersionID",
        "family_id": "familyID",
        "flow_id": "flowID",
        "verification_url": "verificationURL",
    ]

    static func decode(_ raw: String) -> String {
        if let override = acronymOverrides[raw] { return override }
        let parts = raw.split(separator: "_")
        guard let first = parts.first else { return raw }
        return String(first) + parts.dropFirst().map { part in
            guard let head = part.first else { return "" }
            return String(head).uppercased() + part.dropFirst()
        }.joined()
    }
}

private struct FlowSpaceCodingKey: CodingKey {
    let stringValue: String
    let intValue: Int?

    init?(stringValue: String) {
        self.stringValue = stringValue
        intValue = nil
    }

    init?(intValue: Int) {
        stringValue = String(intValue)
        self.intValue = intValue
    }
}
