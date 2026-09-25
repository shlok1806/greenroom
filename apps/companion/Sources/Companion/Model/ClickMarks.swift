import Foundation

/// Where the agent clicked or typed, for the marks over the screen's picture (ADR 0006
/// decision 5, the one thing drawn over it). Positions are the steps' own screen
/// fractions (ADR 0009); a view point is only ever worked out through `ScreenGeometry`.
/// Pure; `ClickMarksTests`.
enum ClickMarks {
    /// Whether the marks show (`m`), kept across launches.
    static let storageKey = "showsClickMarks"

    /// Where a step's mark goes, as a fraction of the guest's screen.
    struct Target: Equatable, Sendable {
        enum Kind: Equatable, Sendable {
            /// The step clicked (or pressed the button) here.
            case click
            /// The step typed or pressed keys, which have no place of their own: the mark
            /// sits at the click before it, where the text went.
            case type
        }

        var fraction: CGPoint
        var kind: Kind
    }

    /// Who the daemon says sent a step's input. The person's own input is not a mark.
    private static let person = "human"

    /// The mark a step leaves: the last place it clicked, else (typing or keys only) the
    /// last click before it in `steps`; nil for a step with no input to show.
    static func target(of step: Step, in steps: [Step]) -> Target? {
        guard step.input?["holder"]?.stringValue != person else { return nil }
        let actions = inputActions(of: step)
        guard !actions.isEmpty else { return nil }
        if let place = actions.compactMap(place(of:)).last {
            return Target(fraction: place, kind: .click)
        }
        guard actions.contains(where: types) else { return nil }
        let before = steps.last { $0.seq < step.seq && !inputActions(of: $0).compactMap(place(of:)).isEmpty }
        guard let place = before.flatMap({ inputActions(of: $0).compactMap(place(of:)).last }) else { return nil }
        return Target(fraction: place, kind: .type)
    }

    /// The marks for steps that arrived after `seq`, oldest first, at most `limit` of them:
    /// a burst (a reconnect reading many steps at once) must not flood the picture. None
    /// on the first read (`seq` nil): those steps were not watched happening.
    static func arrived(after seq: Int?, in steps: [Step], limit: Int = 4) -> [(step: Int, target: Target)] {
        guard let seq else { return [] }
        let fresh = steps.filter { $0.seq > seq }.compactMap { step in
            target(of: step, in: steps).map { (step: step.seq, target: $0) }
        }
        return Array(fresh.suffix(limit))
    }

    /// The marks of the steps the playhead passed over, from `old` (exclusive) to `new`
    /// (inclusive), while a recording plays. Moving backwards passes nothing.
    static func passed(from old: Int?, to new: Int?, in steps: [Step], limit: Int = 4) -> [(step: Int, target: Target)] {
        guard let old, let new, new > old else { return [] }
        let passed = steps.filter { $0.seq > old && $0.seq <= new }.compactMap { step in
            target(of: step, in: steps).map { (step: step.seq, target: $0) }
        }
        return Array(passed.suffix(limit))
    }

    /// A step's input as a list of actions: `machine_input` carries `actions`; the
    /// single-action tools (`machine_click`, `machine_type`, `machine_key`) carry one
    /// action's own fields and take their type from the tool's name.
    static func inputActions(of step: Step) -> [JSONValue] {
        if case .array(let list)? = step.input?["actions"] { return list }
        let single = ["machine_click": "click", "machine_type": "type", "machine_key": "key"]
        guard let type = single[step.tool], case .object(var fields)? = step.input else { return [] }
        fields["type"] = .string(type)
        return [.object(fields)]
    }

    /// Where an action put the pointer down: a click, or a press or release of the button.
    private static func place(of action: JSONValue) -> CGPoint? {
        let type = action["type"]?.stringValue ?? ""
        guard ["click", "down", "up"].contains(type),
              let x = StepSummary.number(action["x"]), let y = StepSummary.number(action["y"]) else { return nil }
        return CGPoint(x: x, y: y)
    }

    private static func types(_ action: JSONValue) -> Bool {
        let type = action["type"]?.stringValue ?? ""
        return type == "type" || type == "key"
    }

    // MARK: - Drawing

    /// One frame of a transient mark: the ring of cells lit (0 is the cell clicked, then
    /// each square ring one cell further out) and how strongly.
    struct Ripple: Equatable, Sendable {
        var ring: Int
        var opacity: Double
    }

    /// A mark `age` seconds after it began: the ring grows out to `rings` while it fades,
    /// over `ms`; nil once it is over. Under Reduce Motion it holds still on the cell
    /// clicked for the same time, at full strength: the information stays, the motion goes.
    static func ripple(age: TimeInterval, ms: Int, rings: Int, reduceMotion: Bool) -> Ripple? {
        let duration = Double(ms) / 1000
        guard age >= 0, duration > 0, age < duration else { return nil }
        if reduceMotion { return Ripple(ring: 0, opacity: 1) }
        let progress = age / duration
        let ring = min(rings, Int(progress * Double(rings + 1)))
        let opacity = 1 - progress * progress
        return Ripple(ring: ring, opacity: opacity)
    }

    /// The cell a mark centres on: one mono cell, centred on the step's place on the
    /// picture. `nil` before there is a picture.
    static func cell(at fraction: CGPoint, image: CGSize, view: CGSize, cell: CGSize) -> CGRect? {
        guard cell.width > 0, cell.height > 0,
              let point = ScreenGeometry.point(atFraction: fraction, image: image, view: view) else { return nil }
        return CGRect(x: point.x - cell.width / 2, y: point.y - cell.height / 2, width: cell.width, height: cell.height)
    }

    /// The cells `ring` steps out from `center`, the square ring around it (ring 0 is the
    /// centre cell itself), in drawing order.
    static func ring(_ ring: Int, around center: CGRect) -> [CGRect] {
        guard ring > 0 else { return [center] }
        var cells: [CGRect] = []
        for dy in -ring...ring {
            for dx in -ring...ring where max(abs(dx), abs(dy)) == ring {
                cells.append(center.offsetBy(dx: CGFloat(dx) * center.width, dy: CGFloat(dy) * center.height))
            }
        }
        return cells
    }
}
