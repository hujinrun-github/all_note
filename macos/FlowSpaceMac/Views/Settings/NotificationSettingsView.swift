import SwiftUI

struct NotificationSettingsView: View {
    let service: AppNotificationService

    @AppStorage(FlowNotificationPreferences.taskKey) private var taskCompletion = true
    @AppStorage(FlowNotificationPreferences.importKey) private var contentImports = true
    @AppStorage(FlowNotificationPreferences.syncKey) private var syncCompletion = true
    @AppStorage(FlowNotificationPreferences.soundKey) private var sound = true

    var body: some View {
        Form {
            Section("系统权限") {
                LabeledContent("通知状态") {
                    Label(service.authorizationState.title, systemImage: authorizationIcon)
                        .foregroundStyle(authorizationColor)
                }

                switch service.authorizationState {
                case .notDetermined:
                    Button("允许系统通知") {
                        Task { await service.requestAuthorization() }
                    }
                    Text("只会在你点击后请求 macOS 通知权限。")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                case .denied:
                    Button("打开系统通知设置") { service.openSystemSettings() }
                    Text("FlowSpace 的通知权限已关闭，需要在系统设置中重新开启。")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                case .authorized, .provisional, .ephemeral:
                    Button("发送测试通知") {
                        Task { await service.sendTestNotification() }
                    }
                    Button("打开系统通知设置") { service.openSystemSettings() }
                case .unknown:
                    Button("刷新权限状态") {
                        Task { await service.refreshAuthorizationState() }
                    }
                }

                if let errorMessage = service.errorMessage {
                    Label(errorMessage, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.red)
                }
            }

            Section("提醒类型") {
                Toggle("任务完成", isOn: $taskCompletion)
                Toggle("播客导入完成或需要处理", isOn: $contentImports)
                Toggle("同步完成", isOn: $syncCompletion)
                Toggle("播放通知提示音", isOn: $sound)
            }

            Section {
                Text("点击带有生成笔记的导入通知，会直接在 FlowSpace 中打开该笔记。应用运行期间会持续检查后台导入进度。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
        .task { await service.refreshAuthorizationState() }
    }

    private var authorizationIcon: String {
        switch service.authorizationState {
        case .authorized, .provisional, .ephemeral: "checkmark.circle.fill"
        case .denied: "xmark.circle.fill"
        case .notDetermined, .unknown: "questionmark.circle"
        }
    }

    private var authorizationColor: Color {
        switch service.authorizationState {
        case .authorized, .provisional, .ephemeral: .green
        case .denied: .red
        case .notDetermined, .unknown: .secondary
        }
    }
}
