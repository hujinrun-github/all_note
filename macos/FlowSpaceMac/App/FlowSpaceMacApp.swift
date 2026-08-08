import CoreSpotlight
import SwiftUI
import UserNotifications

@main
struct FlowSpaceMacApp: App {
    @NSApplicationDelegateAdaptor(FlowSpaceAppDelegate.self) private var appDelegate
    @State private var session = AppSession()

    var body: some Scene {
        WindowGroup("FlowSpace", id: "workspace") {
            RootView()
                .environment(session)
                .frame(minWidth: 900, minHeight: 620)
        }
        .defaultSize(width: 1240, height: 780)
        .commands {
            SidebarCommands()
            FlowSpaceCommands(session: session)
            ImportWindowCommands(session: session)
            AccountWindowCommands(session: session)
        }

        Window("快速捕获", id: "quick-capture") {
            QuickCaptureWindowView()
                .environment(session)
                .frame(minWidth: 500, minHeight: 360)
        }
        .defaultSize(width: 540, height: 400)
        .windowResizability(.contentSize)

        WindowGroup("笔记", for: String.self) { $noteID in
            if let noteID, let store = session.workspaceStore {
                NoteEditorWindowView(noteID: noteID, store: store)
                    .environment(session)
                    .frame(minWidth: 720, minHeight: 560)
            } else {
                ContentUnavailableView("笔记不可用", systemImage: "note.text")
            }
        }
        .defaultSize(width: 980, height: 760)

        WindowGroup("学习脑图", for: RoadmapMindMapRoute.self) { $route in
            if let route, let store = session.workspaceStore {
                RoadmapMindMapView(route: route, store: store)
                    .environment(session)
                    .frame(minWidth: 900, minHeight: 620)
            } else {
                ContentUnavailableView("学习脑图不可用", systemImage: "point.3.connected.trianglepath.dotted")
            }
        }
        .defaultSize(width: 1120, height: 760)

        Window("导入任务", id: "import-activity") {
            if let store = session.workspaceStore {
                ContentImportActivityView(store: store)
                    .environment(session)
                    .frame(minWidth: 720, minHeight: 520)
            } else {
                ContentUnavailableView("导入任务不可用", systemImage: "podcasts")
            }
        }
        .defaultSize(width: 860, height: 680)

        Window("全局搜索", id: "global-search") {
            if let store = session.workspaceStore {
                GlobalSearchWindowView(store: store)
                    .environment(session)
            } else {
                ContentUnavailableView("搜索不可用", systemImage: "magnifyingglass")
                    .frame(minWidth: 560, minHeight: 360)
            }
        }
        .defaultSize(width: 720, height: 560)

        Window("账号管理", id: "account-admin") {
            if session.isAdmin, let store = session.workspaceStore {
                AccountAdminView(store: store)
                    .environment(session)
                    .frame(minWidth: 900, minHeight: 600)
            } else {
                ContentUnavailableView(
                    "需要管理员权限",
                    systemImage: "person.2.badge.gearshape",
                    description: Text("账号管理只对管理员开放。")
                )
                .frame(minWidth: 560, minHeight: 360)
            }
        }
        .defaultSize(width: 1080, height: 720)

        Settings {
            SettingsRootView()
                .environment(session)
        }

        MenuBarExtra(
            "FlowSpace",
            systemImage: "bolt.circle",
            isInserted: Binding(
                get: { session.menuBarExtraEnabled },
                set: { session.menuBarExtraEnabled = $0 }
            )
        ) {
            FlowSpaceMenuBarView()
                .environment(session)
        }
        .menuBarExtraStyle(.menu)
    }
}

final class FlowSpaceAppDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    func applicationWillFinishLaunching(_ notification: Notification) {
        UNUserNotificationCenter.current().delegate = self
    }

    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification
    ) async -> UNNotificationPresentationOptions {
        [.banner, .list, .sound]
    }

    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse
    ) async {
        guard response.actionIdentifier == UNNotificationDefaultActionIdentifier,
              let destination = FlowNotificationDestination.parse(
                userInfo: response.notification.request.content.userInfo
              ) else {
            return
        }
        await MainActor.run {
            NSApp.activate()
            AppNotificationService.shared.receive(destination)
        }
    }

    func application(
        _ application: NSApplication,
        continue userActivity: NSUserActivity,
        restorationHandler: @escaping ([any NSUserActivityRestoring]) -> Void
    ) -> Bool {
        guard userActivity.activityType == CSSearchableItemActionType,
              let identifier = userActivity.userInfo?[CSSearchableItemActivityIdentifier] as? String,
              let route = FlowSpotlightIdentifier.parse(identifier) else { return false }
        NSApp.activate()
        AppSpotlightService.shared.receive(route)
        return true
    }
}

private struct AccountWindowCommands: Commands {
    @Environment(\.openWindow) private var openWindow
    let session: AppSession

    var body: some Commands {
        CommandGroup(after: .windowList) {
            if session.isAdmin {
                Button("账号管理") { openWindow(id: "account-admin") }
                    .keyboardShortcut(",", modifiers: [.command, .shift])
            }
        }
    }
}

private struct FlowSpaceCommands: Commands {
    @Environment(\.openWindow) private var openWindow
    let session: AppSession

    var body: some Commands {
        CommandGroup(after: .newItem) {
            Button("快速捕获") {
                openWindow(id: "quick-capture")
            }
            .keyboardShortcut("n", modifiers: [.command, .shift])

            Button("全局搜索") {
                openWindow(id: "global-search")
            }
            .keyboardShortcut("k", modifiers: .command)
        }

        CommandGroup(after: .importExport) {
            Button("导入播客…") {
                session.presentPodcastImport()
                openWindow(id: "import-activity")
            }
            .keyboardShortcut("i", modifiers: [.command, .shift])
        }
    }
}

private struct ImportWindowCommands: Commands {
    @Environment(\.openWindow) private var openWindow
    let session: AppSession

    var body: some Commands {
        CommandGroup(after: .windowList) {
            Button(importActivityTitle) {
                openWindow(id: "import-activity")
            }
        }
    }

    private var importActivityTitle: String {
        let count = session.workspaceStore?.activeContentImportCount ?? 0
        return count > 0 ? "导入任务（\(count) 个进行中）" : "导入任务"
    }
}
