import XCTest
@testable import FlowSpaceMac

@MainActor
final class AppSessionOfflineRestoreTests: XCTestCase {
    func testColdLaunchRestoresVerifiedCacheInOfflineReadOnlyMode() async throws {
        let serviceURL = URL(string: "http://127.0.0.1:1")!
        let suiteName = "AppSessionOfflineRestoreTests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suiteName))
        defer { defaults.removePersistentDomain(forName: suiteName) }
        defaults.set(serviceURL.absoluteString, forKey: "serviceAddress")

        let cache = RecentWorkspaceCache(isStoredInMemoryOnly: true)
        let user = CurrentUserPayload(
            user: AccountUser(
                id: "user-1",
                email: "user-1@example.test",
                displayName: "离线用户",
                mustChangePassword: false,
                defaultWorkspaceID: "workspace-1",
                role: "member",
                status: "active"
            ),
            workspace: AuthWorkspace(
                id: "workspace-1",
                name: "离线工作区",
                ownerUserID: "user-1"
            ),
            mustChangePassword: false,
            avatarURL: nil
        )
        let project = ProjectV2(
            id: "project-1",
            name: "缓存项目",
            kind: .standard,
            horizon: .short,
            status: .active,
            systemRole: nil,
            revision: 1
        )
        cache.saveIdentity(user, for: serviceURL)
        cache.markV2CapabilitiesVerified(for: serviceURL)
        cache.saveContent(RecentWorkspaceContent(projects: [project]), for: serviceURL)

        let connectivity = ServiceConnectivity()
        let session = AppSession(
            defaults: defaults,
            connectivity: connectivity,
            recentCache: cache
        )

        await session.restore()

        XCTAssertEqual(session.phase, .ready)
        XCTAssertEqual(session.currentUser, user)
        XCTAssertEqual(session.workspaceStore?.projects, [project])
        XCTAssertTrue(connectivity.isOffline)
        XCTAssertFalse(connectivity.allowsMutations)
        XCTAssertNotNil(connectivity.lastSuccessfulResponseAt)
        let searchResults = try await session.workspaceStore?.search("缓存")
        XCTAssertEqual(searchResults?.map(\.id), [project.id])
    }

    func testColdLaunchDoesNotTrustUnverifiedCache() async throws {
        let serviceURL = URL(string: "http://127.0.0.1:1")!
        let suiteName = "AppSessionOfflineRestoreTests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suiteName))
        defer { defaults.removePersistentDomain(forName: suiteName) }
        defaults.set(serviceURL.absoluteString, forKey: "serviceAddress")

        let cache = RecentWorkspaceCache(isStoredInMemoryOnly: true)
        cache.saveContent(
            RecentWorkspaceContent(projects: [
                ProjectV2(
                    id: "project-1",
                    name: "未验证缓存",
                    kind: .standard,
                    horizon: .short,
                    status: .active,
                    systemRole: nil,
                    revision: 1
                )
            ]),
            for: serviceURL
        )

        let session = AppSession(defaults: defaults, recentCache: cache)

        await session.restore()

        XCTAssertEqual(session.phase, .connection)
        XCTAssertNil(session.currentUser)
    }
}
