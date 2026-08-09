import XCTest
@testable import FlowSpaceMac

final class CalendarDropPlannerTests: XCTestCase {
    func testSnapsDropPositionToFifteenMinuteGrid() {
        XCTAssertEqual(CalendarDropPlanner.snappedMinute(y: 0, cellHeight: 60), 0)
        XCTAssertEqual(CalendarDropPlanner.snappedMinute(y: 14, cellHeight: 60), 0)
        XCTAssertEqual(CalendarDropPlanner.snappedMinute(y: 16, cellHeight: 60), 15)
        XCTAssertEqual(CalendarDropPlanner.snappedMinute(y: 33, cellHeight: 60), 30)
        XCTAssertEqual(CalendarDropPlanner.snappedMinute(y: 59, cellHeight: 60), 45)
    }

    func testBuildsProposedDateInSelectedTimezone() throws {
        let timezone = try XCTUnwrap(TimeZone(identifier: "Asia/Shanghai"))
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = timezone
        let day = try XCTUnwrap(calendar.date(from: DateComponents(year: 2026, month: 8, day: 9)))
        let proposed = try XCTUnwrap(CalendarDropPlanner.proposedStart(
            day: day,
            hour: 11,
            minute: 45,
            timezone: timezone
        ))
        let components = calendar.dateComponents([.year, .month, .day, .hour, .minute], from: proposed)
        XCTAssertEqual(components.year, 2026)
        XCTAssertEqual(components.month, 8)
        XCTAssertEqual(components.day, 9)
        XCTAssertEqual(components.hour, 11)
        XCTAssertEqual(components.minute, 45)
    }

    func testResizeSnapsToFifteenMinutesAndClampsBounds() {
        XCTAssertEqual(
            CalendarDropPlanner.resizedDuration(originalMinutes: 30, verticalTranslation: 15, pointsPerHour: 60),
            45
        )
        XCTAssertEqual(
            CalendarDropPlanner.resizedDuration(originalMinutes: 30, verticalTranslation: -100, pointsPerHour: 60),
            15
        )
        XCTAssertEqual(
            CalendarDropPlanner.resizedDuration(originalMinutes: 470, verticalTranslation: 30, pointsPerHour: 60),
            480
        )
    }
}
