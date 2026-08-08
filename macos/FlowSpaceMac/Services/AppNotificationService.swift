import AppKit
import Foundation
import Observation
import UserNotifications

enum FlowNotificationKind: String, Sendable {
    case task
    case contentImport = "content-import"
    case sync
    case test

    var threadIdentifier: String {
        "flowspace.\(rawValue)"
    }
}

enum ContentImportNotificationPolicy {
    static func shouldNotify(
        previous: ContentImportStatus?,
        current: ContentImportStatus
    ) -> Bool {
        guard previous == .active else { return false }
        return current == .completed || current == .failed || current == .needsReview
    }
}

enum FlowNotificationDestination: Equatable, Sendable {
    case note(String)

    private static let destinationKey = "flowspace_destination"
    private static let noteIDKey = "flowspace_note_id"

    var userInfo: [String: String] {
        switch self {
        case .note(let noteID):
            [Self.destinationKey: "note", Self.noteIDKey: noteID]
        }
    }

    static func parse(userInfo: [AnyHashable: Any]) -> FlowNotificationDestination? {
        guard userInfo[destinationKey] as? String == "note",
              let noteID = userInfo[noteIDKey] as? String,
              !noteID.isEmpty else {
            return nil
        }
        return .note(noteID)
    }
}

struct FlowNotificationEvent: Sendable {
    let kind: FlowNotificationKind
    let title: String
    let body: String
    let destination: FlowNotificationDestination?

    @MainActor
    func makeContent(playSound: Bool) -> UNMutableNotificationContent {
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        content.threadIdentifier = kind.threadIdentifier
        content.categoryIdentifier = "FLOWSPACE_\(kind.rawValue.uppercased())"
        content.userInfo = destination?.userInfo ?? [:]
        if playSound {
            content.sound = .default
        }
        return content
    }
}

struct FlowNotificationPreferences: Equatable, Sendable {
    static let taskKey = "notifications.taskCompletion"
    static let importKey = "notifications.contentImports"
    static let syncKey = "notifications.syncCompletion"
    static let soundKey = "notifications.sound"

    let taskCompletion: Bool
    let contentImports: Bool
    let syncCompletion: Bool
    let sound: Bool

    init(defaults: UserDefaults) {
        taskCompletion = defaults.bool(forKey: Self.taskKey)
        contentImports = defaults.bool(forKey: Self.importKey)
        syncCompletion = defaults.bool(forKey: Self.syncKey)
        sound = defaults.bool(forKey: Self.soundKey)
    }

    func allows(_ kind: FlowNotificationKind) -> Bool {
        switch kind {
        case .task: taskCompletion
        case .contentImport: contentImports
        case .sync: syncCompletion
        case .test: true
        }
    }

    static func registerDefaults(in defaults: UserDefaults) {
        defaults.register(defaults: [
            taskKey: true,
            importKey: true,
            syncKey: true,
            soundKey: true,
        ])
    }
}

enum FlowNotificationAuthorizationState: Equatable, Sendable {
    case notDetermined
    case denied
    case authorized
    case provisional
    case ephemeral
    case unknown

    init(_ status: UNAuthorizationStatus) {
        switch status {
        case .notDetermined: self = .notDetermined
        case .denied: self = .denied
        case .authorized: self = .authorized
        case .provisional: self = .provisional
        case .ephemeral: self = .ephemeral
        @unknown default: self = .unknown
        }
    }

    var canDeliver: Bool {
        switch self {
        case .authorized, .provisional, .ephemeral: true
        case .notDetermined, .denied, .unknown: false
        }
    }

    var title: String {
        switch self {
        case .notDetermined: "尚未请求"
        case .denied: "已关闭"
        case .authorized: "已允许"
        case .provisional: "临时允许"
        case .ephemeral: "本次允许"
        case .unknown: "状态未知"
        }
    }
}

@MainActor
@Observable
final class AppNotificationService {
    static let shared = AppNotificationService()

    private(set) var authorizationState: FlowNotificationAuthorizationState = .notDetermined
    private(set) var pendingDestination: FlowNotificationDestination?
    var errorMessage: String?

    private let center: UNUserNotificationCenter
    private let defaults: UserDefaults

    init(
        center: UNUserNotificationCenter = .current(),
        defaults: UserDefaults = .standard
    ) {
        self.center = center
        self.defaults = defaults
        FlowNotificationPreferences.registerDefaults(in: defaults)
    }

    func refreshAuthorizationState() async {
        let settings = await center.notificationSettings()
        authorizationState = FlowNotificationAuthorizationState(settings.authorizationStatus)
    }

    @discardableResult
    func requestAuthorization() async -> Bool {
        errorMessage = nil
        do {
            let granted = try await center.requestAuthorization(options: [.alert, .sound])
            await refreshAuthorizationState()
            return granted
        } catch {
            errorMessage = error.localizedDescription
            await refreshAuthorizationState()
            return false
        }
    }

    func openSystemSettings() {
        guard let url = URL(string: "x-apple.systempreferences:com.apple.Notifications-Settings.extension") else { return }
        NSWorkspace.shared.open(url)
    }

    func sendTestNotification() async {
        await deliver(FlowNotificationEvent(
            kind: .test,
            title: "FlowSpace 通知已开启",
            body: "任务、导入和同步完成后会在这里提醒你。",
            destination: nil
        ))
    }

    func notifyTaskCompleted(title: String) async {
        await deliver(FlowNotificationEvent(
            kind: .task,
            title: "任务已完成",
            body: title,
            destination: nil
        ))
    }

    func notifyContentImportChanged(_ item: ContentImport) async {
        let displayTitle = item.title?.trimmingCharacters(in: .whitespacesAndNewlines)
        let resolvedTitle = displayTitle.flatMap { $0.isEmpty ? nil : $0 }
        switch item.status {
        case .completed:
            await deliver(FlowNotificationEvent(
                kind: .contentImport,
                title: "播客导入完成",
                body: resolvedTitle ?? "笔记已经生成，可以打开查看。",
                destination: item.resultNoteID.map(FlowNotificationDestination.note)
            ))
        case .failed, .needsReview:
            await deliver(FlowNotificationEvent(
                kind: .contentImport,
                title: item.status == .failed ? "播客导入失败" : "播客导入需要处理",
                body: item.errorMessage ?? resolvedTitle ?? "请打开 FlowSpace 查看详情。",
                destination: item.resultNoteID.map(FlowNotificationDestination.note)
            ))
        case .active, .canceled:
            break
        }
    }

    func notifySyncCompleted(targetName: String, summary: String) async {
        await deliver(FlowNotificationEvent(
            kind: .sync,
            title: "同步完成 · \(targetName)",
            body: summary,
            destination: nil
        ))
    }

    func receive(_ destination: FlowNotificationDestination) {
        pendingDestination = destination
    }

    func consumePendingDestination() {
        pendingDestination = nil
    }

    private func deliver(_ event: FlowNotificationEvent) async {
        let preferences = FlowNotificationPreferences(defaults: defaults)
        guard preferences.allows(event.kind) else { return }

        let settings = await center.notificationSettings()
        authorizationState = FlowNotificationAuthorizationState(settings.authorizationStatus)
        guard authorizationState.canDeliver else { return }

        let request = UNNotificationRequest(
            identifier: "flowspace.\(event.kind.rawValue).\(UUID().uuidString)",
            content: event.makeContent(playSound: preferences.sound),
            trigger: nil
        )
        do {
            try await center.add(request)
            errorMessage = nil
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}
