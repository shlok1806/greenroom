// An action's actionability checks in the guest (daemon ADR 0006 points 4 to 6): each try reads
// the target afresh and runs the checks in order, a failing check waits on the backoff and the
// whole set runs again, and a check that still fails at the timeout refuses the action, naming
// what blocked it. Only the checks retry; the input they clear the way for is posted once, by
// the caller. The decisions are logic/ActionChecks.swift; this file reads what they decide on.

import AppKit
import ApplicationServices
import Foundation

/// Which checks an action needs.
struct CheckPlan {
    /// visible, stable and hit: the action lands at a point of the element.
    var pointer = true
    /// Skip the pointer checks when the element already has the keyboard focus (`type` does not
    /// press a field that is focused).
    var pointerUnlessFocused = false
    /// editable: `type` needs a settable value, `setValue` a settable value or a stepper's
    /// increment and decrement.
    var editable: Editable = .no
    /// frontmost: pointer and key input go to the frontmost app.
    var frontmost = true

    enum Editable {
        case no, text, value
    }
}

/// An element cleared for its action.
struct ActionTarget {
    let look: Look
    /// Where to press, nil when the plan needed no pointer checks.
    let point: CGPoint?
    let tried: [CGPoint]
    let log: CheckLog
    let notes: [String]
    let waitedMs: Int
    /// The element had the keyboard focus already.
    let focused: Bool
    /// The ref's own element was gone and its fingerprint found this one, in any try of the
    /// checks (a later try finds the ref already rebound).
    let reResolved: Bool
}

private enum TryOutcome {
    case ready(point: CGPoint?, tried: [CGPoint], focused: Bool)
    case failed(check: ActionCheck, reason: String, detail: [String: Any]?, cause: [String: Any]?, message: String)
}

/// Runs the checks on a reader's ref until they pass or `timeoutMs` runs out (bounded by the
/// call's deadline less `reserve`, which is kept for the input and its effect). Throws
/// `stale_ref` at once for a ref that names nothing, `not_responding` when the app never answered,
/// and `refused` for a check that still failed at the end.
func actionable(_ ref: String, call: Call, timeoutMs: Int, plan: CheckPlan, reserve: TimeInterval) throws -> ActionTarget {
    let start = DispatchTime.now()
    var end = start + .milliseconds(timeoutMs)
    let latest = call.deadline - .milliseconds(Int(reserve * 1000))
    if latest < end { end = max(latest, start) }
    let budgetMs = Int((end.uptimeNanoseconds - start.uptimeNanoseconds) / 1_000_000)
    func elapsedMs() -> Int { Int((DispatchTime.now().uptimeNanoseconds - start.uptimeNanoseconds) / 1_000_000) }

    var log = CheckLog()
    var notes: [String] = []
    var changedUI = false
    var reResolved = false
    var attempt = 0
    var lastLook: Look?
    var lastFailure: (check: ActionCheck, reason: String, detail: [String: Any]?, cause: [String: Any]?, message: String)?
    var failedAt = DispatchTime.now()
    var pid: pid_t?

    while let wait = nextActionWait(attempt: attempt, elapsedMs: elapsedMs(), timeoutMs: budgetMs) {
        if wait > 0 {
            // Slept as scheduled, not cut short by notifications: an app that animates notifies
            // all the time, and every try costs it a round of AX messages.
            let until = min(DispatchTime.now() + .milliseconds(wait), end)
            let now = DispatchTime.now()
            if until > now { usleep(UInt32((until.uptimeNanoseconds - now.uptimeNanoseconds) / 1000)) }
        }
        try call.check()
        if let failed = lastFailure {
            log.charge(failed.check, ms: Int((DispatchTime.now().uptimeNanoseconds - failedAt.uptimeNanoseconds) / 1_000_000))
        }
        attempt += 1

        // attached
        log.mark(.attached)
        let look: Look
        do {
            look = try busy("ax") { try lookForAction(ref, call: call, until: min(end, call.deadline)) }
        } catch let failure as AgentFailure where failure.code == "not_responding" {
            lastFailure = (.attached, "not_responding", ["error": "not_responding"], nil, failure.message)
            failedAt = DispatchTime.now()
            if DispatchTime.now() >= end { break }
            continue
        }
        lastLook = look
        if pid == nil {
            pid = look.pid
            watch(look.pid)
        }
        if look.reResolved {
            reResolved = true
            log.note(.attached, ["reResolved": true])
        }

        let outcome = busy("ax") {
            tryChecks(look, call: call, plan: plan, log: &log, notes: &notes, changedUI: &changedUI, until: end)
        }
        switch outcome {
        case let .ready(point, tried, focused):
            // Re-read the target when the checks moved it (a scroll): its node is what the
            // action reports as its target.
            let final = changedUI ? ((try? busy("ax") { try lookForAction(ref, call: call, until: call.deadline) }) ?? look) : look
            return ActionTarget(look: final, point: point, tried: tried, log: log, notes: notes,
                                waitedMs: elapsedMs(), focused: focused, reResolved: reResolved)
        case let .failed(check, reason, detail, cause, message):
            log.note(check, detail)
            lastFailure = (check, reason, detail, cause, message)
            failedAt = DispatchTime.now()
        }
    }

    guard let failure = lastFailure else {
        throw AgentFailure("deadline", "\(call.op) had no time left for its checks; try again, with a longer timeoutMs")
    }
    log.charge(failure.check, ms: Int((DispatchTime.now().uptimeNanoseconds - failedAt.uptimeNanoseconds) / 1_000_000))
    if failure.reason == "not_responding" {
        throw AgentFailure("not_responding", failure.message)
    }
    let waited = elapsedMs()
    var detail: [String: Any] = ["reason": failure.reason, "waitedMs": waited, "checks": log.wire]
    if let node = lastLook?.node { detail["target"] = node }
    if let cause = failure.cause {
        detail["by"] = cause
        if failure.reason == "modal" { detail["modal"] = cause }
    }
    throw AgentFailure("refused", "\(failure.message) (after \(waited) ms of checks)", detail: detail)
}

/// Looks at the target for one try: resolved as `resolveRef` does, with the 0.5 s messaging
/// timeout of an action's path, and no hit test (the `hit` check makes its own).
private func lookForAction(_ ref: String, call: Call, until: DispatchTime) throws -> Look {
    try look(ref: ref, reader: call.reader, full: false, hitTest: true, until: until)
}

/// One try of every check after `attached`, in `checkOrder`'s order; the first that fails ends
/// the try.
private func tryChecks(_ look: Look, call: Call, plan: CheckPlan, log: inout CheckLog, notes: inout [String],
                       changedUI: inout Bool, until end: DispatchTime) -> TryOutcome {
    var look = look
    let label = targetLabel(look)
    // Focused means it has the keyboard, not only its window's focus: a field whose window is
    // under a panel that took the keyboard (navlab's Inspector) still says AXFocused.
    let focused = look.read.focused == true && focusedElement().map { CFEqual($0, look.element) } == true
    let pointer = plan.pointer && !(plan.pointerUnlessFocused && focused)
    var point: CGPoint?
    var tried: [CGPoint] = []

    for check in checkOrder(pointer: pointer, editable: plan.editable != .no, frontmost: plan.frontmost) {
        log.mark(check)
        switch check {
        case .attached:
            break

        case .modal:
            if let modal = blockingModalCause(of: look, reader: call.reader) {
                let kind = modal["kind"] as? String ?? "sheet"
                return .failed(check: .modal, reason: check.reason, detail: ["kind": kind], cause: modal,
                               message: "\(label) is behind a \(kind)\(named(modal)); handle the \(kind) first")
            }

        case .visible:
            if (look.node["states"] as? [String])?.contains("overflow") == true {
                return .failed(check: .visible, reason: "hidden", detail: ["overflow": true], cause: nil,
                               message: "\(label) is in its toolbar's overflow; press the toolbar's >> button first")
            }
            // Out of view in a scroll area: scrolled in, as `scroll {to: ref}` does.
            if look.shown.vis == nil, look.shown.offscreen != nil, let scrollerRef = look.shown.scroller {
                let scrolled = scrollTargetIntoView(look, scrollerRef: scrollerRef, call: call, until: end)
                if scrolled.steps > 0 || scrolled.via != nil {
                    changedUI = true
                    var note = "scrolled \(scrollerRef) to bring \(look.ref) into view"
                    if let via = scrolled.via { note += " (\(via), \(scrolled.steps) steps)" }
                    if !notes.contains(note) { notes.append(note) }
                    log.note(.visible, ["scrolled": scrollerRef, "steps": scrolled.steps, "via": scrolled.via ?? "wheel"])
                }
                if let again = try? lookForAction(look.ref, call: call, until: min(end, call.deadline)) { look = again }
            }
            if look.shown.vis == nil {
                var detail: [String: Any] = [:]
                if let offscreen = look.shown.offscreen { detail["offscreen"] = offscreen }
                if let scroller = look.shown.scroller { detail["scroller"] = scroller }
                let reason = visibilityReason(offscreen: look.shown.offscreen)
                let message = reason == "offscreen"
                    ? "\(label) is out of view in \(look.shown.scroller ?? "its scroll area") (\(look.shown.offscreen ?? "")) and scrolling did not bring it in"
                    : "\(label) shows nothing on screen (its window may be minimized, hidden or off the screen)"
                return .failed(check: .visible, reason: reason, detail: detail, cause: nil, message: message)
            }

        case .enabled:
            if look.read.enabled == false {
                return .failed(check: .enabled, reason: check.reason, detail: nil, cause: nil, message: "\(label) is disabled")
            }

        case .editable:
            let settable = isSettable(look.element, kAXValueAttribute)
            if plan.editable == .text, !settable {
                return .failed(check: .editable, reason: check.reason, detail: nil, cause: nil,
                               message: "\(label) takes no typed text (its value is not editable)")
            }
            if plan.editable == .value, !settable, !hasStepper(look.element) {
                return .failed(check: .editable, reason: check.reason, detail: nil, cause: nil,
                               message: "\(label) has no value that can be set, and no increment or decrement")
            }

        case .stable:
            let first = look.read.frame
            usleep(50_000)
            let second = readElement(look.element).frame
            if !isStable(first, second) {
                var detail: [String: Any] = [:]
                if let first { detail["from"] = wireRect(first) }
                if let second { detail["to"] = wireRect(second) }
                return .failed(check: .stable, reason: check.reason, detail: detail, cause: nil,
                               message: "\(label) kept moving (its frame changed between reads)")
            }

        case .frontmost:
            switch bringToFront(look, until: end) {
            case .already:
                break
            case let .activated(name):
                changedUI = true
                let note = "activated \(name.isEmpty ? "the app" : name) (it was in the background)"
                if !notes.contains(note) { notes.append(note) }
                log.note(.frontmost, ["activated": name])
                if let again = try? lookForAction(look.ref, call: call, until: min(end, call.deadline)) { look = again }
            case let .refused(front):
                var cause: [String: Any] = [:]
                if !front.isEmpty { cause["app"] = front }
                return .failed(check: .frontmost, reason: check.reason, detail: nil, cause: cause.isEmpty ? nil : cause,
                               message: "the app of \(label) could not be brought to the front\(front.isEmpty ? "" : " (\(front) stayed in front)")")
            }

        case .hit:
            guard let vis = look.shown.vis else {
                return .failed(check: .visible, reason: "hidden", detail: nil, cause: nil, message: "\(label) shows nothing on screen")
            }
            var firstCover: [String: Any]?
            for candidate in pressPoints(in: vis) {
                if DispatchTime.now() >= call.deadline { break }
                tried.append(candidate)
                let test = hitTest(at: candidate, target: look.context)
                if hitReaches(test.relation, role: look.role) {
                    point = candidate
                    break
                }
                if firstCover == nil { firstCover = coverer(of: look.context, hit: test, reader: call.reader) }
            }
            if let point {
                log.note(.hit, ["point": wirePoint(point), "of": tried.count])
                break
            }
            var cause: [String: Any]?
            if let cover = firstCover ?? look.covered {
                var named: [String: Any] = [:]
                if let by = cover["by"] { named["ref"] = by }
                for key in ["role", "name", "where", "app"] { if let value = cover[key] { named[key] = value } }
                cause = named
            }
            return .failed(check: .hit, reason: check.reason, detail: ["tried": tried.count], cause: cause,
                           message: "\(label) is covered\(cause.map { " by \(describeCause($0))" } ?? "") at every point tried")
        }
    }
    return .ready(point: point, tried: tried, focused: focused)
}

// MARK: - Modal

/// The sheet, alert or open menu that blocks an element, as a refusal's `by`: `{ref, kind, role,
/// name, app, pid}`. Nil when none does or the element is inside it.
private func blockingModalCause(of look: Look, reader: String) -> [String: Any]? {
    let chain = look.context.chain
    let appName = processName(look.pid)
    func cause(_ element: AXUIElement, kind: String, role: String, name: String) -> [String: Any] {
        var out: [String: Any] = ["kind": kind, "role": wireRole(role), "pid": Int(look.pid)]
        if let context = elementContext(element).context {
            out["ref"] = knownRef(element, reader: reader) ?? giveRef(element, context.fingerprint(), reader: reader)
        }
        if !name.isEmpty { out["name"] = cut(name).text }
        if !appName.isEmpty { out["app"] = appName }
        return out
    }

    // An open menu of the app takes every click until it closes.
    let menus = openMenus(of: look.pid, in: screenWindowList(), screen: bounds)
    if !menus.isEmpty, !look.context.reads.contains(where: { $0.role == "AXMenu" }) {
        let menu = menus[0]
        let center = CGPoint(x: menu.bounds.midX, y: menu.bounds.midY)
        if let hit = elementAt(center), let context = elementContext(hit).context,
           let at = context.reads.firstIndex(where: { $0.role == "AXMenu" }) {
            return cause(context.chain[at].element, kind: "menu", role: "AXMenu", name: context.reads[at].name)
        }
        var out: [String: Any] = ["kind": "menu", "role": "Menu", "pid": Int(look.pid)]
        if !appName.isEmpty { out["app"] = appName }
        return out
    }

    // A sheet on the element's window blocks that window.
    if let windowIndex = look.context.windowIndex {
        for child in look.context.reads[windowIndex].children.prefix(64) {
            let read = readElement(child)
            guard read.ok, read.role == "AXSheet" else { continue }
            if blockingModal(chain: chain, modals: [AXHandle(child)]) != nil {
                return cause(child, kind: "sheet", role: read.role, name: read.name)
            }
        }
    }

    // An app-modal alert or dialog blocks every other window of the app.
    guard let app = appTarget(pid: look.pid) else { return nil }
    AXUIElementSetMessagingTimeout(app.root, actionMessagingTimeout)
    for window in appWindows(app).windows.prefix(16) {
        guard flagAttribute(window, kAXModalAttribute) == true, flagAttribute(window, kAXMinimizedAttribute) != true else { continue }
        let read = readElement(window)
        guard read.ok, blockingModal(chain: chain, modals: [AXHandle(window)]) != nil else { continue }
        let kind = attentionKind(windowSubrole: read.subrole) ?? "dialog"
        return cause(window, kind: kind, role: read.role, name: read.name)
    }
    return nil
}

// MARK: - Frontmost

private enum Front {
    case already
    case activated(String)
    /// The name of the app that stayed in front.
    case refused(String)
}

/// Brings the element's app to the front and its window to the top, and waits (up to half a
/// second, within the checks' time) for the window server to agree.
private func bringToFront(_ look: Look, until end: DispatchTime) -> Front {
    let front = NSWorkspace.shared.frontmostApplication
    if front?.processIdentifier == look.pid { return .already }
    let app = AXUIElementCreateApplication(look.pid)
    AXUIElementSetMessagingTimeout(app, actionMessagingTimeout)
    // Through accessibility first: a background process's NSRunningApplication.activate may be
    // declined by the cooperative activation of macOS 14 and later.
    AXUIElementSetAttributeValue(app, kAXFrontmostAttribute as CFString, kCFBooleanTrue)
    if let window = look.context.window {
        AXUIElementPerformAction(window, kAXRaiseAction as CFString)
        AXUIElementSetAttributeValue(window, kAXMainAttribute as CFString, kCFBooleanTrue)
    }
    let running = NSRunningApplication(processIdentifier: look.pid)
    var limit = DispatchTime.now() + .milliseconds(500)
    if end < limit { limit = end }
    var asked = false
    while true {
        if NSWorkspace.shared.frontmostApplication?.processIdentifier == look.pid || running?.isActive == true {
            return .activated(running?.localizedName ?? processName(look.pid))
        }
        if DispatchTime.now() >= limit { break }
        if !asked {
            running?.activate(options: [])
            asked = true
        }
        let stamp = UIWaker.shared.stamp(nil)
        nap(50, for: nil, since: stamp, until: limit)
    }
    return .refused(NSWorkspace.shared.frontmostApplication?.localizedName ?? front?.localizedName ?? "")
}

// MARK: - Helpers

/// Whether an element steps its value (a stepper, a slider that refuses a set value).
func hasStepper(_ element: AXUIElement) -> Bool {
    let actions = actionNames(element)
    return actions.contains(kAXIncrementAction) && actions.contains(kAXDecrementAction)
}

/// The actions an element lists, empty when it lists none or cannot be asked.
func actionNames(_ element: AXUIElement) -> [String] {
    var names: CFArray?
    guard AXUIElementCopyActionNames(element, &names) == .success else { return [] }
    return (names as? [String]) ?? []
}

/// An element in a sentence: `e41 Button "Open run"`.
func targetLabel(_ look: Look) -> String {
    var label = "\(look.ref) \(wireRole(look.role))"
    let name = look.read.name
    if !name.isEmpty { label += " \"\(cut(name, limit: 60).text)\"" }
    return label
}

private func named(_ cause: [String: Any]) -> String {
    var out = ""
    if let ref = cause["ref"] as? String { out += " \(ref)" }
    if let name = cause["name"] as? String, !name.isEmpty { out += " \"\(cut(name, limit: 60).text)\"" }
    return out
}

/// A coverer in a sentence: `e70 List "Runs" (in this window)`.
func describeCause(_ cause: [String: Any]) -> String {
    var parts: [String] = []
    if let ref = cause["ref"] as? String { parts.append(ref) }
    if let role = cause["role"] as? String { parts.append(role) }
    if let name = cause["name"] as? String, !name.isEmpty { parts.append("\"\(cut(name, limit: 60).text)\"") }
    var out = parts.isEmpty ? "something" : parts.joined(separator: " ")
    switch cause["where"] as? String {
    case "window": out += " (in this window)"
    case "app": out += " (in another window of the app)"
    case "other": out += " (in \(cause["app"] as? String ?? "another app"))"
    default: break
    }
    return out
}
