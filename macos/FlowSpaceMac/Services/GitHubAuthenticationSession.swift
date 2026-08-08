import AppKit
import AuthenticationServices
import CryptoKit
import Foundation
import Security

struct NativeOAuthPKCE: Equatable, Sendable {
    let verifier: String
    let challenge: String

    static func generate() throws -> NativeOAuthPKCE {
        var bytes = [UInt8](repeating: 0, count: 32)
        let status = bytes.withUnsafeMutableBytes { buffer in
            SecRandomCopyBytes(kSecRandomDefault, buffer.count, buffer.baseAddress!)
        }
        guard status == errSecSuccess else {
            throw ValidationError("无法创建安全的登录请求")
        }
        let verifier = Data(bytes).base64URLEncodedString()
        let digest = SHA256.hash(data: Data(verifier.utf8))
        return NativeOAuthPKCE(
            verifier: verifier,
            challenge: Data(digest).base64URLEncodedString()
        )
    }
}

enum GitHubNativeOAuthCallback {
    static let url = URL(string: "flowspace-mac://oauth/callback")!

    static func exchangeCode(from callbackURL: URL) throws -> String {
        guard callbackURL.scheme == url.scheme,
              callbackURL.host == url.host,
              callbackURL.path == url.path,
              let components = URLComponents(url: callbackURL, resolvingAgainstBaseURL: false) else {
            throw ValidationError("GitHub 返回了无效的登录地址")
        }
        let queryItems = components.queryItems ?? []
        if let errorCode = queryItems.first(where: { $0.name == "error" })?.value, !errorCode.isEmpty {
            throw ValidationError(message(for: errorCode))
        }
        guard let code = queryItems.first(where: { $0.name == "code" })?.value, !code.isEmpty else {
            throw ValidationError("GitHub 登录结果缺少授权码")
        }
        return code
    }

    private static func message(for code: String) -> String {
        switch code {
        case "github_disabled": "当前服务未启用 GitHub 登录"
        case "github_no_verified_email": "GitHub 账号没有可用的已验证邮箱"
        case "github_auto_create_disabled": "当前 GitHub 账号尚未绑定，且服务不允许自动创建账号"
        case "github_pkce_invalid": "GitHub 登录安全校验失败，请重试"
        case "github_native_unavailable": "当前服务暂不支持 macOS GitHub 登录"
        case "github_exchange_failed": "GitHub 授权失败，请重试"
        case "github_profile_failed": "无法读取 GitHub 账号信息，请重试"
        case "github_create_user_failed": "无法创建或绑定 FlowSpace 账号"
        default: "GitHub 登录失败，请重试"
        }
    }
}

@MainActor
final class GitHubAuthenticationSession: NSObject, ASWebAuthenticationPresentationContextProviding {
    private var activeSession: ASWebAuthenticationSession?

    func authenticate(startURL: URL) async throws -> URL {
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                let session = ASWebAuthenticationSession(
                    url: startURL,
                    callbackURLScheme: GitHubNativeOAuthCallback.url.scheme
                ) { [weak self] callbackURL, error in
                    Task { @MainActor in
                        self?.activeSession = nil
                        if let error {
                            continuation.resume(throwing: error)
                        } else if let callbackURL {
                            continuation.resume(returning: callbackURL)
                        } else {
                            continuation.resume(throwing: ValidationError("GitHub 没有返回登录结果"))
                        }
                    }
                }
                session.presentationContextProvider = self
                session.prefersEphemeralWebBrowserSession = false
                activeSession = session
                guard session.start() else {
                    activeSession = nil
                    continuation.resume(throwing: ValidationError("无法打开 GitHub 登录窗口"))
                    return
                }
            }
        } onCancel: {
            Task { @MainActor [weak self] in self?.activeSession?.cancel() }
        }
    }

    func presentationAnchor(for session: ASWebAuthenticationSession) -> ASPresentationAnchor {
        NSApp.keyWindow ?? NSApp.windows.first ?? ASPresentationAnchor()
    }

    static func isCanceled(_ error: Error) -> Bool {
        let nsError = error as NSError
        return nsError.domain == ASWebAuthenticationSessionErrorDomain &&
            nsError.code == ASWebAuthenticationSessionError.Code.canceledLogin.rawValue
    }
}

private extension Data {
    func base64URLEncodedString() -> String {
        base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }
}
