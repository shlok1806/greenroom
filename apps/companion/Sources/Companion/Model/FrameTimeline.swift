import Foundation

/// Maps between frame indices and x positions on the Screen stage's track.
struct FrameTimeline: Equatable, Sendable {
    /// The first frame recorded after the step number changed.
    struct Tick: Equatable, Sendable {
        var x: Double
        var step: Int
        var index: Int
    }

    let count: Int
    let width: Double
    /// Where each frame sits, 0 to 1. Nil spaces frames evenly.
    private let positions: [Double]?

    init(count: Int, width: Double) {
        self.count = max(0, count)
        self.width = max(0, width)
        positions = nil
    }

    /// Spaced by recorded time, with stretches where no step began squeezed to
    /// `idleWeight` of their length, so a long idle tail does not push every step into
    /// the first few points of the track.
    init(frames: [Frame], width: Double, activeFor: TimeInterval = 20, idleWeight: Double = 0.1) {
        count = frames.count
        self.width = max(0, width)
        guard frames.count > 1 else {
            positions = nil
            return
        }
        var cumulative = [0.0]
        var lastChange = frames[0].at
        for index in frames.indices.dropFirst() {
            let frame = frames[index]
            if frame.step != frames[index - 1].step { lastChange = frame.at }
            let gap = max(0, frame.at.timeIntervalSince(frames[index - 1].at))
            let active = frame.at.timeIntervalSince(lastChange) <= activeFor
            // A floor keeps every frame reachable even at equal timestamps.
            cumulative.append(cumulative[index - 1] + max(active ? gap : gap * idleWeight, 0.001))
        }
        let total = cumulative[cumulative.count - 1]
        positions = cumulative.map { $0 / total }
    }

    var isEmpty: Bool { count == 0 }

    var lastIndex: Int { max(0, count - 1) }

    /// A single frame sits at the left edge.
    func x(of index: Int) -> Double {
        guard count > 1 else { return 0 }
        let clamped = min(max(index, 0), lastIndex)
        if let positions { return width * positions[clamped] }
        return width * Double(clamped) / Double(lastIndex)
    }

    /// Clamped, so a drag off either end still lands on a frame.
    func index(atX x: Double) -> Int {
        guard count > 1, width > 0 else { return 0 }
        let fraction = min(max(x / width, 0), 1)
        guard let positions else { return Int((fraction * Double(lastIndex)).rounded()) }
        // The nearest frame, by binary search over the positions.
        var low = 0, high = lastIndex
        while low < high {
            let mid = (low + high) / 2
            if positions[mid] < fraction { low = mid + 1 } else { high = mid }
        }
        if low > 0, fraction - positions[low - 1] < positions[low] - fraction { return low - 1 }
        return low
    }

    /// Measured from `origin`, the run's start, so the player and the header agree.
    static func offset(_ frames: [Frame], at index: Int, from origin: Date? = nil) -> TimeInterval {
        guard let first = frames.first, frames.indices.contains(index) else { return 0 }
        return max(0, frames[index].at.timeIntervalSince(origin ?? first.at))
    }

    static func duration(_ frames: [Frame]) -> TimeInterval {
        guard let first = frames.first, let last = frames.last else { return 0 }
        return max(0, last.at.timeIntervalSince(first.at))
    }

    /// The first frame at or after `step`, else the last: what "show step N" lands on.
    static func index(ofStep step: Int, in frames: [Frame]) -> Int? {
        guard !frames.isEmpty else { return nil }
        return frames.firstIndex { $0.step >= step } ?? frames.count - 1
    }

    /// A mark on the track for a step worth finding: one that failed, or one a verdict cites.
    struct Mark: Equatable, Sendable {
        enum Kind: Equatable, Sendable {
            case failure
            case evidence(verdict: String?)
        }

        var x: Double
        var step: Int
        var kind: Kind
    }

    /// Marks closer than `minGap` points, drawn as one: the first stands for the group.
    struct MarkGroup: Equatable, Sendable {
        var marks: [Mark]
        var first: Mark { marks[0] }
    }

    /// Evidence sorts ahead of failure within a group, so a cited step is never hidden
    /// behind an error dot.
    static func cluster(_ marks: [Mark], minGap: Double) -> [MarkGroup] {
        var groups: [MarkGroup] = []
        for mark in marks.sorted(by: { $0.x < $1.x }) {
            if let last = groups.last, let anchor = last.marks.last, mark.x - anchor.x < minGap {
                var merged = last.marks + [mark]
                merged.sort { ($0.kind == .failure ? 1 : 0) < ($1.kind == .failure ? 1 : 0) }
                groups[groups.count - 1] = MarkGroup(marks: merged)
            } else {
                groups.append(MarkGroup(marks: [mark]))
            }
        }
        return groups
    }

    /// Evidence wins over failure when both land on one step; one mark per step.
    func marks(for frames: [Frame], failed: Set<Int>, evidence: Set<Int>, verdict: String?) -> [Mark] {
        guard count > 1, width > 0 else { return [] }
        var out: [Mark] = []
        for step in failed.union(evidence).sorted() {
            guard let index = FrameTimeline.index(ofStep: step, in: frames) else { continue }
            let kind: Mark.Kind = evidence.contains(step) ? .evidence(verdict: verdict) : .failure
            out.append(Mark(x: x(of: index), step: step, kind: kind))
        }
        return out
    }

    /// A step's tick on the scrubber: where it begins, and whether it errored.
    struct StepTick: Equatable, Sendable {
        var x: Double
        var step: Int
        var failed: Bool
    }

    /// The scrubber's ticks: one where each step begins, and one for every step that
    /// errored even where no frame boundary shows it (steps quicker than the capture
    /// interval), drawn in the failure role. Plain ticks closer than `minGap` points to
    /// one already kept are dropped, so hundreds of steps do not merge into a grey band;
    /// an errored tick is never dropped for a plain one, and replaces a plain one it lands
    /// on. Errored ticks closer than `minGap` to each other merge into the first.
    func stepTicks(for frames: [Frame], failed: Set<Int>, minGap: Double = 5) -> [StepTick] {
        guard count > 1, width > 0, !frames.isEmpty else { return [] }
        var candidates = ticks(for: frames, minGap: 0).map { StepTick(x: $0.x, step: $0.step, failed: failed.contains($0.step)) }
        let placed = Set(candidates.map(\.step))
        for step in failed.subtracting(placed).sorted() {
            guard let index = FrameTimeline.index(ofStep: step, in: frames) else { continue }
            candidates.append(StepTick(x: x(of: index), step: step, failed: true))
        }
        // Errored first, so a plain tick never claims the room an errored one needs.
        var kept: [StepTick] = []
        for tick in candidates.filter(\.failed).sorted(by: { ($0.x, $0.step) < ($1.x, $1.step) }) {
            if let last = kept.last, tick.x - last.x < minGap { continue }
            kept.append(tick)
        }
        for tick in candidates.filter({ !$0.failed }) {
            if kept.contains(where: { abs($0.x - tick.x) < minGap }) { continue }
            kept.append(tick)
        }
        return kept.sorted { ($0.x, $0.step) < ($1.x, $1.step) }
    }

    /// Where each step begins. Ticks closer than `minGap` points are dropped so
    /// hundreds of steps do not merge into a grey band.
    func ticks(for frames: [Frame], minGap: Double = 5) -> [Tick] {
        guard count > 1, width > 0, !frames.isEmpty else { return [] }
        var out: [Tick] = []
        var previousStep = frames[0].step
        for index in frames.indices.dropFirst() {
            let step = frames[index].step
            guard step != previousStep else { continue }
            previousStep = step
            let at = x(of: index)
            if let last = out.last, at - last.x < minGap { continue }
            out.append(Tick(x: at, step: step, index: index))
        }
        return out
    }
}
