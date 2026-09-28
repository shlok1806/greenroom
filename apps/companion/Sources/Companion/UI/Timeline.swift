import Foundation

/// The recording as a timeline under the picture (redesign 7): where each step, failure, key
/// frame and human takeover sits in time, where the screen was idle or sent no picture, and
/// how a point on the bar maps to a moment and a frame. Pure, with a test per rule in
/// `TimelineTests`.
///
/// Time is measured in seconds from the run's start. The bar is piecewise linear in time,
/// except that a stretch where nothing happened for longer than `idleAfter` is squeezed to a
/// short length (and drawn as idle), so a run whose Mac sat for an hour after four minutes of
/// work still spreads its steps across the bar. Every figure the bar shows (elapsed, total)
/// is real time.
struct RecordingTimeline: Equatable, Sendable {
    /// A point worth finding on the bar.
    struct Mark: Equatable, Sendable, Identifiable {
        enum Kind: Equatable, Sendable {
            /// An ordinary step.
            case step
            /// A step that errored, or the proof of a check that failed.
            case failure
            /// A step the person made while holding the screen.
            case human
            /// The picture a check was proven on (the check passed or is not answered).
            case keyFrame
        }

        var step: Int
        var kind: Kind
        /// Seconds from the start.
        var at: TimeInterval
        /// The 1-based number of the check this step proves, if it proves one.
        var check: Int?

        var id: Int { step }
    }

    /// A stretch the bar draws differently.
    struct Span: Equatable, Sendable {
        enum Kind: Equatable, Sendable {
            /// Nothing happened: squeezed on the bar.
            case idle
            /// The screen sent no picture for much longer than its usual interval.
            case noPicture
        }

        var from: TimeInterval
        var to: TimeInterval
        var kind: Kind

        var length: TimeInterval { to - from }
    }

    /// A stretch of the bar: `from`...`to` seconds drawn over `x0`...`x1` (0 to 1).
    struct Segment: Equatable, Sendable {
        var from: TimeInterval
        var to: TimeInterval
        var x0: Double
        var x1: Double
    }

    /// When the run began: second 0.
    var origin: Date
    /// The whole run in seconds, to now while it is live.
    var duration: TimeInterval
    /// Recorded frames, oldest first, with their second.
    var frames: [Frame]
    var frameTimes: [TimeInterval]
    /// One mark per step, oldest first.
    var marks: [Mark]
    var spans: [Span]
    var segments: [Segment]
    /// The run is still going: the bar grows and ends in the Live pin.
    var live: Bool

    /// A quiet stretch longer than this is squeezed.
    static let idleAfter: TimeInterval = 45
    /// A frame gap is "no picture" past this many times the usual interval (and 10 s).
    static let noPictureFactor = 4.0

    static let empty = RecordingTimeline(origin: .epoch, duration: 0, frames: [], frameTimes: [], marks: [], spans: [],
                                         segments: [], live: false)

    var isEmpty: Bool { frames.isEmpty && marks.isEmpty }

    // MARK: - Building

    /// The timeline of a run's record. `start` is the run's start when the daemon knows it;
    /// `live` extends the bar to `now`.
    static func make(frames: [Frame], steps: [Step], checks: [SummaryCheck], start: Date?, live: Bool, now: Date) -> RecordingTimeline {
        let frames = frames.sorted { ($0.at, $0.file) < ($1.at, $1.file) }
        let steps = steps.sorted { $0.seq < $1.seq }
        let firstEvent = [frames.first?.at, steps.first?.at].compactMap { $0 }.min()
        guard let firstEvent else {
            guard live, let start, start > .epoch else { return .empty }
            let duration = max(0, now.timeIntervalSince(start))
            return RecordingTimeline(origin: start, duration: duration, frames: [], frameTimes: [], marks: [], spans: [],
                                     segments: [Segment(from: 0, to: max(duration, 1), x0: 0, x1: 1)], live: true)
        }
        let origin = start.map { $0 > .epoch && $0 <= firstEvent ? $0 : firstEvent } ?? firstEvent
        let lastFrame = frames.last.map { $0.at.timeIntervalSince(origin) } ?? 0
        let lastStep = steps.map { $0.at.timeIntervalSince(origin) + Double($0.durationMs) / 1000 }.max() ?? 0
        let recorded = max(lastFrame, lastStep)
        let duration = live ? max(recorded, now.timeIntervalSince(origin)) : recorded
        let frameTimes = frames.map { max(0, $0.at.timeIntervalSince(origin)) }

        // Marks: one per step, failure over human over key frame over plain.
        var proofs: [Int: (number: Int, failed: Bool)] = [:]
        for (index, check) in checks.enumerated() {
            guard let step = check.picture?.step ?? check.step else { continue }
            let failed = check.state == .fail
            if let held = proofs[step], held.failed || !failed { continue }
            proofs[step] = (index + 1, failed)
        }
        let marks: [Mark] = steps.map { step in
            let at = max(0, step.at.timeIntervalSince(origin))
            let proof = proofs[step.seq]
            let kind: Mark.Kind
            if step.error != nil || proof?.failed == true {
                kind = .failure
            } else if isHuman(step) {
                kind = .human
            } else if proof != nil {
                kind = .keyFrame
            } else {
                kind = .step
            }
            return Mark(step: step.seq, kind: kind, at: at, check: proof?.number)
        }

        // Idle: a quiet stretch between one step's end (or the start) and the next step (or the end).
        var spans: [Span] = []
        var activity = steps.map { (start: max(0, $0.at.timeIntervalSince(origin)),
                                    end: max(0, $0.at.timeIntervalSince(origin)) + Double($0.durationMs) / 1000) }
        if activity.isEmpty { activity = [(0, 0)] }
        var busyUntil: TimeInterval = 0
        for item in activity {
            if item.start - busyUntil > idleAfter {
                spans.append(Span(from: busyUntil + idleAfter / 3, to: item.start - idleAfter / 3, kind: .idle))
            }
            busyUntil = max(busyUntil, item.end)
        }
        if duration - busyUntil > idleAfter {
            spans.append(Span(from: busyUntil + idleAfter / 3, to: duration - (live ? idleAfter / 3 : 0), kind: .idle))
        }

        // No picture: a frame gap far past the usual interval, inside the recorded stretch.
        if frameTimes.count > 2 {
            let gaps = zip(frameTimes.dropFirst(), frameTimes).map { $0 - $1 }.sorted()
            let usual = gaps[gaps.count / 2]
            let threshold = max(10, usual * noPictureFactor)
            for i in frameTimes.indices.dropFirst() where frameTimes[i] - frameTimes[i - 1] > threshold {
                let span = Span(from: frameTimes[i - 1], to: frameTimes[i], kind: .noPicture)
                // A gap inside an idle stretch is idle (the recorder only writes on change).
                if !spans.contains(where: { $0.kind == .idle && $0.from <= span.from + 1 && $0.to >= span.to - 1 }) {
                    spans.append(span)
                }
            }
        }
        spans.sort { $0.from < $1.from }

        return RecordingTimeline(origin: origin, duration: duration, frames: frames, frameTimes: frameTimes, marks: marks,
                                 spans: spans, segments: segments(duration: duration, idle: spans.filter { $0.kind == .idle }),
                                 live: live)
    }

    /// Whether the person made this step while holding the screen (`holder: human`).
    static func isHuman(_ step: Step) -> Bool {
        step.input?["holder"]?.stringValue == "human"
    }

    /// The bar's stretches: active time at its length, each idle stretch squeezed to at most
    /// `max(4 s, 6% of the active time)`, so it reads as a short break.
    static func segments(duration: TimeInterval, idle: [Span]) -> [Segment] {
        let total = max(duration, 0.001)
        var pieces: [(from: TimeInterval, to: TimeInterval, idle: Bool)] = []
        var cursor: TimeInterval = 0
        for span in idle.sorted(by: { $0.from < $1.from }) where span.to > cursor {
            let from = max(cursor, span.from)
            if from > cursor { pieces.append((cursor, from, false)) }
            pieces.append((from, min(span.to, total), true))
            cursor = min(span.to, total)
        }
        if cursor < total { pieces.append((cursor, total, false)) }
        let active = pieces.filter { !$0.idle }.reduce(0) { $0 + ($1.to - $1.from) }
        let squeezed = max(4, active * 0.06)
        let widths = pieces.map { $0.idle ? min($0.to - $0.from, squeezed) : $0.to - $0.from }
        let sum = max(widths.reduce(0, +), 0.001)
        var x = 0.0
        var out: [Segment] = []
        for (piece, width) in zip(pieces, widths) {
            let next = x + width / sum
            out.append(Segment(from: piece.from, to: piece.to, x0: x, x1: next))
            x = next
        }
        if !out.isEmpty { out[out.count - 1].x1 = 1 }
        return out
    }

    // MARK: - Mapping

    /// Where a second sits on the bar, 0 to 1.
    func fraction(of seconds: TimeInterval) -> Double {
        guard duration > 0 else { return live ? 1 : 0 }
        let s = min(max(seconds, 0), duration)
        guard let segment = segments.first(where: { s <= $0.to }) ?? segments.last else { return s / duration }
        let length = segment.to - segment.from
        guard length > 0 else { return segment.x0 }
        return segment.x0 + (segment.x1 - segment.x0) * (s - segment.from) / length
    }

    /// The second at a point on the bar.
    func seconds(atFraction fraction: Double) -> TimeInterval {
        let f = min(max(fraction, 0), 1)
        guard let segment = segments.first(where: { f <= $0.x1 }) ?? segments.last else { return f * duration }
        let width = segment.x1 - segment.x0
        guard width > 0 else { return segment.from }
        return segment.from + (segment.to - segment.from) * (f - segment.x0) / width
    }

    /// The frame showing at a second: the newest recorded at or before it, else the first.
    func frameIndex(at seconds: TimeInterval) -> Int? {
        guard !frameTimes.isEmpty else { return nil }
        var low = 0, high = frameTimes.count
        while low < high {
            let mid = (low + high) / 2
            if frameTimes[mid] <= seconds { low = mid + 1 } else { high = mid }
        }
        return max(0, low - 1)
    }

    func frame(at seconds: TimeInterval) -> Frame? { frameIndex(at: seconds).map { frames[$0] } }

    /// The step under a second: the newest that began at or before it.
    func step(at seconds: TimeInterval) -> Int? {
        marks.last { $0.at <= seconds + 0.0005 }?.step ?? marks.first?.step
    }

    /// A step's second: where it began.
    func seconds(ofStep step: Int) -> TimeInterval? {
        if let mark = marks.first(where: { $0.step == step }) { return mark.at }
        // A step with no record (the frame's own number): its first frame.
        if let index = frames.firstIndex(where: { $0.step >= step }) { return frameTimes[index] }
        return nil
    }

    /// The second of a frame, by file.
    func seconds(ofFrame file: String) -> TimeInterval? {
        frames.firstIndex { $0.file == file }.map { frameTimes[$0] }
    }

    /// One frame along from the one at `seconds`: Left and Right.
    func stepFrame(from seconds: TimeInterval, by delta: Int) -> TimeInterval? {
        guard let index = frameIndex(at: seconds) else { return nil }
        let next = min(max(index + delta, 0), frameTimes.count - 1)
        return frameTimes[next]
    }

    /// The failures in order.
    var failures: [Mark] { marks.filter { $0.kind == .failure } }

    /// The next failure after `seconds` (n), wrapping to the first; nil when there is none.
    func nextFailure(after seconds: TimeInterval?) -> Mark? {
        let all = failures
        guard let seconds else { return all.first }
        return all.first { $0.at > seconds + 0.0005 } ?? all.first
    }

    /// The failure before `seconds` (shift-n), wrapping to the last.
    func previousFailure(before seconds: TimeInterval?) -> Mark? {
        let all = failures
        guard let seconds else { return all.last }
        return all.last { $0.at < seconds - 0.0005 } ?? all.last
    }

    /// Where the playhead lands after `elapsed` real seconds at `speed`, skipping idle
    /// stretches; nil once it passes the end.
    func advance(from seconds: TimeInterval, by elapsed: TimeInterval, speed: Double) -> TimeInterval? {
        var next = seconds + elapsed * speed
        if let idle = spans.first(where: { $0.kind == .idle && next > $0.from && next < $0.to }) {
            next = idle.to
        }
        return next > duration ? nil : next
    }

    /// The mark nearest a point, within `tolerance` of the bar (0 to 1); failures win ties.
    func mark(near fraction: Double, tolerance: Double) -> Mark? {
        var best: (Mark, Double)?
        for mark in marks {
            let d = abs(self.fraction(of: mark.at) - fraction)
            guard d <= tolerance else { continue }
            let score = d - (mark.kind == .failure ? tolerance / 2 : 0)
            if best == nil || score < best!.1 { best = (mark, score) }
        }
        return best?.0
    }

    /// The span under a second, if any.
    func span(at seconds: TimeInterval) -> Span? {
        spans.first { seconds >= $0.from && seconds <= $0.to }
    }

    /// Marks to draw at a bar `width` points wide: every failure, human mark and key frame,
    /// then plain steps only where one does not sit within `minGap` points of a kept mark.
    func visibleMarks(width: Double, minGap: Double = 4) -> [Mark] {
        guard width > 0 else { return [] }
        let strong = marks.filter { $0.kind != .step }
        var kept = strong
        var xs = strong.map { fraction(of: $0.at) * width }.sorted()
        for mark in marks where mark.kind == .step {
            let x = fraction(of: mark.at) * width
            // Binary search for the nearest kept x.
            var low = 0, high = xs.count
            while low < high {
                let mid = (low + high) / 2
                if xs[mid] < x { low = mid + 1 } else { high = mid }
            }
            let near = [low - 1, low].filter { xs.indices.contains($0) }.map { abs(xs[$0] - x) }.min() ?? .infinity
            if near < minGap { continue }
            kept.append(mark)
            xs.insert(x, at: low)
        }
        return kept.sorted { ($0.at, $0.step) < ($1.at, $1.step) }
    }
}

/// How the bar reads in figures and words (hover text and the clock).
enum TimelineWords {
    /// "1:42 / 4:18".
    static func clock(_ seconds: TimeInterval, of total: TimeInterval) -> String {
        "\(Clock.elapsed(Int(seconds.rounded(.down)))) / \(Clock.elapsed(Int(total.rounded(.down))))"
    }

    /// One line for a mark: "Step 14 · Clicked 25%" with what makes it worth finding.
    static func label(_ mark: RecordingTimeline.Mark, steps: [Step], checks: [SummaryCheck]) -> String {
        let step = steps.first { $0.seq == mark.step }
        let phrase = step.map { StepSummary.phrase(for: $0, in: steps) } ?? "Step \(mark.step)"
        var line = "Step \(mark.step) · \(phrase)"
        if let error = step?.error {
            line += " · failed: \(error.split(whereSeparator: \.isNewline).first.map(String.init) ?? error)"
        } else if mark.kind == .human {
            line += " · you had control"
        }
        if let number = mark.check, checks.indices.contains(number - 1) {
            let check = checks[number - 1]
            let state = check.state == .fail ? "failed" : (check.state == .pass ? "passed" : "not answered")
            line += " · check \(number) \(state): \(check.text)"
        }
        return line
    }

    /// Hover words for a span.
    static func label(_ span: RecordingTimeline.Span) -> String {
        let length = Clock.elapsed(Int(span.length.rounded()))
        switch span.kind {
        case .idle: return "Nothing happened for \(length) (drawn short)"
        case .noPicture: return "The screen sent no picture for \(length)"
        }
    }
}
