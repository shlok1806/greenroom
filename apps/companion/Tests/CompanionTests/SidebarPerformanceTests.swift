import AppKit
import SwiftUI
import XCTest

@testable import Companion

/// The runs sidebar stays smooth with 2,000 runs (companion ADR 0019 point 8): the list is lazy
/// (an `NSTableView` underneath), so building it and scrolling it cost what the screen shows,
/// not what the board holds. The numbers print on every run; the bounds are loose enough for a
/// busy CI runner and tight enough to fail if every row were built.
@MainActor
final class SidebarPerformanceTests: XCTestCase {
    func testTheListOfTwoThousandRunsIsWorkedOutInAFewMilliseconds() {
        let board = SyntheticBoard.board(runs: 2000)
        let start = Date()
        var items: [SidebarItem] = []
        for _ in 0..<20 { items = SidebarLayout.items(board, expanded: [.done], selected: "x") }
        let each = Date().timeIntervalSince(start) / 20
        print("sidebar layout of 2000 runs: \(String(format: "%.2f", each * 1000)) ms")
        XCTAssertEqual(items.count, 2000 + 3)
        XCTAssertLessThan(each, 0.05)

        let moved = Summary(runId: board.runs[1500].runId, name: "moved", group: .needsYou, since: Date())
        let applyStart = Date()
        let after = board.applying(moved, macs: nil)
        let applyTime = Date().timeIntervalSince(applyStart)
        print("apply one summary to 2000 runs: \(String(format: "%.2f", applyTime * 1000)) ms")
        XCTAssertEqual(after.groups[0].runs.first?.name, "moved")
        XCTAssertLessThan(applyTime, 0.1)
    }

    func testTwoThousandRunsScrollFrameByFrame() async throws {
        let board = SyntheticBoard.board(runs: 2000)
        let built = Date()
        let host = ParkedHost(RunsSidebar(board: board, connection: .online, selected: nil, width: 248,
                                          select: { _ in }, openSettings: {}, expandedAtStart: [.done]),
                              size: CGSize(width: 248, height: 800))
        defer { host.close() }
        await host.settle(0.2)
        let firstLayout = Date().timeIntervalSince(built) - 0.2
        let scroll = try XCTUnwrap(host.scrollViews.first { ($0.documentView?.frame.height ?? 0) > 10_000 }, "no scroll view of the whole list")
        let height = try XCTUnwrap(scroll.documentView).frame.height
        XCTAssertGreaterThan(height, 2000 * 30, "every run has its row height")

        // Scroll the whole list in 60 steps, laying out and drawing each like a frame.
        var frames: [Double] = []
        let step = (height - 800) / 60
        for i in 1...60 {
            let t = Date()
            scroll.contentView.scroll(to: NSPoint(x: 0, y: Double(i) * step))
            scroll.reflectScrolledClipView(scroll.contentView)
            host.window.contentView?.layoutSubtreeIfNeeded()
            host.window.contentView?.displayIfNeeded()
            frames.append(Date().timeIntervalSince(t))
        }
        frames.sort()
        let median = frames[frames.count / 2], p95 = frames[Int(Double(frames.count) * 0.95)]
        print(String(format: "sidebar 2000 runs: first layout %.0f ms, scroll step median %.1f ms, p95 %.1f ms, list %.0f pt",
                     max(0, firstLayout) * 1000, median * 1000, p95 * 1000, height))
        XCTAssertLessThan(median, 0.030, "a scroll step should cost a frame or two, not the whole list")
        XCTAssertLessThan(p95, 0.100, "no scroll step may stall")
        XCTAssertLessThan(firstLayout, 1.0, "the first layout must not measure every row")
    }
}
