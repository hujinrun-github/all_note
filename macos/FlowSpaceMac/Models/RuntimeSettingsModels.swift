import Foundation

enum ServiceKind: String, Codable, CaseIterable, Identifiable, Sendable {
    case dataStore = "data_store"
    case objectS3 = "object_s3"
    case llmChat = "llm_chat"
    case llmTranscription = "llm_transcription"

    var id: String { rawValue }
    var title: String {
        switch self {
        case .dataStore: "数据库"
        case .objectS3: "对象存储"
        case .llmChat: "文本服务"
        case .llmTranscription: "语音转写"
        }
    }
}

enum ServiceBindingMode: String, Codable, Sendable {
    case `default`
    case custom
    case disabled
    case reuseChat = "reuse_chat"

    var title: String {
        switch self {
        case .default: "平台默认"
        case .custom: "自定义"
        case .disabled: "已关闭"
        case .reuseChat: "复用文本服务"
        }
    }
}

struct ServiceBinding: Codable, Identifiable, Equatable, Sendable {
    var id: ServiceKind { kind }
    let kind: ServiceKind
    let mode: ServiceBindingMode
    let endpointID: String?
    let endpointName: String?
    let provider: String?
    let profileVersionID: String?
    let hasCredentials: Bool
    let revision: Int64
}

struct RuntimeSettings: Codable, Equatable, Sendable {
    let workspaceID: String
    let mode: String
    let epoch: Int64
    let bindingRevision: Int64
    let bindings: [ServiceBinding]

    func binding(_ kind: ServiceKind) -> ServiceBinding? {
        bindings.first { $0.kind == kind }
    }
}

struct ProfileTestInput: Encodable, Sendable {
    let kind: ServiceKind
    let provider: String
    let config: [String: String]
    let secret: String
}

struct ProfileDraftInput: Encodable, Sendable {
    let id: String
    let familyID: String
    let kind: ServiceKind
    let name: String
    let provider: String
    let config: [String: String]
    let secret: String
    let preserveFromVersionID: String?
}

struct ProfileTestResult: Codable, Equatable, Sendable {
    let ok: Bool
    let code: String
    let message: String
}

struct SavedProfile: Codable, Equatable, Sendable {
    let id: String
    let familyID: String
    let kind: ServiceKind
    let version: Int
    let state: String
    let hasCredentials: Bool
}

struct VerifiedProfile: Codable, Equatable, Sendable {
    let endpointID: String
    let profileVersionID: String
    let kind: ServiceKind
}

struct SetServiceBindingInput: Encodable, Sendable {
    let mode: ServiceBindingMode
    let endpointID: String?
    let expectedRevision: Int64
    let expectedRuntimeRevision: Int64
}

struct CodexDeviceAuthorization: Codable, Equatable, Sendable {
    let flowID: String
    let userCode: String
    let verificationURL: String
    let intervalSeconds: Int
    let expiresAt: String
}

enum CodexPollStatus: String, Codable, Sendable {
    case pending
    case connected
    case expired
    case failed
}

struct CodexPollResult: Codable, Equatable, Sendable {
    let status: CodexPollStatus
    let endpointID: String?
    let profileVersionID: String?
}

enum TranscriptionProvider: String, CaseIterable, Identifiable, Sendable {
    case sensevoice
    case funasr
    case wyoming
    case openAICompatible = "openai_compatible"

    var id: String { rawValue }
    var title: String {
        switch self {
        case .sensevoice: "SenseVoice"
        case .funasr: "FunASR"
        case .wyoming: "Faster Whisper（Wyoming TCP）"
        case .openAICompatible: "OpenAI 兼容转写"
        }
    }
}

struct RuntimeProfileDraft: Identifiable {
    let id = UUID()
    let kind: ServiceKind
    var name = ""
    var endpoint = ""
    var namespace = ""
    var secret = ""
    var accessKey = ""
    var objectSecretKey = ""
    var model = ""
    var transcriptionProvider: TranscriptionProvider = .sensevoice

    init(kind: ServiceKind) {
        self.kind = kind
        switch kind {
        case .dataStore:
            name = "PostgreSQL"
            namespace = "public"
        case .objectS3:
            name = "MinIO"
            namespace = "flowspace"
        case .llmChat:
            name = "文本 AI"
        case .llmTranscription:
            name = "语音转写"
            model = "iic/SenseVoiceSmall"
        }
    }

    var provider: String {
        switch kind {
        case .dataStore: "postgres"
        case .objectS3: "minio"
        case .llmChat: "openai_compatible"
        case .llmTranscription: transcriptionProvider.rawValue
        }
    }

    var config: [String: String] {
        switch kind {
        case .dataStore: ["endpoint": endpoint.trimmed, "schema": namespace.trimmed]
        case .objectS3: ["endpoint": endpoint.trimmed, "bucket": namespace.trimmed]
        case .llmChat, .llmTranscription: ["endpoint": endpoint.trimmed, "model": model.trimmed]
        }
    }

    var encodedSecret: String {
        guard kind == .objectS3 else { return secret }
        let object = ["access_key": accessKey.trimmed, "secret_key": objectSecretKey]
        guard let data = try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]) else { return "" }
        return String(decoding: data, as: UTF8.self)
    }

    func testInput() -> ProfileTestInput {
        ProfileTestInput(kind: kind, provider: provider, config: config, secret: encodedSecret)
    }

    func saveInput(versionID: String = UUID().uuidString, familyID: String = UUID().uuidString) -> ProfileDraftInput {
        ProfileDraftInput(
            id: versionID,
            familyID: familyID,
            kind: kind,
            name: name.trimmed,
            provider: provider,
            config: config,
            secret: encodedSecret,
            preserveFromVersionID: nil
        )
    }
}

private extension String {
    var trimmed: String { trimmingCharacters(in: .whitespacesAndNewlines) }
}
