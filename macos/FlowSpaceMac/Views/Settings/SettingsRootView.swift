import SwiftUI

struct SettingsRootView: View {
    @Environment(AppSession.self) private var session
    @AppStorage("defaultCalendarView") private var defaultCalendarView = "week"
    @AppStorage("playCompletionSound") private var playCompletionSound = false
    @AppStorage(CalendarResizePreferences.enabledKey) private var calendarResizeHandleEnabled = true
    @State private var sheet: SettingsSheetDestination?

    var body: some View {
        TabView {
            Form {
                Picker("默认日历视图", selection: $defaultCalendarView) {
                    Text("周").tag("week")
                    Text("月").tag("month")
                    Text("年").tag("year")
                }
                Toggle("完成任务时播放提示音", isOn: $playCompletionSound)
                Toggle("允许拖动日历时间块边缘调整时长", isOn: $calendarResizeHandleEnabled)
                Text("调整按 15 分钟吸附，并在提交前显示改动和时间冲突预览。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
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
                Button("更换服务…") { sheet = .changeService }
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
        .safeAreaInset(edge: .top, spacing: 0) {
            if session.connectivity.isOffline {
                OfflineReadOnlyBanner(
                    lastSuccessfulResponseAt: session.connectivity.lastSuccessfulResponseAt,
                    retry: { Task { await session.retryConnection() } }
                )
            }
        }
        .scenePadding()
        .frame(width: 720, height: 500)
        .sheet(item: $sheet) { destination in
            switch destination {
            case .changeService:
                ServiceSwitchSheet(session: session)
            }
        }
    }
}

private enum SettingsSheetDestination: String, Identifiable {
    case changeService

    var id: String { rawValue }
}

private struct ServiceSwitchSheet: View {
    private enum Phase: Equatable {
        case editing
        case testing
        case verified(URL)
        case failed(String)
        case switching
    }

    @Environment(\.dismiss) private var dismiss
    let session: AppSession
    @State private var address: String
    @State private var phase: Phase = .editing

    init(session: AppSession) {
        self.session = session
        _address = SwiftUI.State(initialValue: session.baseURL?.absoluteString ?? "")
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("新服务") {
                    TextField("https://flowspace.example.com", text: $address)
                        .textFieldStyle(.roundedBorder)
                        .onSubmit { testConnection() }

                    if let normalizedURL, ServerAddress.isInsecureRemote(normalizedURL) {
                        Label(
                            "该地址使用未加密 HTTP，登录信息可能被同一网络中的其他设备读取。",
                            systemImage: "exclamationmark.triangle.fill"
                        )
                        .font(.caption)
                        .foregroundStyle(.orange)
                    }

                    statusContent
                }

                Section("切换影响") {
                    Label("退出当前服务的登录会话", systemImage: "rectangle.portrait.and.arrow.right")
                    Label("清空当前服务的内存缓存和最近内容缓存", systemImage: "trash")
                    Label("候选地址测试不会携带当前服务 Cookie", systemImage: "shield.checkered")
                }

                Section {
                    Text("只有连接测试成功后才能切换。切换完成后需要在新服务重新登录。")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            .formStyle(.grouped)
            .navigationTitle("更换 FlowSpace 服务")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("取消") { dismiss() }
                        .disabled(isBusy)
                }
                ToolbarItem(placement: .confirmationAction) {
                    if case .verified(let url) = phase {
                        Button("确认切换") {
                            Task { await switchService(to: url) }
                        }
                    } else {
                        Button("测试连接") { testConnection() }
                            .disabled(!canTest)
                    }
                }
            }
        }
        .frame(width: 540, height: 430)
        .interactiveDismissDisabled(isBusy)
        .onChange(of: address) {
            guard !isBusy else { return }
            phase = .editing
        }
    }

    @ViewBuilder
    private var statusContent: some View {
        switch phase {
        case .editing:
            Text("先测试新地址，不会改变当前连接。")
                .foregroundStyle(.secondary)
        case .testing:
            HStack {
                ProgressView().controlSize(.small)
                Text("正在测试连接…")
            }
        case .verified(let url):
            Label("连接测试成功：\(url.absoluteString)", systemImage: "checkmark.circle.fill")
                .foregroundStyle(.green)
        case .failed(let message):
            Label(message, systemImage: "xmark.circle.fill")
                .foregroundStyle(.red)
        case .switching:
            HStack {
                ProgressView().controlSize(.small)
                Text("正在切换服务…")
            }
        }
    }

    private var normalizedURL: URL? {
        try? ServerAddress.normalize(address)
    }

    private var isBusy: Bool {
        phase == .testing || phase == .switching
    }

    private var canTest: Bool {
        !isBusy && normalizedURL != nil && normalizedURL != session.baseURL
    }

    private func testConnection() {
        guard canTest else { return }
        phase = .testing
        let candidateAddress = address
        Task {
            do {
                let url = try await session.testService(candidateAddress)
                guard !Task.isCancelled, candidateAddress == address else { return }
                phase = .verified(url)
            } catch is CancellationError {
                return
            } catch {
                guard candidateAddress == address else { return }
                phase = .failed(error.localizedDescription)
            }
        }
    }

    private func switchService(to url: URL) async {
        phase = .switching
        await session.switchService(to: url)
        dismiss()
    }
}
