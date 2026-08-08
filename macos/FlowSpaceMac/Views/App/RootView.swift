import SwiftUI

struct RootView: View {
    @Environment(AppSession.self) private var session
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Group {
            switch session.phase {
            case .starting:
                ProgressView("正在恢复工作区…")
                    .controlSize(.large)
            case .connection:
                ConnectionView()
            case .login:
                LoginView()
            case .passwordChange:
                RequiredPasswordChangeView()
            case .checkingCapabilities:
                ProgressView("正在确认 v2 工作空间…")
                    .controlSize(.large)
            case .unsupported(let reason):
                CapabilityDiagnosticView(reason: reason)
            case .ready:
                if let store = session.workspaceStore {
                    WorkspaceView(store: store)
                } else {
                    ContentUnavailableView("工作区不可用", systemImage: "exclamationmark.triangle")
                }
            }
        }
        .task {
            await session.restore()
        }
        .task(id: session.phase) {
            await monitorContentImports()
        }
        .onChange(of: session.notifications.pendingDestination) {
            openPendingNotificationDestination()
        }
        .onChange(of: session.phase) {
            openPendingNotificationDestination()
            openPendingSpotlightRoute()
        }
        .onChange(of: session.spotlight.pendingRoute) {
            openPendingSpotlightRoute()
        }
    }

    private func monitorContentImports() async {
        guard session.phase == .ready, let store = session.workspaceStore else { return }
        while !Task.isCancelled {
            await store.loadContentImports()
            do {
                try await Task.sleep(for: .seconds(store.activeContentImportCount > 0 ? 8 : 45))
            } catch {
                return
            }
        }
    }

    private func openPendingNotificationDestination() {
        guard session.phase == .ready,
              let destination = session.notifications.pendingDestination else { return }
        switch destination {
        case .note(let noteID):
            openWindow(value: noteID)
        }
        session.notifications.consumePendingDestination()
    }

    private func openPendingSpotlightRoute() {
        guard session.phase == .ready,
              let route = session.spotlight.pendingRoute,
              let currentWorkspaceID = session.currentUser?.workspace.id else { return }
        guard route.workspaceID == currentWorkspaceID else {
            session.workspaceStore?.errorMessage = "这个 Spotlight 结果属于另一个工作空间，请先切换服务或账号。"
            session.spotlight.consumePendingRoute()
            return
        }

        switch route {
        case .note(_, let noteID):
            openWindow(value: noteID)
        case .task(_, let taskID):
            session.requestWorkspaceEntitySelection(.task(taskID))
            session.requestWorkspaceDestination(.tasks)
            openWindow(id: "workspace")
        case .project(_, let projectID):
            session.requestWorkspaceEntitySelection(.project(projectID))
            session.requestWorkspaceDestination(.projects)
            openWindow(id: "workspace")
        }
        session.spotlight.consumePendingRoute()
    }
}
