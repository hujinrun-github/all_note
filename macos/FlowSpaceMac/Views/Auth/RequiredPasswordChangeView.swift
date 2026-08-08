import SwiftUI

struct RequiredPasswordChangeView: View {
    @Environment(AppSession.self) private var session
    @State private var password = ""
    @State private var confirmation = ""

    private var validationMessage: String? {
        guard !password.isEmpty || !confirmation.isEmpty else { return nil }
        guard password.count >= 8, password.range(of: "[A-Za-z]", options: .regularExpression) != nil,
              password.range(of: "[0-9]", options: .regularExpression) != nil else {
            return "密码至少 8 位，并同时包含字母和数字"
        }
        return password == confirmation ? nil : "两次输入的密码不一致"
    }

    var body: some View {
        VStack(spacing: 20) {
            Image(systemName: "lock.rotation")
                .font(.system(size: 40))
                .foregroundStyle(.tint)
            Text("首次登录需要修改密码")
                .font(.title.weight(.semibold))
            Text("完成修改后才能进入工作区")
                .foregroundStyle(.secondary)

            SecureField("新密码", text: $password)
            SecureField("再次输入新密码", text: $confirmation)
                .onSubmit { Task { await submit() } }

            if let message = validationMessage {
                Text(message).foregroundStyle(.red).font(.callout)
            } else if let error = session.errorMessage {
                Text(error).foregroundStyle(.red).font(.callout)
            }

            Button("保存并继续") { Task { await submit() } }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .disabled(session.isWorking || password.isEmpty || validationMessage != nil)
        }
        .frame(width: 380)
        .padding(48)
    }

    private func submit() async {
        guard validationMessage == nil else { return }
        await session.resetRequiredPassword(password)
    }
}

