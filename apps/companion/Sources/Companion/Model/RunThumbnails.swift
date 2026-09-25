import CoreGraphics
import Foundation
import ImageIO
import Observation

/// A run's thumbnail in the runs list: its last recorded frame as a glyph still, the same
/// braille dither the power-down freezes on (companion ADR 0006, moment 5). The daemon
/// names the frame (`RunSummary.lastFrame`); the app fetches that JPEG and samples it
/// here, so nothing is uploaded and the app keeps no run on disk. Held as the rendering,
/// not a picture, so the row draws it in the theme's own colours and a theme change needs
/// no refetch.
struct RunThumbnail: Equatable, Sendable {
    /// The frame it was drawn from: a newer `lastFrame` means a newer thumbnail.
    let file: String
    /// The frame's size in pixels, for its letterbox in the thumbnail's box.
    let picture: CGSize
    let rendering: GlyphRendering

    /// What it holds in memory: the cache is bounded by this, never by a count.
    var bytes: Int {
        rendering.dots.count + rendering.colors.count * MemoryLayout<RGB>.stride + file.utf8.count + 64
    }
}

enum ThumbnailSampler {
    /// The distance between dots, in points, across and down. Whole points, so every dot
    /// lands on a pixel and the still is as crisp on a 1x display as on Retina.
    static let pitch: CGFloat = 2
    /// A dot: half the pitch, so dots never touch.
    static let dot: CGFloat = 1
    /// One braille cell (2 x 4 dots) in a thumbnail, in points.
    static var cell: CGSize { CGSize(width: pitch * CGFloat(GlyphSampler.dotsAcross), height: pitch * CGFloat(GlyphSampler.dotsDown)) }

    /// The thumbnail's box, in points (`tokens.json` `layout`).
    static var size: CGSize {
        let layout = DesignData.shared.tokens.layout
        return CGSize(width: layout.runThumbnailWidth, height: layout.runThumbnailHeight)
    }

    /// The whole cells nearest the picture fitted in `box` less `inset` all round, never
    /// more than fit a point in from its hairline.
    static func grid(picture: CGSize, box: CGSize) -> (columns: Int, rows: Int) {
        let fitted = ScreenGeometry.fitted(image: picture, in: CGSize(width: box.width - 2 * inset, height: box.height - 2 * inset))
        let most = (Int((box.width - 2) / cell.width), Int((box.height - 2) / cell.height))
        return (min(most.0, max(1, Int((fitted.width / cell.width).rounded()))),
                min(most.1, max(1, Int((fitted.height / cell.height).rounded()))))
    }

    /// Between the hairline and the picture's cells.
    static let inset: CGFloat = 2

    /// Decodes `jpeg` only as far as the glyphs need (ImageIO's downsampling decode, a
    /// few pixels a dot, never the whole frame) and samples it at the grid the picture
    /// fills in `box`. Decodes, so it runs off the main actor. nil for data that is not a
    /// picture.
    static func make(jpeg: Data, file: String, box: CGSize = size) -> RunThumbnail? {
        let options = [kCGImageSourceShouldCache: false] as CFDictionary
        guard let source = CGImageSourceCreateWithData(jpeg as CFData, options),
              let properties = CGImageSourceCopyPropertiesAtIndex(source, 0, options) as? [CFString: Any],
              let width = properties[kCGImagePropertyPixelWidth] as? Int,
              let height = properties[kCGImagePropertyPixelHeight] as? Int,
              width > 0, height > 0 else { return nil }
        let picture = CGSize(width: width, height: height)
        let grid = grid(picture: picture, box: box)
        let longest = max(grid.columns * GlyphSampler.dotsAcross, grid.rows * GlyphSampler.dotsDown)
        let decode = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceShouldCacheImmediately: true,
            // Four pixels a dot, so each dot still averages the picture under it.
            kCGImageSourceThumbnailMaxPixelSize: max(8, longest * 4),
        ] as CFDictionary
        guard let image = CGImageSourceCreateThumbnailAtIndex(source, 0, decode),
              let rendering = GlyphSampler.sample(image, columns: grid.columns, rows: grid.rows) else { return nil }
        return RunThumbnail(file: file, picture: picture, rendering: rendering)
    }

    /// Where the `columns` x `rows` cells start in `box`: centred, on a whole point. A cell
    /// holds its dots' trailing gaps, so a 4:3 frame in the 56 x 44 box sits 2 pt in on
    /// every side.
    static func origin(columns: Int, rows: Int, in box: CGSize) -> CGPoint {
        CGPoint(x: ((box.width - CGFloat(columns) * cell.width) / 2).rounded(.down),
                y: ((box.height - CGFloat(rows) * cell.height) / 2).rounded(.down))
    }

    /// The dots to draw for `rendering` in `box`: the lit ones where `lit` (a dark ground,
    /// where the lit parts of the picture are the light ones), else the unlit ones (a light
    /// ground, where dots are the dark parts), so both themes show the picture and never
    /// its negative.
    static func dots(of rendering: GlyphRendering, in box: CGSize, lit: Bool) -> [CGRect] {
        let origin = origin(columns: rendering.columns, rows: rendering.rows, in: box)
        var out: [CGRect] = []
        for row in 0..<rendering.rows {
            for column in 0..<rendering.columns {
                let held = rendering.dots[row * rendering.columns + column]
                let bits = lit ? held : ~held
                guard bits != 0 else { continue }
                for y in 0..<GlyphSampler.dotsDown {
                    for x in 0..<GlyphSampler.dotsAcross where bits & (1 << GlyphSampler.dotBit[y][x]) != 0 {
                        out.append(CGRect(x: origin.x + CGFloat(column * GlyphSampler.dotsAcross + x) * pitch,
                                          y: origin.y + CGFloat(row * GlyphSampler.dotsDown + y) * pitch,
                                          width: dot, height: dot))
                    }
                }
            }
        }
        return out
    }

    /// Per cell, how much of the picture's light it holds, 0 to 1 (in a light theme, how
    /// much dark): the faint wash under the dots that keeps shapes readable at this size.
    static func wash(of rendering: GlyphRendering, in box: CGSize, lit: Bool) -> [(rect: CGRect, amount: Double)] {
        let origin = origin(columns: rendering.columns, rows: rendering.rows, in: box)
        var out: [(CGRect, Double)] = []
        for row in 0..<rendering.rows {
            for column in 0..<rendering.columns {
                let color = rendering.colors[row * rendering.columns + column]
                let light = 0.2126 * color.red + 0.7152 * color.green + 0.0722 * color.blue
                let amount = lit ? light : 1 - light
                guard amount > 0.02 else { continue }
                out.append((CGRect(x: origin.x + CGFloat(column) * cell.width, y: origin.y + CGFloat(row) * cell.height,
                                   width: cell.width, height: cell.height), amount))
            }
        }
        return out
    }

    /// A run with no frames: a short dotted rule across the middle of the box.
    static func emptyMark(in box: CGSize) -> [CGRect] {
        let count = 7
        let left = ((box.width - CGFloat(count - 1) * pitch - dot) / 2).rounded(.down)
        let top = ((box.height - dot) / 2).rounded(.down)
        return (0..<count).map { CGRect(x: left + CGFloat($0) * pitch, y: top, width: dot, height: dot) }
    }
}

/// The thumbnails held, least recently used first out, bounded in bytes (the frame cache
/// gotcha: a count says nothing about memory). A thumbnail is a few KB, so the default
/// holds several hundred runs.
struct ThumbnailCache: Sendable {
    let capacityBytes: Int
    private(set) var totalBytes = 0
    private var entries: [String: RunThumbnail] = [:]
    /// Least recently used first.
    private var order: [String] = []

    init(capacityBytes: Int = 2 * 1024 * 1024) {
        self.capacityBytes = capacityBytes
    }

    var count: Int { entries.count }
    var runIds: [String] { order }

    /// The run's thumbnail, without counting as a use (views read it while drawing).
    func peek(_ runId: String) -> RunThumbnail? { entries[runId] }

    mutating func touch(_ runId: String) {
        guard entries[runId] != nil, order.last != runId else { return }
        order.removeAll { $0 == runId }
        order.append(runId)
    }

    /// Holds `thumbnail` for the run, replacing an older one, then drops the least
    /// recently used until the bytes fit. Returns the runs dropped. One thumbnail larger
    /// than the whole bound is not held.
    @discardableResult
    mutating func insert(_ thumbnail: RunThumbnail, for runId: String) -> [String] {
        if let old = entries.removeValue(forKey: runId) {
            totalBytes -= old.bytes
            order.removeAll { $0 == runId }
        }
        guard thumbnail.bytes <= capacityBytes else { return [] }
        entries[runId] = thumbnail
        order.append(runId)
        totalBytes += thumbnail.bytes
        var dropped: [String] = []
        while totalBytes > capacityBytes, let oldest = order.first {
            order.removeFirst()
            if let gone = entries.removeValue(forKey: oldest) { totalBytes -= gone.bytes }
            dropped.append(oldest)
        }
        return dropped
    }
}

/// Fetches and holds the runs' thumbnails for the list. Rows ask (`request`) as they
/// appear and when their run's last frame changes; nothing polls. A run is fetched once
/// per session per frame: a row coming back into view, a list re-read or a resync with
/// the same `lastFrame` fetches nothing, and a failed frame is not tried again until a
/// newer one is named. A live run's newer frames refetch at most every `refreshInterval`,
/// the newest one winning. At most `concurrency` fetches run at once. JPEGs are decoded and
/// sampled off the main actor; only the small rendering comes back to it.
@Observable
@MainActor
final class RunThumbnails {
    typealias Fetch = @Sendable (_ runId: String, _ file: String) async throws -> Data
    typealias Sample = @Sendable (_ jpeg: Data, _ file: String) -> RunThumbnail?

    /// Bumped when a thumbnail lands or is dropped: what a row's drawing depends on.
    private(set) var revision = 0

    @ObservationIgnored private var cache: ThumbnailCache
    /// The newest frame asked for, per run.
    @ObservationIgnored private var wanted: [String: Frame] = [:]
    /// Runs waiting for a fetch slot, oldest request first.
    @ObservationIgnored private var queue: [String] = []
    @ObservationIgnored private var running: [String: String] = [:]
    /// Runs waiting out `refreshInterval` since their last fetch.
    @ObservationIgnored private var cooling: [String: Task<Void, Never>] = [:]
    @ObservationIgnored private var fetchedAt: [String: Date] = [:]
    /// The frame whose fetch or sampling failed, per run: not tried again.
    @ObservationIgnored private var failed: [String: String] = [:]
    /// Every fetch started, for tests (the no-storm rule).
    @ObservationIgnored private(set) var fetchCount = 0

    let refreshInterval: TimeInterval
    let concurrency: Int
    private let fetch: Fetch
    private let sample: Sample

    init(
        capacityBytes: Int = 2 * 1024 * 1024,
        refreshInterval: TimeInterval = RunThumbnails.liveRefresh,
        concurrency: Int = 4,
        fetch: @escaping Fetch,
        sample: @escaping Sample = { ThumbnailSampler.make(jpeg: $0, file: $1) }
    ) {
        cache = ThumbnailCache(capacityBytes: capacityBytes)
        self.refreshInterval = refreshInterval
        self.concurrency = max(1, concurrency)
        self.fetch = fetch
        self.sample = sample
    }

    /// The run's thumbnail, possibly from an older frame while a newer one loads.
    func thumbnail(_ runId: String) -> RunThumbnail? {
        _ = revision
        return cache.peek(runId)
    }

    var heldBytes: Int { cache.totalBytes }
    var heldRuns: [String] { cache.runIds }

    /// A row shows `runId` with `frame` as its last frame. Cheap and idempotent.
    func request(_ runId: String, frame: Frame?) {
        guard let frame, !frame.file.isEmpty else { return }
        cache.touch(runId)
        if cache.peek(runId)?.file == frame.file || failed[runId] == frame.file { return }
        wanted[runId] = frame
        if running[runId] == frame.file || queue.contains(runId) || cooling[runId] != nil { return }
        if let last = fetchedAt[runId], cache.peek(runId) != nil {
            let wait = refreshInterval - Date().timeIntervalSince(last)
            if wait > 0 {
                cooling[runId] = Task { [weak self] in
                    try? await Task.sleep(for: .seconds(wait))
                    self?.cooled(runId)
                }
                return
            }
        }
        enqueue(runId)
    }

    /// The daemon answered and refused (the frame is gone): that frame is not asked for
    /// again. Anything else (nothing answered, a cancel) may be tried on the next request.
    nonisolated static func givesUp(on error: Error) -> Bool {
        if case DaemonError.status? = error as? DaemonError { return true }
        return false
    }

    /// How often a live run's row may take a newer frame: the list re-draws and refetches
    /// no faster than this, although the recorder captures every 2 s.
    nonisolated static let liveRefresh: TimeInterval = 5

    /// Whether a frame event moves the list's `lastFrame` on: always from none, else only
    /// once `liveRefresh` has passed since the one held, so a live run does not redraw the
    /// list on every capture.
    nonisolated static func advances(_ held: Frame?, to frame: Frame) -> Bool {
        guard let held else { return true }
        return frame.file != held.file && frame.at.timeIntervalSince(held.at) >= liveRefresh
    }

    private func cooled(_ runId: String) {
        cooling[runId] = nil
        enqueue(runId)
    }

    private func enqueue(_ runId: String) {
        guard !queue.contains(runId) else { return }
        queue.append(runId)
        pump()
    }

    private func pump() {
        while running.count < concurrency, !queue.isEmpty {
            let runId = queue.removeFirst()
            guard let frame = wanted[runId], running[runId] == nil,
                  cache.peek(runId)?.file != frame.file, failed[runId] != frame.file else { continue }
            running[runId] = frame.file
            fetchedAt[runId] = Date()
            fetchCount += 1
            let fetch = fetch, sample = sample
            Task { [weak self] in
                do {
                    let data = try await fetch(runId, frame.file)
                    let made = await Task.detached(priority: .utility) { sample(data, frame.file) }.value
                    self?.landed(runId, file: frame.file, made, giveUp: true)
                } catch {
                    self?.landed(runId, file: frame.file, nil, giveUp: RunThumbnails.givesUp(on: error))
                }
            }
        }
    }

    private func landed(_ runId: String, file: String, _ thumbnail: RunThumbnail?, giveUp: Bool) {
        running[runId] = nil
        if let thumbnail {
            cache.insert(thumbnail, for: runId)
            revision += 1
        } else if giveUp {
            failed[runId] = file
        } else {
            // Not reached (offline, cancelled): the row's next appearance asks again.
            wanted[runId] = nil
            fetchedAt[runId] = nil
        }
        // A newer frame named while this one was on its way.
        if let newer = wanted[runId], newer.file != file { request(runId, frame: newer) }
        pump()
    }
}
