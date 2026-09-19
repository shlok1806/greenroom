import Foundation

/// The Screen tab's own state, apart from any view: which frames it knows
/// about, where the scrubber sits, how fast it plays and whether it is
/// following the newest frame. A pure value type (ADR 0008, "Companion"), so
/// its rules can be tested without a window or a daemon.
struct PlayerModel: Equatable, Sendable {
    enum Speed: Double, CaseIterable, Sendable {
        case normal = 1
        case fast = 4
    }

    var frames: [Frame] = []
    var index: Int = 0
    var speed: Speed = .normal
    /// Follows the newest frame as it arrives. Scrubbing, seeking or reaching
    /// the end of a finished run's recording turns this off.
    var live: Bool = true
    var playing: Bool = false

    /// Time left over from the last `advance`, in seconds, so playback stays
    /// smooth across ticks instead of rounding down every one of them.
    private var remainder: TimeInterval = 0

    init() {}

    var current: Frame? {
        guard frames.indices.contains(index) else { return nil }
        return frames[index]
    }

    var isAtEnd: Bool { frames.isEmpty || index >= frames.count - 1 }

    /// Moves the index forward when enough sped-up time has passed to reach
    /// the next frame's timestamp, carrying over whatever is left.
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

    /// Jumps to the first frame at or after `step`, or the last frame if none
    /// reaches it. A jump into the recording's past is no longer live.
    mutating func seek(toStep step: Int) {
        guard !frames.isEmpty else { return }
        remainder = 0
        live = false
        index = frames.firstIndex { $0.step >= step } ?? frames.count - 1
    }

    /// A new frame arrived on the stream. It is always appended (the store
    /// already dedupes by file); the index only follows it while live.
    mutating func append(_ frame: Frame) {
        frames.append(frame)
        if live {
            index = frames.count - 1
            remainder = 0
        }
    }
}
