import SwiftUI
import XCTest

@testable import Companion

/// Motion against its ground truth (docs/22 sections 3, 4 and 6.4): the curves and keyframes
/// against what Chromium drew frame by frame for the Beautiful UI originals
/// (`docs/22-swiftui-clone-plan/specs/*.json`), the springs against Motion's own generator,
/// the palette's ranking against cmdk's own scores. Pure data: no rendering.
final class MotionTests: XCTestCase {
    static var repo: URL {
        URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
            .deletingLastPathComponent().deletingLastPathComponent()
    }

    static var specs: URL { repo.appendingPathComponent("docs/22-swiftui-clone-plan/specs") }

    static var golden: URL {
        URL(fileURLWithPath: #filePath).deletingLastPathComponent().appendingPathComponent("Golden")
    }

    // MARK: - Curves

    func testCSSNamesAndBeziersParse() {
        XCTAssertEqual(Curve(css: "cubic-bezier(0.23, 1, 0.32, 1)"), .outStrong)
        XCTAssertEqual(Curve(css: "cubic-bezier(0.4, 0, 0.2, 1)"), .tailwindDefault)
        XCTAssertEqual(Curve(css: "ease"), .cssEase)
        XCTAssertEqual(Curve(css: "ease-out"), .cssEaseOut)
        XCTAssertEqual(Curve(css: "ease-in-out"), .cssEaseInOut)
        XCTAssertEqual(Curve(css: "linear"), .linear)
        XCTAssertNil(Curve(css: "steps(4)"))
    }

    func testACurveStartsAtZeroEndsAtOneAndIsNotSwiftUIsEaseOut() {
        for curve in [Curve.outStrong, .inOutStrong, .link, .tailwindDefault, .cssEase, .cssEaseOut, .cssEaseInOut, .linear] {
            XCTAssertEqual(curve.value(at: 0), 0)
            XCTAssertEqual(curve.value(at: 1), 1)
            XCTAssertEqual(curve.value(at: -1), 0)
            XCTAssertEqual(curve.value(at: 2), 1)
        }
        // cubic-bezier(0.25, 0.1, 0.25, 1) at half time is 0.8024 (the CSS `ease` value every
        // browser gives).
        XCTAssertEqual(Curve.cssEase.value(at: 0.5), 0.8024, accuracy: 0.0005)
        XCTAssertEqual(Curve.linear.value(at: 0.3), 0.3)
    }

    func testKeyframesApplyTheCurvePerSegmentAndFillBothEnds() {
        let pixel = CSSKeyframes.pixelOn(delay: 0.09)
        XCTAssertEqual(pixel.value(at: 0), 0.15, "the first value shows during the delay")
        XCTAssertEqual(pixel.value(at: 0.09 + 0.65 * 0.18), 1, accuracy: 0.0001)
        XCTAssertEqual(pixel.value(at: 0.09 + 0.65 * 0.30), 1, accuracy: 0.0001)
        // Half way through the first segment, ease-in-out is at half its value.
        XCTAssertEqual(pixel.value(at: 0.09 + 0.65 * 0.09), 0.15 + 0.85 * 0.5, accuracy: 0.001)
        XCTAssertEqual(pixel.value(at: 0.09 + 0.65 * 1.18), 1, accuracy: 0.0001, "it loops")

        let enter = Keyframes.enterOpacity(delay: 0.08)
        XCTAssertEqual(enter.value(at: 0.05), 0)
        XCTAssertEqual(enter.value(at: 5), 1)
        XCTAssertFalse(enter.isDone(at: 0.2))
        XCTAssertTrue(enter.isDone(at: 0.26))
    }

    func testTheClockGivesALoopItsPhaseAtAnyTime() {
        XCTAssertEqual(MotionClock.phase(0.2, period: 0.8), 0.25, accuracy: 1e-9)
        XCTAssertEqual(MotionClock.phase(1.0, period: 0.8), 0.25, accuracy: 1e-9)
        XCTAssertEqual(MotionClock.phase(-0.2, period: 0.8), 0.75, accuracy: 1e-9)
        let frozen = MotionClock.frozen(at: 12.5)
        XCTAssertEqual(frozen.seconds(Date()), 12.5)
        XCTAssertEqual(frozen.seconds(Date().addingTimeInterval(99)), 12.5, "a fixed clock does not move")
    }

    func testTheNumberFlowTableIsItsNinetyPointsEvenlySpaced() {
        let curve = TableCurve.numberFlow
        XCTAssertEqual(curve.table.count, 90)
        XCTAssertEqual(curve.duration, 0.9)
        XCTAssertEqual(curve.value(at: 0), 0)
        XCTAssertEqual(curve.value(at: 1), 1)
        XCTAssertEqual(curve.value(at: 1.0 / 89), 0.005, accuracy: 1e-9)
        XCTAssertEqual(curve.value(at: 1.5 / 89), (0.005 + 0.019) / 2, accuracy: 1e-9, "straight lines between points")
        XCTAssertEqual(curve.table, curve.table.sorted(), "it never goes back")
    }

    // MARK: - Springs, against Motion's generator

    private struct SpringGolden: Decodable {
        struct Case: Decodable {
            struct Options: Decodable {
                var stiffness: Double?
                var damping: Double?
                var mass: Double?
                var visualDuration: Double?
                var duration: Double?
                var bounce: Double?
            }

            var name: String
            var options: Options
            var samples: [[Double]]
        }

        var cases: [Case]
    }

    func testEverySpringFollowsMotionsOwnCurveUntilItRests() throws {
        let golden = try JSONDecoder().decode(SpringGolden.self, from: Data(contentsOf: Self.golden.appendingPathComponent("motion-springs.json")))
        XCTAssertEqual(golden.cases.count, 7)
        for item in golden.cases {
            let o = item.options
            let spring: MotionSpring
            if let k = o.stiffness, let c = o.damping {
                spring = MotionSpring(stiffness: k, damping: c, mass: o.mass ?? 1)
            } else if let v = o.visualDuration {
                spring = MotionSpring(visualDuration: v, bounce: o.bounce ?? 0)
            } else {
                spring = MotionSpring(durationMs: try XCTUnwrap(o.duration), bounce: o.bounce ?? 0.3)
            }
            var worst = 0.0
            // The last sample is Motion snapping to its target at rest; the curve ends before it.
            for sample in item.samples.dropLast() {
                let value = spring.value(from: 0, to: 100, at: sample[0] / 1000)
                worst = max(worst, abs(value - sample[1]))
            }
            XCTAssertLessThan(worst, 0.1, "\(item.name): off by \(worst) of 100 (0.001 of the range allowed)")
        }
    }

    func testTheSpringConversionsAreTheOnesMotionStates() {
        let visual = MotionSpring(visualDuration: 0.3, bounce: 0.2)
        XCTAssertEqual(visual.stiffness, pow(2 * .pi / 0.36, 2), accuracy: 1e-9)
        XCTAssertEqual(visual.damping, 2 * 0.8 * visual.stiffness.squareRoot(), accuracy: 1e-9)
        // Transforms: stiffness 500, damping 25 is a damping ratio of 0.559 and a response of 0.281 s.
        let t = MotionSpring.transformDefault
        XCTAssertEqual(t.damping / (2 * t.stiffness.squareRoot()), 0.559, accuracy: 0.001)
        XCTAssertEqual(2 * .pi / t.stiffness.squareRoot(), 0.281, accuracy: 0.001)
    }

    // MARK: - Keyframes and transitions, against what the browser drew

    private struct Spec: Decodable {
        struct Run: Decodable {
            var engine: String
            var theme: String
            var font: String
            var animations: [Animation]?
            var curves: [Row]?
        }

        struct Animation: Decodable {
            struct Keyframe: Decodable {
                var offset: Double
                var easing: String
                var opacity: String?
                var transform: String?
            }

            var kind: String
            var name: String
            var target: String
            var bornAtMs: Double
            var durationMs: Double
            var delayMs: Double
            var easing: String
            var iterations: Iterations
            var keyframes: [Keyframe]
        }

        enum Iterations: Decodable {
            case count(Double), infinite

            init(from decoder: any Decoder) throws {
                let c = try decoder.singleValueContainer()
                if let n = try? c.decode(Double.self) { self = .count(n) } else { self = .infinite }
            }
        }

        struct Row: Decodable {
            struct Sample: Decodable {
                var target: String
                var opacity: Double?
                var transform: String?
            }

            var tMs: Double
            var sample: [Sample]
        }

        var piece: String
        var runs: [Run]
    }

    private enum Property: String { case opacity, translateY, translateX, scale, rotate }

    /// One property of one animation of a spec, as our keyframes.
    private func keyframes(_ animation: Spec.Animation, _ property: Property) -> CSSKeyframes? {
        func number(_ text: String?, in function: String) -> Double? {
            guard let text else { return nil }
            if text == "none" { return property == .scale ? 1 : 0 }
            guard let open = text.range(of: function + "(") else { return nil }
            let rest = text[open.upperBound...]
            guard let close = rest.firstIndex(of: ")") else { return nil }
            return Double(rest[..<close].filter { $0.isNumber || $0 == "." || $0 == "-" })
        }
        let stops: [CSSKeyframes.Stop] = animation.keyframes.compactMap { frame in
            let value: Double? = switch property {
            case .opacity: frame.opacity.flatMap(Double.init)
            default: number(frame.transform, in: property.rawValue)
            }
            // A transition's keyframes are linear; its one curve is the animation's easing.
            let css = animation.kind == "CSSTransition" ? animation.easing : frame.easing
            guard let value, let curve = Curve(css: css) else { return nil }
            return CSSKeyframes.Stop(offset: frame.offset, value: value, curve: curve)
        }
        guard stops.count == animation.keyframes.count, stops.count >= 2 else { return nil }
        guard Set(stops.map(\.value)).count > 1 else { return nil }
        var loops = false
        if case .infinite = animation.iterations { loops = true }
        return CSSKeyframes(stops: stops, duration: animation.durationMs / 1000, delay: animation.delayMs / 1000, loops: loops)
    }

    private func matrix(_ text: String?) -> [Double]? {
        guard let text, text.hasPrefix("matrix("), text.hasSuffix(")") else { return nil }
        let parts = text.dropFirst(7).dropLast().split(separator: ",").compactMap { Double($0.trimmingCharacters(in: .whitespaces)) }
        return parts.count == 6 ? parts : nil
    }

    /// For every spec, every animation of opacity, translation, scale or rotation that is the
    /// only one on its element and property: our keyframes give the browser's value at every
    /// frame it sampled, within the plan's motion thresholds (docs/22 section 6.3).
    func testOurKeyframesGiveTheBrowsersValueAtEveryFrame() throws {
        let files = try FileManager.default.contentsOfDirectory(at: Self.specs, includingPropertiesForKeys: nil)
            .filter { $0.pathExtension == "json" && !["tokens.json", "source-constants.json"].contains($0.lastPathComponent) }
        XCTAssertGreaterThanOrEqual(files.count, 12, "the specs of docs/22 are missing")
        var checked = 0, animationsChecked = 0
        var report: [String] = []
        for file in files.sorted(by: { $0.lastPathComponent < $1.lastPathComponent }) {
            let spec = try JSONDecoder().decode(Spec.self, from: Data(contentsOf: file))
            guard let run = spec.runs.first(where: { $0.animations != nil && $0.curves != nil }),
                  let animations = run.animations, let rows = run.curves else { continue }
            var pieceChecked = 0
            for property in [Property.opacity, .translateY, .translateX, .scale, .rotate] {
                let candidates = animations.compactMap { a in keyframes(a, property).map { (a, $0) } }
                for (animation, frames) in candidates {
                    // Only where nothing else moves the same property of the same element.
                    let rivals = candidates.filter { $0.0.target == animation.target }.count
                    guard rivals == 1 else { continue }
                    var worst = 0.0
                    var samples = 0
                    for row in rows where row.tMs >= animation.bornAtMs {
                        guard let sample = row.sample.first(where: { $0.target == animation.target }) else { continue }
                        let t = (row.tMs - animation.bornAtMs) / 1000
                        if !frames.loops, t > frames.delay + frames.duration + 0.02 { continue }
                        let ours = frames.value(at: t)
                        let theirs: Double?
                        switch property {
                        case .opacity: theirs = sample.opacity
                        case .translateY: theirs = matrix(sample.transform)?[5]
                        case .translateX: theirs = matrix(sample.transform)?[4]
                        case .scale: theirs = matrix(sample.transform).map { ($0[0] * $0[0] + $0[1] * $0[1]).squareRoot() }
                        case .rotate:
                            theirs = matrix(sample.transform).map { m in
                                var degrees = atan2(m[1], m[0]) * 180 / .pi
                                if degrees < 0 { degrees += 360 }
                                return degrees
                            }
                        }
                        guard let theirs else { continue }
                        var difference = abs(ours - theirs)
                        if property == .rotate { difference = min(difference, abs(360 - difference)) }
                        worst = max(worst, difference)
                        samples += 1
                    }
                    guard samples > 0 else { continue }
                    let allowed: Double = switch property {
                    case .opacity: 0.02
                    case .translateX, .translateY: 0.5
                    case .scale: 0.005
                    case .rotate: 1
                    }
                    if worst > allowed { report.append("\(spec.piece) \(animation.name) \(property.rawValue) on \(animation.target): off by \(worst)") }
                    checked += samples
                    animationsChecked += 1
                    pieceChecked += 1
                }
            }
            print("motion spec \(spec.piece): \(pieceChecked) animations checked")
        }
        print("motion specs: \(animationsChecked) animations, \(checked) sampled frames")
        XCTAssertGreaterThan(animationsChecked, 40)
        XCTAssertTrue(report.isEmpty, report.joined(separator: "\n"))
    }

    // MARK: - The palette's ranking, against cmdk's own scores

    private struct ScoreGolden: Decodable {
        struct Case: Decodable {
            var string: String
            var query: String
            var aliases: [String]?
            var score: Double
        }

        var cases: [Case]
    }

    func testThePaletteScoresEveryCaseAsCmdkDoes() throws {
        let golden = try JSONDecoder().decode(ScoreGolden.self, from: Data(contentsOf: Self.golden.appendingPathComponent("cmdk-scores.json")))
        XCTAssertGreaterThan(golden.cases.count, 500)
        for item in golden.cases {
            let ours = CommandScore.score(item.string, item.query, aliases: item.aliases ?? [])
            XCTAssertEqual(ours, item.score, accuracy: 1e-12, "\"\(item.query)\" for \"\(item.string)\"")
        }
    }
}
