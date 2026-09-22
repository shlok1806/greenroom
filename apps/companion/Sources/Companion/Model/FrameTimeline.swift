import Foundation

/// The arithmetic behind the Screen tab's timeline: where a frame sits on a
/// track of a given width, which frame a point on that track means, and where
/// the steps fall along it.
///
/// A run's recording is thousands of frames wide and a track is a few hundred
/// points wide, so every one of those questions is a rounding decision. They
/// live here, as a pure value type, for the same reason `ScreenGeometry` does:
/// a timeline that lands one frame off is a bug nobody can see in a
/// screenshot, and one that is tested cannot drift.
struct FrameTimeline: Equatable, Sendable {
    /// A step boundary drawn on the track: the first frame recorded after the
    /// step number changed.
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

    /// The last index a frame can have.
    var lastIndex: Int { max(0, count - 1) }

    /// Where the playhead for `index` sits, in points from the left. A
    /// recording with one frame has one position, the left edge, rather than a
    /// division by zero.
    func x(of index: Int) -> Double {
        guard count > 1 else { return 0 }
        let clamped = min(max(index, 0), lastIndex)
        return width * Double(clamped) / Double(lastIndex)
    }

    /// Which frame a point on the track means. Points off either end mean the
    /// nearest frame, because a drag that leaves the track still has to land
    /// somewhere.
    func index(atX x: Double) -> Int {
        guard count > 1, width > 0 else { return 0 }
        let fraction = min(max(x / width, 0), 1)
        return Int((fraction * Double(lastIndex)).rounded())
    }

    /// How far into the recording a frame is. Measured from the first frame
    /// rather than from the run's creation: the recording starts when the
    /// machine is ready, which is not when the run was made, and a timeline
    /// that starts at 04:31 reads as a fault.
    static func offset(_ frames: [Frame], at index: Int) -> TimeInterval {
        guard let first = frames.first, frames.indices.contains(index) else { return 0 }
        return max(0, frames[index].at.timeIntervalSince(first.at))
    }

    /// The whole recording's length.
    static func duration(_ frames: [Frame]) -> TimeInterval {
        guard let first = frames.first, let last = frames.last else { return 0 }
        return max(0, last.at.timeIntervalSince(first.at))
    }

    /// Where each step begins along the track.
    ///
    /// A 2385-frame run holds a few hundred steps, and drawn naively they
    /// merge into a grey band that says nothing. Ticks closer together than
    /// `minGap` points are dropped, so what is left is a readable map of where
    /// the work happened rather than a smear.
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
