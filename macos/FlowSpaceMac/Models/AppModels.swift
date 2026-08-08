import Foundation

struct AccountUser: Codable, Equatable, Identifiable, Sendable {
    let id: String
    let email: String
    let displayName: String
    let mustChangePassword: Bool
    let defaultWorkspaceID: String
    let role: String
    let status: String
}

struct AuthWorkspace: Codable, Equatable, Sendable {
    let id: String
    let name: String
    let ownerUserID: String
}

struct LoginPayload: Codable, Equatable, Sendable {
    let user: AccountUser
    let workspace: AuthWorkspace
}

struct CurrentUserPayload: Codable, Equatable, Sendable {
    let user: AccountUser
    let workspace: AuthWorkspace
    let mustChangePassword: Bool
    let avatarURL: String?
}

struct TaskDomainCapabilities: Codable, Equatable, Sendable {
    let modelVersion: String
    let available: Bool
    let error: CapabilityError?
}

struct CapabilityError: Codable, Equatable, Sendable {
    let code: String
    let message: String
    let retryable: Bool
}

struct HealthResponse: Codable, Equatable, Sendable {
    let status: String
}

enum ServerAddress {
    static func normalize(_ rawValue: String) throws -> URL {
        let trimmed = rawValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            throw ValidationError("请输入服务地址")
        }

        let value = trimmed.contains("://") ? trimmed : "http://\(trimmed)"
        guard var components = URLComponents(string: value),
              let scheme = components.scheme?.lowercased(),
              scheme == "http" || scheme == "https",
              let host = components.host,
              !host.isEmpty else {
            throw ValidationError("服务地址必须是有效的 HTTP 或 HTTPS 地址")
        }

        components.scheme = scheme
        components.query = nil
        components.fragment = nil
        if components.path == "/" {
            components.path = ""
        } else {
            components.path = components.path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
            if !components.path.isEmpty {
                components.path = "/" + components.path
            }
        }

        guard let url = components.url else {
            throw ValidationError("无法解析服务地址")
        }
        return url
    }

    static func isInsecureRemote(_ url: URL) -> Bool {
        guard url.scheme == "http", let host = url.host?.lowercased() else { return false }
        return host != "localhost" && host != "127.0.0.1" && host != "::1"
    }
}

struct ValidationError: LocalizedError, Equatable {
    let message: String

    init(_ message: String) {
        self.message = message
    }

    var errorDescription: String? { message }
}
