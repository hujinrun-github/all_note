import Foundation

enum ContentImportStatus: String, Codable, CaseIterable, Identifiable, Sendable {
    case active
    case completed
    case failed
    case needsReview = "needs_review"
    case canceled

    var id: String { rawValue }

    var title: String {
        switch self {
        case .active: "处理中"
        case .completed: "已完成"
        case .failed: "失败"
        case .needsReview: "需要处理"
        case .canceled: "已取消"
        }
    }

    var systemImage: String {
        switch self {
        case .active: "arrow.trianglehead.2.clockwise.rotate.90"
        case .completed: "checkmark.circle.fill"
        case .failed, .needsReview: "exclamationmark.triangle.fill"
        case .canceled: "xmark.circle.fill"
        }
    }
}

struct ResolvedPodcastEpisode: Codable, Equatable, Sendable {
    let sourceType: String
    let submittedURL: String
    let canonicalURL: String
    let externalID: String
    let feedURL: String?
    let title: String
    let podcastTitle: String?
    let coverURL: String?
    let description: String?
    let durationSeconds: Int64?
    let hasPublicTranscript: Bool
}

struct ContentImport: Codable, Identifiable, Equatable, Sendable {
    let id: String
    let sourceURL: String
    let sourceType: String?
    let canonicalURL: String?
    let title: String?
    let podcastTitle: String?
    let coverURL: String?
    let description: String?
    let durationSeconds: Int64?
    let status: ContentImportStatus
    let stage: String
    let progress: Int
    let summarizeWithAI: Bool
    let summaryPrompt: String?
    let includeTranscript: Bool
    let language: String
    let folderID: String?
    let projectIDs: [String]
    let tags: [String]
    let resultNoteID: String?
    let resultNoteAvailable: Bool?
    let errorCode: String?
    let errorMessage: String?
    let retryable: Bool?
    let revision: Int64
    let createdAt: Int64
    let updatedAt: Int64

    var isTextAIFailure: Bool {
        errorCode?.hasPrefix("TEXT_AI_") == true || errorCode == "IMPORT_OUTPUT_INVALID"
    }

    var canDelete: Bool { status != .active }
}

struct CreateContentImportInput: Encodable, Sendable {
    let sourceURL: String
    let summarizeWithAI: Bool
    let summaryPrompt: String?
    let includeTranscript: Bool
    let language: String
    let folderID: String?
    let projectIDs: [String]
    let tags: [String]

    enum CodingKeys: String, CodingKey {
        case sourceURL = "source_url"
        case summarizeWithAI = "summarize_with_ai"
        case summaryPrompt = "summary_prompt"
        case includeTranscript = "include_transcript"
        case language
        case folderID = "folder_id"
        case projectIDs = "project_ids"
        case tags
    }
}

struct ContentImportPresentation: Identifiable, Equatable, Sendable {
    let id = UUID()
    let projectID: String
}

struct ContentImportDraft: Equatable, Sendable {
    static let defaultSummaryPrompt = "你是严谨的播客笔记编辑。只根据逐字稿提炼，不补充外部事实。返回 JSON 对象：title、summary、key_points、chapters、action_items。key_points、chapters、action_items 必须是字符串数组。"

    var sourceURL = ""
    var summarizeWithAI = false
    var summaryPrompt = defaultSummaryPrompt
    var includeTranscript = false
    var projectID = ""
    var tags = "播客"

    var parsedTags: [String] {
        tags.split(whereSeparator: { $0 == "," || $0 == "，" })
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
    }
}

enum ContentImportFilter: String, CaseIterable, Identifiable {
    case all
    case active
    case failed
    case completed

    var id: String { rawValue }
    var title: String {
        switch self {
        case .all: "全部"
        case .active: "进行中"
        case .failed: "需处理"
        case .completed: "已完成"
        }
    }
}
