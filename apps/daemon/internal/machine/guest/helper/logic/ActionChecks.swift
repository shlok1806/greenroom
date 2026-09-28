// The decisions of an action's actionability checks (daemon ADR 0006 points 4 to 6): the checks
// and their order, the backoff between tries, the points a press may land on, what a hit test
// at one of them must find, and the log of how long each check held the action up. Pure: the
// guest reads the facts (Actionability.swift), the host tests run recorded ones.

import CoreGraphics
import Foundation

/// How long an action's checks may run: 5 s unless the request says, never over 30 s (daemon ADR
/// 0006 point 10).
let defaultActionTimeoutMs = 5000
let maxActionTimeoutMs = 30000

func actionTimeoutMs(_ requested: Int?) -> Int {
    guard let requested, requested > 0 else { return defaultActionTimeoutMs }
    return min(requested, maxActionTimeoutMs)
}

/// The checks, in the order they run. The raw values are the wire's `checks[].check`.
enum ActionCheck: String, CaseIterable {
    /// The ref still names an element (or re-resolves to exactly one).
    case attached
    /// The target app has no sheet, alert or open menu the element is outside of.
    case modal
    /// Something of the element shows; an element scrolled out of view is scrolled in first.
    case visible
    /// AXEnabled is not false.
    case enabled
    /// The element takes a typed text or a value (type and setValue only).
    case editable
    /// The same frame in two reads 50 ms apart.
    case stable
    /// A hit test at a point of it finds it (or something inside it).
    case hit
    /// Its app is frontmost, activated when it is not.
    case frontmost

    /// The `refused` reason when this check still fails at the timeout. `visible` has two:
    /// `offscreen` for an element a scroll area hides, `hidden` for anything else.
    var reason: String {
        switch self {
        case .attached: return "hidden"
        case .modal: return "modal"
        case .visible: return "hidden"
        case .enabled: return "disabled"
        case .editable: return "not_editable"
        case .stable: return "unstable"
        case .hit: return "covered"
        case .frontmost: return "not_frontmost"
        }
    }
}

/// Which checks an action runs after `attached`, in the order they run. `pointer` is whether the
/// action lands at a point of the element (visible, stable, hit); `editable` whether it needs a
/// value it can change; `frontmost` whether its input goes to the frontmost app.
///
/// `frontmost` runs before `hit`, where daemon ADR 0006 point 4 lists it last: bringing an app
/// to the front raises its window, which changes what a hit test at its points finds, so a hit
/// test made before it would blame the window that was in front for covering the element.
func checkOrder(pointer: Bool, editable: Bool, frontmost: Bool) -> [ActionCheck] {
    var checks: [ActionCheck] = [.modal]
    if pointer { checks.append(.visible) }
    checks.append(.enabled)
    if editable { checks.append(.editable) }
    if pointer { checks.append(.stable) }
    if frontmost { checks.append(.frontmost) }
    if pointer { checks.append(.hit) }
    return checks
}

/// The waits before each try, Playwright's backoff: 0, 20, 100, 100, 500 ms, then every 500 ms.
let actionBackoffMs = [0, 20, 100, 100, 500]

/// The wait before try `attempt` (0 is the first).
func actionBackoff(attempt: Int) -> Int {
    attempt >= 0 && attempt < actionBackoffMs.count ? actionBackoffMs[attempt] : actionBackoffMs[actionBackoffMs.count - 1]
}

/// The wait before try `attempt`, or nil when the checks are out of time: a try that would start
/// after the timeout is not made. `elapsedMs` is how long the checks have run.
func nextActionWait(attempt: Int, elapsedMs: Int, timeoutMs: Int) -> Int? {
    let wait = actionBackoff(attempt: attempt)
    if attempt > 0, elapsedMs + wait > timeoutMs { return nil }
    return wait
}

/// How much a frame may move between the two reads of `stable` and still be the same frame:
/// layout that jitters by less than half a point is not an animation.
let stableTolerance: CGFloat = 0.5

func isStable(_ first: CGRect?, _ second: CGRect?) -> Bool {
    guard let a = first.flatMap(finiteRect), let b = second.flatMap(finiteRect) else { return false }
    return abs(a.minX - b.minX) <= stableTolerance && abs(a.minY - b.minY) <= stableTolerance
        && abs(a.width - b.width) <= stableTolerance && abs(a.height - b.height) <= stableTolerance
}

/// The points of a visible rect a press tries: its center, then the other eight points of a 3x3
/// grid (at a sixth, a half and five sixths of each side), edges before corners, so a partly
/// covered control is still reached where it shows. A side too short for three points apart
/// has only its middle, so a sliver of a rect is not tried nine times.
func pressPoints(in rect: CGRect) -> [CGPoint] {
    grid(rect, divisions: 3)
}

/// The least room a grid cell needs on a side: closer points than this are the same place.
private let gridCell: CGFloat = 2

/// The points of a rect on an n by n grid (n odd), the center first, then by distance from it.
/// A side shorter than n cells has only its middle.
func grid(_ rect: CGRect, divisions n: Int) -> [CGPoint] {
    guard let rect = finiteRect(rect), rectShows(rect), n >= 1 else { return [] }
    let columns = rect.width >= CGFloat(n) * gridCell ? n : 1
    let rows = rect.height >= CGFloat(n) * gridCell ? n : 1
    var points: [(point: CGPoint, distance: Int, order: Int)] = []
    for row in 0..<rows {
        for column in 0..<columns {
            let x = rect.minX + rect.width * CGFloat(2 * column + 1) / CGFloat(2 * columns)
            let y = rect.minY + rect.height * CGFloat(2 * row + 1) / CGFloat(2 * rows)
            // Distance in cells from the middle: the center, then edge midpoints, then corners.
            let distance = abs(2 * row + 1 - rows) + abs(2 * column + 1 - columns)
            let point = CGPoint(x: (x * 100).rounded() / 100, y: (y * 100).rounded() / 100)
            points.append((point, distance, points.count))
        }
    }
    return points.sorted { ($0.distance, $0.order) < ($1.distance, $1.order) }.map(\.point)
}

/// Roles that take no events of their own, whose press lands on what holds them: a label inside
/// a button, an icon inside a cell. A hit on an ancestor of one of these is a hit on it.
let passThroughRoles: Set<String> = ["AXStaticText", "AXImage"]

/// Whether a hit test at a point of an element lets a press there reach it: the element itself or
/// something inside it; for static text and images, also something they are inside.
func hitReaches(_ relation: HitRelation, role: String) -> Bool {
    switch relation {
    case .same, .descendant: return true
    case .ancestor: return passThroughRoles.contains(role)
    case .other: return false
    }
}

/// The first of the modal surfaces (sheets, alerts, open menus) that an element is not inside,
/// given the element's chain of parents: the one that blocks it. Nil when there is none, or the
/// element is inside one of them.
func blockingModal<Handle: Hashable>(chain: [Handle], modals: [Handle]) -> Handle? {
    if modals.isEmpty { return nil }
    if modals.contains(where: { chain.contains($0) }) { return nil }
    return modals.first
}

/// The `visible` check's refusal reason: `offscreen` for an element a scroll area hides, else
/// `hidden`.
func visibilityReason(offscreen: String?) -> String {
    offscreen != nil ? "offscreen" : "hidden"
}

/// How long each check held an action up, for the result's `checks`: the time between two tries
/// is charged to the check that failed the first of them. Every check that ran is listed, in
/// order, with 0 ms when it passed at once.
struct CheckLog {
    private(set) var ms: [ActionCheck: Int] = [:]
    private(set) var details: [ActionCheck: [String: Any]] = [:]
    private(set) var ran: [ActionCheck] = []

    mutating func mark(_ check: ActionCheck) {
        if !ran.contains(check) { ran.append(check) }
    }

    mutating func charge(_ check: ActionCheck, ms added: Int) {
        mark(check)
        ms[check, default: 0] += max(0, added)
    }

    /// Replaces what a check says about itself (the last failure, or how it passed).
    mutating func note(_ check: ActionCheck, _ detail: [String: Any]?) {
        mark(check)
        details[check] = detail
    }

    /// The wire's `checks`: `[{check, ms, detail?}]` in the order the checks run.
    var wire: [[String: Any]] {
        ActionCheck.allCases.filter { ran.contains($0) }.map { check in
            var item: [String: Any] = ["check": check.rawValue, "ms": ms[check] ?? 0]
            if let detail = details[check], !detail.isEmpty { item["detail"] = detail }
            return item
        }
    }
}
