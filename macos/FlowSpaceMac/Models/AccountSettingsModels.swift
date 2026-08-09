import Foundation

enum AccountRole: String, Codable, CaseIterable, Identifiable, Sendable {
    case admin
    case user

    var id: Self { self }
    var title: String { self == .admin ? "管理员" : "普通用户" }
}

enum AccountStatus: String, Codable, Sendable {
    case active
    case disabled

    var title: String { self == .active ? "启用" : "禁用" }
}

struct UserProfile: Codable, Equatable, Sendable {
    let userID: String
    let email: String
    let displayName: String
    let locale: String
    let timeZone: String
    let avatarURL: String?
    let updatedAt: Int64
}

struct UpdateUserProfileInput: Encodable, Equatable, Sendable {
    let displayName: String
    let locale: String
    let timeZone: String
}

struct AvatarUploadResult: Decodable, Equatable, Sendable {
    let avatarURL: String
    let sha256: String
    let width: Int
    let height: Int
}

struct AccountPagination: Codable, Equatable, Sendable {
    let page: Int
    let pageSize: Int
    let total: Int
}

struct AdminUserPage: Equatable, Sendable {
    let users: [AccountUser]
    let pagination: AccountPagination
}

struct CreateAdminUserInput: Encodable, Equatable, Sendable {
    let email: String
    let displayName: String
    let temporaryPassword: String
    let role: AccountRole
}

struct UpdateAdminUserInput: Encodable, Equatable, Sendable {
    let email: String
    let displayName: String
    let role: AccountRole
}

enum PasswordPolicy {
    static func validate(_ password: String) throws {
        guard (8...72).contains(password.count),
              password.range(of: "[A-Za-z]", options: .regularExpression) != nil,
              password.range(of: "[0-9]", options: .regularExpression) != nil else {
            throw ValidationError("密码需要为 8–72 个字符，并同时包含字母和数字")
        }
    }
}

extension AccountUser {
    var accountRole: AccountRole { AccountRole(rawValue: role) ?? .user }
    var accountStatus: AccountStatus { AccountStatus(rawValue: status) ?? .disabled }
    var initials: String {
        String((displayName.trimmingCharacters(in: .whitespacesAndNewlines).first
                ?? email.trimmingCharacters(in: .whitespacesAndNewlines).first
                ?? "?")).uppercased()
    }
}
