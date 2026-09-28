import AppKit
import SwiftUI
import XCTest

@testable import Companion

/// Companion ADR 0021 (#219): no clock runs while nobody can see the run window.
@MainActor
final class OnScreenTests: XCTestCase {
    override func tearDown() async throws {
        OnScreen.shared.visible = true
        try await super.tearDown()
    }

    /// How many times a `Clocked` view draws in `seconds`.
    private func draws(visible: Bool, seconds: Double = 0.6) async -> Int {
        OnScreen.shared.visible = visible
        let counter = Counter()
        let host = ParkedHost(Clocked { _ in counter.tick() }, size: CGSize(width: 40, height: 40))
        defer { host.close() }
        await host.settle(seconds)
        return counter.count
    }

    func testClocksRunWhileTheWindowShowsAndStopWhileItDoesNot() async {
        let shown = await draws(visible: true)
        let hidden = await draws(visible: false)
        XCTAssertGreaterThan(shown, 10, "a clock on screen moves every frame")
        XCTAssertLessThanOrEqual(hidden, 2, "a clock off screen draws its moment once")
    }

    /// A window that is not the run window (a test's, the harness's) never changes the fact.
    func testOnlyTheRunWindowSaysWhetherItIsOnScreen() async {
        OnScreen.shared.visible = true
        let host = ParkedHost(OnScreenReader(), size: CGSize(width: 40, height: 40))
        defer { host.close() }
        host.window.orderOut(nil)
        await host.settle(0.2)
        XCTAssertTrue(OnScreen.shared.visible)
        XCTAssertFalse(AppDelegate.isRunWindow(host.window))
    }

    func testTypeMetricsAreTheFacesOwn() {
        for style in TypeStyle.allCases {
            let font = style.nsFont
            XCTAssertEqual(style.naturalLineHeight, font.ascender - font.descender + font.leading, "\(style)")
        }
    }
}

@MainActor
private final class Counter {
    var count = 0

    func tick() -> some View {
        count += 1
        return Color.clear
    }
}
