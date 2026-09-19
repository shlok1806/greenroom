import XCTest

@testable import Companion

/// `Chrome.relative` is the one piece of the sidebar's clock that is pure, so it is the
/// piece worth pinning: every threshold, with an explicit `now`.
final class ChromeTests: XCTestCase {
    private let now = Date(timeIntervalSince1970: 1_700_000_000)

    private func relative(secondsAgo: TimeInterval) -> String {
        Chrome.relative(now.addingTimeInterval(-secondsAgo), now: now)
    }

    func testJustNowUnderTenSeconds() {
        XCTAssertEqual(relative(secondsAgo: 0), "just now")
        XCTAssertEqual(relative(secondsAgo: 9.9), "just now")
    }

    func testSeconds() {
        XCTAssertEqual(relative(secondsAgo: 10), "10 s ago")
        XCTAssertEqual(relative(secondsAgo: 12), "12 s ago")
        XCTAssertEqual(relative(secondsAgo: 59), "59 s ago")
    }

    func testMinutes() {
        XCTAssertEqual(relative(secondsAgo: 60), "1 min ago")
        XCTAssertEqual(relative(secondsAgo: 3 * 60), "3 min ago")
        XCTAssertEqual(relative(secondsAgo: 3599), "59 min ago")
    }

    func testHours() {
        XCTAssertEqual(relative(secondsAgo: 3600), "1 hr ago")
        XCTAssertEqual(relative(secondsAgo: 2 * 3600), "2 hr ago")
        XCTAssertEqual(relative(secondsAgo: 86_399), "23 hr ago")
    }

    func testDays() {
        XCTAssertEqual(relative(secondsAgo: 86_400), "1 day ago")
        XCTAssertEqual(relative(secondsAgo: 2 * 86_400), "2 days ago")
        XCTAssertEqual(relative(secondsAgo: 604_799), "6 days ago")
    }

    func testWeeks() {
        XCTAssertEqual(relative(secondsAgo: 604_800), "1 wk ago")
        XCTAssertEqual(relative(secondsAgo: 3 * 604_800), "3 wk ago")
    }

    /// A clock skewed forward reads as the present, never as a negative age.
    func testFutureDatesClampToJustNow() {
        XCTAssertEqual(Chrome.relative(now.addingTimeInterval(120), now: now), "just now")
    }

    /// An open verdict carries no suffix; a closed one carries exactly one.
    func testVerdictGlyphs() {
        XCTAssertNil(Chrome.glyph(for: .proposed))
        XCTAssertNil(Chrome.glyph(for: .none))
        XCTAssertNil(Chrome.glyph(for: .unknown("weird")))
        XCTAssertEqual(Chrome.glyph(for: .accepted), "checkmark")
        XCTAssertEqual(Chrome.glyph(for: .contested), "exclamationmark")
        XCTAssertEqual(Chrome.glyph(for: .rejected), "xmark")
    }
}
