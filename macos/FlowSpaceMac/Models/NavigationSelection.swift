import SwiftUI

enum WorkspaceDestination: String, CaseIterable, Identifiable, Hashable {
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

