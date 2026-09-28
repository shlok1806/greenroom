import SwiftUI

// Motion (docs/22 sections 3.2, 3.3 and C06; Figma Tokens, Motion). Every curve, keyframe
// animation and spring the components use, as pure functions of time, so a test or the
// harness can ask for the value at any moment and the running app animates with the same
// numbers. Only state changes move; keyboard navigation and high-frequency actions never
// animate (Emil Kowalski, "You don't need animations"); everything stops under Reduce Motion.

/// Durations, scales and the animations built from them.
enum Motion {
    /// Press and hover.
    static let press: Double = 0.12
    /// A row settling, a panel sliding.
    static let settle: Double = 0.18
    /// A verdict landing.
    static let land: Double = 0.24
    /// The Checking ring turns once in this long, linear.
    static let ring: Double = 0.8
    /// The Thinking shimmer crosses the word in this long.
    static let shimmer: Double = 1.6
    /// A pressed control's scale.
    static let pressedScale: CGFloat = 0.97
    /// Where an entering element starts: this scale at opacity 0, never from 0.
    static let enterScale: CGFloat = 0.96
    /// The blur over a content swap, in points.
    static let swapBlur: CGFloat = 2

    /// The design's curve, cubic-bezier(0.23, 1, 0.32, 1), for every duration above.
    static func easeOut(_ duration: Double) -> Animation { Curve.outStrong.animation(duration) }

    /// The animation for a state change, or none under Reduce Motion.
    static func change(_ duration: Double, reduce: Bool) -> Animation? {
        reduce ? nil : easeOut(duration)
    }
}

/// A CSS timing function. `cubic-bezier(x1, y1, x2, y2)` is the same unit Bezier SwiftUI's
/// `timingCurve` and `UnitCurve.bezier` evaluate, so the conversion is exact. SwiftUI's own
/// `.easeOut` and `.easeInOut` are not CSS's, so they are never used.
struct Curve: Equatable, Sendable {
    var x1: Double
    var y1: Double
    var x2: Double
    var y2: Double

    /// Beautiful UI's `--ease-out-strong`, and the Figma file's one curve.
    static let outStrong = Curve(x1: 0.23, y1: 1, x2: 0.32, y2: 1)
    /// Beautiful UI's `--ease-in-out-strong`.
    static let inOutStrong = Curve(x1: 0.77, y1: 0, x2: 0.175, y2: 1)
    /// Beautiful UI's `--ease-link`.
    static let link = Curve(x1: 0.16, y1: 1, x2: 0.3, y2: 1)
    /// Tailwind's default transition curve (a `transition-*` class with no ease class).
    static let tailwindDefault = Curve(x1: 0.4, y1: 0, x2: 0.2, y2: 1)
    /// CSS `ease`.
    static let cssEase = Curve(x1: 0.25, y1: 0.1, x2: 0.25, y2: 1)
    /// CSS `ease-in`.
    static let cssEaseIn = Curve(x1: 0.42, y1: 0, x2: 1, y2: 1)
    /// CSS `ease-out`.
    static let cssEaseOut = Curve(x1: 0, y1: 0, x2: 0.58, y2: 1)
    /// CSS `ease-in-out`.
    static let cssEaseInOut = Curve(x1: 0.42, y1: 0, x2: 0.58, y2: 1)
    /// CSS `linear`.
    static let linear = Curve(x1: 0, y1: 0, x2: 1, y2: 1)

    var unit: UnitCurve {
        UnitCurve.bezier(startControlPoint: UnitPoint(x: x1, y: y1), endControlPoint: UnitPoint(x: x2, y: y2))
    }

    /// Progress of the value at `progress` of the time, both 0 to 1.
    func value(at progress: Double) -> Double {
        if progress <= 0 { return 0 }
        if progress >= 1 { return 1 }
        if self == .linear { return progress }
        return unit.value(at: progress)
    }

    func animation(_ duration: Double) -> Animation {
        .timingCurve(x1, y1, x2, y2, duration: duration)
    }

    /// Parses `cubic-bezier(a, b, c, d)`, `linear`, `ease`, `ease-in`, `ease-out` and
    /// `ease-in-out`, as the extracted specs write them.
    init?(css: String) {
        let text = css.trimmingCharacters(in: .whitespaces)
        switch text {
        case "linear": self = .linear
        case "ease": self = .cssEase
        case "ease-in": self = .cssEaseIn
        case "ease-out": self = .cssEaseOut
        case "ease-in-out": self = .cssEaseInOut
        default:
            guard text.hasPrefix("cubic-bezier("), text.hasSuffix(")") else { return nil }
            let numbers = text.dropFirst("cubic-bezier(".count).dropLast().split(separator: ",")
                .compactMap { Double($0.trimmingCharacters(in: .whitespaces)) }
            guard numbers.count == 4 else { return nil }
            self.init(x1: numbers[0], y1: numbers[1], x2: numbers[2], y2: numbers[3])
        }
    }

    init(x1: Double, y1: Double, x2: Double, y2: Double) {
        (self.x1, self.y1, self.x2, self.y2) = (x1, y1, x2, y2)
    }
}

/// A CSS `@keyframes` animation or a transition as a pure function of time. The curve applies
/// per segment between keyframes, not to the whole animation (docs/22 section 3.2), and with
/// `fill: both` the first value shows during the delay and the last after the end.
struct CSSKeyframes: Equatable, Sendable {
    struct Stop: Equatable, Sendable {
        /// 0 to 1.
        var offset: Double
        var value: Double
        /// The curve from this stop to the next.
        var curve: Curve
    }

    var stops: [Stop]
    var duration: Double
    var delay: Double = 0
    /// Repeats forever, each turn `duration` long.
    var loops = false

    /// From one value to another over `duration` with one curve: a transition, `fade-up`,
    /// `pop-in`.
    init(from: Double, to: Double, duration: Double, delay: Double = 0, curve: Curve = .outStrong, loops: Bool = false) {
        stops = [Stop(offset: 0, value: from, curve: curve), Stop(offset: 1, value: to, curve: curve)]
        self.duration = duration
        self.delay = delay
        self.loops = loops
    }

    init(stops: [Stop], duration: Double, delay: Double = 0, loops: Bool = false) {
        self.stops = stops.sorted { $0.offset < $1.offset }
        self.duration = duration
        self.delay = delay
        self.loops = loops
    }

    /// The value `time` seconds after the animation was born.
    func value(at time: Double) -> Double {
        guard let first = stops.first, let last = stops.last else { return 0 }
        guard duration > 0 else { return time >= delay ? last.value : first.value }
        var local = (time - delay) / duration
        if local <= 0 { return first.value }
        if loops {
            local = local.truncatingRemainder(dividingBy: 1)
        } else if local >= 1 {
            return last.value
        }
        if local <= first.offset { return first.value }
        for index in 1..<stops.count where local <= stops[index].offset {
            let (a, b) = (stops[index - 1], stops[index])
            let span = b.offset - a.offset
            guard span > 0 else { return b.value }
            let progress = a.curve.value(at: (local - a.offset) / span)
            return a.value + (b.value - a.value) * progress
        }
        return last.value
    }

    /// Whether the animation has ended by `time`.
    func isDone(at time: Double) -> Bool { !loops && time >= delay + duration }

    /// Beautiful UI's `pixel-on`: opacity 0.15, 1, 1, 0.15, 0.15 at 0, 18, 42, 62 and 100%,
    /// `ease-in-out` per segment, 650 ms, forever.
    static func pixelOn(delay: Double) -> CSSKeyframes {
        CSSKeyframes(stops: [
            Stop(offset: 0, value: 0.15, curve: .cssEaseInOut),
            Stop(offset: 0.18, value: 1, curve: .cssEaseInOut),
            Stop(offset: 0.42, value: 1, curve: .cssEaseInOut),
            Stop(offset: 0.62, value: 0.15, curve: .cssEaseInOut),
            Stop(offset: 1, value: 0.15, curve: .cssEaseInOut),
        ], duration: 0.65, delay: delay, loops: true)
    }
}

/// The shared keyframes, with the Figma file's timings where it names them and the original's
/// where it does not (docs/22 section 1).
enum Keyframes {
    /// An element entering: opacity 0 to 1 (Figma: 180 ms; Beautiful UI `fade-up` 450 ms).
    static func enterOpacity(delay: Double = 0, duration: Double = Motion.settle) -> CSSKeyframes {
        CSSKeyframes(from: 0, to: 1, duration: duration, delay: delay)
    }

    /// An element entering: scale 0.96 to 1 (Figma; Beautiful UI `pop-in` starts at 0.95).
    static func enterScale(delay: Double = 0, duration: Double = Motion.settle) -> CSSKeyframes {
        CSSKeyframes(from: Motion.enterScale, to: 1, duration: duration, delay: delay)
    }

    /// Beautiful UI `fade-up`: rises 8 pt to its place.
    static func riseOffset(delay: Double = 0, duration: Double = Motion.settle) -> CSSKeyframes {
        CSSKeyframes(from: 8, to: 0, duration: duration, delay: delay)
    }

    /// A detail opening in place: 0 to 1 of its height and opacity, Beautiful UI's grid-rows
    /// reveal, 300 ms there, the Figma settle here.
    static func reveal(duration: Double = Motion.settle) -> CSSKeyframes {
        CSSKeyframes(from: 0, to: 1, duration: duration)
    }

    /// A chevron turning as its row opens: Tailwind's default curve, as Beautiful UI's does.
    static func chevron(duration: Double = Motion.settle) -> CSSKeyframes {
        CSSKeyframes(from: 0, to: 1, duration: duration, curve: .tailwindDefault)
    }
}

/// Beautiful UI's agent entrances (redesign 7), the originals' timings as extracted
/// (`task-rows.json`, `tool-chips.json`, `thinking-state.json`, `stream-text.json`): the Figma
/// file names durations for state changes (press, settle, landing) but none for the agent's
/// work arriving, so the originals' win there (docs/22 section 1). `ComponentMotionTests`
/// holds each against its spec's animation list.
enum AgentMotion {
    /// `fade-up`: from 8 below at opacity 0, on the design's curve.
    static let rise: CGFloat = 8
    static let curve = Curve.outStrong
    /// Task rows enter over 450 ms, 80 ms apart.
    static let taskRow: Double = 0.45
    static let taskRowStagger: Double = 0.08
    /// A tool call's row enters over 300 ms (ToolChips, one every 700 ms in the demo).
    static let toolChip: Double = 0.3
    /// Thinking's trace rows enter over 320 ms, 120 ms apart.
    static let traceRow: Double = 0.32
    static let traceStagger: Double = 0.12
    /// Thinking's label swaps to "Thought for ..." with `fade-in 350ms ease-out`.
    static let labelFadeIn: Double = 0.35
    static let labelCurve = Curve.cssEaseOut
    /// StreamText's caret: 2 pt wide, 1.05 em tall, radius 1.
    static let caretWidth: CGFloat = 2
    static let caretEm: CGFloat = 1.05

    /// `fade-up` at `time` seconds after the element was born, as the browser draws it:
    /// opacity 0 to 1 and the rise 8 to 0 on the curve, after `delay`.
    static func fadeUp(at time: Double, duration: Double, delay: Double = 0) -> (opacity: Double, offset: CGFloat) {
        let progress = curve.value(at: (time - delay) / duration)
        return (progress, rise * CGFloat(1 - progress))
    }
}

/// An element of the agent's work arriving while the person watches: Beautiful UI's
/// `fade-up`. `active` is false for what was already there when the view opened, which shows
/// at once; nothing moves under Reduce Motion.
struct FadeUp: ViewModifier {
    var active: Bool
    var duration: Double
    var delay: Double = 0
    @State private var shown = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    func body(content: Content) -> some View {
        let visible = shown || !active || reduceMotion
        content
            .opacity(visible ? 1 : 0)
            .offset(y: visible ? 0 : AgentMotion.rise)
            .onAppear {
                guard active, !reduceMotion, !shown else { return }
                withAnimation(AgentMotion.curve.animation(duration).delay(delay)) { shown = true }
            }
    }
}

extension View {
    func fadeUp(_ active: Bool, duration: Double, delay: Double = 0) -> some View {
        modifier(FadeUp(active: active, duration: duration, delay: delay))
    }
}

/// Which items of a growing list arrived while the view was open, and in which order, so
/// each new one fades up with its stagger. What was there when the view opened (or when its
/// run changed) is known and shows at once.
struct Arrivals: Equatable {
    private(set) var known: Set<String>?
    private var batch: [String] = []

    /// Everything in `ids` is already there.
    mutating func reset(_ ids: [String]) {
        known = Set(ids)
        batch = []
    }

    /// Notes a new list: the ids not known yet are this batch, in order.
    mutating func note(_ ids: [String]) {
        guard let known else { return }
        let fresh = ids.filter { !known.contains($0) }
        if !fresh.isEmpty {
            batch = fresh
            self.known = known.union(fresh)
        }
    }

    /// Whether `id` arrived while the view was open.
    func isNew(_ id: String) -> Bool { batch.contains(id) }

    /// The delay before `id` enters: its place in its batch times the stagger.
    func delay(_ id: String, stagger: Double) -> Double {
        Double(batch.firstIndex(of: id) ?? 0) * stagger
    }
}

// MARK: - Loops and the clock

/// The clock every loop reads (the checking ring, the shimmer, the pixel grid). Loops are
/// never `repeatForever`: each works out its phase from the time, so the harness can render
/// any loop at any moment by fixing the clock.
struct MotionClock: Equatable, Sendable {
    /// A fixed time, for snapshots and tests; nil follows the real clock.
    var fixed: Date?
    /// When loops count from.
    var origin = Date(timeIntervalSinceReferenceDate: 0)

    static let live = MotionClock()

    static func frozen(at seconds: Double) -> MotionClock {
        MotionClock(fixed: Date(timeIntervalSinceReferenceDate: seconds))
    }

    /// Seconds since the origin at `date`.
    func seconds(_ date: Date) -> Double { (fixed ?? date).timeIntervalSince(origin) }

    /// 0 to 1 through a loop of `period` seconds.
    static func phase(_ seconds: Double, period: Double) -> Double {
        guard period > 0 else { return 0 }
        let p = seconds.truncatingRemainder(dividingBy: period) / period
        return p < 0 ? p + 1 : p
    }
}

struct MotionClockKey: EnvironmentKey {
    static let defaultValue = MotionClock.live
}

extension EnvironmentValues {
    var motionClock: MotionClock {
        get { self[MotionClockKey.self] }
        set { self[MotionClockKey.self] = newValue }
    }
}

/// Draws `content` with the seconds on the motion clock: every frame while `running`, once
/// while it is not, under Reduce Motion, when the clock is fixed, or while the window is off
/// screen (companion ADR 0021).
struct Clocked<Content: View>: View {
    var running = true
    @ViewBuilder var content: (Double) -> Content
    @Environment(\.motionClock) private var clock
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        if clock.fixed != nil || !running || reduceMotion || !OnScreen.shared.visible {
            content(reduceMotion ? 0 : clock.seconds(Date()))
        } else {
            TimelineView(.animation) { context in content(clock.seconds(context.date)) }
        }
    }
}

// MARK: - Springs (Motion to SwiftUI)

/// A spring as Motion states it, converted to the three physical numbers SwiftUI takes
/// (docs/22 section 3.3). Both evaluate the same damped oscillator, so the curves agree until
/// Motion reports it at rest. Ported from motiondivision/motion,
/// `packages/motion-dom/src/animation/generators/spring.ts` (MIT).
struct MotionSpring: Equatable, Sendable {
    var stiffness: Double
    var damping: Double
    var mass: Double = 1

    /// Motion's defaults when a spring names nothing.
    static let physicsDefault = MotionSpring(stiffness: 100, damping: 10)
    /// Motion's implicit spring for x, y, rotate and other transforms.
    static let transformDefault = MotionSpring(stiffness: 500, damping: 25)
    /// Motion's implicit spring for scale.
    static let scaleDefault = MotionSpring(stiffness: 550, damping: 30)

    init(stiffness: Double, damping: Double, mass: Double = 1) {
        (self.stiffness, self.damping, self.mass) = (stiffness, damping, mass)
    }

    /// `visualDuration` (seconds) and `bounce`: stiffness (2 pi / (1.2 v)) squared, damping
    /// 2 zeta sqrt(k), zeta = clamp(0.05, 1, 1 - bounce).
    init(visualDuration: Double, bounce: Double = 0) {
        let root = (2 * Double.pi) / (visualDuration * 1.2)
        let k = root * root
        self.init(stiffness: k, damping: 2 * min(1, max(0.05, 1 - bounce)) * k.squareRoot())
    }

    /// `duration` (milliseconds) and `bounce` with no visual duration: Motion's `findSpring`,
    /// twelve Newton steps from a first guess of 5 / duration.
    init(durationMs: Double, bounce: Double = 0.3, velocity: Double = 0, mass: Double = 1) {
        let zeta = min(1, max(0.05, 1 - bounce))
        let duration = min(10, max(0.01, durationMs / 1000))
        let safeMin = 0.001
        func angular(_ w: Double, _ z: Double) -> Double { w * (1 - z * z).squareRoot() }
        let envelope: (Double) -> Double
        let derivative: (Double) -> Double
        if zeta < 1 {
            envelope = { w in
                let decay = w * zeta
                let a = decay - velocity
                return safeMin - (a / angular(w, zeta)) * exp(-decay * duration)
            }
            derivative = { w in
                let decay = w * zeta
                let delta = decay * duration
                let d = delta * velocity + velocity
                let e = zeta * zeta * w * w * duration
                let factor: Double = -envelope(w) + safeMin > 0 ? -1 : 1
                return (factor * ((d - e) * exp(-delta))) / angular(w * w, zeta)
            }
        } else {
            envelope = { w in -safeMin + exp(-w * duration) * ((w - velocity) * duration + 1) }
            derivative = { w in exp(-w * duration) * ((velocity - w) * (duration * duration)) }
        }
        var w = 5 / duration
        for _ in 1..<12 { w -= envelope(w) / derivative(w) }
        if w.isNaN {
            self.init(stiffness: 100, damping: 10, mass: mass)
        } else {
            let k = w * w * mass
            self.init(stiffness: k, damping: zeta * 2 * (mass * k).squareRoot(), mass: mass)
        }
    }

    var spring: Spring { Spring(mass: mass, stiffness: stiffness, damping: damping) }

    var animation: Animation { .spring(spring) }

    /// The value at `time` seconds, going from `from` to `to` from rest.
    func value(from: Double = 0, to: Double = 1, at time: Double) -> Double {
        from + spring.value(target: to - from, time: max(0, time))
    }
}

// MARK: - CSS linear()

/// CSS `linear(p0, p1, ...)` with no percentages: the points are evenly spaced in time and
/// joined by straight lines. NumberFlow's digit roll is one.
struct TableCurve: CustomAnimation, Equatable {
    var table: [Double]
    var duration: Double

    func value(at progress: Double) -> Double {
        guard table.count > 1 else { return progress >= 1 ? 1 : 0 }
        if progress <= 0 { return table[0] }
        if progress >= 1 { return table[table.count - 1] }
        let position = progress * Double(table.count - 1)
        let index = Int(position)
        let fraction = position - Double(index)
        return table[index] + (table[index + 1] - table[index]) * fraction
    }

    func animate<V: VectorArithmetic>(value: V, time: TimeInterval, context: inout AnimationContext<V>) -> V? {
        guard time < duration else { return nil }
        return value.scaled(by: self.value(at: time / duration))
    }

    /// NumberFlow's digit transform: 900 ms over its spring table. From barvian/number-flow,
    /// `packages/number-flow/src/lite.ts` (MIT).
    static let numberFlow = TableCurve(table: [
        0, 0.005, 0.019, 0.039, 0.066, 0.096, 0.129, 0.165, 0.202, 0.24, 0.278, 0.316, 0.354, 0.39, 0.426, 0.461,
        0.494, 0.526, 0.557, 0.586, 0.614, 0.64, 0.665, 0.689, 0.711, 0.731, 0.751, 0.769, 0.786, 0.802, 0.817,
        0.831, 0.844, 0.856, 0.867, 0.877, 0.887, 0.896, 0.904, 0.912, 0.919, 0.925, 0.931, 0.937, 0.942, 0.947,
        0.951, 0.955, 0.959, 0.962, 0.965, 0.968, 0.971, 0.973, 0.976, 0.978, 0.98, 0.981, 0.983, 0.984, 0.986,
        0.987, 0.988, 0.989, 0.99, 0.991, 0.992, 0.992, 0.993, 0.994, 0.994, 0.995, 0.995, 0.996, 0.996, 0.9963,
        0.9967, 0.9969, 0.9972, 0.9975, 0.9977, 0.9979, 0.9981, 0.9982, 0.9984, 0.9985, 0.9987, 0.9988, 0.9989, 1,
    ], duration: 0.9)
}

extension Animation {
    /// NumberFlow's digit roll.
    static var numberFlow: Animation { Animation(TableCurve.numberFlow) }
}
