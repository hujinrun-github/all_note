import AppKit
import SwiftUI
import UniformTypeIdentifiers

struct ProfileSettingsView: View {
    @Environment(AppSession.self) private var session
    @State private var displayName = ""
    @State private var locale = "zh-CN"
    @State private var timeZone = "Asia/Shanghai"
    @State private var isImportingAvatar = false
    @State private var isWorking = false
    @State private var errorMessage: String?
    @State private var notice: String?

    let store: WorkspaceStore

    var body: some View {
        Form {
            if store.isLoadingProfile && store.profile == nil {
                Section { ProgressView("正在加载个人资料…") }
            } else if let profile = store.profile {
                Section("账户头像") {
                    HStack(spacing: 18) {
                        avatar(for: profile)
                            .frame(width: 72, height: 72)
                            .clipShape(.circle)
                            .overlay { Circle().stroke(.quaternary) }

                        VStack(alignment: .leading, spacing: 8) {
                            HStack {
                                Button("选择图片…") { isImportingAvatar = true }
                                if profile.avatarURL != nil {
                                    Button("移除头像", role: .destructive) {
                                        Task { await removeAvatar() }
                                    }
                                }
                            }
                            Text("JPEG、PNG 或 WebP，最大 2 MiB；头像属于用户账户，不依赖工作空间对象存储。")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }
                    .padding(.vertical, 4)
                }

                Section("个人资料") {
                    TextField("显示名称", text: $displayName)
                        .textFieldStyle(.roundedBorder)
                    LabeledContent("邮箱", value: profile.email)
                    Picker("界面语言", selection: $locale) {
                        Text("简体中文").tag("zh-CN")
                        Text("English").tag("en-US")
                        Text("日本語").tag("ja-JP")
                    }
                    TextField("时区", text: $timeZone, prompt: Text("Asia/Shanghai"))
                        .textFieldStyle(.roundedBorder)
                    Text("使用 IANA 时区名称，例如 Asia/Shanghai、Asia/Tokyo 或 America/Los_Angeles。")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }

                messageSection

                Section {
                    HStack {
                        Spacer()
                        Button("保存个人资料") { Task { await save() } }
                            .buttonStyle(.borderedProminent)
                            .disabled(isWorking || displayName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    }
                }
            } else if let errorMessage {
                ContentUnavailableView(
                    "无法加载个人资料",
                    systemImage: "person.crop.circle.badge.exclamationmark",
                    description: Text(errorMessage)
                )
                Button("重试") { Task { await load() } }
            }
        }
        .formStyle(.grouped)
        .task { if store.profile == nil { await load() } else { applyProfile() } }
        .fileImporter(
            isPresented: $isImportingAvatar,
            allowedContentTypes: [.jpeg, .png, UTType(filenameExtension: "webp") ?? .image],
            allowsMultipleSelection: false
        ) { result in
            guard case .success(let urls) = result, let url = urls.first else {
                if case .failure(let error) = result { errorMessage = error.localizedDescription }
                return
            }
            Task { await uploadAvatar(from: url) }
        }
    }

    @ViewBuilder
    private func avatar(for profile: UserProfile) -> some View {
        if let data = store.profileAvatarData, let image = NSImage(data: data) {
            Image(nsImage: image)
                .resizable()
                .scaledToFill()
                .accessibilityLabel("当前头像")
        } else {
            ZStack {
                Color.accentColor.opacity(0.15)
                Text(String((profile.displayName.first ?? profile.email.first ?? "?")).uppercased())
                    .font(.title.bold())
                    .foregroundStyle(.tint)
            }
            .accessibilityLabel("默认头像")
        }
    }

    @ViewBuilder
    private var messageSection: some View {
        if let errorMessage {
            Section { Label(errorMessage, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red) }
        } else if let notice {
            Section { Label(notice, systemImage: "checkmark.circle.fill").foregroundStyle(.green) }
        }
    }

    private func load() async {
        errorMessage = nil
        do {
            try await store.loadProfile()
            applyProfile()
        } catch {
            errorMessage = readable(error)
        }
    }

    private func applyProfile() {
        guard let profile = store.profile else { return }
        displayName = profile.displayName
        locale = profile.locale
        timeZone = profile.timeZone
    }

    private func save() async {
        await perform(notice: "个人资料已保存") {
            try await store.updateProfile(displayName: displayName, locale: locale, timeZone: timeZone)
            try await session.refreshCurrentUser()
            applyProfile()
        }
    }

    private func uploadAvatar(from url: URL) async {
        await perform(notice: "头像已更新") {
            let hasAccess = url.startAccessingSecurityScopedResource()
            defer { if hasAccess { url.stopAccessingSecurityScopedResource() } }
            let values = try url.resourceValues(forKeys: [.contentTypeKey])
            let mimeType = values.contentType?.preferredMIMEType ?? ""
            let data = try await Task.detached { try Data(contentsOf: url) }.value
            try await store.uploadProfileAvatar(data: data, mimeType: mimeType)
            try await session.refreshCurrentUser()
        }
    }

    private func removeAvatar() async {
        await perform(notice: "头像已移除") {
            try await store.deleteProfileAvatar()
            try await session.refreshCurrentUser()
        }
    }

    private func perform(notice: String, operation: () async throws -> Void) async {
        isWorking = true
        errorMessage = nil
        self.notice = nil
        defer { isWorking = false }
        do {
            try await operation()
            self.notice = notice
        } catch {
            errorMessage = readable(error)
        }
    }

    private func readable(_ error: Error) -> String {
        (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
    }
}
