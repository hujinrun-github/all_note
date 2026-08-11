import Foundation

enum ProjectKind: String, Codable, CaseIterable, Sendable {
    case standard
    case learning
}

enum ProjectHorizon: String, Codable, CaseIterable, Sendable {
    case short
    case long
}

enum ProjectStatus: String, Codable, CaseIterable, Sendable {
    case planning
    case active
    case paused
    case completed
    case archived
}

struct ProjectV2: Codable, Identifiable, Hashable, Sendable {
    let id: String
    let name: String
    let kind: ProjectKind
    let horizon: ProjectHorizon
    let status: ProjectStatus
    let systemRole: String?
    let revision: Int
}

struct CreateProjectInput: Codable, Sendable {
    let name: String
    let kind: ProjectKind
    let horizon: ProjectHorizon
    let status: ProjectStatus
}

struct UpdateProjectInput: Codable, Sendable {
    let name: String?
    let kind: ProjectKind?
    let horizon: ProjectHorizon?
    let expectedProjectRevision: Int
}

enum ProjectLifecycleCommand: String, Sendable {
    case activate
    case pause
    case resume
    case complete
    case archive
    case restore
}

struct ProjectCommandInput: Codable, Sendable {
    let expectedProjectRevision: Int
    let restoreTo: ProjectStatus?
}

struct ProjectCommandResponse: Codable, Sendable {
    let projectID: String
    let projectRevision: Int
    let status: ProjectStatus?
    let deleted: Bool?
}

struct MoveTaskInput: Codable, Sendable {
    let projectID: String
    let expectedTaskRevision: Int
    let expectedScheduleRevision: Int
}

struct UpdateTaskDefinitionInput: Codable, Sendable {
    let title: String?
    let description: String?
    var attachmentLinks: [TaskAttachmentLink]? = nil
    var completionRequirements: [TaskCompletionRequirement]? = nil
    let priority: Int?
    let projectID: String?
    let taskNoteID: String?
    let expectedTaskRevision: Int
    let expectedScheduleRevision: Int
}

enum TaskLifecycleStatus: String, Codable, CaseIterable, Sendable {
    case draft
    case active
    case paused
    case completed
    case cancelled
    case archived

    var title: String {
        switch self {
        case .draft: "草稿"
        case .active: "进行中"
        case .paused: "已暂停"
        case .completed: "已完成"
        case .cancelled: "已取消"
        case .archived: "已归档"
        }
    }
}

enum ExecutionStatus: String, Codable, CaseIterable, Sendable {
    case open
    case active
    case blocked
    case done
    case skipped
    case cancelled

    var isTerminal: Bool {
        self == .done || self == .skipped || self == .cancelled
    }
}

enum RecurrenceType: String, Codable, CaseIterable, Sendable {
    case none
    case daily
    case weekly
    case monthly
}

enum TimingType: String, Codable, CaseIterable, Sendable {
    case unscheduled
    case date
    case timeBlock = "time_block"
}

enum TaskPriorityLevel: Int, CaseIterable, Identifiable, Sendable {
    case normal = 0
    case medium = 1
    case high = 2
    case urgent = 3

    var id: Int { rawValue }

    var title: String {
        switch self {
        case .normal: "普通"
        case .medium: "中"
        case .high: "高"
        case .urgent: "紧急"
        }
    }
}

struct TaskAttachmentLink: Codable, Identifiable, Hashable, Sendable {
    var id: String { url }
    var name: String
    var url: String
}

enum TaskCompletionRequirementKind: String, Codable, CaseIterable, Identifiable, Sendable {
    case article
    case video
    case check

    var id: String { rawValue }

    var title: String {
        switch self {
        case .article: "文章"
        case .video: "视频"
        case .check: "检查项"
        }
    }

    var systemImage: String {
        switch self {
        case .article: "book"
        case .video: "play.rectangle"
        case .check: "checkmark.square"
        }
    }
}

struct TaskCompletionRequirement: Codable, Identifiable, Hashable, Sendable {
    var id: String
    var kind: TaskCompletionRequirementKind
    var title: String
    var url: String?
    var completed: Bool
}

enum CalendarDisplayMode: String, CaseIterable, Identifiable, Sendable {
    case week
    case month
    case year

    var id: String { rawValue }
    var title: String {
        switch self {
        case .week: "周"
        case .month: "月"
        case .year: "年"
        }
    }
}

struct TaskV2: Codable, Identifiable, Hashable, Sendable {
    let id: String
    let projectID: String
    let roadmapNodeID: String?
    let taskNoteID: String?
    let title: String
    let description: String?
    var attachmentLinks: [TaskAttachmentLink] = []
    var completionRequirements: [TaskCompletionRequirement] = []
    let priority: Int
    let sortOrder: Int
    let lifecycleStatus: TaskLifecycleStatus
    let revision: Int
    let scheduleRevision: Int

    init(
        id: String,
        projectID: String,
        roadmapNodeID: String?,
        taskNoteID: String?,
        title: String,
        description: String?,
        attachmentLinks: [TaskAttachmentLink] = [],
        completionRequirements: [TaskCompletionRequirement] = [],
        priority: Int,
        sortOrder: Int,
        lifecycleStatus: TaskLifecycleStatus,
        revision: Int,
        scheduleRevision: Int
    ) {
        self.id = id
        self.projectID = projectID
        self.roadmapNodeID = roadmapNodeID
        self.taskNoteID = taskNoteID
        self.title = title
        self.description = description
        self.attachmentLinks = attachmentLinks
        self.completionRequirements = completionRequirements
        self.priority = priority
        self.sortOrder = sortOrder
        self.lifecycleStatus = lifecycleStatus
        self.revision = revision
        self.scheduleRevision = scheduleRevision
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        projectID = try container.decode(String.self, forKey: .projectID)
        roadmapNodeID = try container.decodeIfPresent(String.self, forKey: .roadmapNodeID)
        taskNoteID = try container.decodeIfPresent(String.self, forKey: .taskNoteID)
        title = try container.decode(String.self, forKey: .title)
        description = try container.decodeIfPresent(String.self, forKey: .description)
        attachmentLinks = try container.decodeIfPresent([TaskAttachmentLink].self, forKey: .attachmentLinks) ?? []
        completionRequirements = try container.decodeIfPresent([TaskCompletionRequirement].self, forKey: .completionRequirements) ?? []
        priority = try container.decode(Int.self, forKey: .priority)
        sortOrder = try container.decode(Int.self, forKey: .sortOrder)
        lifecycleStatus = try container.decode(TaskLifecycleStatus.self, forKey: .lifecycleStatus)
        revision = try container.decode(Int.self, forKey: .revision)
        scheduleRevision = try container.decode(Int.self, forKey: .scheduleRevision)
    }

    private enum CodingKeys: String, CodingKey {
        case id, projectID, roadmapNodeID, taskNoteID, title, description
        case attachmentLinks, completionRequirements, priority, sortOrder
        case lifecycleStatus, revision, scheduleRevision
    }
}

struct OccurrenceV2: Codable, Identifiable, Hashable, Sendable {
    let id: String
    let taskID: String
    let projectID: String?
    let title: String?
    let occurrenceKey: String
    let executionStatus: ExecutionStatus
    let revision: Int
    let generatedScheduleRevision: Int
    let plannedDate: String?
    let allDayEndDate: String?
    let plannedStartAt: String?
    let plannedEndAt: String?
    let dueAt: String?
    let blockedReason: String?
    let nextAction: String?
    let location: String?
    let recurrenceType: RecurrenceType?
    let recurring: Bool?
    let taskRevision: Int?
    let scheduleRevision: Int?
    let timingType: TimingType?
    let timezone: String?
}

struct CalendarEntryV2: Codable, Identifiable, Hashable, Sendable {
    var id: String { occurrenceID }

    let projectID: String
    let projectRevision: Int
    let taskID: String
    let taskRevision: Int
    let taskTitle: String
    let occurrenceID: String
    let occurrenceKey: String
    let occurrenceRevision: Int
    let scheduleRevision: Int
    let generatedScheduleRevision: Int
    let executionStatus: ExecutionStatus
    let timingType: TimingType
    let timezone: String
    let recurring: Bool
    let plannedDate: String?
    let allDayEndDate: String?
    let plannedStartAt: String?
    let plannedEndAt: String?
    let dueAt: String?
    let location: String?
}

struct ExpectedRevisions: Encodable, Sendable {
    let expectedTaskRevision: Int
    let expectedScheduleRevision: Int
    let expectedOccurrenceRevisions: [String: Int]
}

struct BlockOccurrenceInput: Encodable, Sendable {
    let expectedTaskRevision: Int
    let expectedScheduleRevision: Int
    let expectedOccurrenceRevisions: [String: Int]
    let blockedReason: String
    let nextAction: String
}

struct TaskCommandResponse: Codable, Sendable {
    let taskRevision: Int
    let scheduleRevision: Int?
    let occurrenceRevisions: [String: Int]
}

struct ScheduleInput: Codable, Sendable {
    let recurrenceType: RecurrenceType
    let timingType: TimingType
    let timezone: String
    let startsOn: String?
    let endsOn: String?
    let localStartTime: String?
    let durationMinutes: Int?
    let rule: RecurrenceRule?
}

struct OccurrenceTimingInput: Codable, Sendable {
    let timingType: TimingType
    let timezone: String
    let plannedDate: String
    let allDayEndDate: String?
    let localStartTime: String?
    let durationMinutes: Int?
}

struct RescheduleOccurrenceInput: Codable, Sendable {
    let expectedTaskRevision: Int
    let expectedScheduleRevision: Int
    let expectedOccurrenceRevision: Int
    let timing: OccurrenceTimingInput
    let selectedOffsets: [String: Int]?
}

struct RescheduleThisAndFollowingInput: Codable, Sendable {
    let expectedTaskRevision: Int
    let expectedScheduleRevision: Int
    let effectiveFrom: String
    let generateThroughExclusive: String
    let schedule: ScheduleInput
    let selectedOffsets: [String: Int]?
}

struct ScheduleCommandResponse: Codable, Sendable {
    let taskRevision: Int
    let scheduleRevision: Int
    let occurrenceRevision: Int?
    let scheduleVersion: Int?
    let offsetCandidates: [ScheduleOffsetCandidate]?
}

struct ScheduleOffsetCandidate: Codable, Equatable, Identifiable, Sendable {
    let offsetSeconds: Int
    let utc: String

    var id: String { utc }
}

struct RecurrenceRule: Codable, Sendable {
    let interval: Int
    let weekdays: [Int]?
    let monthDays: [Int]?
}

struct CreateTaskInput: Codable, Sendable {
    let projectID: String
    let roadmapNodeID: String?
    let title: String
    let description: String?
    var completionRequirements: [TaskCompletionRequirement] = []
    let priority: Int
    let sortOrder: Int
    let schedule: ScheduleInput
}

struct CreateTaskResponse: Codable, Sendable {
    let task: TaskV2
    let occurrences: [OccurrenceV2]
}

struct TaskDraft: Identifiable, Equatable {
    let id = UUID()
    var title = ""
    var description = ""
    var projectID = ""
    var roadmapNodeID: String?
    var completionRequirements: [TaskCompletionRequirement] = []
    var priority = 0
    var timingType: TimingType = .unscheduled
    var date = Date()
    var durationMinutes = 30
    var recurrenceType: RecurrenceType = .none

    static func scheduled(at date: Date, projectID: String = "") -> TaskDraft {
        var draft = TaskDraft()
        draft.date = date
        draft.projectID = projectID
        draft.timingType = .timeBlock
        return draft
    }
}

struct OccurrenceCollection: Identifiable, Equatable {
    let taskID: String
    let task: TaskV2?
    let occurrences: [OccurrenceV2]

    var id: String { taskID }
    var title: String { task?.title ?? occurrences.first?.title ?? "未命名任务" }

    static func group(_ occurrences: [OccurrenceV2], tasks: [String: TaskV2]) -> [OccurrenceCollection] {
        var order: [String] = []
        var grouped: [String: [OccurrenceV2]] = [:]
        for occurrence in occurrences {
            if grouped[occurrence.taskID] == nil { order.append(occurrence.taskID) }
            grouped[occurrence.taskID, default: []].append(occurrence)
        }
        return order.map { taskID in
            OccurrenceCollection(
                taskID: taskID,
                task: tasks[taskID],
                occurrences: grouped[taskID, default: []].sorted(by: OccurrenceV2.scheduleAscending)
            )
        }
    }
}

extension OccurrenceV2 {
    static func scheduleAscending(_ lhs: OccurrenceV2, _ rhs: OccurrenceV2) -> Bool {
        let left = lhs.plannedStartAt ?? lhs.plannedDate ?? lhs.occurrenceKey
        let right = rhs.plannedStartAt ?? rhs.plannedDate ?? rhs.occurrenceKey
        return left < right
    }
}

extension CalendarEntryV2 {
    init?(occurrence: OccurrenceV2, task: TaskV2, project: ProjectV2?) {
        let resolvedProjectID = occurrence.projectID ?? task.projectID
        guard !resolvedProjectID.isEmpty else { return nil }
        projectID = resolvedProjectID
        projectRevision = project?.revision ?? 1
        taskID = task.id
        taskRevision = occurrence.taskRevision ?? task.revision
        taskTitle = occurrence.title ?? task.title
        occurrenceID = occurrence.id
        occurrenceKey = occurrence.occurrenceKey
        occurrenceRevision = occurrence.revision
        scheduleRevision = occurrence.scheduleRevision ?? task.scheduleRevision
        generatedScheduleRevision = occurrence.generatedScheduleRevision
        executionStatus = occurrence.executionStatus
        timingType = occurrence.timingType ?? (occurrence.plannedStartAt == nil ? .date : .timeBlock)
        timezone = occurrence.timezone ?? TimeZone.current.identifier
        recurring = occurrence.recurring ?? (occurrence.recurrenceType.map { $0 != .none } ?? false)
        plannedDate = occurrence.plannedDate
        allDayEndDate = occurrence.allDayEndDate
        plannedStartAt = occurrence.plannedStartAt
        plannedEndAt = occurrence.plannedEndAt
        dueAt = occurrence.dueAt
        location = occurrence.location
    }
}

extension Date {
    static func flowSpaceISO8601(_ value: String) -> Date? {
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return fractional.date(from: value) ?? ISO8601DateFormatter().date(from: value)
    }
}
