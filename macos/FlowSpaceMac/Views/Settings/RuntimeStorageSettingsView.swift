import SwiftUI

struct RuntimeStorageSettingsView: View {
    let store: WorkspaceStore
    @State private var draft: RuntimeProfileDraft?
    @State private var notice: RuntimeNotice?

    var body: some View {
        VStack(spacing: 0) {
            settingsHeader
            Divider()
            if store.isLoadingRuntime && store.runtime == nil {
                ProgressView("正在读取运行时绑定…")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollView {
                    VStack(spacing: 16) {
                        if let notice { RuntimeNoticeView(notice: notice) }
                        RuntimeServiceCard(
                            kind: .dataStore,
                            description: "保存笔记、任务和同步数据。自定义配置验证后仍需执行数据迁移。",
                            binding: store.runtime?.binding(.dataStore),
                            configure: { draft = RuntimeProfileDraft(kind: .dataStore) }
                        )
                        RuntimeServiceCard(
                            kind: .objectS3,
                            description: "保存图片、附件和语音文件。Bucket 必须预先存在。",
                            binding: store.runtime?.binding(.objectS3),
                            configure: { draft = RuntimeProfileDraft(kind: .objectS3) }
                        )
                    }
                    .padding(18)
                }
            }
        }
        .task { await store.loadRuntimeSettings() }
        .sheet(item: $draft) { draft in
            RuntimeProfileEditorSheet(
                draft: draft,
                store: store,
                save: { value in await save(value) }
            )
        }
    }

    private var settingsHeader: some View {
        HStack {
            VStack(alignment: .leading, spacing: 3) {
                Text("运行时存储").font(.title2.weight(.semibold))
                Text("当前工作区 · \(store.runtime?.mode ?? "状态未知") · binding revision \(store.runtime?.bindingRevision ?? 0)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Button("刷新", systemImage: "arrow.clockwise") {
                Task { await store.loadRuntimeSettings() }
            }
            .disabled(store.isLoadingRuntime)
        }
        .padding(18)
    }

    private func save(_ draft: RuntimeProfileDraft) async -> Bool {
        do {
            _ = try await store.saveAndApplyRuntimeProfile(draft)
            self.draft = nil
            notice = RuntimeNotice(
                message: draft.kind == .dataStore
                    ? "数据库配置已保存并验证；尚未切换，仍需启动数据迁移。"
                    : "对象存储已保存、验证并启用。"
            )
            return true
        } catch {
            notice = RuntimeNotice(message: runtimeErrorMessage(error), isError: true)
            return false
        }
    }
}

struct RuntimeServiceCard: View {
    let kind: ServiceKind
    let description: String
    let binding: ServiceBinding?
    let configure: () -> Void

    var body: some View {
        GroupBox {
            VStack(alignment: .leading, spacing: 13) {
                HStack(spacing: 12) {
                    Image(systemName: kind == .dataStore ? "cylinder.split.1x2" : "externaldrive.badge.icloud")
                        .font(.title2)
                        .foregroundStyle(.tint)
                        .frame(width: 34)
                    VStack(alignment: .leading, spacing: 3) {
                        Text(kind.title).font(.headline)
                        Text(description).font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer()
                    Text(binding?.mode.title ?? "平台默认")
                        .font(.caption.weight(.semibold))
                        .padding(.horizontal, 9)
                        .padding(.vertical, 5)
                        .background(.tint.opacity(0.1), in: .capsule)
                }
                Divider()
                LabeledContent("提供方", value: binding?.provider ?? "平台默认")
                LabeledContent("端点", value: binding?.endpointName ?? "自动选择")
                LabeledContent("凭据", value: binding?.hasCredentials == true ? "已安全保存" : "平台管理")
                LabeledContent("Revision", value: String(binding?.revision ?? 0))
                HStack {
                    Spacer()
                    Button("添加自定义配置", systemImage: "slider.horizontal.3", action: configure)
                }
            }
            .padding(4)
        }
    }
}

struct RuntimeProfileEditorSheet: View {
    @Environment(\.dismiss) private var dismiss
    @State private var draft: RuntimeProfileDraft
    @State private var notice: RuntimeNotice?
    let store: WorkspaceStore
    let save: (RuntimeProfileDraft) async -> Bool

    init(draft: RuntimeProfileDraft, store: WorkspaceStore, save: @escaping (RuntimeProfileDraft) async -> Bool) {
        _draft = State(initialValue: draft)
        self.store = store
        self.save = save
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Label("配置\(draft.kind.title)", systemImage: icon)
                .font(.title2.weight(.semibold))

            Form {
                TextField("配置名称", text: $draft.name)
                if draft.kind == .llmTranscription {
                    Picker("语音服务类型", selection: $draft.transcriptionProvider) {
                        ForEach(TranscriptionProvider.allCases) { provider in Text(provider.title).tag(provider) }
                    }
                    .onChange(of: draft.transcriptionProvider) {
                        if draft.transcriptionProvider == .wyoming, draft.model.isEmpty { draft.model = "auto" }
                    }
                }
                TextField(endpointLabel, text: $draft.endpoint)
                switch draft.kind {
                case .dataStore:
                    SecureField("数据库密码", text: $draft.secret)
                    TextField("Schema", text: $draft.namespace)
                case .objectS3:
                    TextField("Access Key", text: $draft.accessKey)
                    SecureField("Secret Key", text: $draft.objectSecretKey)
                    TextField("Bucket 名称", text: $draft.namespace)
                case .llmChat:
                    TextField("模型名称", text: $draft.model)
                    SecureField("API Key", text: $draft.secret)
                case .llmTranscription:
                    TextField("模型名称", text: $draft.model)
                    if draft.transcriptionProvider != .wyoming {
                        SecureField("API Key（无需鉴权时可留空）", text: $draft.secret)
                    }
                }
            }
            .formStyle(.grouped)

            if draft.kind == .dataStore {
                Label("验证配置不会立即切换数据库。切换必须通过独立迁移流程完成。", systemImage: "exclamationmark.triangle")
                    .font(.caption)
                    .foregroundStyle(.orange)
            } else if draft.kind == .objectS3 {
                Text("Bucket 必须预先存在，FlowSpace 不会用用户凭据静默创建。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else if draft.kind == .llmTranscription, draft.transcriptionProvider == .wyoming {
                Text("Wyoming 使用 TCP 地址且不需要 API Key，例如 tcp://192.168.1.13:20300。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            if let notice { RuntimeNoticeView(notice: notice) }

            HStack {
                Button("测试连接") { Task { await test() } }
                    .disabled(store.isMutating)
                Spacer()
                Button("取消") { dismiss() }.disabled(store.isMutating)
                Button(draft.kind == .dataStore ? "保存并验证" : "保存并启用") {
                    Task { if await save(draft) { dismiss() } }
                }
                .buttonStyle(.borderedProminent)
                .disabled(store.isMutating)
            }
        }
        .padding(22)
        .frame(width: 580, height: 590)
        .interactiveDismissDisabled(store.isMutating)
    }

    private var icon: String {
        switch draft.kind {
        case .dataStore: "cylinder.split.1x2"
        case .objectS3: "externaldrive.badge.icloud"
        case .llmChat: "message.badge.waveform"
        case .llmTranscription: "waveform"
        }
    }
    private var endpointLabel: String {
        draft.kind == .llmTranscription && draft.transcriptionProvider == .wyoming ? "Wyoming TCP 地址" : "服务地址"
    }

    private func test() async {
        do {
            let result = try await store.testRuntimeProfile(draft)
            notice = RuntimeNotice(message: result.message.isEmpty ? "连接测试通过" : result.message)
        } catch {
            notice = RuntimeNotice(message: runtimeErrorMessage(error), isError: true)
        }
    }
}

struct RuntimeNotice: Equatable {
    let message: String
    var isError = false
}

struct RuntimeNoticeView: View {
    let notice: RuntimeNotice
    var body: some View {
        Label(notice.message, systemImage: notice.isError ? "exclamationmark.triangle.fill" : "checkmark.circle.fill")
            .font(.callout)
            .foregroundStyle(notice.isError ? .red : .green)
            .padding(10)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background((notice.isError ? Color.red : Color.green).opacity(0.07), in: .rect(cornerRadius: 9))
    }
}

func runtimeErrorMessage(_ error: Error) -> String {
    guard let apiError = error as? APIError else { return error.localizedDescription }
    switch apiError.code {
    case "revision_conflict": return "运行时设置已被其他窗口修改，请刷新后重试"
    case "profile_verification_failed": return "服务验证失败：\(apiError.message)"
    default: return apiError.message
    }
}
