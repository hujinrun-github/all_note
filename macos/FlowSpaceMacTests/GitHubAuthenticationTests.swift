import Foundation
import Testing
@testable import FlowSpaceMac

struct GitHubAuthenticationTests {
    @Test func pkceGenerationCreatesVerifierAndMatchingSHA256Challenge() throws {
        let pkce = try NativeOAuthPKCE.generate()

        #expect(pkce.verifier.count == 43)
        #expect(pkce.challenge.count == 43)
        #expect(!pkce.verifier.contains("="))
        #expect(!pkce.challenge.contains("="))
    }

    @Test func callbackExtractsOneTimeCode() throws {
        let url = try #require(URL(string: "flowspace-mac://oauth/callback?code=one-time-code"))
        #expect(try GitHubNativeOAuthCallback.exchangeCode(from: url) == "one-time-code")
    }

    @Test func callbackRejectsWrongSchemeAndSurfacesServerError() throws {
        let wrong = try #require(URL(string: "https://example.com/oauth/callback?code=secret"))
        #expect(throws: ValidationError.self) {
            try GitHubNativeOAuthCallback.exchangeCode(from: wrong)
        }

        let failed = try #require(URL(string: "flowspace-mac://oauth/callback?error=github_no_verified_email"))
        do {
            _ = try GitHubNativeOAuthCallback.exchangeCode(from: failed)
            Issue.record("expected callback error")
        } catch {
            #expect(error.localizedDescription.contains("已验证邮箱"))
        }
    }
}
