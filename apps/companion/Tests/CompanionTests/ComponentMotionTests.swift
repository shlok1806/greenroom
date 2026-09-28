import SwiftUI
import XCTest

@testable import Companion

/// The components' own motion against the browser's frames (docs/22 section 6.3): the loop a
/// component draws, sampled where Chromium sampled the original, with the original's period
/// swapped for the Figma file's where the file names one (docs/22 section 1). `MotionTests`
/// holds the keyframes themselves; this holds the components that use them.
@MainActor
final class ComponentMotionTests: XCTestCase {
    private struct Spec: Decodable {
        struct Run: Decodable {
            var animations: [Animation]?
            var curves: [Row]?
        }

        struct Animation: Decodable {
            struct Keyframe: Decodable {
                var offset: Double
                var easing: String
            }

            var name: String
            var target: String
            var bornAtMs: Double
            var durationMs: Double
            var easing: String
            var keyframes: [Keyframe]
        }

        struct Row: Decodable {
            struct Sample: Decodable {
                var target: String
                var frame: [Double]?
                var transform: String?
                var backgroundPosition: String?
            }

            var tMs: Double
            var sample: [Sample]
        }

        var runs: [Run]
    }

    private func spec(_ name: String) throws -> (animations: [Spec.Animation], rows: [Spec.Row]) {
        let url = MotionTests.specs.appendingPathComponent(name + ".json")
        let spec = try JSONDecoder().decode(Spec.self, from: Data(contentsOf: url))
        let run = try XCTUnwrap(spec.runs.first { $0.animations != nil && $0.curves != nil })
        return (run.animations ?? [], run.curves ?? [])
    }

    /// The checking ring (C02) turns as Beautiful UI's `spin` does: linearly, a full turn a
    /// period, at every frame Chromium drew, over the Figma file's 800 ms instead of 1.1 s.
    func testTheCheckingRingTurnsAsTheOriginalSpinsAtEveryFrame() throws {
        let (animations, rows) = try spec("task-rows")
        let spin = try XCTUnwrap(animations.first { $0.name == "spin" })
        XCTAssertEqual(spin.easing, "linear")
        XCTAssertTrue(spin.keyframes.allSatisfy { $0.easing == "linear" })
        var worst = 0.0, samples = 0
        for row in rows {
            guard let transform = row.sample.first(where: { $0.target == spin.target })?.transform,
                  let m = Self.matrix(transform) else { continue }
            var theirs = atan2(m[1], m[0]) * 180 / .pi
            if theirs < 0 { theirs += 360 }
            let u = ((row.tMs - spin.bornAtMs) / spin.durationMs).truncatingRemainder(dividingBy: 1)
            let ours = StatusGlyph.angle(at: u * Motion.ring)
            let d = abs(ours - theirs)
            worst = max(worst, min(d, 360 - d))
            samples += 1
        }
        print("component motion: checking ring, \(samples) frames, worst \(worst) degrees")
        XCTAssertGreaterThan(samples, 50)
        XCTAssertLessThanOrEqual(worst, 1)
    }

    /// The shimmer (C08) travels as Beautiful UI's `shimmer-text` does: its gradient's
    /// `background-position-x` from 150% to -50%, linear, at every frame Chromium drew, over
    /// the Figma file's 1.6 s instead of 1.8 s. Held within half a point at the word's width.
    func testTheShimmerTravelsAsTheOriginalAtEveryFrame() throws {
        let (animations, rows) = try spec("shimmer")
        let shimmer = try XCTUnwrap(animations.first { $0.name == "shimmer-text" })
        XCTAssertTrue(shimmer.keyframes.allSatisfy { $0.easing == "linear" })
        var worst = 0.0, samples = 0
        for row in rows {
            guard let sample = row.sample.first(where: { $0.target == shimmer.target }),
                  let position = sample.backgroundPosition, let width = sample.frame?[2],
                  let percent = Double(position.split(separator: " ")[0].dropLast()) else { continue }
            let u = ((row.tMs - shimmer.bornAtMs) / shimmer.durationMs).truncatingRemainder(dividingBy: 1)
            // background-size 200%: position p puts the left edge at (W - 2W) p.
            let theirs = -width * percent / 100
            let ours = Double(ShimmerText.offset(at: u * Motion.shimmer, width: width))
            worst = max(worst, abs(ours - theirs))
            samples += 1
        }
        print("component motion: shimmer, \(samples) frames, worst \(worst) pt")
        XCTAssertGreaterThan(samples, 50)
        XCTAssertLessThanOrEqual(worst, 0.5)
    }

    /// Every state change (a pressed button, a detail opening, the header's word landing) runs
    /// on the curve the originals' entrances run on in the browser, cubic-bezier(0.23, 1, 0.32,
    /// 1), for the Figma file's durations, and not at all under Reduce Motion.
    func testEveryStateChangeRunsOnTheOriginalsCurveForTheFigmaDurations() throws {
        let (animations, _) = try spec("task-rows")
        let fadeUp = try XCTUnwrap(animations.first { $0.name == "fade-up" })
        XCTAssertEqual(Curve(css: fadeUp.keyframes[0].easing), Curve.outStrong)
        for duration in [Motion.press, Motion.settle, Motion.land] {
            XCTAssertEqual(Motion.change(duration, reduce: false), Animation.timingCurve(0.23, 1, 0.32, 1, duration: duration))
            XCTAssertNil(Motion.change(duration, reduce: true))
        }
        XCTAssertEqual([Motion.press, Motion.settle, Motion.land], [0.12, 0.18, 0.24])
        XCTAssertEqual(Motion.pressedScale, 0.97)
    }

    private static func matrix(_ text: String) -> [Double]? {
        guard text.hasPrefix("matrix("), text.hasSuffix(")") else { return nil }
        let parts = text.dropFirst(7).dropLast().split(separator: ",").compactMap { Double($0.trimmingCharacters(in: .whitespaces)) }
        return parts.count == 6 ? parts : nil
    }
}
