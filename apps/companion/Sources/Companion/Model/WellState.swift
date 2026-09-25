import Foundation

/// What the screen's well says while it has no picture (design spec, States): the machine
/// booting or the live screen connecting (the loader), the recording being read or a frame
/// loading (the tick), or why there is nothing to show. Pure; `WellStateTests`.
enum WellState: Equatable, Sendable {
    /// The machine is starting: the loader, timed from the run's start.
    case booting
    /// The live screen is connecting and there is no recording to show meanwhile.
    case connecting
    /// The frame list has not been read yet ("never blank": the stage ticks).
    case reading
    /// There are frames; the one at the playhead is on its way.
    case loadingFrame
    /// The machine failed while booting, in the daemon's words when it gave any.
    case failedToStart(String?)
    /// A ready machine that has not been captured yet.
    case waitingForFirstFrame
    /// The run is over and nothing was captured.
    case noFrames

    /// `frames` is nil until the run's frame list has been read.
    static func of(facts: RunFacts, frames: [Frame]?, connecting: Bool) -> WellState {
        if facts.phase == .booting { return .booting }
        if let frames, !frames.isEmpty { return .loadingFrame }
        if connecting { return .connecting }
        guard frames != nil else { return .reading }
        if case .failed(let reason) = facts.phase { return .failedToStart(reason) }
        return facts.machineReady ? .waitingForFirstFrame : .noFrames
    }
}
