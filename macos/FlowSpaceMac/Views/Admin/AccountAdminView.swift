import SwiftUI

struct AccountAdminView: View {
    @Environment(AppSession.self) private var session
    @State private var query = ""
    @State private var page = 1
    @State private var presentation: AccountAdminPresentation?
    @State private var errorMessage: String?
    @State private var notice: String?

    let store: WorkspaceStore

    private var searchKey: SearchKey { SearchKey(query: query, page: page) }
    private var totalPages: Int {
        max(1, Int(ceil(Double(store.adminPagination.total) / Double(store.adminPagination.pageSize))))
    }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()

            if store.isLoadingAdminUsers && store.adminUsers.isEmpty {
                Spacer()
                ProgressView("正在加载账号…")
                Spacer()
            } else if store.adminUsers.isEmpty {
                ContentUnavailableView(
                    query.isEmpty ? "还没有可管理的账号" : "没有匹配的账号",
                    systemImage: "person.2.slash",
                    description: Text(query.isEmpty ? "创建账号后会显示在这里。" : "换一个邮箱或昵称再试。")
                )
            } else {
                accountTable
            }

            Divider()
            footer
        }
        .navigationTitle("账号管理")
        .task(id: searchKey) {
            do {
                if !query.isEmpty { try await Task.sleep(for: .milliseconds(300)) }
                try await reload()
            } catch is CancellationError {
                return
            } catch {
                handle(error)
            }
        }
        .onChange(of: query) { _, _ in page = 1 }
        .sheet(item: $presentation) { presentation in
            switch presentation {
            case .create:
                CreateAccountSheet { input in
                    try await complete("账号已创建，用户首次登录后需要修改临时密码。") {
                        try await store.createAdminUser(input)
                    }
                }
            case .edit(let user):
                EditAccountSheet(user: user) { input in
                    try await complete("账号资料与角色已更新。") {
                        try await store.updateAdminUser(user, input: input)
                        if user.id == session.currentUser?.user.id, input.role == user.accountRole {
                            try await session.refreshCurrentUser()
                        }
                    }
                }
            case .resetPassword(let user):
                ResetAccountPasswordSheet(user: user) { password in
                    try await complete("临时密码已设置，目标用户下次登录后需要修改密码。") {
                        try await store.resetAdminPassword(for: user, temporaryPassword: password)
                    }
                }
            case .changeStatus(let user):
                ChangeAccountStatusSheet(user: user) {
                    try await complete("账号状态已更新。") {
                        let next: AccountStatus = user.accountStatus == .active ? .disabled : .active
                        try await store.setAdminUserStatus(user, status: next)
                    }
                }
            }
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 3) {
                    Text("用户与权限").font(.title2.bold())
                    Text("集中管理登录身份、角色与账号状态；修改会立即生效。")
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Button("创建账号", systemImage: "person.badge.plus") { presentation = .create }
                    .buttonStyle(.borderedProminent)
            }

            HStack {
                TextField("搜索邮箱或昵称", text: $query)
                    .textFieldStyle(.roundedBorder)
                    .frame(maxWidth: 320)
                if store.isLoadingAdminUsers { ProgressView().controlSize(.small) }
                Spacer()
                metric("总数", value: store.adminPagination.total)
                metric("本页启用", value: store.adminUsers.count { $0.accountStatus == .active })
                metric("本页管理员", value: store.adminUsers.count { $0.accountRole == .admin })
            }

            if let errorMessage {
                Label(errorMessage, systemImage: "exclamationmark.triangle.fill")
                    .foregroundStyle(.red)
            } else if let notice {
                Label(notice, systemImage: "checkmark.circle.fill")
                    .foregroundStyle(.green)
            }
        }
        .padding(20)
    }

    private var accountTable: some View {
        Table(store.adminUsers) {
            TableColumn("用户") { user in
                HStack(spacing: 9) {
                    Text(user.initials)
                        .font(.headline)
                        .frame(width: 30, height: 30)
                        .background(.tint.opacity(0.14), in: .circle)
                    VStack(alignment: .leading, spacing: 1) {
                        Text(user.displayName).lineLimit(1)
                        if user.mustChangePassword {
                            Text("首次登录需改密").font(.caption2).foregroundStyle(.orange)
                        }
                    }
                }
            }
            .width(min: 160, ideal: 220)

            TableColumn("邮箱", value: \.email)
                .width(min: 180, ideal: 260)

            TableColumn("角色") { user in
                Text(user.accountRole.title)
            }
            .width(90)

            TableColumn("状态") { user in
                Label(
                    user.accountStatus.title,
                    systemImage: user.accountStatus == .active ? "checkmark.circle.fill" : "minus.circle.fill"
                )
                .foregroundStyle(user.accountStatus == .active ? .green : .secondary)
            }
            .width(90)

            TableColumn("操作") { user in
                Menu("操作", systemImage: "ellipsis.circle") {
                    Button("编辑资料与角色", systemImage: "pencil") { presentation = .edit(user) }
                    Button("设置临时密码", systemImage: "key") { presentation = .resetPassword(user) }
                    Divider()
                    Button(
                        user.accountStatus == .active ? "禁用账号" : "启用账号",
                        systemImage: user.accountStatus == .active ? "person.crop.circle.badge.xmark" : "person.crop.circle.badge.checkmark"
                    ) { presentation = .changeStatus(user) }
                }
                .menuStyle(.borderlessButton)
            }
            .width(100)
        }
    }

    private var footer: some View {
        HStack {
            Text("共 \(store.adminPagination.total) 个账号")
                .foregroundStyle(.secondary)
            Spacer()
            Button("上一页", systemImage: "chevron.left") { page -= 1 }
                .labelStyle(.iconOnly)
                .disabled(page <= 1 || store.isLoadingAdminUsers)
            Text("\(page) / \(totalPages)")
                .monospacedDigit()
                .frame(minWidth: 64)
            Button("下一页", systemImage: "chevron.right") { page += 1 }
                .labelStyle(.iconOnly)
                .disabled(page >= totalPages || store.isLoadingAdminUsers)
            Button("刷新", systemImage: "arrow.clockwise") {
                Task {
                    do { try await reload() }
                    catch { handle(error) }
                }
            }
            .labelStyle(.iconOnly)
            .disabled(store.isLoadingAdminUsers)
        }
        .padding(.horizontal, 20)
        .padding(.vertical, 12)
    }

    private func metric(_ label: String, value: Int) -> some View {
        HStack(spacing: 5) {
            Text(label).foregroundStyle(.secondary)
            Text(value, format: .number).fontWeight(.semibold).monospacedDigit()
        }
    }

    private func reload() async throws {
        errorMessage = nil
        try await store.loadAdminUsers(page: page, query: query)
    }

    private func complete(_ message: String, operation: () async throws -> Void) async throws {
        do {
            try await operation()
            try await reload()
            notice = message
            errorMessage = nil
        } catch {
            if let apiError = error as? APIError, apiError.isUnauthorized {
                session.handleUnauthorized()
            }
            throw error
        }
    }

    private func handle(_ error: Error) {
        if let apiError = error as? APIError, apiError.isUnauthorized {
            session.handleUnauthorized()
        }
        errorMessage = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
        notice = nil
    }
}

private struct SearchKey: Hashable {
    let query: String
    let page: Int
}

private enum AccountAdminPresentation: Identifiable {
    case create
    case edit(AccountUser)
    case resetPassword(AccountUser)
    case changeStatus(AccountUser)

    var id: String {
        switch self {
        case .create: "create"
        case .edit(let user): "edit-\(user.id)"
        case .resetPassword(let user): "password-\(user.id)"
        case .changeStatus(let user): "status-\(user.id)"
        }
    }
}

private struct CreateAccountSheet: View {
    @Environment(\.dismiss) private var dismiss
    @State private var email = ""
    @State private var displayName = ""
    @State private var temporaryPassword = ""
    @State private var role: AccountRole = .user
    @State private var isWorking = false
    @State private var errorMessage: String?

    let submit: (CreateAdminUserInput) async throws -> Void

    var body: some View {
        AccountSheetContainer(
            title: "创建账号",
            subtitle: "临时密码只发送给目标用户，首次登录后必须修改。",
            isWorking: isWorking,
            canSubmit: !email.isEmpty && !displayName.isEmpty && !temporaryPassword.isEmpty,
            errorMessage: errorMessage,
            submitTitle: "创建账号",
            submit: { Task { await save() } },
            cancel: { dismiss() }
        ) {
            TextField("邮箱", text: $email).textContentType(.emailAddress)
            TextField("显示名称", text: $displayName)
            SecureField("临时密码", text: $temporaryPassword)
            Picker("初始角色", selection: $role) {
                ForEach(AccountRole.allCases) { Text($0.title).tag($0) }
            }
            Text("密码需要为 8–72 个字符，并同时包含字母和数字。")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    private func save() async {
        await run {
            try PasswordPolicy.validate(temporaryPassword)
            try await submit(CreateAdminUserInput(
                email: email,
                displayName: displayName,
                temporaryPassword: temporaryPassword,
                role: role
            ))
            temporaryPassword = ""
        }
    }

    private func run(_ operation: () async throws -> Void) async {
        isWorking = true; errorMessage = nil; defer { isWorking = false }
        do { try await operation(); dismiss() }
        catch { errorMessage = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription }
    }
}

private struct EditAccountSheet: View {
    @Environment(\.dismiss) private var dismiss
    @State private var email: String
    @State private var displayName: String
    @State private var role: AccountRole
    @State private var isWorking = false
    @State private var errorMessage: String?

    let user: AccountUser
    let submit: (UpdateAdminUserInput) async throws -> Void

    init(user: AccountUser, submit: @escaping (UpdateAdminUserInput) async throws -> Void) {
        self.user = user
        self.submit = submit
        _email = State(initialValue: user.email)
        _displayName = State(initialValue: user.displayName)
        _role = State(initialValue: user.accountRole)
    }

    var body: some View {
        AccountSheetContainer(
            title: "编辑账号",
            subtitle: "角色修改会立即生效，并撤销目标用户的其他登录会话。",
            isWorking: isWorking,
            canSubmit: !email.isEmpty && !displayName.isEmpty,
            errorMessage: errorMessage,
            submitTitle: "保存修改",
            submit: { Task { await save() } },
            cancel: { dismiss() }
        ) {
            TextField("邮箱", text: $email).textContentType(.emailAddress)
            TextField("显示名称", text: $displayName)
            Picker("角色", selection: $role) {
                ForEach(AccountRole.allCases) { Text($0.title).tag($0) }
            }
        }
    }

    private func save() async {
        isWorking = true; errorMessage = nil; defer { isWorking = false }
        do {
            try await submit(UpdateAdminUserInput(email: email, displayName: displayName, role: role))
            dismiss()
        } catch {
            errorMessage = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
        }
    }
}

private struct ResetAccountPasswordSheet: View {
    @Environment(\.dismiss) private var dismiss
    @State private var password = ""
    @State private var confirmation = ""
    @State private var isWorking = false
    @State private var errorMessage: String?

    let user: AccountUser
    let submit: (String) async throws -> Void

    var body: some View {
        AccountSheetContainer(
            title: "设置临时密码",
            subtitle: "为 \(user.displayName) 设置临时密码；现有会话将被撤销。",
            isWorking: isWorking,
            canSubmit: !password.isEmpty && !confirmation.isEmpty,
            errorMessage: errorMessage,
            submitTitle: "设置密码",
            submit: { Task { await save() } },
            cancel: { dismiss() }
        ) {
            SecureField("临时密码", text: $password)
            SecureField("确认临时密码", text: $confirmation)
            Text("密码不会写入偏好设置或日志。")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    private func save() async {
        errorMessage = nil
        guard password == confirmation else { errorMessage = "两次输入的临时密码不一致"; return }
        isWorking = true; defer { isWorking = false }
        do {
            try PasswordPolicy.validate(password)
            try await submit(password)
            password = ""; confirmation = ""
            dismiss()
        } catch {
            errorMessage = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
        }
    }
}

private struct ChangeAccountStatusSheet: View {
    @Environment(\.dismiss) private var dismiss
    @State private var isWorking = false
    @State private var errorMessage: String?

    let user: AccountUser
    let submit: () async throws -> Void

    private var disabling: Bool { user.accountStatus == .active }

    var body: some View {
        AccountSheetContainer(
            title: disabling ? "禁用账号？" : "启用账号？",
            subtitle: disabling
                ? "\(user.displayName) 将无法继续登录，现有会话也会被撤销。"
                : "\(user.displayName) 将恢复登录权限。",
            isWorking: isWorking,
            canSubmit: true,
            errorMessage: errorMessage,
            submitTitle: disabling ? "禁用账号" : "启用账号",
            submitRole: disabling ? .destructive : nil,
            submit: { Task { await save() } },
            cancel: { dismiss() }
        ) {
            if disabling && user.accountRole == .admin {
                Label("至少必须保留一个启用的管理员；服务端会再次校验。", systemImage: "exclamationmark.shield")
                    .foregroundStyle(.orange)
            }
        }
    }

    private func save() async {
        isWorking = true; errorMessage = nil; defer { isWorking = false }
        do { try await submit(); dismiss() }
        catch { errorMessage = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription }
    }
}

private struct AccountSheetContainer<Content: View>: View {
    let title: String
    let subtitle: String
    let isWorking: Bool
    let canSubmit: Bool
    let errorMessage: String?
    let submitTitle: String
    var submitRole: ButtonRole?
    let submit: () -> Void
    let cancel: () -> Void
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            VStack(alignment: .leading, spacing: 4) {
                Text(title).font(.title2.bold())
                Text(subtitle).foregroundStyle(.secondary)
            }
            Form { content }.formStyle(.grouped)
            if let errorMessage {
                Label(errorMessage, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
            }
            HStack {
                Spacer()
                Button("取消", action: cancel).keyboardShortcut(.cancelAction)
                Button(submitTitle, role: submitRole, action: submit)
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.defaultAction)
                    .disabled(isWorking || !canSubmit)
            }
        }
        .padding(24)
        .frame(width: 480)
    }
}
