import SwiftUI

struct UpdateSettingsView: View {
    @Environment(AppUpdateService.self) private var updateService

    var body: some View {
        Form {
            Section("应用版本") {
                LabeledContent("FlowSpace", value: updateService.versionDescription)
                updateStatus
            }

            Section("自动更新") {
                Toggle(
                    "自动检查更新",
                    isOn: Binding(
                        get: { updateService.automaticallyChecksForUpdates },
                        set: { updateService.setAutomaticallyChecksForUpdates($0) }
                    )
                )
                .disabled(!updateService.configuration.isConfigured)

                Toggle(
                    "自动下载并安装更新",
                    isOn: Binding(
                        get: { updateService.automaticallyDownloadsUpdates },
                        set: { updateService.setAutomaticallyDownloadsUpdates($0) }
                    )
                )
                .disabled(
                    !updateService.configuration.isConfigured ||
                    !updateService.automaticallyChecksForUpdates
                )

                Button("检查更新…") {
                    updateService.checkForUpdates()
                }
                .disabled(!updateService.canCheckForUpdates)
                .accessibilityIdentifier("settings.checkForUpdates")
            }

            Section {
                Text("更新包同时通过 Sparkle Ed25519 签名和 Apple Developer ID 签名验证。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
    }

    @ViewBuilder
    private var updateStatus: some View {
        switch updateService.configuration.state {
        case .configured(let feedURL):
            LabeledContent("更新源", value: feedURL.host ?? feedURL.absoluteString)
            Label("安全更新通道已启用", systemImage: "checkmark.seal.fill")
                .foregroundStyle(.green)
        case .unconfigured:
            Label("此开发构建未配置更新通道", systemImage: "hammer")
                .foregroundStyle(.secondary)
            Text("正式分发构建会在签名和公证前强制校验更新源与公钥。")
                .font(.caption)
                .foregroundStyle(.secondary)
        case .invalid(let message):
            Label(message, systemImage: "exclamationmark.triangle.fill")
                .foregroundStyle(.orange)
        }
    }
}
