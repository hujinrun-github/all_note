import SwiftUI

struct CapabilityDiagnosticView: View {
    @Environment(AppSession.self) private var session
    let reason: String

    var body: some View {
        ContentUnavailableView {
            Label("需要 v2 工作空间", systemImage: "externaldrive.badge.exclamationmark")
        } description: {
            Text(reason)
        } actions: {
            Button("重新检查") { Task { await session.retryCapabilityCheck() } }
                .buttonStyle(.borderedProminent)
            Button("更换服务") { Task { await session.changeService() } }
        }
    }
}
