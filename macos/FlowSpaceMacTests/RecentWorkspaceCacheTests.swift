import XCTest
@testable import FlowSpaceMac

@MainActor
final class RecentWorkspaceCacheTests: XCTestCase {
    private let serviceURL = URL(string: "https://flowspace.example.test")!

    func testRoundTripsVerifiedIdentityAndRecentContent() {
        let cache = RecentWorkspaceCache(isStoredInMemoryOnly: true)
        let user = makeUser(id: "user-1", workspaceID: "workspace-1")
        let project = ProjectV2(
            id: "project-1",
            name: "日语学习",
            kind: .learning,
            horizon: .long,
            status: .active,
            systemRole: nil,
            revision: 3
        )

        cache.saveIdentity(user, for: serviceURL)
        cache.markV2CapabilitiesVerified(for: serviceURL)
        cache.saveContent(RecentWorkspaceContent(projects: [project]), for: serviceURL)

        let snapshot = cache.load(for: serviceURL)
        XCTAssertTrue(cache.isAvailable)
        XCTAssertEqual(snapshot?.currentUser, user)
        XCTAssertEqual(snapshot?.v2CapabilitiesVerified, true)
        XCTAssertEqual(snapshot?.content.projects, [project])
        XCTAssertNotNil(snapshot?.cachedAt)
    }

    func testChangingAccountClearsPriorWorkspaceContentAndVerification() {
        let cache = RecentWorkspaceCache(isStoredInMemoryOnly: true)
        cache.saveIdentity(makeUser(id: "user-1", workspaceID: "workspace-1"), for: serviceURL)
        cache.markV2CapabilitiesVerified(for: serviceURL)
        cache.saveContent(
            RecentWorkspaceContent(notes: [
                FlowNote(
                    id: "note-1",
                    title: "私有笔记",
                    body: "content",
                    folderID: "folder-1",
                    tags: "[]",
                    projects: [],
                    createdAt: 1,
                    updatedAt: 2
                )
            ]),
            for: serviceURL
        )

        cache.saveIdentity(makeUser(id: "user-2", workspaceID: "workspace-2"), for: serviceURL)

        let snapshot = cache.load(for: serviceURL)
        XCTAssertEqual(snapshot?.currentUser?.user.id, "user-2")
        XCTAssertFalse(snapshot?.v2CapabilitiesVerified ?? true)
        XCTAssertTrue(snapshot?.content.notes.isEmpty ?? false)
    }

    func testRemoveDeletesServiceScopedCache() {
        let cache = RecentWorkspaceCache(isStoredInMemoryOnly: true)
        cache.saveIdentity(makeUser(id: "user-1", workspaceID: "workspace-1"), for: serviceURL)

        cache.remove(for: serviceURL)

        XCTAssertNil(cache.load(for: serviceURL))
    }

    func testPasswordChangeRequirementInvalidatesVerifiedCache() {
        let cache = RecentWorkspaceCache(isStoredInMemoryOnly: true)
        let regularUser = makeUser(id: "user-1", workspaceID: "workspace-1")
        cache.saveIdentity(regularUser, for: serviceURL)
        cache.markV2CapabilitiesVerified(for: serviceURL)
        let requiredUser = CurrentUserPayload(
            user: AccountUser(
                id: regularUser.user.id,
                email: regularUser.user.email,
                displayName: regularUser.user.displayName,
                mustChangePassword: true,
                defaultWorkspaceID: regularUser.user.defaultWorkspaceID,
                role: regularUser.user.role,
                status: regularUser.user.status
            ),
            workspace: regularUser.workspace,
            mustChangePassword: true,
            avatarURL: nil
        )

        cache.saveIdentity(requiredUser, for: serviceURL)

        XCTAssertFalse(cache.load(for: serviceURL)?.v2CapabilitiesVerified ?? true)
    }

    private func makeUser(id: String, workspaceID: String) -> CurrentUserPayload {
        CurrentUserPayload(
            user: AccountUser(
                id: id,
                email: "\(id)@example.test",
                displayName: id,
                mustChangePassword: false,
                defaultWorkspaceID: workspaceID,
                role: "member",
                status: "active"
            ),
            workspace: AuthWorkspace(id: workspaceID, name: workspaceID, ownerUserID: id),
            mustChangePassword: false,
            avatarURL: nil
        )
    }
}
