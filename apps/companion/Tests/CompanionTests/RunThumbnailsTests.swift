import AppKit
import ImageIO
import SwiftUI
import XCTest

@testable import Companion

/// A small JPEG like a recorded frame: `width` x `height`, the left half white and the
/// right half black.
private func syntheticJPEG(width: Int = 256, height: Int = 192) throws -> Data {
    let context = try XCTUnwrap(CGContext(
        data: nil, width: width, height: height, bitsPerComponent: 8, bytesPerRow: 0,
        space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue
    ))
    context.setFillColor(red: 0, green: 0, blue: 0, alpha: 1)
    context.fill(CGRect(x: 0, y: 0, width: width, height: height))
    context.setFillColor(red: 1, green: 1, blue: 1, alpha: 1)
    context.fill(CGRect(x: 0, y: 0, width: width / 2, height: height))
    let image = try XCTUnwrap(context.makeImage())
    let data = NSMutableData()
    let destination = try XCTUnwrap(CGImageDestinationCreateWithData(data, "public.jpeg" as CFString, 1, nil))
    CGImageDestinationAddImage(destination, image, [kCGImageDestinationLossyCompressionQuality: 0.9] as CFDictionary)
    XCTAssertTrue(CGImageDestinationFinalize(destination))
    return data as Data
}

private func frame(_ file: String, at seconds: Double = 0) -> Frame {
    Frame(at: Date(timeIntervalSince1970: seconds), file: file, step: 1)
}

private func thumbnail(_ file: String, cells: Int = 4) -> RunThumbnail {
    RunThumbnail(
        file: file, picture: CGSize(width: 4, height: 3),
        rendering: GlyphRendering(columns: cells, rows: 1, dots: Array(repeating: 0xFF, count: cells),
                                  colors: Array(repeating: RGB(red: 1, green: 1, blue: 1), count: cells))
    )
}

final class ThumbnailSamplerTests: XCTestCase {
    func testAFrameBecomesAGlyphStillOffASmallJPEG() throws {
        let jpeg = try syntheticJPEG()
        let made = try XCTUnwrap(ThumbnailSampler.make(jpeg: jpeg, file: "1.jpg", box: CGSize(width: 56, height: 44)))
        XCTAssertEqual(made.file, "1.jpg")
        XCTAssertEqual(made.picture, CGSize(width: 256, height: 192))
        // A 4:3 frame fills 13 x 5 cells of 4 x 8 pt, 2 pt in from the box's edge.
        XCTAssertEqual(made.rendering.columns, 13)
        XCTAssertEqual(made.rendering.rows, 5)
        let rendering = made.rendering
        for row in 0..<rendering.rows {
            XCTAssertEqual(rendering.dots[row * rendering.columns], 0xFF, "the white half lights every dot")
            XCTAssertEqual(rendering.dots[row * rendering.columns + rendering.columns - 1], 0, "the black half lights none")
        }
    }

    func testDataThatIsNotAPictureMakesNothing() {
        XCTAssertNil(ThumbnailSampler.make(jpeg: Data("not a jpeg".utf8), file: "x.jpg"))
        XCTAssertNil(ThumbnailSampler.make(jpeg: Data(), file: "x.jpg"))
    }

    func testTheGridFollowsThePicturesShapeInWholeCells() {
        let box = CGSize(width: 56, height: 44)
        XCTAssertTrue(ThumbnailSampler.grid(picture: CGSize(width: 1024, height: 768), box: box) == (13, 5))
        XCTAssertTrue(ThumbnailSampler.grid(picture: CGSize(width: 1440, height: 900), box: box) == (13, 4))
        XCTAssertTrue(ThumbnailSampler.grid(picture: CGSize(width: 768, height: 1024), box: box) == (8, 5))
    }

    func testDotsSitOnWholePointsInsideTheBox() throws {
        let box = CGSize(width: 56, height: 44)
        let still = try XCTUnwrap(ThumbnailSampler.make(jpeg: try syntheticJPEG(), file: "1.jpg", box: box))
        let lit = ThumbnailSampler.dots(of: still.rendering, in: box, lit: true)
        let unlit = ThumbnailSampler.dots(of: still.rendering, in: box, lit: false)
        XCTAssertFalse(lit.isEmpty)
        XCTAssertEqual(lit.count + unlit.count, 13 * 5 * 8, "a light theme draws exactly the dots a dark one leaves out")
        for dot in lit + unlit {
            XCTAssertEqual(dot.minX, dot.minX.rounded())
            XCTAssertEqual(dot.minY, dot.minY.rounded())
            XCTAssertTrue(CGRect(origin: .zero, size: box).insetBy(dx: 1, dy: 1).contains(dot), "\(dot) crosses the hairline")
        }
        // The lit half is the white one, on the left.
        XCTAssertLessThan(lit.map(\.midX).max()!, box.width / 2 + 1)
        XCTAssertEqual(ThumbnailSampler.origin(columns: 13, rows: 5, in: box), CGPoint(x: 2, y: 2))
    }

    func testTheEmptyMarkIsCentredInTheBox() {
        let box = CGSize(width: 56, height: 44)
        let mark = ThumbnailSampler.emptyMark(in: box)
        XCTAssertFalse(mark.isEmpty)
        let bounds = mark.reduce(CGRect.null) { $0.union($1) }
        XCTAssertEqual(bounds.midX, box.width / 2, accuracy: 1)
        XCTAssertEqual(bounds.midY, box.height / 2, accuracy: 1)
    }
}

final class ThumbnailCacheTests: XCTestCase {
    func testTheCacheIsBoundedInBytesAndDropsTheLeastRecentlyUsed() {
        let one = thumbnail("a.jpg")
        var cache = ThumbnailCache(capacityBytes: one.bytes * 3)
        cache.insert(one, for: "run-1")
        cache.insert(thumbnail("b.jpg"), for: "run-2")
        cache.insert(thumbnail("c.jpg"), for: "run-3")
        XCTAssertEqual(cache.count, 3)
        cache.touch("run-1") // run-2 is now the least recently used

        let dropped = cache.insert(thumbnail("d.jpg"), for: "run-4")
        XCTAssertEqual(dropped, ["run-2"])
        XCTAssertNil(cache.peek("run-2"))
        XCTAssertEqual(cache.runIds, ["run-3", "run-1", "run-4"])
        XCTAssertLessThanOrEqual(cache.totalBytes, cache.capacityBytes)
    }

    func testANewerFrameReplacesTheRunsThumbnailWithoutCountingTwice() {
        var cache = ThumbnailCache(capacityBytes: 1_000_000)
        cache.insert(thumbnail("a.jpg"), for: "run-1")
        let before = cache.totalBytes
        cache.insert(thumbnail("b.jpg"), for: "run-1")
        XCTAssertEqual(cache.count, 1)
        XCTAssertEqual(cache.totalBytes, before)
        XCTAssertEqual(cache.peek("run-1")?.file, "b.jpg")
    }

    func testBytesGrowWithTheRenderingAndOneTooBigIsNotHeld() {
        XCTAssertGreaterThan(thumbnail("a.jpg", cells: 100).bytes, thumbnail("a.jpg", cells: 10).bytes)
        var cache = ThumbnailCache(capacityBytes: thumbnail("a.jpg", cells: 10).bytes)
        cache.insert(thumbnail("big.jpg", cells: 100), for: "run-1")
        XCTAssertEqual(cache.count, 0)
        XCTAssertEqual(cache.totalBytes, 0)
    }

    /// A real frame's still is a few KB, so the default bound holds hundreds of runs.
    func testTheDefaultBoundHoldsHundredsOfRealThumbnails() throws {
        let still = try XCTUnwrap(ThumbnailSampler.make(jpeg: try syntheticJPEG(), file: "1.jpg"))
        XCTAssertLessThan(still.bytes, 4096)
        XCTAssertGreaterThan(ThumbnailCache().capacityBytes / still.bytes, 500)
    }
}

/// Counts fetches per run and frame, and can hold them back or fail them.
private actor FetchLog {
    private(set) var calls: [String] = []
    var failWith: Error?
    var inFlight = 0
    var mostInFlight = 0
    var delay: Duration = .zero

    func record(_ runId: String, _ file: String) async throws -> Data {
        calls.append("\(runId)/\(file)")
        inFlight += 1
        mostInFlight = max(mostInFlight, inFlight)
        defer { inFlight -= 1 }
        if delay > .zero { try? await Task.sleep(for: delay) }
        if let failWith { throw failWith }
        return Data(file.utf8)
    }

    func fail(_ error: Error?) { failWith = error }
    func slow(_ duration: Duration) { delay = duration }
}

@MainActor
final class RunThumbnailsTests: XCTestCase {
    private func thumbnails(_ log: FetchLog, refresh: TimeInterval = 5, concurrency: Int = 4) -> RunThumbnails {
        RunThumbnails(refreshInterval: refresh, concurrency: concurrency,
                      fetch: { runId, file in try await log.record(runId, file) },
                      sample: { data, file in String(data: data, encoding: .utf8) == file ? thumbnail(file) : nil })
    }

    private func settle(_ condition: @MainActor () async -> Bool, _ message: String = "") async {
        for _ in 0..<200 where !(await condition()) { try? await Task.sleep(for: .milliseconds(10)) }
        let met = await condition()
        XCTAssertTrue(met, message)
    }

    func testEachRunIsFetchedOncePerFrame() async {
        let log = FetchLog()
        let store = thumbnails(log)
        // The rows appear, a list re-read renames the same frames, rows scroll back in.
        for _ in 0..<5 {
            store.request("run-1", frame: frame("a.jpg"))
            store.request("run-2", frame: frame("b.jpg"))
            store.request("run-3", frame: nil)
        }
        await settle({ store.thumbnail("run-1") != nil && store.thumbnail("run-2") != nil })
        store.request("run-1", frame: frame("a.jpg"))
        store.request("run-2", frame: frame("b.jpg"))
        try? await Task.sleep(for: .milliseconds(50))
        let calls = await log.calls
        XCTAssertEqual(calls.sorted(), ["run-1/a.jpg", "run-2/b.jpg"])
        XCTAssertEqual(store.fetchCount, 2)
        XCTAssertNil(store.thumbnail("run-3"), "a run with no frames fetches nothing")
    }

    func testANewerFrameRefetchesAtMostOncePerIntervalAndTheNewestWins() async {
        let log = FetchLog()
        let store = thumbnails(log, refresh: 0.3)
        store.request("run-1", frame: frame("1.jpg"))
        await settle({ store.thumbnail("run-1")?.file == "1.jpg" })
        // A live run captures every 2 s; three new frames land inside one interval.
        store.request("run-1", frame: frame("2.jpg"))
        store.request("run-1", frame: frame("3.jpg"))
        store.request("run-1", frame: frame("4.jpg"))
        XCTAssertEqual(store.thumbnail("run-1")?.file, "1.jpg", "the older still shows until the new one lands")
        await settle({ store.thumbnail("run-1")?.file == "4.jpg" })
        let calls = await log.calls
        XCTAssertEqual(calls, ["run-1/1.jpg", "run-1/4.jpg"])
    }

    func testAFrameTheDaemonRefusedIsNotAskedForAgain() async {
        let log = FetchLog()
        await log.fail(DaemonError.status(code: 404, body: "no frame"))
        let store = thumbnails(log, refresh: 0)
        store.request("run-1", frame: frame("gone.jpg"))
        await settle({ await log.calls.count == 1 })
        try? await Task.sleep(for: .milliseconds(50))
        store.request("run-1", frame: frame("gone.jpg"))
        store.request("run-1", frame: frame("gone.jpg"))
        try? await Task.sleep(for: .milliseconds(50))
        var calls = await log.calls
        XCTAssertEqual(calls, ["run-1/gone.jpg"])

        // A newer frame is tried.
        await log.fail(nil)
        store.request("run-1", frame: frame("new.jpg"))
        await settle({ store.thumbnail("run-1")?.file == "new.jpg" })
        calls = await log.calls
        XCTAssertEqual(calls, ["run-1/gone.jpg", "run-1/new.jpg"])
    }

    func testAFetchNothingAnsweredIsTriedOnTheNextRequest() async {
        let log = FetchLog()
        await log.fail(DaemonError.notReachable("offline"))
        let store = thumbnails(log)
        store.request("run-1", frame: frame("a.jpg"))
        await settle({ await log.calls.count == 1 })
        try? await Task.sleep(for: .milliseconds(50))
        await log.fail(nil)
        store.request("run-1", frame: frame("a.jpg"))
        await settle({ store.thumbnail("run-1") != nil })
        let calls = await log.calls
        XCTAssertEqual(calls, ["run-1/a.jpg", "run-1/a.jpg"])
    }

    func testAPictureThatWillNotSampleIsNotFetchedAgain() async {
        let log = FetchLog()
        let store = RunThumbnails(fetch: { runId, file in try await log.record(runId, file) }, sample: { _, _ in nil })
        store.request("run-1", frame: frame("a.jpg"))
        await settle({ await log.calls.count == 1 })
        try? await Task.sleep(for: .milliseconds(50))
        store.request("run-1", frame: frame("a.jpg"))
        try? await Task.sleep(for: .milliseconds(50))
        let calls = await log.calls
        XCTAssertEqual(calls.count, 1)
        XCTAssertNil(store.thumbnail("run-1"))
    }

    func testAListOfManyRunsFetchesAFewAtATime() async {
        let log = FetchLog()
        await log.slow(.milliseconds(20))
        let store = thumbnails(log, concurrency: 3)
        for index in 0..<20 { store.request("run-\(index)", frame: frame("\(index).jpg")) }
        await settle({ store.heldRuns.count == 20 }, "every run's thumbnail lands")
        let most = await log.mostInFlight
        let calls = await log.calls
        XCTAssertLessThanOrEqual(most, 3)
        XCTAssertEqual(calls.count, 20)
    }

    func testTheCacheBoundHoldsInTheStore() async {
        let log = FetchLog()
        let store = RunThumbnails(capacityBytes: thumbnail("0.jpg").bytes * 2,
                                  fetch: { runId, file in try await log.record(runId, file) },
                                  sample: { _, file in thumbnail(file) })
        for index in 0..<3 { store.request("run-\(index)", frame: frame("\(index).jpg")) }
        await settle({ await log.calls.count == 3 && store.heldRuns.count == 2 })
        XCTAssertLessThanOrEqual(store.heldBytes, thumbnail("0.jpg").bytes * 2)
    }
}

@MainActor
final class RunThumbnailRowTests: XCTestCase {
    /// A frame event moves the row's last frame on, for any listed run, no faster than
    /// the live refresh: the list does not redraw on every capture.
    func testFrameEventsMoveTheListsLastFrameAtMostEveryFewSeconds() {
        let store = RunStore()
        store.runs = [RunSummary(runId: "run-1", createdAt: Date(timeIntervalSince1970: 0), status: .ready)]
        XCTAssertEqual(store.apply(.frame(runId: "run-1", frame: frame("1.jpg", at: 100))), .nothing)
        XCTAssertEqual(store.runs[0].lastFrame?.file, "1.jpg", "a run with no frame takes the first at once")
        store.apply(.frame(runId: "run-1", frame: frame("2.jpg", at: 102)))
        XCTAssertEqual(store.runs[0].lastFrame?.file, "1.jpg")
        store.apply(.frame(runId: "run-1", frame: frame("3.jpg", at: 100 + RunThumbnails.liveRefresh)))
        XCTAssertEqual(store.runs[0].lastFrame?.file, "3.jpg")
        XCTAssertNil(store.frames["run-1"], "the frame list of a run not open is still not held")
    }

    private func height(of row: some View, width: CGFloat) -> CGFloat {
        let host = NSHostingView(rootView: row.frame(width: width).environment(\.theme, DesignData.shared.theme(.dark)))
        host.layout()
        return host.fittingSize.height
    }

    private func row(_ run: RunSummary, title: String, thumbnails: RunThumbnails) -> RunRow {
        RunRow(run: run, title: title, facts: RunFacts.derive(summary: run, detail: nil, messages: nil, steps: nil, verdict: nil, now: Date()),
               now: Date(), selected: false, thumbnails: thumbnails) {}
    }

    /// The box is reserved before the thumbnail lands, and is the same for a run with no
    /// frames: a row never moves.
    func testTheRowReservesTheThumbnailsRoom() async {
        let recorded = RunSummary(runId: "run-1", createdAt: Date(timeIntervalSince1970: 0), status: .finished,
                                  lastFrame: frame("a.jpg"))
        var bare = recorded
        bare.lastFrame = nil
        let empty = RunThumbnails(fetch: { _, _ in Data() }, sample: { _, _ in nil })
        let landed = RunThumbnails(fetch: { _, file in Data(file.utf8) }, sample: { _, file in thumbnail(file) })
        landed.request("run-1", frame: frame("a.jpg"))
        for _ in 0..<200 where landed.thumbnail("run-1") == nil { try? await Task.sleep(for: .milliseconds(10)) }
        XCTAssertNotNil(landed.thumbnail("run-1"))

        let box = ThumbnailSampler.size
        for width in [240.0, 280, 380] {
            for title in ["Run a36ea9", "TipSplit: verify it through the UI only: set Bill to 120, choose 20 percent"] {
                let loading = height(of: row(recorded, title: title, thumbnails: empty), width: width)
                let shown = height(of: row(recorded, title: title, thumbnails: landed), width: width)
                let none = height(of: row(bare, title: title, thumbnails: empty), width: width)
                XCTAssertEqual(loading, shown, "the row grew when its thumbnail landed at \(width) pt")
                XCTAssertEqual(loading, none, "a run with no frames is a different height at \(width) pt")
                XCTAssertGreaterThanOrEqual(loading, box.height + 2 * Space.s, "the row is shorter than its thumbnail")
            }
        }
        let thumb = NSHostingView(rootView: RunThumbnailView(thumbnail: nil, recorded: false))
        XCTAssertEqual(thumb.fittingSize, box)
    }
}
