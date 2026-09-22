import Foundation

/// Maps between frame indices and x positions on the Screen tab's track.
struct FrameTimeline: Equatable, Sendable {
    /// The first frame recorded after the step number changed.
    struct Tick: Equatable, Sendable {
        var x: Double
        var step: Int
        var index: Int
    }

    let count: Int
    let width: Double

    init(count: Int, width: Double) {
        self.count = max(0, count)
        self.width = max(0, width)
    }

    var isEmpty: Bool { count == 0 }

    var lastIndex: Int { max(0, count - 1) }

    /// A single frame sits at the left edge.
    func x(of index: Int) -> Double {
        guard count > 1 else { return 0 }
        let clamped = min(max(index, 0), lastIndex)
        return width * Double(clamped) / Double(lastIndex)
    }

    /// Clamped, so a drag off either end still lands on a frame.
    func index(atX x: Double) -> Int {
        guard count > 1, width > 0 else { return 0 }
        let fraction = min(max(x / width, 0), 1)
        return Int((fraction * Double(lastIndex)).rounded())
    }

    /// Measured from the first frame, not run creation: recording starts when
    /// the machine is ready.
    static func offset(_ frames: [Frame], at index: Int) -> TimeInterval {
        guard let first = frames.first, frames.indices.contains(index) else { return 0 }
        return max(0, frames[index].at.timeIntervalSince(first.at))
    }

    static func duration(_ frames: [Frame]) -> TimeInterval {
        guard let first = frames.first, let last = frames.last else { return 0 }
        return max(0, last.at.timeIntervalSince(first.at))
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
