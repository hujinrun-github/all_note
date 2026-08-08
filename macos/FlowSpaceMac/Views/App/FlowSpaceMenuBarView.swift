import AppKit
import SwiftUI

struct FlowSpaceMenuBarView: View {
    @Environment(AppSession.self) private var session
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        if let nextTask = session.menuBarNextTask {
            Button {
                openToday()
            } label: {
                Label("\(nextTask.shortTitle) · \(nextTask.schedule)", systemImage: "checkmark.circle")
            }
        } else {
            Text(emptyTaskTitle)
        }

        Divider()

        Button("快速捕获", systemImage: "bolt.fill") {
            activateAndOpenWindow(id: "quick-capture")
        }

        Button("打开今日", systemImage: "calendar.badge.clock") {
            openToday()
        }

        Divider()

        Label(connectionTitle, systemImage: connectionImage)

        Divider()

        Button("退出 FlowSpace", systemImage: "power") {
            NSApp.terminate(nil)
        }
        .task(id: session.phase) {
            guard session.phase == .ready else { return }
            while !Task.isCancelled, session.phase == .ready {
                await session.refreshMenuBarSummary()
                do {
                    try await Task.sleep(for: .seconds(60))
                } catch {
                    return
                }
            }
        }
    }

    private var emptyTaskTitle: String {
        switch session.phase {
        case .ready: "今天没有待完成任务"
        case .starting, .checkingCapabilities: "正在连接 FlowSpace…"
        case .connection: "尚未连接服务"
        case .login, .passwordChange: "需要登录 FlowSpace"
        case .unsupported: "当前工作区不可用"
        }
    }

    private var connectionTitle: String {
        if session.workspaceStore?.isSyncing == true { return "正在同步" }
        if session.menuBarRefreshError != nil { return "连接异常" }
        switch session.phase {
        case .ready: return "已连接"
        case .starting, .checkingCapabilities: return "正在连接"
        case .connection: return "未连接"
        case .login, .passwordChange: return "需要登录"
        case .unsupported: return "工作区不可用"
        }
    }

    private var connectionImage: String {
        if session.workspaceStore?.isSyncing == true { return "arrow.triangle.2.circlepath" }
        if session.menuBarRefreshError != nil { return "wifi.exclamationmark" }
        return session.phase == .ready ? "circle.fill" : "circle.dashed"
    }

    private func openToday() {
        session.requestWorkspaceDestination(.today)
        activateAndOpenWindow(id: "workspace")
    }

    private func activateAndOpenWindow(id: String) {
        NSApp.activate()
        openWindow(id: id)
    }
}
