import SwiftUI

enum WorkspaceDestination: String, CaseIterable, Codable, Identifiable, Hashable, Sendable {
    case today
    case tasks
    case inbox
    case projects
    case calendar
    case notes
    case review

    var id: String { rawValue }

    var title: String {
        switch self {
        case .today: "今日"
        case .tasks: "任务"
        case .inbox: "未整理"
        case .projects: "项目"
        case .calendar: "日历"
        case .notes: "笔记"
        case .review: "每日总结"
        }
    }

    var systemImage: String {
        switch self {
        case .today: "calendar.badge.clock"
        case .tasks: "checkmark"
        case .inbox: "tray"
        case .projects: "folder"
        case .calendar: "calendar"
        case .notes: "note.text"
        case .review: "chart.bar.doc.horizontal"
        }
    }
}

struct WorkspaceWindowRoute: Codable, Hashable, Sendable {
    let destination: WorkspaceDestination
    let selection: WorkspaceEntitySelection?
    let taskView: TaskWorkspaceView?

    static func task(_ taskID: String, view: TaskWorkspaceView) -> Self {
        Self(destination: .tasks, selection: .task(taskID), taskView: view)
    }

    static func project(_ projectID: String) -> Self {
        Self(destination: .projects, selection: .project(projectID), taskView: nil)
    }

    static func occurrence(_ occurrenceID: String, view: TaskWorkspaceView) -> Self {
        Self(destination: .tasks, selection: .occurrence(occurrenceID), taskView: view)
    }
}

enum WorkspaceWebLink {
    static func task(baseURL: URL, taskID: String, view: TaskWorkspaceView) -> URL? {
        guard var components = URLComponents(url: baseURL, resolvingAgainstBaseURL: false) else {
            return nil
        }
        let basePath = components.path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        components.path = basePath.isEmpty ? "/tasks" : "/\(basePath)/tasks"
        components.queryItems = [
            URLQueryItem(name: "view", value: view.rawValue),
            URLQueryItem(name: "task_id", value: taskID),
        ]
        components.fragment = nil
        return components.url
    }

    static func project(baseURL: URL, projectID: String) -> URL? {
        guard var components = URLComponents(url: baseURL, resolvingAgainstBaseURL: false) else {
            return nil
        }
        var allowed = CharacterSet.urlPathAllowed
        allowed.remove(charactersIn: "/?#")
        guard let encodedProjectID = projectID.addingPercentEncoding(withAllowedCharacters: allowed) else {
            return nil
        }
        let basePath = components.percentEncodedPath.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        components.percentEncodedPath = basePath.isEmpty
            ? "/projects/\(encodedProjectID)"
            : "/\(basePath)/projects/\(encodedProjectID)"
        components.queryItems = nil
        components.fragment = nil
        return components.url
    }
}

enum TodayScope: String, CaseIterable, Identifiable {
    case today
    case week
    case month
    case overdue
    case completed

    var id: String { rawValue }

    var title: String {
        switch self {
        case .today: "今天"
        case .week: "本周"
        case .month: "本月"
        case .overdue: "已逾期"
        case .completed: "已完成"
        }
    }
}
