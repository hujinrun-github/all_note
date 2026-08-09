import CoreSpotlight
import Foundation
import Observation
import UniformTypeIdentifiers

enum FlowSpotlightEntity: String, CaseIterable, Sendable {
    case task
    case project
    case note
}

enum FlowSpotlightRoute: Equatable, Sendable {
    case task(workspaceID: String, id: String)
    case project(workspaceID: String, id: String)
    case note(workspaceID: String, id: String)

    var workspaceID: String {
        switch self {
        case .task(let workspaceID, _), .project(let workspaceID, _), .note(let workspaceID, _):
            workspaceID
        }
    }
}

struct FlowSpotlightDocument: Equatable, Sendable {
    let uniqueIdentifier: String
    let domainIdentifier: String
    let title: String
    let contentDescription: String
    let textContent: String
    let keywords: [String]
    let route: FlowSpotlightRoute
}

enum FlowSpotlightIdentifier {
    private static let prefix = "flowspace"

    static func make(entity: FlowSpotlightEntity, workspaceID: String, id: String) -> String {
        [prefix, entity.rawValue, encode(workspaceID), encode(id)].joined(separator: ".")
    }

    static func domain(entity: FlowSpotlightEntity, workspaceID: String) -> String {
        [prefix, "workspace", encode(workspaceID), entity.rawValue].joined(separator: ".")
    }

    static func workspaceDomain(workspaceID: String) -> String {
        [prefix, "workspace", encode(workspaceID)].joined(separator: ".")
    }

    static func parse(_ value: String) -> FlowSpotlightRoute? {
        let parts = value.split(separator: ".", omittingEmptySubsequences: false).map(String.init)
        guard parts.count == 4,
              parts[0] == prefix,
              let entity = FlowSpotlightEntity(rawValue: parts[1]),
              let workspaceID = decode(parts[2]),
              let id = decode(parts[3]),
              !workspaceID.isEmpty,
              !id.isEmpty else { return nil }

        return switch entity {
        case .task: .task(workspaceID: workspaceID, id: id)
        case .project: .project(workspaceID: workspaceID, id: id)
        case .note: .note(workspaceID: workspaceID, id: id)
        }
    }

    private static func encode(_ value: String) -> String {
        Data(value.utf8)
            .base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }

    private static func decode(_ value: String) -> String? {
        var base64 = value
            .replacingOccurrences(of: "-", with: "+")
            .replacingOccurrences(of: "_", with: "/")
        let remainder = base64.count % 4
        if remainder != 0 { base64 += String(repeating: "=", count: 4 - remainder) }
        guard let data = Data(base64Encoded: base64) else { return nil }
        return String(data: data, encoding: .utf8)
    }
}

enum FlowSpotlightDocumentBuilder {
    static func tasks(
        _ tasks: [TaskV2],
        projects: [String: ProjectV2],
        workspaceID: String
    ) -> [FlowSpotlightDocument] {
        tasks.compactMap { task in
            guard task.lifecycleStatus != .archived, task.lifecycleStatus != .cancelled else { return nil }
            let projectName = projects[task.projectID]?.name ?? "未分类项目"
            let description = [
                task.description?.trimmingCharacters(in: .whitespacesAndNewlines),
                projectName + " · " + taskStatus(task.lifecycleStatus) + " · 优先级 " + String(task.priority)
            ]
                .compactMap { $0 }
                .filter { !$0.isEmpty }
                .joined(separator: "\n")
            let route = FlowSpotlightRoute.task(workspaceID: workspaceID, id: task.id)
            return document(
                entity: .task,
                id: task.id,
                workspaceID: workspaceID,
                title: task.title,
                description: description,
                text: [task.title, task.description ?? "", projectName].joined(separator: "\n"),
                keywords: unique(["FlowSpace", "任务", projectName, taskStatus(task.lifecycleStatus)]),
                route: route
            )
        }
    }

    static func projects(_ projects: [ProjectV2], workspaceID: String) -> [FlowSpotlightDocument] {
        projects.compactMap { project in
            guard project.status != .archived else { return nil }
            let kind = project.kind == .learning ? "学习项目" : "标准项目"
            let description = kind + " · " + projectStatus(project.status) + " · "
                + (project.horizon == .long ? "长期" : "短期")
            let route = FlowSpotlightRoute.project(workspaceID: workspaceID, id: project.id)
            return document(
                entity: .project,
                id: project.id,
                workspaceID: workspaceID,
                title: project.name,
                description: description,
                text: project.name + "\n" + description,
                keywords: unique(["FlowSpace", "项目", kind, projectStatus(project.status)]),
                route: route
            )
        }
    }

    static func notes(_ notes: [FlowNote], workspaceID: String) -> [FlowSpotlightDocument] {
        notes.map { note in
            let preview = limited(note.plainTextPreview, to: 280)
            let projectNames = note.projects.map(\.name)
            let route = FlowSpotlightRoute.note(workspaceID: workspaceID, id: note.id)
            return document(
                entity: .note,
                id: note.id,
                workspaceID: workspaceID,
                title: note.title.isEmpty ? "未命名笔记" : note.title,
                description: preview,
                text: limited([note.title, note.plainTextPreview].joined(separator: "\n"), to: 20_000),
                keywords: unique(["FlowSpace", "笔记"] + note.parsedTags + projectNames),
                route: route
            )
        }
    }

    private static func document(
        entity: FlowSpotlightEntity,
        id: String,
        workspaceID: String,
        title: String,
        description: String,
        text: String,
        keywords: [String],
        route: FlowSpotlightRoute
    ) -> FlowSpotlightDocument {
        FlowSpotlightDocument(
            uniqueIdentifier: FlowSpotlightIdentifier.make(entity: entity, workspaceID: workspaceID, id: id),
            domainIdentifier: FlowSpotlightIdentifier.domain(entity: entity, workspaceID: workspaceID),
            title: title,
            contentDescription: description,
            textContent: text,
            keywords: keywords,
            route: route
        )
    }

    private static func unique(_ values: [String]) -> [String] {
        var seen = Set<String>()
        return values.filter { !$0.isEmpty && seen.insert($0).inserted }
    }

    private static func limited(_ value: String, to limit: Int) -> String {
        guard value.count > limit else { return value }
        return String(value.prefix(limit)) + "…"
    }

    private static func taskStatus(_ status: TaskLifecycleStatus) -> String {
        switch status {
        case .draft: "草稿"
        case .active: "已发布"
        case .paused: "已暂停"
        case .completed: "已完成"
        case .cancelled: "已取消"
        case .archived: "已归档"
        }
    }

    private static func projectStatus(_ status: ProjectStatus) -> String {
        switch status {
        case .planning: "规划中"
        case .active: "进行中"
        case .paused: "已暂停"
        case .completed: "已完成"
        case .archived: "已归档"
        }
    }
}

@MainActor
@Observable
final class AppSpotlightService {
    static let shared = AppSpotlightService()

    private(set) var pendingRoute: FlowSpotlightRoute?
    private(set) var lastError: String?

    private let index: CSSearchableIndex
    private var revisions: [String: Int] = [:]
    private var queuedOperations: [String: Task<Void, Never>] = [:]
    private var inactiveWorkspaceIDs: Set<String> = []

    init(index: CSSearchableIndex = CSSearchableIndex(name: "FlowSpaceContent")) {
        self.index = index
    }

    func replaceTasks(
        _ tasks: [TaskV2],
        projects: [String: ProjectV2],
        workspaceID: String
    ) async {
        await replace(
            FlowSpotlightDocumentBuilder.tasks(tasks, projects: projects, workspaceID: workspaceID),
            entity: .task,
            workspaceID: workspaceID
        )
    }

    func activateWorkspace(_ workspaceID: String) {
        inactiveWorkspaceIDs.remove(workspaceID)
    }

    func replaceProjects(_ projects: [ProjectV2], workspaceID: String) async {
        await replace(
            FlowSpotlightDocumentBuilder.projects(projects, workspaceID: workspaceID),
            entity: .project,
            workspaceID: workspaceID
        )
    }

    func replaceNotes(_ notes: [FlowNote], workspaceID: String) async {
        await replace(
            FlowSpotlightDocumentBuilder.notes(notes, workspaceID: workspaceID),
            entity: .note,
            workspaceID: workspaceID
        )
    }

    func deleteWorkspace(_ workspaceID: String) async {
        inactiveWorkspaceIDs.insert(workspaceID)
        let domains = FlowSpotlightEntity.allCases.map {
            FlowSpotlightIdentifier.domain(entity: $0, workspaceID: workspaceID)
        }
        for domain in domains {
            revisions[domain, default: 0] += 1
        }
        for domain in domains {
            await queuedOperations[domain]?.value
        }
        do {
            try await delete(domains: [FlowSpotlightIdentifier.workspaceDomain(workspaceID: workspaceID)])
            lastError = nil
        } catch {
            lastError = error.localizedDescription
        }
    }

    func receive(_ route: FlowSpotlightRoute) {
        pendingRoute = route
    }

    func consumePendingRoute() {
        pendingRoute = nil
    }

    private func replace(
        _ documents: [FlowSpotlightDocument],
        entity: FlowSpotlightEntity,
        workspaceID: String
    ) async {
        guard CSSearchableIndex.isIndexingAvailable(), !inactiveWorkspaceIDs.contains(workspaceID) else { return }
        let domain = FlowSpotlightIdentifier.domain(entity: entity, workspaceID: workspaceID)
        let revision = (revisions[domain] ?? 0) + 1
        revisions[domain] = revision
        let previous = queuedOperations[domain]
        let operation = Task { [weak self] in
            await previous?.value
            guard let self else { return }
            await self.performReplace(
                documents,
                domain: domain,
                workspaceID: workspaceID,
                revision: revision
            )
        }
        queuedOperations[domain] = operation
        await operation.value
    }

    private func performReplace(
        _ documents: [FlowSpotlightDocument],
        domain: String,
        workspaceID: String,
        revision: Int
    ) async {
        guard revisions[domain] == revision, !inactiveWorkspaceIDs.contains(workspaceID) else { return }
        do {
            try await delete(domains: [domain])
            guard revisions[domain] == revision, !inactiveWorkspaceIDs.contains(workspaceID) else { return }
            if !documents.isEmpty {
                try await index(items: documents.map(searchableItem))
            }
            guard revisions[domain] == revision, !inactiveWorkspaceIDs.contains(workspaceID) else { return }
            lastError = nil
        } catch {
            guard revisions[domain] == revision else { return }
            lastError = error.localizedDescription
        }
    }

    private func searchableItem(_ document: FlowSpotlightDocument) -> CSSearchableItem {
        let attributes = CSSearchableItemAttributeSet(contentType: .item)
        attributes.title = document.title
        attributes.displayName = document.title
        attributes.contentDescription = document.contentDescription
        attributes.textContent = document.textContent
        attributes.keywords = document.keywords
        return CSSearchableItem(
            uniqueIdentifier: document.uniqueIdentifier,
            domainIdentifier: document.domainIdentifier,
            attributeSet: attributes
        )
    }

    private func index(items: [CSSearchableItem]) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            index.indexSearchableItems(items) { error in
                if let error { continuation.resume(throwing: error) }
                else { continuation.resume() }
            }
        }
    }

    private func delete(domains: [String]) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            index.deleteSearchableItems(withDomainIdentifiers: domains) { error in
                if let error { continuation.resume(throwing: error) }
                else { continuation.resume() }
            }
        }
    }
}
