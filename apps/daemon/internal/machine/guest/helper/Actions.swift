// The acting ops of the toolkit (daemon ADR 0006, "Ops and results" and points 4 to 8): `press`,
// `type`, `setValue` and `key`. Each clears its target with the actionability checks
// (Actionability.swift), reads the app's tree, posts its input once, waits for the app to
// settle and reads the tree again: the daemon computes the effect from `before` and `after`.
// The input is never repeated, whatever the read-back says.

import AppKit
import ApplicationServices
import CoreGraphics
import Foundation

func registerActionOps() {
    register("press", .input) { call in
        let args = try call.args(PressArgs.self)
        try requireAccessibility()
        return try press(args, call: call)
    }
    register("type", .input) { call in
        let args = try call.args(TypeArgs.self)
        try requireAccessibility()
        return try typeText(args, call: call)
    }
    register("setValue", .input) { call in
        let args = try call.args(SetValueArgs.self)
        try requireAccessibility()
        return try setValue(args, call: call)
    }
    register("key", .input) { call in
        let args = try call.args(KeyArgs.self)
        try requireAccessibility()
        return try pressKey(args, call: call)
    }
}

/// What is kept of a call's deadline for the input, the settle and the after tree once the
/// checks end: the settle's 2 s, a tree, and room to answer.
private let effectReserve: TimeInterval = 3.5

// MARK: - The run of one action

/// The part every action shares: the before tree, the settle and the after tree of its app.
final class ActionRun {
    let app: AppTarget
    let call: Call
    private(set) var before: AppTree?

    init(app: AppTarget, call: Call) {
        self.app = app
        self.call = call
        watch(app.pid)
    }

    /// A tree's wall budget: 1.5 s, less when the call has little time left.
    private func treeBudget(keep: TimeInterval) -> TimeInterval {
        max(0.2, min(1.5, call.remaining - keep))
    }

    /// Reads the before tree. A fresh agent's first read of a window can see its chrome add a
    /// child, so one signature is taken first to let that happen before the tree that counts.
    func readBefore() {
        _ = busy("ax") { treeSignature(app, call: call, budget: 0.5) }
        before = busy("ax") { appTree(app, reader: call.reader, call: call, budget: treeBudget(keep: effectReserve)) }
    }

    func settle() -> Settled {
        settleUI(app, call: call, reserve: 1)
    }

    /// The result fields every action has: `before`, `after`, `settled`, `settledMs`, `appGone`.
    /// A tree of an app that stopped answering is left out, so the effect is unverifiable
    /// rather than a false "no change".
    func finish(_ settled: Settled) -> [String: Any] {
        var result: [String: Any] = ["settled": settled.settled, "settledMs": settled.ms]
        if let before, before.responding { result["before"] = before.object }
        let gone = settled.gone || !processLives(pid: app.pid, started: app.started)
        if gone {
            result["appGone"] = true
            return result
        }
        let after = busy("ax") { appTree(app, reader: call.reader, call: call, budget: max(0.2, min(1.5, call.remaining - 0.3))) }
        if after.responding { result["after"] = after.object }
        return result
    }
}

/// The fields of a target cleared by its checks.
private func checkedFields(_ target: ActionTarget, via: String) -> [String: Any] {
    var result: [String: Any] = ["target": target.look.node, "via": via, "checks": target.log.wire, "waitedMs": target.waitedMs]
    if let point = target.point { result["point"] = wirePoint(point) }
    if !target.tried.isEmpty { result["tried"] = target.tried.map(wirePoint) }
    if !target.notes.isEmpty { result["notes"] = target.notes }
    if target.look.reResolved { result["reResolved"] = true }
    return result
}

/// Something that appeared over the target at the pressed point after the input, as a node;
/// nil when the point still reaches the target, or the target is gone (a press that navigated
/// away has nothing over it).
private func overlay(at point: CGPoint, over target: Look, reader: String) -> [String: Any]? {
    guard readElement(target.element).ok else { return nil }
    let test = hitTest(at: point, target: target.context)
    guard test.hit != nil, !hitReaches(test.relation, role: target.role),
          let at = covererIndex(hit: test.chain, isListed: { knownRef($0.element, reader: reader) != nil }) else { return nil }
    return look(element: test.chain[at].element, reader: reader, hitTest: false)?.node
}

/// Throws AgentFailure for a Failure of the input code (Input.swift), which speaks to its old
/// one-shot callers.
private func inputFailure<T>(_ body: () throws -> T) throws -> T {
    do {
        return try body()
    } catch let failure as Failure {
        throw AgentFailure("internal", failure.message)
    }
}

// MARK: - Posting

private func keyEvent(_ code: CGKeyCode, down: Bool, flags: CGEventFlags) {
    guard let event = CGEvent(keyboardEventSource: source, virtualKey: code, keyDown: down) else { return }
    event.flags = flags
    post(event)
}

/// A press at a point: the pointer moves there, the modifiers go down as real key events, the
/// clicks carry click state 1, 2, 3 (catalog I2), the modifiers come up. Returns once the window
/// server has applied every event.
private func postPress(at point: CGPoint, button name: String, count: Int, mods: [String]) throws {
    let (button, downType, upType, _) = mouseButton(name)
    let modifiers = flags(mods)
    let heldKeys = modifierKeys.filter { modifiers.contains($0.0) }
    let base = sessionEvents()
    let first = posted
    var state: CGEventFlags = []
    for (flag, key) in heldKeys {
        state.insert(flag)
        keyEvent(key, down: true, flags: state)
    }
    cursor = point
    if let move = CGEvent(mouseEventSource: source, mouseType: .mouseMoved, mouseCursorPosition: point, mouseButton: button) {
        move.flags = modifiers
        post(move)
    }
    for click in clickSequence(count: count) {
        guard let event = CGEvent(mouseEventSource: source, mouseType: click.down ? downType : upType,
                                  mouseCursorPosition: point, mouseButton: button) else { continue }
        event.setIntegerValueField(.mouseEventClickState, value: Int64(click.state))
        event.flags = modifiers
        post(event)
    }
    for (flag, key) in heldKeys.reversed() {
        state.remove(flag)
        keyEvent(key, down: false, flags: state)
    }
    try inputFailure { try settle(since: base, want: posted &- first) }
}

/// A key with modifiers, as `press(key:mods:)` posts it, waited for.
private func postKey(_ name: String, mods: [String]) throws {
    let base = sessionEvents()
    let first = posted
    do {
        try inputFailure { try press(key: name, mods: mods) }
    } catch {
        try? settle(since: base, want: posted &- first)
        throw error
    }
    try inputFailure { try settle(since: base, want: posted &- first) }
}

/// The keys a typed character that is a control is sent as, in either typing mode: a line end is
/// the return key and a tab the tab key, which is what an app acts on.
private func controlKey(_ character: Character) -> CGKeyCode? {
    switch character {
    case "\n", "\r", "\r\n": return 36
    case "\t": return 48
    default: return nil
    }
}

/// One character as the event's Unicode string (catalog I9's `unicode`): exact whatever the
/// layout. A key event carries at most 20 UTF-16 units, so a longer cluster goes in parts.
private func postUnicode(_ character: Character) {
    if let code = controlKey(character) {
        keyEvent(code, down: true, flags: [])
        usleep(1_000)
        keyEvent(code, down: false, flags: [])
        return
    }
    let units = Array(String(character).utf16)
    var start = 0
    while start < units.count {
        let part = Array(units[start..<min(start + 20, units.count)])
        start += part.count
        guard let down = CGEvent(keyboardEventSource: source, virtualKey: 0, keyDown: true),
              let up = CGEvent(keyboardEventSource: source, virtualKey: 0, keyDown: false) else { continue }
        part.withUnsafeBufferPointer { buffer in
            guard let base = buffer.baseAddress else { return }
            down.keyboardSetUnicodeString(stringLength: part.count, unicodeString: base)
            up.keyboardSetUnicodeString(stringLength: part.count, unicodeString: base)
        }
        // Text is never a shortcut, whatever modifier state the source holds.
        down.flags = []
        up.flags = []
        post(down)
        usleep(1_000)
        post(up)
    }
}

/// One character as a US keyboard sends it (catalog I9's `keys`): shift down around the key when
/// it needs shift.
private func postStroke(_ stroke: KeyStroke) {
    let shift: CGKeyCode = 56
    if stroke.shift { keyEvent(shift, down: true, flags: .maskShift) }
    let flags: CGEventFlags = stroke.shift ? .maskShift : []
    keyEvent(CGKeyCode(stroke.code), down: true, flags: flags)
    usleep(1_000)
    keyEvent(CGKeyCode(stroke.code), down: false, flags: flags)
    if stroke.shift { keyEvent(shift, down: false, flags: []) }
}

// MARK: - press

private struct PressArgs: Decodable {
    var ref: String?
    var point: [Double]?
    var reason: String?
    var force: Bool?
    var button: String?
    var count: Int?
    var mods: [String]?
    var via: String?
    var timeoutMs: Int?
}

private func validateMods(_ mods: [String]?) throws -> [String] {
    let mods = mods ?? []
    if let bad = unknownModifier(mods) {
        throw AgentFailure("bad_request", "unknown modifier \(bad); use cmd, shift, alt, ctrl or fn")
    }
    return mods
}

private func press(_ args: PressArgs, call: Call) throws -> [String: Any] {
    let button = (args.button ?? "left").lowercased()
    guard pressButtons.contains(button) else {
        throw AgentFailure("bad_request", "unknown button \(args.button ?? ""); use left, right or middle")
    }
    let count = args.count ?? 1
    guard (1...maxPressCount).contains(count) else {
        throw AgentFailure("bad_request", "count \(count) is out of range; pass 1 (a click), 2 (a double click) or 3")
    }
    let mods = try validateMods(args.mods)
    let via = (args.via ?? "pointer").lowercased()
    guard via == "pointer" || via == "ax" else {
        throw AgentFailure("bad_request", "via \(via) is not one of pointer, ax")
    }
    switch (meaningful(args.ref), args.point) {
    case let (ref?, nil):
        if via == "ax" {
            guard count == 1, mods.isEmpty else {
                throw AgentFailure("bad_request", "via \"ax\" presses once and holds no modifiers; leave out count and mods, or press with the pointer")
            }
            return try pressByAX(ref, args: args, call: call)
        }
        return try pressRef(ref, button: button, count: count, mods: mods, args: args, call: call)
    case let (nil, point?):
        guard via == "pointer" else {
            throw AgentFailure("bad_request", "via \"ax\" presses an element and needs its ref; a point is pressed with the pointer")
        }
        return try pressPoint(point, button: button, count: count, mods: mods, args: args, call: call)
    case (_?, _?):
        throw AgentFailure("bad_request", "pass ref or point, not both; a ref is better when the element has one")
    case (nil, nil):
        throw AgentFailure("bad_request", "press needs ref (from machine_snapshot or machine_find), or point with a reason")
    }
}

private func pressRef(_ ref: String, button: String, count: Int, mods: [String], args: PressArgs, call: Call) throws -> [String: Any] {
    let timeoutMs = actionTimeoutMs(args.timeoutMs)
    let app = try appOfRef(ref, call: call)
    let run = ActionRun(app: app, call: call)
    run.readBefore()
    let target = try actionable(ref, call: call, timeoutMs: timeoutMs, plan: CheckPlan(), reserve: effectReserve)
    if target.changedUI { run.readBefore() }
    guard let point = target.point else {
        throw AgentFailure("internal", "the checks cleared \(ref) without a point to press")
    }
    try call.check()
    try postPress(at: point, button: button, count: count, mods: mods)

    let settled = run.settle()
    var result = checkedFields(target, via: "pointer")
    if let over = busy("ax", { overlay(at: point, over: target.look, reader: call.reader) }) { result["overlay"] = over }
    return result.merging(run.finish(settled)) { $1 }
}

private func pressByAX(_ ref: String, args: PressArgs, call: Call) throws -> [String: Any] {
    let timeoutMs = actionTimeoutMs(args.timeoutMs)
    let app = try appOfRef(ref, call: call)
    let run = ActionRun(app: app, call: call)
    run.readBefore()
    var plan = CheckPlan()
    plan.pointer = false
    plan.frontmost = false
    let target = try actionable(ref, call: call, timeoutMs: timeoutMs, plan: plan, reserve: effectReserve)
    let listed = actionNames(target.look.element)
    guard let action = [kAXPressAction, kAXConfirmAction, kAXPickAction].first(where: listed.contains) else {
        throw AgentFailure("bad_request", "\(targetLabel(target.look)) lists no press action (it lists \(listed.isEmpty ? "none" : listed.joined(separator: ", "))); press it with the pointer")
    }
    try call.check()
    var notes = target.notes
    let error = AXUIElementPerformAction(target.look.element, action as CFString)
    switch error {
    case .success:
        break
    case .cannotComplete:
        // "Does not necessarily mean that the function has failed": a SwiftUI AXPress can
        // return before its effect lands. The effect says what happened.
        notes.append("the app did not confirm \(action) in time; it may still have happened, the effect below says")
    default:
        throw AgentFailure("ax_error", "\(action) on \(targetLabel(target.look)) failed (AXError \(error.rawValue)); press it with the pointer")
    }
    let settled = run.settle()
    var result = checkedFields(target, via: "ax")
    result["action"] = action
    if !notes.isEmpty { result["notes"] = notes }
    return result.merging(run.finish(settled)) { $1 }
}

/// The element at a point that has a ref in the reader's table: the hit itself, or what holds it
/// up to the first container (a label inside a button is the button's). A container's ref (a
/// window, a scroll area, a canvas group) never makes a point ambiguous: a point is how content
/// inside one is pressed.
private func refAtPoint(_ hit: AXUIElement, reader: String) -> (ref: String, element: AXUIElement)? {
    guard let context = elementContext(hit).context else { return nil }
    for (index, handle) in context.chain.enumerated() {
        if holderRoles.contains(context.reads[index].role) { break }
        if let ref = knownRef(handle.element, reader: reader) { return (ref, handle.element) }
    }
    return nil
}

private func pressPoint(_ values: [Double], button: String, count: Int, mods: [String], args: PressArgs, call: Call) throws -> [String: Any] {
    guard values.count == 2, values.allSatisfy(\.isFinite) else {
        throw AgentFailure("bad_request", "point must be [x, y] in guest points")
    }
    let point = CGPoint(x: values[0], y: values[1])
    guard bounds.contains(point) else {
        throw AgentFailure("bad_request", "point (\(Int(point.x)), \(Int(point.y))) is off the \(Int(bounds.width))x\(Int(bounds.height)) screen")
    }
    guard meaningful(args.reason) != nil else {
        throw AgentFailure("bad_request", "a press at a point needs a reason: say why the target has no ref (a canvas, a game); otherwise pass its ref")
    }
    let hit = busy("ax") { elementAt(point) }
    if let hit, args.force != true, let known = busy("ax", { refAtPoint(hit, reader: call.reader) }) {
        let node = busy("ax") { look(element: known.element, reader: call.reader, hitTest: false)?.node } ?? ["ref": known.ref]
        throw AgentFailure("ambiguous", "the point (\(Int(point.x)), \(Int(point.y))) hits \(known.ref), which has a ref; use \(known.ref) (pass force to press the point anyway)",
                           detail: ["candidates": [node], "ref": known.ref])
    }
    let app: AppTarget
    if let hit, let owner = appTarget(pid: AXHandle(hit).pid) {
        app = owner
    } else {
        app = try appTarget(named: nil)
    }
    let run = ActionRun(app: app, call: call)
    run.readBefore()
    let targetLook = hit.flatMap { element in busy("ax") { look(element: element, reader: call.reader, hitTest: false) } }
    try call.check()
    try postPress(at: point, button: button, count: count, mods: mods)
    let settled = run.settle()
    var result: [String: Any] = ["point": wirePoint(point), "tried": [wirePoint(point)], "via": "pointer", "checks": [] as [Any], "waitedMs": 0]
    if let targetLook { result["target"] = targetLook.node }
    return result.merging(run.finish(settled)) { $1 }
}

/// The app a ref's element is in, for its before tree: `stale_ref` when it quit.
private func appOfRef(_ ref: String, call: Call) throws -> AppTarget {
    let resolved = try busy("ax") { try resolveRef(ref, reader: call.reader, until: min(call.deadline, .now() + .seconds(3))) }
    guard let app = appTarget(pid: resolved.pid) else {
        throw AgentFailure("stale_ref", "\(ref)'s app is no longer running (it quit or crashed); take a new machine_snapshot",
                           detail: ["ref": ref, "reason": "app_quit"])
    }
    return app
}

// MARK: - type

private struct TypeArgs: Decodable {
    var ref: String?
    var text: String
    var replace: Bool?
    var submit: String?
    var via: String?
    var paceMs: Int?
    var timeoutMs: Int?
}

/// Reads an element's value until it stops changing (two reads 100 ms apart agree), at most 1 s
/// and never into the time kept for the effect.
private func settledValue(_ element: AXUIElement, call: Call) -> String? {
    var last = textAttribute(element, kAXValueAttribute)
    let limit = min(DispatchTime.now() + .seconds(1), call.deadline - .milliseconds(Int(effectReserve * 1000) / 2))
    while DispatchTime.now() < limit {
        usleep(100_000)
        let now = textAttribute(element, kAXValueAttribute)
        if now == last { return now }
        last = now
    }
    return last
}

private func typeText(_ args: TypeArgs, call: Call) throws -> [String: Any] {
    let replace = args.replace ?? false
    let submit = meaningful(args.submit)?.lowercased()
    if let submit, submit != "return", submit != "tab" {
        throw AgentFailure("bad_request", "submit \(submit) is not one of return, tab; leave it out to press nothing after the text")
    }
    let via = (args.via ?? "unicode").lowercased()
    var strokes: [KeyStroke] = []
    switch via {
    case "unicode":
        break
    case "keys":
        switch usKeyStrokes(for: args.text) {
        case let .success(found): strokes = found
        case let .failure(unmapped): throw AgentFailure("bad_request", unmapped.message)
        }
    default:
        throw AgentFailure("bad_request", "via \(via) is not one of unicode, keys")
    }
    if args.text.isEmpty, !replace, submit == nil {
        throw AgentFailure("bad_request", "text is empty; pass the text to type (with replace, empty text clears the field)")
    }
    let pace = typingPaceMs(args.paceMs)
    let timeoutMs = actionTimeoutMs(args.timeoutMs)

    // The target: the ref, pressed to focus it unless it has the focus; else whatever has the
    // focus in the frontmost app, which is also what is read back.
    var target: ActionTarget?
    var element: AXUIElement?
    let app: AppTarget
    var run: ActionRun
    if let ref = meaningful(args.ref) {
        app = try appOfRef(ref, call: call)
        run = ActionRun(app: app, call: call)
        run.readBefore()
        var plan = CheckPlan()
        plan.pointerUnlessFocused = true
        plan.editable = .text
        let cleared = try actionable(ref, call: call, timeoutMs: timeoutMs, plan: plan, reserve: effectReserve)
        if cleared.changedUI { run.readBefore() }
        target = cleared
        element = cleared.look.element
    } else {
        let focus = busy("ax") { focusedElement() }
        if let focus, let owner = appTarget(pid: AXHandle(focus).pid) {
            app = owner
            element = focus
        } else {
            app = try appTarget(named: nil)
        }
        run = ActionRun(app: app, call: call)
        run.readBefore()
    }
    if let element { AXUIElementSetMessagingTimeout(element, actionMessagingTimeout) }
    let secret = element.map { readElement($0).secret } ?? false
    var notes = target?.notes ?? []

    try call.check()
    if let point = target?.point {
        try postPress(at: point, button: "left", count: 1, mods: [])
        // Focus follows the click a moment later; wait for it (bounded), or say it did not come.
        if let element {
            let limit = DispatchTime.now() + .milliseconds(500)
            var focused = flagAttribute(element, kAXFocusedAttribute) == true
            while !focused, DispatchTime.now() < limit {
                let stamp = UIWaker.shared.stamp(app.pid)
                nap(50, for: app.pid, since: stamp, until: limit)
                focused = flagAttribute(element, kAXFocusedAttribute) == true
            }
            if !focused {
                notes.append("\(target.map { $0.look.ref } ?? "the target") did not report the focus after the press; the text went to whatever had it")
            }
        }
    }
    if replace {
        try postKey("a", mods: ["cmd"])
        if args.text.isEmpty { try postKey("delete", mods: []) }
    }

    // One character at a time, each applied by the window server before the pace starts, so a
    // busy field is never handed keys faster than a hand types them.
    let characters = Array(args.text)
    var typedCount = 0
    var lastApplied: DispatchTime?
    var slow = false
    for (index, character) in characters.enumerated() {
        if let lastApplied, pace > 0 {
            let next = lastApplied + .milliseconds(pace)
            let now = DispatchTime.now()
            if next > now { usleep(UInt32((next.uptimeNanoseconds - now.uptimeNanoseconds) / 1000)) }
        }
        // A pause (a person took the screen) ends the action here; a cancel or the deadline stops
        // the typing and answers with its effect. What was typed stays typed either way.
        do {
            try call.check()
        } catch var failure as AgentFailure {
            if failure.code == "paused" {
                failure.detail = (failure.detail ?? [:]).merging(["posted": typedCount, "of": characters.count]) { $1 }
                throw failure
            }
            notes.append("stopped after \(typedCount) of \(characters.count) characters: \(failure.message)")
            break
        }
        let base = sessionEvents()
        let first = posted
        if via == "keys" {
            postStroke(strokes[index])
        } else {
            postUnicode(character)
        }
        do {
            try settle(since: base, want: posted &- first, limit: 1)
        } catch {
            slow = true
        }
        lastApplied = DispatchTime.now()
        typedCount += 1
    }
    if slow { notes.append("the machine was slow to apply some keys (over 1 s)") }
    if let submit, typedCount == characters.count {
        try postKey(submit, mods: [])
    }

    var result: [String: Any] = [:]
    if let target {
        result = checkedFields(target, via: via)
    } else {
        result["via"] = via
        if let element, let focus = busy("ax", { look(element: element, reader: call.reader, hitTest: false) }) {
            result["target"] = focus.node
        }
    }
    let typed = String(characters.prefix(typedCount))
    result["typed"] = secret ? secretText(typed.count) : typed
    if secret { result["secret"] = true }
    if let element, let value = busy("ax", { settledValue(element, call: call) }) {
        result["readBack"] = secret ? secretText(value.count) : value
        result["readBackOK"] = readBackMatches(typed: args.text, readBack: value, replace: replace, secret: secret,
                                               submittedReturn: submit == "return")
    }
    if !notes.isEmpty { result["notes"] = notes }
    let settled = run.settle()
    return result.merging(run.finish(settled)) { $1 }
}

// MARK: - setValue

/// A value given as text or as a number: the daemon sends text, a model may write a number.
private struct ValueText: Decodable {
    let text: String

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let text = try? container.decode(String.self) {
            self.text = text
        } else if let number = try? container.decode(Double.self) {
            self.text = number == number.rounded() && abs(number) < 1e15 ? String(Int(number)) : String(number)
        } else if let flag = try? container.decode(Bool.self) {
            self.text = flag ? "1" : "0"
        } else {
            throw DecodingError.dataCorruptedError(in: container, debugDescription: "value must be text or a number")
        }
    }
}

private struct SetValueArgs: Decodable {
    var ref: String
    var value: ValueText
    var timeoutMs: Int?
}

/// Whether a read-back is the value asked for: equal text, or equal numbers (a slider says 0.5
/// for "0.50").
private func valueMatches(asked: String, got: String) -> Bool {
    if asked == got { return true }
    if let a = Double(asked.trimmingCharacters(in: .whitespaces)), let b = Double(got.trimmingCharacters(in: .whitespaces)) {
        return abs(a - b) <= max(1e-9, abs(a) * 1e-6)
    }
    return false
}

private func setValue(_ args: SetValueArgs, call: Call) throws -> [String: Any] {
    let timeoutMs = actionTimeoutMs(args.timeoutMs)
    let app = try appOfRef(args.ref, call: call)
    let run = ActionRun(app: app, call: call)
    run.readBefore()
    var plan = CheckPlan()
    plan.pointer = false
    plan.frontmost = false
    plan.editable = .value
    let target = try actionable(args.ref, call: call, timeoutMs: timeoutMs, plan: plan, reserve: effectReserve)
    let element = target.look.element
    let asked = args.value.text
    let secret = target.look.read.secret
    var notes = target.notes

    // A number where the element holds a number, else the text.
    let current = readAttribute(element, kAXValueAttribute).value
    var newValue: CFTypeRef = asked as CFString
    var wanted: Double?
    if let current, CFGetTypeID(current) == CFNumberGetTypeID() || CFGetTypeID(current) == CFBooleanGetTypeID() {
        guard let number = Double(asked.trimmingCharacters(in: .whitespaces)) else {
            throw AgentFailure("bad_request", "\(targetLabel(target.look)) holds a number; pass value as a number, such as 0.5")
        }
        newValue = NSNumber(value: number)
        wanted = number
    }

    try call.check()
    var how = "value"
    var done = false
    if isSettable(element, kAXValueAttribute) {
        let error = AXUIElementSetAttributeValue(element, kAXValueAttribute as CFString, newValue)
        switch error {
        case .success:
            done = true
        case .cannotComplete:
            done = true
            notes.append("the app did not confirm the new value in time; the read-back says what it holds")
        default:
            if !hasStepper(element) {
                throw AgentFailure("ax_error", "setting the value of \(targetLabel(target.look)) failed (AXError \(error.rawValue)); type into it or press its controls instead")
            }
        }
    }
    if !done {
        // A stepper, or a slider that refuses a set value: step toward the number, bounded.
        guard let wanted else {
            throw AgentFailure("refused", "\(targetLabel(target.look)) takes no value directly and steps only numbers",
                               detail: ["reason": "not_editable", "target": target.look.node, "checks": target.log.wire])
        }
        how = "steps"
        var steps = 0
        var now = numberAttribute(element, kAXValueAttribute)
        while let value = now, abs(value - wanted) > 1e-9, steps < 200, call.remaining > effectReserve, !call.cancelled {
            let up = value < wanted
            AXUIElementPerformAction(element, (up ? kAXIncrementAction : kAXDecrementAction) as CFString)
            steps += 1
            usleep(30_000)
            let next = numberAttribute(element, kAXValueAttribute)
            if next == nil || next == value { break } // at a limit, or it does not step
            if let next, up ? next > wanted : next < wanted { now = next; break } // stepped past
            now = next
        }
        notes.append("stepped \(steps) times with \(kAXIncrementAction)/\(kAXDecrementAction)")
    }

    var result = checkedFields(target, via: "ax")
    result["how"] = how
    if secret { result["secret"] = true }
    if let value = busy("ax", { settledValue(element, call: call) }) {
        result["readBack"] = secret ? secretText(value.count) : value
        result["readBackOK"] = secret ? value.count == asked.count : valueMatches(asked: asked, got: value)
    }
    if !notes.isEmpty { result["notes"] = notes }
    let settled = run.settle()
    return result.merging(run.finish(settled)) { $1 }
}

// MARK: - key

private struct KeyArgs: Decodable {
    var key: String
    var mods: [String]?
    var ref: String?
    var timeoutMs: Int?
}

private func pressKey(_ args: KeyArgs, call: Call) throws -> [String: Any] {
    guard keyCode(for: args.key) != nil else {
        throw AgentFailure("bad_request", "unknown key \(args.key); name a key such as return, tab, escape, delete, up, f5 or a letter")
    }
    let mods = try validateMods(args.mods)
    let timeoutMs = actionTimeoutMs(args.timeoutMs)

    var target: ActionTarget?
    var focusNote: String?
    let app: AppTarget
    let run: ActionRun
    if let ref = meaningful(args.ref) {
        app = try appOfRef(ref, call: call)
        run = ActionRun(app: app, call: call)
        run.readBefore()
        // Focus by AXFocused when the element takes it, else by a press with every check.
        let resolved = try resolveRef(ref, reader: call.reader, until: min(call.deadline, .now() + .seconds(3)))
        var plan = CheckPlan()
        plan.pointerUnlessFocused = true
        if isSettable(resolved.element, kAXFocusedAttribute) { plan.pointer = false }
        let cleared = try actionable(ref, call: call, timeoutMs: timeoutMs, plan: plan, reserve: effectReserve)
        if cleared.changedUI { run.readBefore() }
        target = cleared
        try call.check()
        if !cleared.focused {
            if let point = cleared.point {
                try postPress(at: point, button: "left", count: 1, mods: [])
                focusNote = "focused \(ref) with a press"
            } else {
                AXUIElementSetAttributeValue(cleared.look.element, kAXFocusedAttribute as CFString, kCFBooleanTrue)
                focusNote = "focused \(ref) through accessibility (AXFocused)"
            }
        }
    } else {
        app = try appTarget(named: nil)
        run = ActionRun(app: app, call: call)
        run.readBefore()
    }
    try call.check()
    try postKey(args.key, mods: mods)

    let settled = run.settle()
    var result: [String: Any] = [:]
    if let target {
        result = checkedFields(target, via: target.point != nil ? "pointer" : "ax")
        if let focusNote { result["notes"] = target.notes + [focusNote] }
    }
    return result.merging(run.finish(settled)) { $1 }
}
