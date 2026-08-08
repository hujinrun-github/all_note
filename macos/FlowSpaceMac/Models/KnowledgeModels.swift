import Foundation

struct NoteProject: Codable, Identifiable, Hashable, Sendable {
    let id: String
    let name: String
    let type: String
}

struct FlowNote: Codable, Identifiable, Hashable, Sendable {
    let id: String
    var title: String
    var body: String
    var folderID: String
    var tags: String
    var projects: [NoteProject]
    let createdAt: Int64
    var updatedAt: Int64

    var parsedTags: [String] {
        guard let data = tags.data(using: .utf8),
              let values = try? JSONDecoder().decode([String].self, from: data) else { return [] }
        return values.filter { !$0.isEmpty }
    }

    static func normalizedTags(from input: String) -> [String] {
        var seen: Set<String> = []
        return input
            .split(whereSeparator: { $0 == "," || $0 == "，" || $0 == "\n" })
            .compactMap { rawValue in
                var value = String(rawValue).trimmingCharacters(in: .whitespacesAndNewlines)
                while value.hasPrefix("#") { value.removeFirst() }
                value = value.trimmingCharacters(in: .whitespacesAndNewlines)
                guard !value.isEmpty else { return nil }
                let key = value.lowercased()
                guard seen.insert(key).inserted else { return nil }
                return value
            }
    }

    static func tagsJSON(_ values: [String]) -> String {
        let normalized = normalizedTags(from: values.joined(separator: ","))
        guard let data = try? JSONEncoder().encode(normalized),
              let value = String(data: data, encoding: .utf8) else { return "[]" }
        return value
    }

    var plainTextPreview: String {
        body
            .replacingOccurrences(of: #"[`*_>#\[\]()-]"#, with: " ", options: .regularExpression)
            .replacingOccurrences(of: #"\s+"#, with: " ", options: .regularExpression)
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }
}

struct CreateNoteInput: Encodable, Sendable {
    let title: String
    let body: String
    let folderID: String
    let tags: String
    let projectIDs: [String]?

    enum CodingKeys: String, CodingKey {
        case title
        case body
        case folderID
        case tags
        case projectIDs = "project_ids"
    }
}

struct UpdateNoteInput: Encodable, Sendable {
    let title: String?
    let body: String?
    let folderID: String?
    let tags: String?
    let projectIDs: [String]?

    enum CodingKeys: String, CodingKey {
        case title
        case body
        case folderID
        case tags
        case projectIDs = "project_ids"
    }
}

struct SearchResultItem: Codable, Identifiable, Hashable, Sendable {
    let type: String
    let id: String
    let title: String
    let highlight: String
    let folderID: String?
    let done: Int?
    let kind: String?
    let updatedAt: Int64
}

struct SummaryData: Codable, Equatable, Sendable {
    let groups: [SummaryDateGroup]
    let activeDays: Int
    let projectCount: Int
}

struct SummaryDateGroup: Codable, Identifiable, Equatable, Sendable {
    var id: String { date }
    let date: String
    let tasks: [SummaryTask]
    let count: Int
}

struct SummaryTask: Codable, Identifiable, Equatable, Sendable {
    let id: String
    let title: String
    let done: Int
    let plannedDate: String?
    let due: Int64?
    let completedAt: Int64?
    let noteID: String?
    let project: SummaryProject?
    let linkedNotes: [SummaryNoteReference]?
    let executionType: String?
    let occurrenceDate: String?
}

struct SummaryProject: Codable, Equatable, Sendable {
    let id: String
    let name: String
    let type: String
}

struct SummaryNoteReference: Codable, Identifiable, Equatable, Sendable {
    let id: String
    let title: String
}

enum SummaryPeriod: String, CaseIterable, Identifiable {
    case week
    case month

    var id: String { rawValue }
    var title: String { self == .week ? "本周" : "本月" }
}
