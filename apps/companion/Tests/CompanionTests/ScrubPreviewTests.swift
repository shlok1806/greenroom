import AppKit
import SwiftUI
import XCTest

@testable import Companion

/// The scrub bar's hover preview (companion ADR 0023): the state machine, the check labels
/// over close marks, and the rendered bar, which must draw the same before a hover, after
/// hover then exit, and after the window was hidden and shown again.
@MainActor
final class ScrubPreviewTests: XCTestCase {
    private var hosts: [ParkedHost] = []

    override func tearDown() async throws {
        OnScreen.shared.visible = true
        hosts.forEach { $0.close() }
        hosts = []
        try await super.tearDown()
    }

    // MARK: - The state machine

    private func preview(_ events: ScrubPreview.Event...) -> ScrubPreview {
        var preview = ScrubPreview()
        events.forEach { preview.handle($0) }
        return preview
    }

    func testHoverShowsThePreviewWhereThePointerIsAndExitHidesIt() {
        XCTAssertFalse(ScrubPreview().shows)
        XCTAssertEqual(preview(.hovered(x: 120, width: 400)).x, 120)
        XCTAssertEqual(preview(.hovered(x: 120, width: 400), .hovered(x: 130, width: 400)).x, 130)
        XCTAssertFalse(preview(.hovered(x: 120, width: 400), .exited).shows)
    }

    func testThePreviewPointsInsideTheTrack() {
        XCTAssertEqual(preview(.hovered(x: -8, width: 400)).x, 0)
        XCTAssertEqual(preview(.hovered(x: 480, width: 400)).x, 400)
        XCTAssertEqual(preview(.dragged(x: 900, width: 400)).x, 400)
    }

    /// The bug: a drag that ended off the bar left the preview standing, and with it the
    /// missing bar.
    func testADragThatEndsAwayFromTheTrackHidesThePreview() {
        let away = preview(.hovered(x: 100, width: 400), .dragged(x: 100, width: 400), .dragged(x: 180, width: 400),
                           .exited, .released(inside: false))
        XCTAssertFalse(away.shows)
        XCTAssertFalse(away.dragging)
        // The pointer never sends a hover end after a drag leaves; nothing else is needed.
        XCTAssertFalse(preview(.dragged(x: 100, width: 400), .released(inside: false)).shows)
    }

    func testADragHoldsThePreviewWhileThePointerWandersAndAClickOnTheTrackKeepsIt() {
        let wandering = preview(.dragged(x: 100, width: 400), .exited)
        XCTAssertTrue(wandering.shows, "a held drag keeps the preview when the pointer leaves the track")
        XCTAssertTrue(wandering.dragging)
        let click = preview(.hovered(x: 100, width: 400), .dragged(x: 100, width: 400), .released(inside: true))
        XCTAssertEqual(click.x, 100, "a click on the track leaves the preview under the pointer")
        XCTAssertFalse(click.dragging)
        XCTAssertFalse(preview(.dragged(x: 100, width: 400), .released(inside: true), .exited).shows)
    }

    /// Window deactivation, occlusion, a scroll and a run change all arrive as `dismissed`.
    func testDismissalHidesThePreviewEvenMidDragAndOnlyTheNextHoverBringsItBack() {
        XCTAssertFalse(preview(.hovered(x: 100, width: 400), .dismissed).shows)
        let midDrag = preview(.dragged(x: 100, width: 400), .dismissed)
        XCTAssertFalse(midDrag.shows)
        XCTAssertFalse(midDrag.dragging)
        XCTAssertFalse(preview(.dismissed, .exited).shows)
        XCTAssertFalse(preview(.hovered(x: 100, width: 400), .dismissed, .released(inside: true)).shows)
        XCTAssertEqual(preview(.hovered(x: 100, width: 400), .dismissed, .hovered(x: 40, width: 400)).x, 40)
    }

    func testAPointIsInsideTheTrackOnlyWithinItsBounds() {
        XCTAssertTrue(ScrubPreview.inside(CGPoint(x: 10, y: 10), width: 400, height: 32))
        XCTAssertFalse(ScrubPreview.inside(CGPoint(x: 10, y: -1), width: 400, height: 32))
        XCTAssertFalse(ScrubPreview.inside(CGPoint(x: 10, y: 40), width: 400, height: 32))
        XCTAssertFalse(ScrubPreview.inside(CGPoint(x: 401, y: 10), width: 400, height: 32))
    }

    func testTheShellChangesNothingWhenAnEventLeavesThePreviewAsItWas() throws {
        let shell = ShellModel(store: try Self.store(.done))
        shell.dismissScrubPreview()
        XCTAssertEqual(shell.scrubPreview, ScrubPreview())
        shell.scrubPreview(.hovered(x: 50, width: 400))
        XCTAssertEqual(shell.scrubPreview.x, 50)
        shell.select(run: "another run")
        XCTAssertFalse(shell.scrubPreview.shows, "a run change hides the preview")
    }

    // MARK: - Check labels

    private let t0 = Date(timeIntervalSince1970: 1_790_000_000)

    private func timeline(checkSteps: [Int], failed: Set<Int> = []) -> RecordingTimeline {
        let steps = (1...100).map { Step(seq: $0, at: t0.addingTimeInterval(Double($0)), tool: "machine_click", durationMs: 100) }
        let checks = checkSteps.enumerated().map { index, step in
            SummaryCheck(id: "\(index)", text: "Check \(index + 1)", state: failed.contains(index + 1) ? .fail : .pass, step: step)
        }
        return RecordingTimeline.make(frames: [], steps: steps, checks: checks, start: t0, live: false, now: t0)
    }

    func testFarApartChecksEachKeepTheirNumber() {
        let t = timeline(checkSteps: [10, 50, 90])
        let labels = t.checkLabels(t.marks, width: 400)
        XCTAssertEqual(labels.map(\.text), ["1", "2", "3"])
    }

    /// The finished run from the report: checks 2 and 3 about 6 pt apart drew "2" and "3" on
    /// top of each other.
    func testChecksWhoseLabelsWouldTouchShareOneLabelAtTheirMiddle() throws {
        let t = timeline(checkSteps: [10, 70, 71], failed: [3])
        let labels = t.checkLabels(t.marks, width: 400)
        XCTAssertEqual(labels.map(\.text), ["1", "2,3"])
        let x2 = t.fraction(of: t.marks[69].at) * 400, x3 = t.fraction(of: t.marks[70].at) * 400
        XCTAssertEqual(try XCTUnwrap(labels.last).x, (x2 + x3) / 2, accuracy: 0.001)
        XCTAssertTrue(try XCTUnwrap(labels.last).failed, "a merged label is red when one of its checks failed")
        // Wide enough, they part again.
        XCTAssertEqual(t.checkLabels(t.marks, width: 4000).map(\.text), ["1", "2", "3"])
    }

    func testMergedLabelsNeverOverlap() {
        let t = timeline(checkSteps: Array(stride(from: 2, through: 98, by: 2)))
        for width in [200.0, 400, 800, 1600] {
            let labels = t.checkLabels(t.marks, width: width)
            for (a, b) in zip(labels, labels.dropFirst()) {
                XCTAssertGreaterThanOrEqual(b.x - a.x, (a.width + b.width) / 2 - 0.001, "\(width): \(a.text) and \(b.text)")
            }
            XCTAssertEqual(labels.flatMap(\.numbers), Array(1...49), "every number is drawn once, in order")
        }
    }

    // MARK: - The rendered bar

    private typealias F = StateFixtures

    private struct NoScreen: ScreenSource {
        func liveScreen(runId: String) -> AsyncThrowingStream<ScreenMessage, Error> { AsyncThrowingStream { _ in } }
    }

    private static let greyPNG: Data = {
        let image = NSImage(size: NSSize(width: 64, height: 48))
        image.lockFocus()
        NSColor.gray.setFill()
        NSRect(x: 0, y: 0, width: 64, height: 48).fill()
        image.unlockFocus()
        return NSBitmapImageRep(data: image.tiffRepresentation!)!.representation(using: .png, properties: [:])!
    }()

    /// TipSplit in `state`, with forty steps and frames ten seconds apart.
    private static func store(_ state: F.State) throws -> RunStore {
        let png = greyPNG
        let client = StubURLProtocol.client { request in
            let path = request.url?.path ?? ""
            if path.contains("/frames/") || path.contains("/artifacts/") { return StubURLProtocol.Reply(body: png) }
            return .json("{}", status: 404)
        }
        let store = RunStore(client: client, controlClient: GrantingControlClient(), screenSource: NoScreen())
        store.board = F.board(try SummaryTests.golden(), state: state)
        store.reachable = true
        store.selectedRunId = F.tipSplit
        store.steps[F.tipSplit] = (1...40).map { i in
            Step(seq: i, at: F.start.addingTimeInterval(Double(i) * 6), tool: "machine_click", error: i == 17 ? "exit 1" : nil, durationMs: 800)
        }
        store.frames[F.tipSplit] = (1...40).map { i in
            Frame(at: F.start.addingTimeInterval(Double(i) * 6 + 1), file: "f\(i).jpg", step: i)
        }
        return store
    }

    /// Where the bar sits in the test window, in points from the top: the room above holds
    /// the preview.
    private static let barTop: CGFloat = 300
    private static let size = CGSize(width: 640, height: 400)

    private func host(_ shell: ShellModel, frozen: Bool = true) async throws -> ParkedHost {
        let summary = try XCTUnwrap(shell.summary)
        let view = VStack(spacing: 0) {
            Color.clear.frame(height: Self.barTop)
            TimelineBar(shell: shell, summary: summary)
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 24)
        .frame(width: Self.size.width, height: Self.size.height)
        .background(Palette.bgStage)
        .environment(\.frozenNow, frozen ? F.start.addingTimeInterval(400) : nil)
        let host = ParkedHost(view, size: Self.size)
        hosts.append(host)
        await host.settle(0.6)
        return host
    }

    /// RGBA rows `from`..<`to` (points) of the window's picture at 2x.
    private func rows(_ image: CGImage, from: CGFloat, to: CGFloat) -> [UInt8] {
        let bitmap = NSBitmapImageRep(cgImage: image)
        guard let data = bitmap.bitmapData else { return [] }
        let channels = bitmap.samplesPerPixel
        var out: [UInt8] = []
        for y in max(0, Int(from * 2))..<min(bitmap.pixelsHigh, Int(to * 2)) {
            let row = data + y * bitmap.bytesPerRow
            for x in 0..<bitmap.pixelsWide {
                out += [row[x * channels], row[x * channels + 1], row[x * channels + 2]]
            }
        }
        return out
    }

    /// How many pixels of the rail's row differ from the stage's ground: the bar drew.
    private func railInk(_ image: CGImage) -> Int {
        let row = rows(image, from: Self.barTop + TimelineTrack.railY, to: Self.barTop + TimelineTrack.railY + 0.5)
        let ground = Array(row.prefix(3))
        return stride(from: 0, to: row.count, by: 3).filter { Array(row[$0..<$0 + 3]) != ground }.count
    }

    private func snap(_ host: ParkedHost) async throws -> CGImage {
        await host.settle(0.3)
        return try XCTUnwrap(host.image())
    }

    func testThePreviewFloatsAboveTheBarAndExitLeavesTheBarAsItWas() async throws {
        let shell = ShellModel(store: try Self.store(.done))
        let host = try await host(shell)
        let before = try await snap(host)
        XCTAssertGreaterThan(railInk(before), 600, "the bar draws across the width")

        shell.scrubPreview(.hovered(x: 200, width: 592))
        let hovering = try await snap(host)
        let bar = (from: Self.barTop + 8, to: Self.barTop + TimelineBar.height)
        XCTAssertEqual(rows(hovering, from: bar.from, to: bar.to), rows(before, from: bar.from, to: bar.to),
                       "the preview never moves or covers the bar and its controls")
        XCTAssertNotEqual(rows(hovering, from: Self.barTop - 120, to: Self.barTop - 20), rows(before, from: Self.barTop - 120, to: Self.barTop - 20),
                          "the preview shows above the bar")

        shell.scrubPreview(.exited)
        let after = try await snap(host)
        XCTAssertEqual(rows(after, from: 0, to: Self.size.height), rows(before, from: 0, to: Self.size.height),
                       "after the pointer leaves, the window is as it was")
    }

    func testADragEndingAwayLeavesNoPreviewAndTheBarDrawn() async throws {
        let shell = ShellModel(store: try Self.store(.done))
        let host = try await host(shell)
        let before = try await snap(host)
        shell.scrubPreview(.dragged(x: 300, width: 592))
        shell.scrubPreview(.released(inside: false))
        let after = try await snap(host)
        XCTAssertEqual(rows(after, from: 0, to: Self.barTop), rows(before, from: 0, to: Self.barTop), "no preview stands")
        XCTAssertGreaterThan(railInk(after), 600)
    }

    func testHidingTheWindowDismissesThePreviewAndShowingItDrawsTheBarAgain() async throws {
        for state in [F.State.done, .live] {
            let shell = ShellModel(store: try Self.store(state))
            let host = try await host(shell, frozen: state == .done)
            let before = try await snap(host)
            shell.scrubPreview(.hovered(x: 200, width: 592))
            _ = try await snap(host)

            OnScreen.shared.visible = false
            let hidden = try await snap(host)
            XCTAssertFalse(shell.scrubPreview.shows, "\(state): going off screen dismisses the preview")
            XCTAssertGreaterThan(railInk(hidden), 600, "\(state)")

            OnScreen.shared.visible = true
            let shown = try await snap(host)
            XCTAssertGreaterThan(railInk(shown), 600, "\(state): the bar draws again once the window shows")
            XCTAssertEqual(rows(shown, from: 0, to: Self.barTop), rows(before, from: 0, to: Self.barTop),
                           "\(state): no preview comes back by itself")
            if state == .done {
                XCTAssertEqual(rows(shown, from: 0, to: Self.size.height), rows(before, from: 0, to: Self.size.height), "\(state)")
            }
            host.close()
        }
    }

    /// The playhead holds while nobody can see the window (companion ADR 0021).
    func testPlaybackHoldsWhileTheWindowIsOffScreen() async throws {
        let shell = ShellModel(store: try Self.store(.done))
        shell.seek(to: 10)
        OnScreen.shared.visible = false
        shell.play()
        try await Task.sleep(for: .milliseconds(400))
        XCTAssertEqual(try XCTUnwrap(shell.playhead), 10, accuracy: 0.001)
        OnScreen.shared.visible = true
        try await Task.sleep(for: .milliseconds(400))
        XCTAssertGreaterThan(try XCTUnwrap(shell.playhead), 10)
        shell.pause()
    }
}
