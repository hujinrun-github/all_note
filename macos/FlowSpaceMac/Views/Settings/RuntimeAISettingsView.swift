import SwiftUI

struct RuntimeAISettingsView: View {
    let store: WorkspaceStore
    @State private var draft: RuntimeProfileDraft?
    @State private var notice: RuntimeNotice?
    @State private var codexAuthorization: CodexDeviceAuthorization?

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                VStack(alignment: .leading, spacing: 3) {
                    Text("AI 服务").font(.title2.weight(.semibold))
                    Text("文本对话与语音转写分别绑定；也可以使用 Codex 订阅。")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Button("刷新", systemImage: "arrow.clockwise") { Task { await store.loadRuntimeSettings() } }
                    .disabled(store.isLoadingRuntime)
            }
            .padding(18)
            Divider()

            if store.isLoadingRuntime && store.runtime == nil {
                ProgressView("正在读取 AI 服务…")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollView {
                    VStack(spacing: 16) {
                        if let notice { RuntimeNoticeView(notice: notice) }
                        codexCard
                        aiServiceCard(.llmChat)
                        aiServiceCard(.llmTranscription)
                    }
                    .padding(18)
                }
            }
        }
        .task { await store.loadRuntimeSettings() }
        .sheet(item: $draft) { draft in
            RuntimeProfileEditorSheet(draft: draft, store: store) { value in
                await save(value)
            }
        }
    }

    private var codexCard: some View {
        GroupBox {
            VStack(alignment: .leading, spacing: 12) {
                HStack {
                    Image(systemName: "terminal").font(.title2).foregroundStyle(.tint)
                    VStack(alignment: .leading, spacing: 3) {
                        Text("Codex 订阅").font(.headline)
                        Text("通过 OpenAI 设备授权连接，不需要填写 API Key。")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer()
                    if codexAuthorization == nil {
                        Button("连接 Codex 订阅") { Task { await startCodex() } }
                            .buttonStyle(.borderedProminent)
                            .disabled(store.isMutating)
                    }
                }
                if let authorization = codexAuthorization {
                    Divider()
                    HStack {
                        VStack(alignment: .leading, spacing: 5) {
                            Text("授权码").font(.caption).foregroundStyle(.secondary)
                            Text(authorization.userCode).font(.title2.monospaced().weight(.bold)).textSelection(.enabled)
                        }
                        Spacer()
                        if let url = URL(string: authorization.verificationURL) {
                            Link("打开 OpenAI 授权页", destination: url)
                        }
                        Button("我已完成授权") { Task { await pollCodex(authorization) } }
                            .buttonStyle(.borderedProminent)
                            .disabled(store.isMutating)
                    }
                }
            }
            .padding(4)
        }
    }

    private func aiServiceCard(_ kind: ServiceKind) -> some View {
        let binding = store.runtime?.binding(kind)
        return GroupBox {
            VStack(alignment: .leading, spacing: 12) {
                HStack(spacing: 12) {
                    Image(systemName: kind == .llmChat ? "message.badge.waveform" : "waveform")
                        .font(.title2).foregroundStyle(.tint).frame(width: 32)
                    VStack(alignment: .leading, spacing: 3) {
                        Text(kind.title).font(.headline)
                        Text(kind == .llmChat ? "支持总结、路线生成和日文注音。" : "支持附件音频与播客逐字稿。")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer()
                    Text(binding?.mode.title ?? "平台默认")
                        .font(.caption.weight(.semibold))
                        .padding(.horizontal, 9).padding(.vertical, 5)
                        .background(.tint.opacity(0.1), in: .capsule)
                }
                if let binding {
                    LabeledContent("提供方", value: binding.provider ?? "平台默认")
                    LabeledContent("端点", value: binding.endpointName ?? "自动选择")
                    LabeledContent("Revision", value: String(binding.revision))
                }
                HStack {
                    Menu("切换模式") {
                        Button("使用平台默认") { Task { await changeMode(kind, .default) } }
                        Button("关闭服务") { Task { await changeMode(kind, .disabled) } }
                        if kind == .llmTranscription {
                            Button("复用文本服务") { Task { await changeMode(kind, .reuseChat) } }
                        }
                    }
                    .disabled(store.isMutating)
                    Spacer()
                    Button("配置自定义服务", systemImage: "slider.horizontal.3") {
                        draft = RuntimeProfileDraft(kind: kind)
                    }
                }
            }
            .padding(4)
        }
    }

    private func save(_ draft: RuntimeProfileDraft) async -> Bool {
        do {
            _ = try await store.saveAndApplyRuntimeProfile(draft)
            self.draft = nil
            notice = RuntimeNotice(message: "\(draft.kind.title)已验证并启用")
            return true
        } catch {
            notice = RuntimeNotice(message: runtimeErrorMessage(error), isError: true)
            return false
        }
    }

    private func changeMode(_ kind: ServiceKind, _ mode: ServiceBindingMode) async {
        do {
            try await store.changeServiceMode(kind: kind, mode: mode)
            notice = RuntimeNotice(message: "\(kind.title)已切换为\(mode.title)")
        } catch {
            notice = RuntimeNotice(message: runtimeErrorMessage(error), isError: true)
        }
    }

    private func startCodex() async {
        do {
            codexAuthorization = try await store.startCodexSubscription()
            notice = RuntimeNotice(message: "授权码已生成，请在 OpenAI 页面完成登录")
        } catch {
            notice = RuntimeNotice(message: runtimeErrorMessage(error), isError: true)
        }
    }

    private func pollCodex(_ authorization: CodexDeviceAuthorization) async {
        do {
            let result = try await store.pollCodexSubscription(authorization)
            switch result.status {
            case .pending:
                notice = RuntimeNotice(message: "尚未收到授权结果，请完成登录后再试", isError: true)
            case .connected:
                notice = RuntimeNotice(message: "Codex 订阅已连接并启用")
                codexAuthorization = nil
            case .expired, .failed:
                notice = RuntimeNotice(message: "授权已过期或失败，请重新连接", isError: true)
                codexAuthorization = nil
            }
        } catch {
            notice = RuntimeNotice(message: runtimeErrorMessage(error), isError: true)
        }
    }
}
