import Foundation
import XCTest
@testable import FlowSpaceMac

final class RichEditorBridgeTests: XCTestCase {
    func testParsesGenerationScopedMarkdownChange() throws {
        let message = try XCTUnwrap(RichEditorBridgeMessage(body: [
            "type": "change",
            "noteID": "note-1",
            "generation": "generation-2",
            "markdown": "## 标题\n\n正文",
            "wordCount": 4,
        ]))

        XCTAssertEqual(message.kind, .change)
        XCTAssertEqual(message.noteID, "note-1")
        XCTAssertEqual(message.generation, "generation-2")
        XCTAssertEqual(message.markdown, "## 标题\n\n正文")
        XCTAssertEqual(message.wordCount, 4)
    }

    func testParsesSelectionAndRejectsUnknownMessages() throws {
        let selection = try XCTUnwrap(RichEditorBridgeMessage(body: [
            "type": "selection",
            "noteID": "note-1",
            "generation": "generation-1",
            "selectedText": "日本語",
        ]))

        XCTAssertEqual(selection.selectedText, "日本語")
        XCTAssertNil(RichEditorBridgeMessage(body: ["type": "unexpected"]))
        XCTAssertNil(RichEditorBridgeMessage(body: "not-an-object"))

        let state = try XCTUnwrap(RichEditorBridgeMessage(body: [
            "type": "documentState",
            "noteID": "note-1",
            "generation": "generation-1",
            "wordCount": 12,
        ]))
        XCTAssertEqual(state.wordCount, 12)
    }
}
