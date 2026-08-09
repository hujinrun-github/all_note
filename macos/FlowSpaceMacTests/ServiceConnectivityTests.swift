import XCTest
@testable import FlowSpaceMac

@MainActor
final class ServiceConnectivityTests: XCTestCase {
    func testReachabilityPreservesLastSuccessAndOriginalOfflineStart() {
        let connectivity = ServiceConnectivity()
        let success = Date(timeIntervalSince1970: 100)
        let offlineStart = Date(timeIntervalSince1970: 120)

        connectivity.receive(.reachable(success))
        XCTAssertEqual(connectivity.lastSuccessfulResponseAt, success)
        XCTAssertFalse(connectivity.isOffline)

        connectivity.receive(.unreachable(message: "连接失败", at: offlineStart))
        connectivity.receive(.unreachable(message: "仍然失败", at: Date(timeIntervalSince1970: 150)))

        XCTAssertTrue(connectivity.isOffline)
        XCTAssertFalse(connectivity.allowsMutations)
        XCTAssertEqual(
            connectivity.state,
            .offline(message: "仍然失败", since: offlineStart)
        )
    }

    func testSuccessfulResponseLeavesReadOnlyMode() {
        let connectivity = ServiceConnectivity()
        connectivity.receive(.unreachable(message: "连接失败", at: Date(timeIntervalSince1970: 100)))

        connectivity.receive(.reachable(Date(timeIntervalSince1970: 200)))

        XCTAssertFalse(connectivity.isOffline)
        XCTAssertTrue(connectivity.allowsMutations)
    }

    func testRetryCheckPreservesReadOnlyUntilSuccessfulResponse() {
        let connectivity = ServiceConnectivity()
        let offlineStart = Date(timeIntervalSince1970: 100)
        connectivity.receive(.unreachable(message: "连接失败", at: offlineStart))

        connectivity.beginChecking()

        XCTAssertTrue(connectivity.isOffline)
        XCTAssertFalse(connectivity.allowsMutations)
        XCTAssertEqual(connectivity.state, .checking)

        connectivity.receive(.unreachable(message: "重试失败", at: Date(timeIntervalSince1970: 200)))
        XCTAssertEqual(
            connectivity.state,
            .offline(message: "重试失败", since: offlineStart)
        )
    }

    func testAPIClientBlocksMutationBeforeNetworkRequestWhenOffline() async {
        let client = APIClient(
            baseURL: URL(string: "http://127.0.0.1:1")!,
            mutationAllowed: { false }
        )

        do {
            _ = try await client.login(email: "test@example.com", password: "password", rememberMe: false)
            XCTFail("离线写入应在发出网络请求前被阻止")
        } catch let error as APIError {
            XCTAssertEqual(error.code, "offline_read_only")
        } catch {
            XCTFail("收到意外错误：\(error)")
        }
    }
}
