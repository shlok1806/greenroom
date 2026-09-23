import Foundation

/// The Screen stage's player state (ADR 0008), kept out of the view so it is testable.
struct PlayerModel: Equatable, Sendable {
    enum Speed: Double, CaseIterable, Sendable {
        case normal = 1
        case fast = 4
    }

    var frames: [Frame] = []
    var index: Int = 0
    var speed: Speed = .normal
    /// Follows the newest frame as it arrives.
    var live = true
    var playing = false

    /// Recorded time carried between `advance` calls so ticks do not round down.
    private var remainder: TimeInterval = 0

    var current: Frame? {
        guard frames.indices.contains(index) else { return nil }
        return frames[index]
    }

    /// Whether a finished image load is still wanted. Frame files are named by
    /// capture time, so this also rejects a load from a previous run.
    func shows(_ file: String) -> Bool { current?.file == file }

    var isAtEnd: Bool { frames.isEmpty || index >= frames.count - 1 }

    /// Advances by recorded time (`elapsed` times `speed`), frame by frame.
    mutating func advance(by elapsed: TimeInterval) {
        guard playing, elapsed > 0, frames.count > 1, index < frames.count - 1 else { return }
        remainder += elapsed * speed.rawValue
        while index < frames.count - 1 {
            let gap = frames[index + 1].at.timeIntervalSince(frames[index].at)
            guard gap <= 0 || remainder >= gap else { break }
            remainder -= max(gap, 0)
            index += 1
        }
        if isAtEnd {
            remainder = 0
            if !live { playing = false }
        }
    }

    /// The first frame at or after `step`, else the last. Leaves live mode.
    mutating func seek(toStep step: Int) {
        guard !frames.isEmpty else { return }
        remainder = 0
        live = false
        index = FrameTimeline.index(ofStep: step, in: frames) ?? 0
    }

    /// The store already dedupes by file; the index follows only while live.
    mutating func append(_ frame: Frame) {
        frames.append(frame)
        if live {
            index = frames.count - 1
            remainder = 0
        }
    }
}
