import Foundation

enum RoadmapNodeType: String, Codable, CaseIterable, Identifiable, Sendable {
    case stage
    case topic
    case milestone

    var id: String { rawValue }

    var title: String {
        switch self {
        case .stage: "阶段"
        case .topic: "主题"
        case .milestone: "里程碑"
        }
    }

    var systemImage: String {
        switch self {
        case .stage: "flag.checkered"
        case .topic: "book.pages"
        case .milestone: "star"
        }
    }
}

enum RoadmapStatus: String, Codable, Sendable {
    case draft
    case active
    case completed
    case failed
    case archived
}

enum RoadmapEdgeType: String, Codable, Sendable {
    case prerequisite
    case related
    case suggestedOrder = "suggested_order"
}

struct RoadmapNodeProgress: Codable, Hashable, Sendable {
    let tasks: Int
    let total: Int
    let open: Int
    let active: Int
    let blocked: Int
    let done: Int
    let skipped: Int
    let cancelled: Int

    var completionFraction: Double {
        guard total > 0 else { return 0 }
        return Double(done + skipped + cancelled) / Double(total)
    }
}

struct RoadmapNodeV2: Codable, Identifiable, Hashable, Sendable {
    let id: String
    let projectID: String
    let roadmapID: String
    let parentID: String?
    let title: String
    let description: String
    let nodeType: RoadmapNodeType
    let position: Int
    let revision: Int
    let progress: RoadmapNodeProgress
}

struct RoadmapEdgeV2: Codable, Identifiable, Hashable, Sendable {
    let id: String
    let fromNodeID: String
    let toNodeID: String
    let edgeType: RoadmapEdgeType
    let revision: Int
}

struct RoadmapV2: Codable, Identifiable, Hashable, Sendable {
    let id: String
    let projectID: String
    let title: String
    let description: String
    let status: RoadmapStatus
    let revision: Int
    let nodes: [RoadmapNodeV2]
    let edges: [RoadmapEdgeV2]

    var stages: [RoadmapNodeV2] {
        nodes.filter { $0.nodeType == .stage }.sorted(by: RoadmapNodeV2.positionAscending)
    }
}

struct RoadmapCreateInput: Encodable, Sendable {
    let title: String
    let description: String?
}

struct RoadmapGenerateInput: Encodable, Sendable {
    let prompt: String?
}

struct RoadmapNodeInput: Encodable, Sendable {
    let parentID: String?
    let title: String
    let description: String?
    let nodeType: RoadmapNodeType
    let position: Int?
    let expectedRevision: Int?
}

struct RoadmapMindMapRoute: Codable, Hashable, Sendable {
    let projectID: String
    let nodeID: String
}

struct RoadmapNodeDraft: Identifiable {
    let id = UUID()
    var editingNodeID: String?
    var parentID: String?
    var title = ""
    var description = ""
    var nodeType: RoadmapNodeType = .topic
    var expectedRevision: Int?

    init(parentID: String? = nil, nodeType: RoadmapNodeType = .topic) {
        self.parentID = parentID
        self.nodeType = nodeType
    }

    init(node: RoadmapNodeV2) {
        editingNodeID = node.id
        parentID = node.parentID
        title = node.title
        description = node.description
        nodeType = node.nodeType
        expectedRevision = node.revision
    }
}

extension RoadmapNodeV2 {
    static func positionAscending(_ lhs: RoadmapNodeV2, _ rhs: RoadmapNodeV2) -> Bool {
        if lhs.position != rhs.position { return lhs.position < rhs.position }
        return lhs.title.localizedStandardCompare(rhs.title) == .orderedAscending
    }
}

