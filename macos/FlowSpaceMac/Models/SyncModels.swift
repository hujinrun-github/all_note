import Foundation

enum SyncTargetType: String, Codable, CaseIterable, Identifiable, Sendable {
    case obsidian
    case notion

    var id: String { rawValue }
    var title: String { self == .notion ? "Notion" : "Obsidian" }
    var systemImage: String { self == .notion ? "square.grid.2x2" : "diamond" }
}

struct SyncTarget: Codable, Identifiable, Hashable, Sendable {
    let id: String
    let type: SyncTargetType
    let name: String
    let vaultPath: String
    let baseFolder: String
    let configJSON: String
    let enabled: Bool
    let autoSync: Bool
    let isDefault: Bool?
    let createdAt: Int64
    let updatedAt: Int64

    var parsedConfig: [String: Any] {
        guard let data = configJSON.data(using: .utf8),
              let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return [:] }
        return object
    }
}

enum SyncStateStatus: String, Codable, Sendable {
    case synced
    case pending
    case failed
    case externalDeleted = "external_deleted"
}

struct NoteSyncState: Codable, Hashable, Sendable {
    let noteID: String
    let targetID: String
    let externalPath: String
    let externalID: String
    let externalURL: String
    let contentHash: String
    let externalHash: String
    let externalMtime: Int64?
    let lastDirection: String
    let lastSyncedAt: Int64?
    let status: SyncStateStatus
    let errorMessage: String?
}

struct NoteSyncBinding: Codable, Hashable, Sendable {
    let noteID: String
    let targetID: String
    let createdAt: Int64
    let updatedAt: Int64
}

struct NoteSyncBindingCandidate: Codable, Hashable, Sendable {
    let target: SyncTarget
    let state: NoteSyncState?
}

struct NoteSyncBindingResponse: Codable, Sendable {
    let binding: NoteSyncBinding?
    let target: SyncTarget?
    let state: NoteSyncState?
    let candidates: [NoteSyncBindingCandidate]?
    let bindingMismatch: Bool?
    let defaultTargetMissing: Bool?
    let bindingRequired: Bool?
    let boundTargetID: String?
    let boundTargetName: String?
}

struct SaveNoteSyncBindingResponse: Codable, Sendable {
    let binding: NoteSyncBinding
    let target: SyncTarget
    let changedTarget: Bool
}

struct SaveSyncTargetInput: Encodable, Sendable {
    let id: String?
    let type: SyncTargetType
    let name: String
    let vaultPath: String
    let baseFolder: String
    let configJSON: String
    let enabled: Bool
    let autoSync: Bool
    let isDefault: Bool

    enum CodingKeys: String, CodingKey {
        case id, type, name, enabled
        case vaultPath = "vault_path"
        case baseFolder = "base_folder"
        case configJSON = "config_json"
        case autoSync = "auto_sync"
        case isDefault = "is_default"
    }
}

struct SaveNoteSyncBindingInput: Encodable, Sendable {
    let targetID: String
    let expectedTargetID: String?
    let confirmChangedTarget: Bool
}

struct DeleteNoteSyncBindingInput: Encodable, Sendable {
    let expectedTargetID: String
    let expectedUpdatedAt: Int64
}

struct SyncResultItem: Codable, Identifiable, Sendable {
    var id: String { noteID }
    let noteID: String
    let status: String
    let externalPath: String?
    let externalID: String?
    let externalURL: String?
    let errorMessage: String?
}

struct SyncBatchResult: Codable, Sendable {
    let synced: Int
    let failed: Int
    let items: [SyncResultItem]
}

struct TargetSyncResult: Codable, Sendable {
    let pushed: Int
    let pulled: Int
    let imported: Int
    let externalDeleted: Int
    let conflictPulled: Int?
    let unsupported: Int?
    let failed: Int
    let items: [SyncResultItem]
}

struct SyncTargetDraft: Identifiable {
    let id = UUID()
    var targetID: String?
    var type: SyncTargetType
    var name: String
    var vaultPath = ""
    var baseFolder = "FlowSpace Notes"
    var dataSourceID = ""
    var token = ""
    var tokenConfigured = false
    var tokenEnv = ""
    var titleProperty = "Name"
    var tags = "sync"
    var autoSync = false
    var isDefault = false

    init(type: SyncTargetType) {
        self.type = type
        name = type == .notion ? "Personal Notion" : "Obsidian Vault"
    }

    init(target: SyncTarget) {
        targetID = target.id
        type = target.type
        name = target.name
        vaultPath = target.vaultPath
        baseFolder = target.baseFolder
        autoSync = target.autoSync
        isDefault = target.isDefault ?? false
        let config = target.parsedConfig
        dataSourceID = config["data_source_id"] as? String ?? ""
        tokenConfigured = config["token_set"] as? Bool ?? false
        tokenEnv = config["token_env"] as? String ?? ""
        titleProperty = config["title_property"] as? String ?? "Name"
        tags = (config["required_tags"] as? [String] ?? []).joined(separator: ", ")
    }

    var parsedTags: [String] {
        tags.split(whereSeparator: { $0 == "," || $0 == "，" })
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
    }

    func input() throws -> SaveSyncTargetInput {
        var config: [String: Any] = ["required_tags": parsedTags]
        if type == .notion {
            config["data_source_id"] = dataSourceID.trimmingCharacters(in: .whitespacesAndNewlines)
            config["title_property"] = titleProperty.trimmingCharacters(in: .whitespacesAndNewlines)
            if !token.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                config["token"] = token.trimmingCharacters(in: .whitespacesAndNewlines)
            } else if !tokenEnv.isEmpty {
                config["token_env"] = tokenEnv
            }
        }
        let data = try JSONSerialization.data(withJSONObject: config, options: [.sortedKeys])
        return SaveSyncTargetInput(
            id: targetID,
            type: type,
            name: name.trimmingCharacters(in: .whitespacesAndNewlines),
            vaultPath: type == .obsidian ? vaultPath.trimmingCharacters(in: .whitespacesAndNewlines) : "",
            baseFolder: type == .obsidian ? baseFolder.trimmingCharacters(in: .whitespacesAndNewlines) : "",
            configJSON: String(decoding: data, as: UTF8.self),
            enabled: true,
            autoSync: autoSync,
            isDefault: isDefault
        )
    }
}
