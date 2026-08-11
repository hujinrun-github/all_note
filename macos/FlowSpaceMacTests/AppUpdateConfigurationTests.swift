import Foundation
import XCTest
@testable import FlowSpaceMac

final class AppUpdateConfigurationTests: XCTestCase {
    func testEmptyUpdateConfigurationIsAnExplicitDevelopmentState() {
        let configuration = AppUpdateConfiguration(infoDictionary: [:])

        XCTAssertEqual(configuration.state, .unconfigured)
        XCTAssertFalse(configuration.isConfigured)
    }

    func testAcceptsHTTPSFeedAndEd25519PublicKey() {
        let configuration = AppUpdateConfiguration(infoDictionary: [
            "SUFeedURL": "https://updates.flowspace.example/appcast.xml",
            "SUPublicEDKey": Data(repeating: 7, count: 32).base64EncodedString(),
        ])

        XCTAssertEqual(
            configuration.state,
            .configured(feedURL: URL(string: "https://updates.flowspace.example/appcast.xml")!)
        )
        XCTAssertTrue(configuration.isConfigured)
    }

    func testRejectsInsecureFeedAndMalformedKey() {
        let insecure = AppUpdateConfiguration(infoDictionary: [
            "SUFeedURL": "http://updates.flowspace.example/appcast.xml",
            "SUPublicEDKey": Data(repeating: 7, count: 32).base64EncodedString(),
        ])
        let malformedKey = AppUpdateConfiguration(infoDictionary: [
            "SUFeedURL": "https://updates.flowspace.example/appcast.xml",
            "SUPublicEDKey": "not-a-public-key",
        ])

        XCTAssertEqual(insecure.state, .invalid("更新源必须是有效的 HTTPS 地址。"))
        XCTAssertEqual(malformedKey.state, .invalid("更新签名公钥必须是有效的 Ed25519 公钥。"))
    }
}
