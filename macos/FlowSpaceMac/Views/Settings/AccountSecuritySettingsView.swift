import SwiftUI

struct AccountSecuritySettingsView: View {
    @State private var newPassword = ""
    @State private var confirmation = ""
    @State private var isWorking = false
    @State private var errorMessage: String?
    @State private var notice: String?

    let store: WorkspaceStore

    var body: some View {
        Form {
            Section("重置登录密码") {
                Text("验证当前登录会话后即可设置新密码，无需再次输入原密码。")
                    .foregroundStyle(.secondary)
                SecureField("新密码", text: $newPassword)
                    .textFieldStyle(.roundedBorder)
                SecureField("确认新密码", text: $confirmation)
                    .textFieldStyle(.roundedBorder)
                Text("密码策略：8–72 个字符，至少包含一个字母和一个数字。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            if let errorMessage {
                Section { Label(errorMessage, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red) }
            } else if let notice {
                Section { Label(notice, systemImage: "checkmark.circle.fill").foregroundStyle(.green) }
            }

            Section {
                HStack {
                    Spacer()
                    Button("重置密码") { Task { await resetPassword() } }
                        .buttonStyle(.borderedProminent)
                        .disabled(isWorking || newPassword.isEmpty || confirmation.isEmpty)
                }
            }
        }
        .formStyle(.grouped)
    }

    private func resetPassword() async {
        errorMessage = nil
        notice = nil
        guard newPassword == confirmation else {
            errorMessage = "两次输入的新密码不一致"
            return
        }
        isWorking = true
        defer { isWorking = false }
        do {
            try await store.resetOwnPassword(newPassword)
            newPassword = ""
            confirmation = ""
            notice = "密码已重置；当前会话会保留，其他设备需要重新登录。"
        } catch {
            errorMessage = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
        }
    }
}
