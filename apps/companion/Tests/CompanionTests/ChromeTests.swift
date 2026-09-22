import XCTest

@testable import Companion

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

    // MARK: - Clock

    func testAClockGrowsAnHoursColumnRatherThanCountingToFourHundred() {
        XCTAssertEqual(Chrome.clock(0), "0:00")
        XCTAssertEqual(Chrome.clock(7), "0:07")
        XCTAssertEqual(Chrome.clock(723), "12:03")
        XCTAssertEqual(Chrome.clock(3600), "1:00:00")
        XCTAssertEqual(Chrome.clock(28_572), "7:56:12")
    }

    func testAClockNeverGoesBackwards() {
        XCTAssertEqual(Chrome.clock(-90), "0:00")
    }

    // MARK: - Counts

    func testACountLosesItsDigitsOnceItHasEnoughOfThem() {
        XCTAssertEqual(Chrome.count(0), "0")
        XCTAssertEqual(Chrome.count(999), "999")
        XCTAssertEqual(Chrome.count(2385), "2.4k")
        XCTAssertEqual(Chrome.count(24_000), "24k")
        XCTAssertEqual(Chrome.count(1_500_000), "1.5M")
    }

    // MARK: - Run names

    func testARunIdGivesUpItsHash() {
        XCTAssertEqual(Chrome.runHash("20260921-050808-8ecfd5"), "8ecfd5")
    }

    func testALongHashIsCutDown() {
        XCTAssertEqual(Chrome.runHash("20260921-201720-40e7ef2a554f9922"), "40e7ef")
    }

    func testAnIdWithNoHashHasNone() {
        XCTAssertEqual(Chrome.runHash("something-else"), "")
        XCTAssertEqual(Chrome.runHash("plain"), "")
    }

    // MARK: - Days

    func testTodayAndYesterdayAreNamedRatherThanDated() {
        XCTAssertEqual(Chrome.day(now, now: now), "Today")
        XCTAssertEqual(Chrome.day(now.addingTimeInterval(-86_400), now: now), "Yesterday")
        XCTAssertNotEqual(Chrome.day(now.addingTimeInterval(-5 * 86_400), now: now), "Yesterday")
    }

    // MARK: - Shape

    func testShapesDropWholeTermsLeastUsefulFirst() {
        XCTAssertEqual(
            Chrome.shapes(steps: 181, frames: 5, messages: 12),
            ["181 steps · 5 frames · 12 msgs", "181 steps · 5 frames", "181 steps"]
        )
    }

    func testShapesSkipAbsentTermsAndSingularise() {
        XCTAssertEqual(Chrome.shapes(steps: 1, frames: nil, messages: 1), ["1 step · 1 msg", "1 step"])
        XCTAssertEqual(Chrome.shapes(steps: 0, frames: 0, messages: 0), ["empty"])
    }
}
