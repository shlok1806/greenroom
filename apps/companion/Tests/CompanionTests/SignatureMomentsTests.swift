import CoreGraphics
import XCTest

@testable import Companion

/// The signature moments' pure parts (ADR 0006): the glyph rendering, the reveal and
/// power-down timing, click-mark placement, the house lights and Reduce Motion.
final class GlyphRenderingTests: XCTestCase {
    /// `width` x `height` RGBA pixels, each from `color(x, y)`.
    private func pixels(width: Int, height: Int, _ color: (Int, Int) -> (UInt8, UInt8, UInt8)) -> [UInt8] {
        var out: [UInt8] = []
        for y in 0..<height {
            for x in 0..<width {
                let (r, g, b) = color(x, y)
                out += [r, g, b, 255]
            }
        }
        return out
    }

    func testBlackHasNoDotsAndWhiteHasEveryDot() throws {
        // Two cells across, one down: the left one black, the right one white.
        let rgba = pixels(width: 4, height: 4) { x, _ in x < 2 ? (0, 0, 0) : (255, 255, 255) }
        let rendering = try XCTUnwrap(GlyphSampler.sample(rgba: rgba, width: 4, height: 4, columns: 2, rows: 1))
        XCTAssertEqual(rendering.dots, [0x00, 0xFF])
        XCTAssertEqual(rendering.text, "\u{2800}\u{28FF}")
        XCTAssertEqual(rendering.colors[0], RGB(red: 0, green: 0, blue: 0))
        XCTAssertEqual(rendering.colors[1], RGB(red: 1, green: 1, blue: 1))
    }

    /// A flat mid grey is not stretched (nothing to stretch), so it dithers to exactly
    /// the Bayer thresholds under one half: four of each cell's eight dots.
    func testAFlatGreyDithersToHalfTheDots() throws {
        let rgba = pixels(width: 8, height: 8) { _, _ in (128, 128, 128) }
        let rendering = try XCTUnwrap(GlyphSampler.sample(rgba: rgba, width: 8, height: 8, columns: 4, rows: 2))
        for bits in rendering.dots {
            XCTAssertEqual(bits.nonzeroBitCount, 4, String(bits, radix: 2))
        }
        // An ordered dither: the pattern repeats every two cells across and every cell down.
        XCTAssertEqual(rendering.dots[0], rendering.dots[2])
        XCTAssertEqual(rendering.dots[0], rendering.dots[4])
    }

    /// Each dot averages the pixels under it, and each cell's colour its dots.
    func testDotsAverageThePixelsUnderThem() throws {
        // 4 x 8 pixels to one cell (2 x 4 dots): every dot is a 2 x 2 box, half red half blue.
        let rgba = pixels(width: 4, height: 8) { x, _ in x % 2 == 0 ? (255, 0, 0) : (0, 0, 255) }
        let rendering = try XCTUnwrap(GlyphSampler.sample(rgba: rgba, width: 4, height: 8, columns: 1, rows: 1))
        XCTAssertEqual(rendering.colors[0].red, 0.5, accuracy: 0.001)
        XCTAssertEqual(rendering.colors[0].green, 0, accuracy: 0.001)
        XCTAssertEqual(rendering.colors[0].blue, 0.5, accuracy: 0.001)
    }

    /// A decoded picture is read top row first: white over black gives lit top cells.
    func testAnImageIsSampledUpright() throws {
        let width = 4, height = 8
        var rgba = pixels(width: width, height: height) { _, y in y < height / 2 ? (255, 255, 255) : (0, 0, 0) }
        let image = try rgba.withUnsafeMutableBytes { buffer -> CGImage in
            let context = try XCTUnwrap(CGContext(
                data: buffer.baseAddress, width: width, height: height, bitsPerComponent: 8, bytesPerRow: width * 4,
                space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue
            ))
            return try XCTUnwrap(context.makeImage())
        }
        let rendering = try XCTUnwrap(GlyphSampler.sample(image, columns: 2, rows: 2))
        XCTAssertEqual(rendering.dots, [0xFF, 0xFF, 0x00, 0x00])
    }

    func testNothingToSampleIsNil() {
        XCTAssertNil(GlyphSampler.sample(rgba: [], width: 0, height: 0, columns: 2, rows: 2))
        XCTAssertNil(GlyphSampler.sample(rgba: [0, 0, 0, 255], width: 1, height: 1, columns: 0, rows: 1))
    }

    /// The grid covers the picture as it is fitted into the well, one mono cell each.
    func testTheGridIsTheFittedPictureInCells() {
        let grid = GlyphSampler.grid(picture: CGSize(width: 1024, height: 768), view: CGSize(width: 800, height: 600),
                                     cell: CGSize(width: 8, height: 18))
        XCTAssertEqual(grid.columns, 100)
        XCTAssertEqual(grid.rows, 33)
        let none = GlyphSampler.grid(picture: .zero, view: CGSize(width: 800, height: 600), cell: CGSize(width: 8, height: 18))
        XCTAssertEqual(none.columns, 1)
        XCTAssertEqual(none.rows, 1)
    }
}

final class GlyphMotionTests: XCTestCase {
    func testProgressRunsZeroToOneOverItsTime() {
        XCTAssertEqual(GlyphMotion.progress(elapsed: -1, ms: 600), 0)
        XCTAssertEqual(GlyphMotion.progress(elapsed: 0.3, ms: 600), 0.5, accuracy: 1e-9)
        XCTAssertEqual(GlyphMotion.progress(elapsed: 5, ms: 600), 1)
        XCTAssertEqual(GlyphMotion.progress(elapsed: 0, ms: 0), 1)
    }

    /// Every cell turns inside the moment: none before it starts, all by its end.
    func testEveryCellTurnsBetweenTheStartAndTheEnd() {
        let columns = 40, rows = 12
        var orders: [Double] = []
        for row in 0..<rows {
            for column in 0..<columns {
                orders.append(GlyphMotion.order(column: column, row: row, columns: columns, rows: rows))
            }
        }
        XCTAssertTrue(orders.allSatisfy { $0 >= 0 && $0 < 1 })
        // The reveal: all covered at 0, none at 1; the power-down the other way round.
        XCTAssertTrue(orders.allSatisfy { GlyphMotion.coveredDuringReveal(order: $0, progress: 0) })
        XCTAssertFalse(orders.contains { GlyphMotion.coveredDuringReveal(order: $0, progress: 1) })
        XCTAssertFalse(orders.contains { GlyphMotion.dissolved(order: $0, progress: 0) })
        XCTAssertTrue(orders.allSatisfy { GlyphMotion.dissolved(order: $0, progress: 1) })
        // Mid-way about half have turned, not a wipe of whole rows.
        let half = orders.filter { GlyphMotion.dissolved(order: $0, progress: 0.5) }.count
        XCTAssertEqual(Double(half) / Double(orders.count), 0.5, accuracy: 0.15)
        XCTAssertEqual(orders, (0..<rows).flatMap { row in (0..<columns).map { GlyphMotion.order(column: $0, row: row, columns: columns, rows: rows) } },
                       "the order is the same every time")
    }

    /// The sweep runs from the top left: the top-left cells turn earlier on average.
    func testTheSweepStartsAtTheTopLeft() {
        let columns = 40, rows = 12
        let early = (0..<4).map { GlyphMotion.order(column: $0, row: 0, columns: columns, rows: rows) }.reduce(0, +)
        let late = (36..<40).map { GlyphMotion.order(column: $0, row: 11, columns: columns, rows: rows) }.reduce(0, +)
        XCTAssertLessThan(early, late)
    }

    func testAPowerDownDissolvesHoldsThenEnds() {
        XCTAssertEqual(GlyphMotion.powerDown(elapsed: 0, dissolveMs: 600, holdMs: 900), .dissolving(0))
        XCTAssertEqual(GlyphMotion.powerDown(elapsed: 0.3, dissolveMs: 600, holdMs: 900), .dissolving(0.5))
        XCTAssertEqual(GlyphMotion.powerDown(elapsed: 0.6, dissolveMs: 600, holdMs: 900), .still(0))
        guard case .still(let held) = GlyphMotion.powerDown(elapsed: 1.05, dissolveMs: 600, holdMs: 900) else {
            return XCTFail("holding by then")
        }
        XCTAssertEqual(held, 0.5, accuracy: 1e-9)
        XCTAssertEqual(GlyphMotion.powerDown(elapsed: 1.5, dissolveMs: 600, holdMs: 900), .done)
    }

    func testTheMomentsTakeTheTokensTimes() {
        let motion = DesignData.shared.tokens.motion
        XCTAssertEqual(motion.bootReveal.ms, 600)
        XCTAssertEqual(motion.powerDown.dissolveMs, 600)
        XCTAssertEqual(GlyphMotion.duration(of: .reveal, tokens: motion), 0.6, accuracy: 1e-9)
        XCTAssertEqual(GlyphMotion.duration(of: .powerDown, tokens: motion),
                       Double(motion.powerDown.dissolveMs + motion.powerDown.holdMs) / 1000, accuracy: 1e-9)
    }

    /// Reduce Motion: no reveal and no dissolve; the end state shows at once.
    func testReduceMotionSkipsTheMoments() {
        XCTAssertTrue(GlyphMotion.plays(reduceMotion: false))
        XCTAssertFalse(GlyphMotion.plays(reduceMotion: true))
    }
}

final class ClickMarksTests: XCTestCase {
    private let at = Date(timeIntervalSince1970: 1_000_000)

    private func input(_ seq: Int, _ actions: [[String: JSONValue]], holder: String = "verifier") -> Step {
        Step(seq: seq, at: at, tool: "machine_input",
             input: .object(["actions": .array(actions.map(JSONValue.object)), "holder": .string(holder)]))
    }

    private func click(_ x: Double, _ y: Double) -> [String: JSONValue] {
        ["type": .string("click"), "x": .double(x), "y": .double(y)]
    }

    private let typing: [String: JSONValue] = ["type": .string("type"), "text": .string("120")]

    func testAClickIsMarkedWhereItClicked() {
        let step = input(6, [click(0.461, 0.401)])
        XCTAssertEqual(ClickMarks.target(of: step, in: [step]),
                       ClickMarks.Target(fraction: CGPoint(x: 0.461, y: 0.401), kind: .click))
    }

    /// Several actions: the last place the pointer went down.
    func testTheLastClickOfAStepWins() {
        let step = input(3, [click(0.1, 0.1), typing, click(0.9, 0.2)])
        XCTAssertEqual(ClickMarks.target(of: step, in: [step])?.fraction, CGPoint(x: 0.9, y: 0.2))
    }

    /// `machine_click` records the action's own fields; its type is the tool's.
    func testTheSingleActionToolsAreMarkedToo() {
        let step = Step(seq: 2, at: at, tool: "machine_click", input: .object(["x": .double(0.25), "y": .int(1)]))
        XCTAssertEqual(ClickMarks.target(of: step, in: [step])?.fraction, CGPoint(x: 0.25, y: 1))
    }

    /// Typing has no place of its own: it is marked at the click before it.
    func testTypingIsMarkedAtTheClickBeforeIt() {
        let first = input(6, [click(0.461, 0.401)])
        let shortcut = input(7, [["type": .string("key"), "key": .string("a"), "mods": .array([.string("cmd")])]])
        let typed = input(8, [typing])
        let steps = [first, shortcut, typed]
        XCTAssertEqual(ClickMarks.target(of: typed, in: steps),
                       ClickMarks.Target(fraction: CGPoint(x: 0.461, y: 0.401), kind: .type))
        XCTAssertEqual(ClickMarks.target(of: shortcut, in: steps)?.kind, .type)
        XCTAssertNil(ClickMarks.target(of: typed, in: [typed]), "no click before it, nowhere to mark")
    }

    func testStepsWithNoInputAndThePersonsOwnInputAreNotMarked() {
        let exec = Step(seq: 4, at: at, tool: "machine_exec", input: .object(["command": .string("swift test")]))
        XCTAssertNil(ClickMarks.target(of: exec, in: [exec]))
        let scroll = input(5, [["type": .string("scroll"), "x": .double(0.5), "y": .double(0.5), "deltaY": .int(3)]])
        XCTAssertNil(ClickMarks.target(of: scroll, in: [scroll]))
        let human = input(9, [click(0.5, 0.5)], holder: "human")
        XCTAssertNil(ClickMarks.target(of: human, in: [human]))
    }

    /// Only steps that arrive while watching ripple; never the ones read on opening.
    func testOnlyStepsThatArriveRipple() {
        let steps = [input(6, [click(0.1, 0.1)]), input(7, [typing]), input(8, [click(0.3, 0.3)]),
                     Step(seq: 9, at: at, tool: "machine_screenshot")]
        XCTAssertTrue(ClickMarks.arrived(after: nil, in: steps).isEmpty)
        XCTAssertEqual(ClickMarks.arrived(after: 6, in: steps).map(\.step), [7, 8])
        XCTAssertEqual(ClickMarks.arrived(after: 0, in: steps, limit: 2).map(\.step), [7, 8], "a burst keeps the newest")
        XCTAssertEqual(ClickMarks.passed(from: 5, to: 7, in: steps).map(\.step), [6, 7])
        XCTAssertTrue(ClickMarks.passed(from: 8, to: 6, in: steps).isEmpty, "scrubbing back passes nothing")
    }

    /// A fraction lands on the picture as fitted in the well, through `ScreenGeometry`:
    /// a wide picture in a square view sits in the middle band.
    func testAMarkIsPlacedOnThePictureThroughScreenGeometry() throws {
        let image = CGSize(width: 1000, height: 500)
        let view = CGSize(width: 400, height: 400)
        XCTAssertEqual(ScreenGeometry.point(atFraction: CGPoint(x: 0.5, y: 0.5), image: image, view: view),
                       CGPoint(x: 200, y: 200))
        XCTAssertEqual(ScreenGeometry.point(atFraction: CGPoint(x: 0, y: 0), image: image, view: view), CGPoint(x: 0, y: 100))
        XCTAssertEqual(ScreenGeometry.point(atFraction: CGPoint(x: 1.2, y: 1), image: image, view: view), CGPoint(x: 400, y: 300))
        // The round trip with the input side agrees.
        let point = try XCTUnwrap(ScreenGeometry.point(atFraction: CGPoint(x: 0.25, y: 0.75), image: image, view: view))
        let back = try XCTUnwrap(ScreenGeometry.fraction(at: point, image: image, view: view))
        XCTAssertEqual(back.x, 0.25, accuracy: 1e-9)
        XCTAssertEqual(back.y, 0.75, accuracy: 1e-9)

        let cell = try XCTUnwrap(ClickMarks.cell(at: CGPoint(x: 0.5, y: 0.5), image: image, view: view, cell: CGSize(width: 8, height: 18)))
        XCTAssertEqual(cell, CGRect(x: 196, y: 191, width: 8, height: 18))
        XCTAssertNil(ClickMarks.cell(at: CGPoint(x: 0.5, y: 0.5), image: .zero, view: view, cell: CGSize(width: 8, height: 18)))
    }

    func testARingIsTheSquareOfCellsAroundTheClick() {
        let center = CGRect(x: 100, y: 100, width: 8, height: 18)
        XCTAssertEqual(ClickMarks.ring(0, around: center), [center])
        let one = ClickMarks.ring(1, around: center)
        XCTAssertEqual(one.count, 8)
        XCTAssertTrue(one.contains(CGRect(x: 92, y: 82, width: 8, height: 18)))
        XCTAssertFalse(one.contains(center))
        XCTAssertEqual(ClickMarks.ring(3, around: center).count, 24)
    }

    /// The ripple grows out and fades over the token's time, then ends.
    func testARippleGrowsFadesAndEnds() throws {
        let start = try XCTUnwrap(ClickMarks.ripple(age: 0, ms: 800, rings: 3, reduceMotion: false))
        XCTAssertEqual(start, ClickMarks.Ripple(ring: 0, opacity: 1))
        let middle = try XCTUnwrap(ClickMarks.ripple(age: 0.4, ms: 800, rings: 3, reduceMotion: false))
        XCTAssertEqual(middle.ring, 2)
        XCTAssertEqual(middle.opacity, 0.75, accuracy: 1e-9)
        let late = try XCTUnwrap(ClickMarks.ripple(age: 0.79, ms: 800, rings: 3, reduceMotion: false))
        XCTAssertEqual(late.ring, 3)
        XCTAssertNil(ClickMarks.ripple(age: 0.8, ms: 800, rings: 3, reduceMotion: false))
        XCTAssertNil(ClickMarks.ripple(age: -0.1, ms: 800, rings: 3, reduceMotion: false))
    }

    /// Reduce Motion: the mark holds still on the cell clicked for the same time.
    func testUnderReduceMotionTheMarkHoldsStill() {
        for age in [0, 0.2, 0.5, 0.79] {
            XCTAssertEqual(ClickMarks.ripple(age: age, ms: 800, rings: 3, reduceMotion: true), ClickMarks.Ripple(ring: 0, opacity: 1))
        }
        XCTAssertNil(ClickMarks.ripple(age: 0.8, ms: 800, rings: 3, reduceMotion: true))
    }

    func testTheMarksTakeTheTokensTimes() {
        let tokens = DesignData.shared.tokens.motion.clickMark
        XCTAssertEqual(tokens.ms, 800)
        XCTAssertGreaterThan(tokens.rings, 0)
    }

    /// `m` is a screen key in the registry, performed by the screen while it offers it.
    func testMTogglesTheMarksOnTheScreen() {
        let spec = ActionRegistry.spec(.clickMarks)
        XCTAssertEqual(spec.keyLabel, "m")
        XCTAssertEqual(spec.contexts, [.screen])
        XCTAssertEqual(spec.group, .screen)
        XCTAssertFalse(spec.destructive)

        var state = ActionState()
        state.runOpen = true
        state.runCount = 1
        state.pane = .stage
        state.stage = .screen
        XCTAssertEqual(KeyResolver.resolve(.char("m"), state), .swallow, "not offered: nothing happens")
        state.available = [HandlerKey(id: .clickMarks, context: .screen)]
        XCTAssertEqual(KeyResolver.resolve(.char("m"), state), .perform(.clickMarks, .screen))
        state.stage = .steps
        XCTAssertNotEqual(KeyResolver.resolve(.char("m"), state), .perform(.clickMarks, .screen), "only on the screen")
        XCTAssertEqual(ActionRules.whyDisabled(.clickMarks, ActionState()), "Show the screen first")
    }
}

final class HouseLightsTests: XCTestCase {
    private func state(driving: Bool, responder: KeyResponder) -> ActionState {
        var s = ActionState()
        s.runOpen = true
        s.driving = driving
        s.responder = responder
        return s
    }

    /// Down only while the lease is held and the machine has the keys.
    func testTheLightsGoDownOnlyWhileDriving() {
        XCTAssertTrue(HouseLights.down(state(driving: true, responder: .guest)))
        XCTAssertFalse(HouseLights.down(state(driving: false, responder: .other)))
        // The lease held, but the person clicked the conversation: lights up to read it.
        XCTAssertFalse(HouseLights.down(state(driving: true, responder: .text)))
        // The screen covered (a zoom elsewhere) lets go of the keys: lights up.
        XCTAssertFalse(HouseLights.down(state(driving: true, responder: .other)))
    }

    func testTheyDimByTheTokensAmount() {
        let tokens = DesignData.shared.tokens.motion.houseLights
        XCTAssertEqual(tokens.dim, 0.7, accuracy: 1e-9)
        XCTAssertEqual(HouseLights.dim(down: true, tokens: tokens), 0.7, accuracy: 1e-9)
        XCTAssertEqual(HouseLights.dim(down: false, tokens: tokens), 0)
    }

    /// Reduce Motion: the scrim changes at once (no spring).
    func testReduceMotionDimsAtOnce() {
        XCTAssertNil(PaneMotion.settle(reduceMotion: true))
        XCTAssertNotNil(PaneMotion.settle(reduceMotion: false))
    }
}

final class BootLogTests: XCTestCase {
    private let now = Date(timeIntervalSince1970: 1_000_000)

    private func facts(_ status: MachineStatus, detail: RunDetail, steps: [Step]) -> RunFacts {
        RunFacts.derive(summary: nil, detail: detail, messages: [], steps: steps, verdict: nil, now: now)
    }

    private func detail(_ status: MachineStatus, bootSeconds: Double? = nil) -> RunDetail {
        let machine = Machine(runId: "r", name: "greenroom-r", image: "greenroom-lean-a", ip: status == .ready ? "192.168.64.3" : nil,
                              status: status, error: nil, bootSeconds: bootSeconds, createdAt: now.addingTimeInterval(-30),
                              dir: "", control: nil)
        return RunDetail(runId: "r", image: "greenroom-lean-a", machineName: "greenroom-r", createdAt: now.addingTimeInterval(-30),
                         machine: machine)
    }

    private var create: Step {
        Step(seq: 1, at: now.addingTimeInterval(-30), tool: "machine_create", input: .object(["image": .string("greenroom-lean-a")]),
             durationMs: 675)
    }

    /// Booting: the create the daemon recorded, then the boot still going, counted from
    /// when the machine was made.
    func testBootingSaysWhatTheDaemonSaid() {
        let lines = BootLog.lines(facts: facts(.booting, detail: detail(.booting), steps: [create]), detail: detail(.booting),
                                  image: nil, steps: [create], connecting: nil)
        XCTAssertEqual(lines.map(\.word), ["create", "boot"])
        XCTAssertEqual(lines[0].detail, "greenroom-lean-a")
        XCTAssertEqual(lines[0].seconds ?? 0, 0.675, accuracy: 1e-9)
        XCTAssertEqual(lines[1].detail, "greenroom-r")
        XCTAssertEqual(lines[1].state, .working(since: now.addingTimeInterval(-30 + 0.675)))
    }

    /// Ready and connecting: the boot's time, the address, the screen still coming.
    func testReadyAndConnecting() {
        let connecting = now.addingTimeInterval(-2)
        let ready = detail(.ready, bootSeconds: 28.4)
        let lines = BootLog.lines(facts: facts(.ready, detail: ready, steps: [create]), detail: ready, image: nil,
                                  steps: [create], connecting: connecting)
        XCTAssertEqual(lines.map(\.word), ["create", "boot", "ready", "screen"])
        XCTAssertEqual(lines[1].seconds, 28.4)
        XCTAssertEqual(lines[2].detail, "192.168.64.3")
        XCTAssertEqual(lines[3].state, .working(since: connecting))
        XCTAssertEqual(BootLog.took(0.675), "0.7s")
        XCTAssertEqual(BootLog.took(28.4), "28s")
        XCTAssertEqual(BootLog.took(64), "01:04")
    }

    /// A finished run has no boot to show.
    func testAnEndedRunHasNoLines() {
        var gone = detail(.ready)
        gone.machine = nil
        gone.destroyedAt = now
        XCTAssertTrue(BootLog.lines(facts: facts(.ready, detail: gone, steps: []), detail: gone, image: nil, steps: [], connecting: nil).isEmpty)
    }
}
