import Foundation
import SwiftData

@Model
final class RecentWorkspaceCacheRecord {
    @Attribute(.unique) var cacheKey: String
    var payload: Data
    var updatedAt: Date

    init(cacheKey: String, payload: Data, updatedAt: Date) {
        self.cacheKey = cacheKey
        self.payload = payload
        self.updatedAt = updatedAt
    }
}

struct RecentWorkspaceContent: Codable, Sendable {
    var projects: [ProjectV2] = []
    var tasks: [TaskV2] = []
    var occurrences: [OccurrenceV2] = []
    var calendarEntries: [CalendarEntryV2] = []
    var notes: [FlowNote] = []
    var roadmapsByProjectID: [String: RoadmapV2] = [:]
    var summary: SummaryData?
    var summaryAttention: [OccurrenceV2] = []
}

struct RecentWorkspaceSnapshot: Codable, Sendable {
    var currentUser: CurrentUserPayload?
    var v2CapabilitiesVerified = false
    var content = RecentWorkspaceContent()
    var cachedAt: Date?
}

@MainActor
final class RecentWorkspaceCache {
    private let container: ModelContainer?
    private let encoder = JSONEncoder()
    private let decoder = JSONDecoder()

    init(isStoredInMemoryOnly: Bool = false) {
        let schema = Schema([RecentWorkspaceCacheRecord.self])
        let configuration = ModelConfiguration(
            "RecentWorkspaceCache",
            schema: schema,
            isStoredInMemoryOnly: isStoredInMemoryOnly
        )
        container = try? ModelContainer(for: schema, configurations: [configuration])
    }

    var isAvailable: Bool { container != nil }

    func load(for serviceURL: URL) -> RecentWorkspaceSnapshot? {
        guard let record = fetchRecord(for: serviceURL),
              let snapshot = try? decoder.decode(RecentWorkspaceSnapshot.self, from: record.payload) else {
            return nil
        }
        return snapshot
    }

    func saveIdentity(_ user: CurrentUserPayload, for serviceURL: URL) {
        var snapshot = load(for: serviceURL) ?? RecentWorkspaceSnapshot()
        if let cachedUser = snapshot.currentUser,
           cachedUser.user.id != user.user.id || cachedUser.workspace.id != user.workspace.id {
            snapshot = RecentWorkspaceSnapshot()
        }
        snapshot.currentUser = user
        if user.mustChangePassword || user.user.mustChangePassword {
            snapshot.v2CapabilitiesVerified = false
        }
        save(snapshot, for: serviceURL)
    }

    func markV2CapabilitiesVerified(for serviceURL: URL) {
        var snapshot = load(for: serviceURL) ?? RecentWorkspaceSnapshot()
        snapshot.v2CapabilitiesVerified = true
        save(snapshot, for: serviceURL)
    }

    func invalidateV2Capabilities(for serviceURL: URL) {
        var snapshot = load(for: serviceURL) ?? RecentWorkspaceSnapshot()
        snapshot.v2CapabilitiesVerified = false
        save(snapshot, for: serviceURL)
    }

    func saveContent(_ content: RecentWorkspaceContent, for serviceURL: URL) {
        var snapshot = load(for: serviceURL) ?? RecentWorkspaceSnapshot()
        snapshot.content = content
        save(snapshot, for: serviceURL)
    }

    func remove(for serviceURL: URL) {
        guard let container, let record = fetchRecord(for: serviceURL) else { return }
        let context = ModelContext(container)
        if let stored = context.model(for: record.persistentModelID) as? RecentWorkspaceCacheRecord {
            context.delete(stored)
            try? context.save()
        }
    }

    private func save(_ snapshot: RecentWorkspaceSnapshot, for serviceURL: URL) {
        guard let container else { return }
        var snapshot = snapshot
        let savedAt = Date()
        snapshot.cachedAt = savedAt
        guard let payload = try? encoder.encode(snapshot) else { return }
        let context = ModelContext(container)
        if let existing = fetchRecord(for: serviceURL, context: context) {
            existing.payload = payload
            existing.updatedAt = savedAt
        } else {
            context.insert(RecentWorkspaceCacheRecord(
                cacheKey: Self.key(for: serviceURL),
                payload: payload,
                updatedAt: savedAt
            ))
        }
        try? context.save()
    }

    private func fetchRecord(for serviceURL: URL, context: ModelContext? = nil) -> RecentWorkspaceCacheRecord? {
        guard let container else { return nil }
        let context = context ?? ModelContext(container)
        let key = Self.key(for: serviceURL)
        var descriptor = FetchDescriptor<RecentWorkspaceCacheRecord>(
            predicate: #Predicate { $0.cacheKey == key }
        )
        descriptor.fetchLimit = 1
        return try? context.fetch(descriptor).first
    }

    private static func key(for serviceURL: URL) -> String {
        serviceURL.absoluteString
    }
}
