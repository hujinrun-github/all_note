import SwiftUI

struct SettingsRootView: View {
    @Environment(AppSession.self) private var session
    @AppStorage("defaultCalendarView") private var defaultCalendarView = "week"
    @AppStorage("playCompletionSound") private var playCompletionSound = false

    var body: some View {
        TabView {
            Form {
                Picker("默认日历视图", selection: $defaultCalendarView) {
                    Text("周").tag("week")
                    Text("月").tag("month")
                    Text("年").tag("year")
                }
                Toggle("完成任务时播放提示音", isOn: $playCompletionSound)
                Toggle(
                    "在菜单栏显示 FlowSpace",
                    isOn: Binding(
                        get: { session.menuBarExtraEnabled },
                        set: { session.menuBarExtraEnabled = $0 }
                    )
                )
            }
            .formStyle(.grouped)
            .tabItem { Label("通用", systemImage: "gear") }

            Form {
                LabeledContent("当前服务", value: session.baseURL?.absoluteString ?? "未连接")
                if let url = session.baseURL, ServerAddress.isInsecureRemote(url) {
                    Label("当前远程连接使用未加密 HTTP", systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.orange)
                }
                Button("更换服务") { Task { await session.changeService() } }
            }
            .formStyle(.grouped)
            .tabItem { Label("连接", systemImage: "network") }

            Group {
                if let store = session.workspaceStore {
                    ProfileSettingsView(store: store)
                } else {
                    ContentUnavailableView("个人资料不可用", systemImage: "person.crop.circle")
                }
            }
            .tabItem { Label("个人资料", systemImage: "person.crop.circle") }

            Group {
                if let store = session.workspaceStore {
                    AccountSecuritySettingsView(store: store)
                } else {
                    ContentUnavailableView("安全设置不可用", systemImage: "lock")
                }
            }
            .tabItem { Label("安全", systemImage: "lock") }
            Group {
                if let store = session.workspaceStore {
                    SyncSettingsView(store: store)
                } else {
                    ContentUnavailableView("同步不可用", systemImage: "arrow.triangle.2.circlepath")
                }
            }
            .tabItem { Label("同步", systemImage: "arrow.triangle.2.circlepath") }
            Group {
                if let store = session.workspaceStore {
                    RuntimeStorageSettingsView(store: store)
                } else {
                    ContentUnavailableView("运行时存储不可用", systemImage: "externaldrive")
                }
            }
            .tabItem { Label("存储", systemImage: "externaldrive") }

            Group {
                if let store = session.workspaceStore {
                    RuntimeAISettingsView(store: store)
                } else {
                    ContentUnavailableView("AI 设置不可用", systemImage: "sparkles")
                }
            }
            .tabItem { Label("AI", systemImage: "sparkles") }
            NotificationSettingsView(service: session.notifications)
                .tabItem { Label("通知", systemImage: "bell") }
        }
        .scenePadding()
        .frame(width: 720, height: 500)
    }
}
