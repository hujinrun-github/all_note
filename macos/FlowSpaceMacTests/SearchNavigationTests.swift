import XCTest
@testable import FlowSpaceMac

final class SearchNavigationTests: XCTestCase {
    func testSearchResultsMapToPreciseDestinations() {
        XCTAssertEqual(result(type: "note", id: "note-1").route, .note("note-1"))
        XCTAssertEqual(
            result(type: "project", id: "project-1").route,
            .workspace(.projects, .project("project-1"))
        )
        XCTAssertEqual(
            result(type: "event", id: "occurrence-1").route,
            .workspace(.calendar, .occurrence("occurrence-1"))
        )
        XCTAssertEqual(
            result(type: "task", id: "task-1").route,
            .workspace(.tasks, .task("task-1"))
        )
    }

    func testCommandOnlyFilteringUsesTitlesAndKeywords() {
        XCTAssertEqual(
            CommandPaletteCommand.available(isAdmin: false).filter { $0.matches("完整搜索") },
            [.fullSearch]
        )
        XCTAssertFalse(CommandPaletteCommand.available(isAdmin: false).contains(.accountAdmin))
        XCTAssertTrue(CommandPaletteCommand.available(isAdmin: true).contains(.accountAdmin))
    }

    func testRecentResultHistoryDeduplicatesAndLimitsItems() {
        var json = "[]"
        for index in 0..<10 {
            json = SearchResultHistory.remembering(result(type: "note", id: "\(index)"), in: json)
        }
        let decoded = SearchResultHistory.decode(json)
        XCTAssertEqual(decoded.count, 8)
        XCTAssertEqual(decoded.first?.id, "9")

        let updated = SearchResultHistory.remembering(result(type: "note", id: "5"), in: json)
        let deduplicated = SearchResultHistory.decode(updated)
        XCTAssertEqual(deduplicated.first?.id, "5")
        XCTAssertEqual(deduplicated.filter { $0.id == "5" }.count, 1)
    }

    private func result(type: String, id: String) -> SearchResultItem {
        SearchResultItem(
            type: type,
            id: id,
            title: id,
            highlight: "",
            folderID: nil,
            done: nil,
            kind: nil,
            updatedAt: 0
        )
    }
}
