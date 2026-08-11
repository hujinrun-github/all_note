import XCTest

final class FlowSpaceMacUITests: XCTestCase {
    @MainActor
    private func launchApp() -> XCUIApplication {
        let app = XCUIApplication()
        app.launchEnvironment["FLOWSPACE_UI_TESTING"] = "1"
        app.launchArguments.append("-ApplePersistenceIgnoreState")
        app.launch()
        return app
    }

    @MainActor
    func testFreshLaunchShowsConnectionScreen() {
        let app = launchApp()
        defer { app.terminate() }

        let titleExists = app.staticTexts["连接 FlowSpace"].waitForExistence(timeout: 8)
        let address = app.textFields["connection-address"]
        let addressExists = address.exists
        let addressValue = address.value as? String
        let connectIsEnabled = app.buttons["connection-submit"].isEnabled

        XCTAssertTrue(titleExists)
        XCTAssertTrue(addressExists)
        XCTAssertEqual(addressValue, "http://127.0.0.1:4100")
        XCTAssertTrue(connectIsEnabled)
    }

    @MainActor
    func testEmptyAddressDisablesConnectButton() {
        let app = launchApp()
        defer { app.terminate() }

        let address = app.textFields["connection-address"]
        let addressExists = address.waitForExistence(timeout: 8)
        XCTAssertTrue(addressExists)

        address.click()
        address.typeKey("a", modifierFlags: .command)
        address.typeKey(.delete, modifierFlags: [])

        let connectIsEnabled = app.buttons["connection-submit"].isEnabled
        XCTAssertFalse(connectIsEnabled)
    }
}
