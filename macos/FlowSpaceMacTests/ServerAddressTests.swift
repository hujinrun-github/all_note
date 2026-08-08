import Foundation
import XCTest
@testable import FlowSpaceMac

final class ServerAddressTests: XCTestCase {
    func testNormalizesAddressWithoutSchemeAndRemovesTrailingSlash() throws {
        let url = try ServerAddress.normalize(" 127.0.0.1:4100/ ")
        XCTAssertEqual(url.absoluteString, "http://127.0.0.1:4100")
    }

    func testRejectsUnsupportedScheme() {
        XCTAssertThrowsError(try ServerAddress.normalize("file:///tmp/flowspace"))
    }

    func testWarnsOnlyForInsecureRemoteHTTP() throws {
        XCTAssertFalse(ServerAddress.isInsecureRemote(try ServerAddress.normalize("http://localhost:4100")))
        XCTAssertTrue(ServerAddress.isInsecureRemote(try ServerAddress.normalize("http://192.168.1.13:4100")))
        XCTAssertFalse(ServerAddress.isInsecureRemote(try ServerAddress.normalize("https://flowspace.example.com")))
    }
}

