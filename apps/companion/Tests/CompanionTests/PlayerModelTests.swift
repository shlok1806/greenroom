import XCTest

@testable import Companion

final class PlayerModelTests: XCTestCase {
    private func frame(_ step: Int, secondsFromStart: Double) -> Frame {
        Frame(at: Date(timeIntervalSince1970: secondsFromStart), file: "f\(step).jpg", step: step)
    }

    func testCurrentAndIsAtEndOnAnEmptyPlayer() {
        var player = PlayerModel()
        XCTAssertNil(player.current)
        XCTAssertTrue(player.isAtEnd)

        player.frames = [frame(1, secondsFromStart: 0)]
        XCTAssertEqual(player.current?.step, 1)
        XCTAssertTrue(player.isAtEnd)
    }

    func testAdvanceDoesNothingWhilePaused() {
        var player = PlayerModel()
        player.frames = [frame(1, secondsFromStart: 0), frame(2, secondsFromStart: 2)]
        player.playing = false
        player.advance(by: 5)
        XCTAssertEqual(player.index, 0)
    }

    func testAdvanceMovesForwardOnceEnoughTimeHasPassed() {
        var player = PlayerModel()
        player.frames = [
            frame(1, secondsFromStart: 0),
            frame(2, secondsFromStart: 2),
            frame(3, secondsFromStart: 4),
        ]
        player.playing = true

        player.advance(by: 1)
        XCTAssertEqual(player.index, 0, "one second is not yet the two seconds to frame 2")

        player.advance(by: 1)
        XCTAssertEqual(player.index, 1, "the two seconds together reach frame 2")

        player.advance(by: 2)
        XCTAssertEqual(player.index, 2)
        XCTAssertTrue(player.isAtEnd)
    }

    func testFastSpeedCoversTheSameGapInAQuarterTheWallTime() {
        var player = PlayerModel()
        player.frames = [frame(1, secondsFromStart: 0), frame(2, secondsFromStart: 4)]
        player.playing = true
        player.speed = .fast

        player.advance(by: 1)
        XCTAssertEqual(player.index, 1, "4x speed turns 1 wall second into 4 recorded seconds")
    }

    /// A finished run's recording stops playing at the last frame; a live
    /// run keeps `playing` on so a frame that arrives later is picked up.
    func testReachingTheEndStopsPlayingOnlyWhenNotLive() {
        var finished = PlayerModel()
        finished.frames = [frame(1, secondsFromStart: 0), frame(2, secondsFromStart: 1)]
        finished.playing = true
        finished.live = false
        finished.advance(by: 5)
        XCTAssertTrue(finished.isAtEnd)
        XCTAssertFalse(finished.playing)

        var live = PlayerModel()
        live.frames = [frame(1, secondsFromStart: 0), frame(2, secondsFromStart: 1)]
        live.playing = true
        live.live = true
        live.advance(by: 5)
        XCTAssertTrue(live.isAtEnd)
        XCTAssertTrue(live.playing)
    }

    func testSeekJumpsToTheFirstFrameAtOrAfterTheStepAndLeavesLive() {
        var player = PlayerModel()
        player.frames = [frame(1, secondsFromStart: 0), frame(5, secondsFromStart: 1), frame(9, secondsFromStart: 2)]
        player.live = true

        player.seek(toStep: 4)
        XCTAssertEqual(player.current?.step, 5)
        XCTAssertFalse(player.live)

        // A step past the last frame lands on the last frame.
        player.seek(toStep: 100)
        XCTAssertEqual(player.current?.step, 9)
    }

    func testSeekOnAnEmptyPlayerDoesNothing() {
        var player = PlayerModel()
        player.seek(toStep: 3)
        XCTAssertNil(player.current)
    }

    func testAppendFollowsWhileLiveAndHoldsPositionOtherwise() {
        var live = PlayerModel()
        live.frames = [frame(1, secondsFromStart: 0)]
        live.live = true
        live.append(frame(2, secondsFromStart: 1))
        XCTAssertEqual(live.frames.count, 2)
        XCTAssertEqual(live.current?.step, 2, "a live player follows a newly arrived frame")

        var scrubbedBack = PlayerModel()
        scrubbedBack.frames = [frame(1, secondsFromStart: 0), frame(2, secondsFromStart: 1)]
        scrubbedBack.index = 0
        scrubbedBack.live = false
        scrubbedBack.append(frame(3, secondsFromStart: 2))
        XCTAssertEqual(scrubbedBack.frames.count, 3)
        XCTAssertEqual(scrubbedBack.current?.step, 1, "scrubbed back and not live, a new frame must not move the scrubber")
    }
    private func named(_ file: String) -> Frame {
        Frame(at: Date(timeIntervalSince1970: 0), file: file, step: 1)
    }

    // MARK: - What a finished load may paint

    /// A frame's image is fetched asynchronously and the fetch outlives the
    /// request for it. Switching runs leaves the old load suspended, and when
    /// it resumes it must not paint the previous run's screen under the new
    /// run's name.
    func testAFrameThatIsNoLongerCurrentIsNotShown() {
        var player = PlayerModel()
        player.frames = [named("a.jpg"), named("b.jpg")]
        player.index = 0
        XCTAssertTrue(player.shows("a.jpg"))
        XCTAssertFalse(player.shows("b.jpg"))
        player.index = 1
        XCTAssertFalse(player.shows("a.jpg"))
    }

    /// The run was switched: the player was reset and holds nothing, so a
    /// load that comes back has nothing to paint onto.
    func testAResetPlayerShowsNothing() {
        let player = PlayerModel()
        XCTAssertFalse(player.shows("a.jpg"))
        XCTAssertNil(player.current)
    }

    /// Switching to a run that has frames of its own: the load still in
    /// flight for the old run's frame is refused.
    func testAPlayerSwitchedToAnotherRunRefusesTheOldRunsFrame() {
        var player = PlayerModel()
        player.frames = [named("old.jpg")]
        XCTAssertTrue(player.shows("old.jpg"))
        player = PlayerModel()
        player.frames = [named("new.jpg")]
        XCTAssertFalse(player.shows("old.jpg"))
        XCTAssertTrue(player.shows("new.jpg"))
    }
}
