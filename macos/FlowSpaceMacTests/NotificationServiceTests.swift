import UserNotifications
import XCTest
@testable import FlowSpaceMac

@MainActor
final class NotificationServiceTests: XCTestCase {
    func testRegisteredDefaultsEnableAllNotificationKindsAndSound() throws {
        let suiteName = "NotificationServiceTests.defaults.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suiteName))
        defer { defaults.removePersistentDomain(forName: suiteName) }

        FlowNotificationPreferences.registerDefaults(in: defaults)
        let preferences = FlowNotificationPreferences(defaults: defaults)

        XCTAssertTrue(preferences.allows(.task))
        XCTAssertTrue(preferences.allows(.contentImport))
        XCTAssertTrue(preferences.allows(.sync))
        XCTAssertTrue(preferences.allows(.test))
        XCTAssertTrue(preferences.sound)
    }

    func testPreferencesRespectPerKindOverrides() throws {
        let suiteName = "NotificationServiceTests.overrides.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suiteName))
        defer { defaults.removePersistentDomain(forName: suiteName) }

        FlowNotificationPreferences.registerDefaults(in: defaults)
        defaults.set(false, forKey: FlowNotificationPreferences.importKey)

        let preferences = FlowNotificationPreferences(defaults: defaults)
        XCTAssertTrue(preferences.allows(.task))
        XCTAssertFalse(preferences.allows(.contentImport))
        XCTAssertTrue(preferences.allows(.sync))
    }

    func testNoteDestinationRoundTripsThroughNotificationPayload() {
        let destination = FlowNotificationDestination.note("note-42")
        XCTAssertEqual(
            FlowNotificationDestination.parse(userInfo: destination.userInfo),
            destination
        )
        XCTAssertNil(FlowNotificationDestination.parse(userInfo: [:]))
    }

    func testEventBuildsGroupedContentWithOptionalSoundAndDestination() {
        let event = FlowNotificationEvent(
            kind: .contentImport,
            title: "播客导入完成",
            body: "示例节目",
            destination: .note("note-42")
        )

        let silent = event.makeContent(playSound: false)
        XCTAssertEqual(silent.title, "播客导入完成")
        XCTAssertEqual(silent.body, "示例节目")
        XCTAssertEqual(silent.threadIdentifier, FlowNotificationKind.contentImport.threadIdentifier)
        XCTAssertNil(silent.sound)
        XCTAssertEqual(
            FlowNotificationDestination.parse(userInfo: silent.userInfo),
            .note("note-42")
        )

        XCTAssertNotNil(event.makeContent(playSound: true).sound)
    }

    func testImportNotificationsOnlyFollowAnActiveToActionableTransition() {
        XCTAssertTrue(ContentImportNotificationPolicy.shouldNotify(previous: .active, current: .completed))
        XCTAssertTrue(ContentImportNotificationPolicy.shouldNotify(previous: .active, current: .failed))
        XCTAssertTrue(ContentImportNotificationPolicy.shouldNotify(previous: .active, current: .needsReview))
        XCTAssertFalse(ContentImportNotificationPolicy.shouldNotify(previous: nil, current: .completed))
        XCTAssertFalse(ContentImportNotificationPolicy.shouldNotify(previous: .completed, current: .completed))
        XCTAssertFalse(ContentImportNotificationPolicy.shouldNotify(previous: .active, current: .canceled))
    }
}
