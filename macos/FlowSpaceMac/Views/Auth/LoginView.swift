import SwiftUI

struct LoginView: View {
    @Environment(AppSession.self) private var session
    @State private var email = ""
    @State private var password = ""
    @State private var rememberMe = true

    var body: some View {
        HStack(spacing: 0) {
            LoginPreview()
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .background(.tint.opacity(0.08))

            VStack(alignment: .leading, spacing: 20) {
                Spacer()
                VStack(alignment: .leading, spacing: 6) {
                    Text("登录工作台")
                        .font(.largeTitle.weight(.semibold))
                    Text(session.baseURL?.host ?? "FlowSpace")
                        .foregroundStyle(.secondary)
                }

                Form {
                    TextField("邮箱", text: $email)
                        .textContentType(.emailAddress)
                        .accessibilityIdentifier("login-email")
                    SecureField("密码", text: $password)
                        .textContentType(.password)
                        .onSubmit { Task { await submit() } }
                        .accessibilityIdentifier("login-password")
                    Toggle("记住我", isOn: $rememberMe)
                }
                .formStyle(.grouped)
                .scrollDisabled(true)
                .frame(height: 150)

                if let error = session.errorMessage {
                    Label(error, systemImage: "exclamationmark.circle")
                        .foregroundStyle(.red)
                        .font(.callout)
                        .accessibilityIdentifier("login-error")
                }

                Button {
                    Task { await submit() }
                } label: {
                    HStack {
                        Spacer()
                        if session.isWorking { ProgressView().controlSize(.small) }
                        else { Text("登录") }
                        Spacer()
                    }
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .disabled(session.isWorking || email.isEmpty || password.isEmpty)

                if session.isGitHubLoginAvailable {
                    HStack {
                        Rectangle().fill(.separator).frame(height: 1)
                        Text("或").font(.caption).foregroundStyle(.secondary)
                        Rectangle().fill(.separator).frame(height: 1)
                    }

                    Button {
                        Task { await session.loginWithGitHub() }
                    } label: {
                        HStack {
                            Spacer()
                            Label("使用 GitHub 登录", systemImage: "chevron.left.forwardslash.chevron.right")
                            Spacer()
                        }
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.large)
                    .disabled(session.isWorking)
                    .accessibilityIdentifier("login-github")
                }

                Button("更换服务") { Task { await session.changeService() } }
                    .buttonStyle(.link)
                Spacer()
            }
            .padding(48)
            .frame(width: 460)
        }
        .task { await session.loadAuthProviders() }
    }

    private func submit() async {
        await session.login(email: email, password: password, rememberMe: rememberMe)
    }
}

private struct LoginPreview: View {
    var body: some View {
        VStack(alignment: .leading, spacing: 24) {
            Label("FlowSpace", systemImage: "square.stack.3d.up.fill")
                .font(.title2.weight(.semibold))
            Spacer()
            Text("把任务、项目和日程\n放回同一个工作台")
                .font(.system(size: 36, weight: .semibold, design: .rounded))
            Text("原生 macOS 工作流，连接现有 Web v2 数据。")
                .foregroundStyle(.secondary)
            Spacer()
            VStack(alignment: .leading, spacing: 12) {
                PreviewLine(icon: "checkmark.circle.fill", title: "完成发布说明", tint: .green)
                PreviewLine(icon: "calendar", title: "11:00 单词学习", tint: .orange)
                PreviewLine(icon: "folder.fill", title: "整理日语学习项目", tint: .blue)
            }
            .padding(20)
            .background(.regularMaterial, in: .rect(cornerRadius: 16))
        }
        .padding(48)
    }
}

private struct PreviewLine: View {
    let icon: String
    let title: String
    let tint: Color

    var body: some View {
        Label(title, systemImage: icon)
            .symbolRenderingMode(.hierarchical)
            .foregroundStyle(tint)
    }
}
