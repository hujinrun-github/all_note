import SwiftUI

struct ConnectionView: View {
    @Environment(AppSession.self) private var session
    @State private var address = "http://127.0.0.1:4100"
    @State private var normalizedURL: URL?

    var body: some View {
        VStack(spacing: 24) {
            Spacer()
            Image(systemName: "square.stack.3d.up.fill")
                .font(.system(size: 44, weight: .semibold))
                .foregroundStyle(.tint)
                .accessibilityHidden(true)

            VStack(spacing: 8) {
                Text("连接 FlowSpace")
                    .font(.largeTitle.weight(.semibold))
                Text("输入你的 FlowSpace Web 服务地址")
                    .foregroundStyle(.secondary)
            }

            VStack(alignment: .leading, spacing: 10) {
                TextField("https://flowspace.example.com", text: $address)
                    .textFieldStyle(.roundedBorder)
                    .onSubmit { Task { await connect() } }
                    .accessibilityLabel("服务地址")

                if let normalizedURL, ServerAddress.isInsecureRemote(normalizedURL) {
                    Label("该地址使用未加密 HTTP，登录信息可能被同一网络中的其他设备读取。", systemImage: "exclamationmark.triangle.fill")
                        .font(.caption)
                        .foregroundStyle(.orange)
                }

                if let error = session.errorMessage {
                    Label(error, systemImage: "exclamationmark.circle")
                        .font(.callout)
                        .foregroundStyle(.red)
                        .accessibilityIdentifier("connection-error")
                }

                Button {
                    Task { await connect() }
                } label: {
                    if session.isWorking {
                        ProgressView().controlSize(.small)
                    } else {
                        Text("测试并连接")
                    }
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .disabled(session.isWorking || address.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
            .frame(width: 420)
            Spacer()
        }
        .padding(40)
        .onChange(of: address, initial: true) {
            normalizedURL = try? ServerAddress.normalize(address)
        }
    }

    private func connect() async {
        await session.connect(address)
    }
}

