import Foundation
import XCTest
@testable import FlowSpaceMac

final class AccountSettingsCodingTests: XCTestCase {
    func testDecodesProfileAndAdminPaginationAcronyms() throws {
        let profile = try JSONDecoder.flowSpace().decode(UserProfile.self, from: Data(profileJSON.utf8))
        let pagination = try JSONDecoder.flowSpace().decode(
            AccountPagination.self,
            from: Data(#"{"page":2,"page_size":20,"total":45}"#.utf8)
        )

        XCTAssertEqual(profile.userID, "user-1")
        XCTAssertEqual(profile.avatarURL, "/api/settings/profile/avatar?v=2")
        XCTAssertEqual(profile.timeZone, "Asia/Shanghai")
        XCTAssertEqual(pagination.pageSize, 20)
        XCTAssertEqual(pagination.total, 45)
    }

    func testEncodesAccountMutationsWithBackendKeys() throws {
        let create = CreateAdminUserInput(
            email: "new@example.com",
            displayName: "New User",
            temporaryPassword: "secret123",
            role: .user
        )
        let data = try JSONEncoder.flowSpace().encode(create)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        XCTAssertEqual(object["display_name"] as? String, "New User")
        XCTAssertEqual(object["temporary_password"] as? String, "secret123")
        XCTAssertEqual(object["role"] as? String, "user")
    }

    func testPasswordPolicyMatchesWebPolicy() throws {
        XCTAssertNoThrow(try PasswordPolicy.validate("resetPass123"))
        XCTAssertThrowsError(try PasswordPolicy.validate("onlyletters"))
        XCTAssertThrowsError(try PasswordPolicy.validate("12345678"))
        XCTAssertThrowsError(try PasswordPolicy.validate("a1"))
        XCTAssertThrowsError(try PasswordPolicy.validate(String(repeating: "a", count: 72) + "1"))
    }

    private let profileJSON = #"""
    {
      "user_id":"user-1",
      "email":"user@example.com",
      "display_name":"用户",
      "locale":"zh-CN",
      "time_zone":"Asia/Shanghai",
      "avatar_url":"/api/settings/profile/avatar?v=2",
      "updated_at":1786190000
    }
    """#
}
