import Foundation

enum SearchResultRoute: Equatable {
    case note(String)
    case workspace(WorkspaceDestination, WorkspaceEntitySelection?)
}

extension SearchResultItem {
    var route: SearchResultRoute {
        switch type {
        case "note":
            .note(id)
        case "project":
            .workspace(.projects, .project(id))
        case "event":
            .workspace(.calendar, .occurrence(id))
        default:
            .workspace(.tasks, .task(id))
        }
    }
}

enum CommandPaletteCommand: String, CaseIterable, Identifiable {
    case today
    case tasks
    case inbox
    case projects
    case calendar
    case notes
    case review
    case quickCapture
    case fullSearch
    case importActivity
    case accountAdmin

    var id: String { rawValue }

    var title: String {
        switch self {
        case .today: "打开今日"
        case .tasks: "打开任务"
        case .inbox: "打开未整理"
        case .projects: "打开项目"
        case .calendar: "打开日历"
        case .notes: "打开笔记库"
        case .review: "打开每日总结"
        case .quickCapture: "快速捕获"
        case .fullSearch: "打开完整搜索"
        case .importActivity: "查看导入任务"
        case .accountAdmin: "账号管理"
        }
    }

    var subtitle: String {
        switch self {
        case .quickCapture: "新建任务、笔记或日程"
        case .fullSearch: "筛选并浏览全部搜索结果"
        case .importActivity: "查看播客和内容导入进度"
        case .accountAdmin: "管理工作空间账号"
        default: "切换工作区视图"
        }
    }

    var systemImage: String {
        switch self {
        case .today: "calendar.badge.clock"
        case .tasks: "checkmark.circle"
        case .inbox: "tray"
        case .projects: "folder"
        case .calendar: "calendar"
        case .notes: "note.text"
        case .review: "chart.bar.doc.horizontal"
        case .quickCapture: "plus.square"
        case .fullSearch: "magnifyingglass"
        case .importActivity: "arrow.down.circle"
        case .accountAdmin: "person.2.badge.gearshape"
        }
    }

    var destination: WorkspaceDestination? {
        switch self {
        case .today: .today
        case .tasks: .tasks
        case .inbox: .inbox
        case .projects: .projects
        case .calendar: .calendar
        case .notes: .notes
        case .review: .review
        default: nil
        }
    }

    static func available(isAdmin: Bool) -> [Self] {
        allCases.filter { isAdmin || $0 != .accountAdmin }
    }

    func matches(_ query: String) -> Bool {
        let value = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !value.isEmpty else { return true }
        return "\(title) \(subtitle) \(rawValue)".localizedCaseInsensitiveContains(value)
    }
}

enum SearchResultHistory {
    static func decode(_ json: String) -> [SearchResultItem] {
        guard let data = json.data(using: .utf8) else { return [] }
        return (try? JSONDecoder().decode([SearchResultItem].self, from: data)) ?? []
    }

    static func remembering(
        _ result: SearchResultItem,
        in json: String,
        limit: Int = 8
    ) -> String {
        let existing = decode(json).filter { $0.type != result.type || $0.id != result.id }
        guard let data = try? JSONEncoder().encode(Array(([result] + existing).prefix(limit))),
              let value = String(data: data, encoding: .utf8) else { return json }
        return value
    }
}
