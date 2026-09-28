// One element looked at on its own, the way a snapshot would list it: its node, what of it
// shows, and what covers it. For the actions (station S3), which check and report one target
// at a time: `look(ref:)` for a ref, `look(element:)` for an element met some other way (the
// overlay a press revealed, what a point hits), `coverer` to name what a hit test found.

import AppKit
import ApplicationServices
import Foundation

/// An element as a snapshot would list it.
struct Look {
    let ref: String
    let element: AXUIElement
    /// True when the ref's own element was gone and its fingerprint found this one.
    let reResolved: Bool
    /// The element and everything above it (Refs.swift).
    let context: ElementContext
    /// What of it shows: `vis`, `offscreen`, `scroller`, `clipped`.
    let shown: Visibility
    /// What covers it at its visible center, as the node's `covered`; nil when nothing does, when
    /// nothing of it shows, or when it was not hit-tested.
    let covered: [String: Any]?
    /// The hit test at its visible center, when there was one.
    let hit: HitTest?
    /// The wire's `node`, `covered` included.
    let node: [String: Any]

    var read: AXRead { context.read }
    var role: String { context.role }
    var pid: pid_t { context.pid }

    /// The middle of what shows of it, nil when nothing does.
    var visibleCenter: CGPoint? { shown.vis.map { CGPoint(x: $0.midX, y: $0.midY) } }
}

/// Looks at the element a reader's ref names (resolved as `resolveRef` does, so it throws
/// `stale_ref` and `not_responding` as that does). `full` sends its text whole; `hitTest` asks
/// the window server what is at its visible center.
func look(ref: String, reader: String, full: Bool = false, hitTest: Bool = true, until: DispatchTime? = nil) throws -> Look {
    let resolved = try resolveRef(ref, reader: reader, until: until)
    let (context, error) = elementContext(resolved.element)
    guard let context else {
        if error == .cannotComplete {
            throw AgentFailure("not_responding", "\(ref)'s app (\(processName(resolved.pid))) is not answering accessibility; try again shortly")
        }
        throw AgentFailure("stale_ref", "\(ref) went away while it was being read; take a new machine_snapshot",
                           detail: ["ref": ref, "reason": "gone"])
    }
    // A re-resolved ref names a rebuilt element that a walk may already have given a ref of its
    // own; walks keep finding it under that one (from the first re-resolution on, not only in
    // the call that re-resolved it), so the node carries it and the daemon's lead line says
    // "e101 was re-resolved to e143".
    let current = knownRef(resolved.element, reader: reader) ?? ref
    return look(context, ref: current, reResolved: resolved.reResolved, reader: reader, full: full, hitTest: hitTest)
}

/// Looks at an element met without a ref, giving it the reader's ref. Nil when it cannot be
/// read (it is gone, or its app does not answer).
func look(element: AXUIElement, reader: String, full: Bool = false, hitTest: Bool = true) -> Look? {
    guard let context = elementContext(element).context else { return nil }
    let ref = giveRef(element, context.fingerprint(), reader: reader)
    return look(context, ref: ref, reResolved: false, reader: reader, full: full, hitTest: hitTest)
}

private func look(_ context: ElementContext, ref: String, reResolved: Bool, reader: String, full: Bool, hitTest doHit: Bool) -> Look {
    let read = context.read
    let role = context.role
    var shown = context.shown(to: reader)
    var more: [String] = []

    // A toolbar item behind the overflow chevron shows nothing until the chevron is pressed
    // (daemon ADR 0006, M15).
    if let toolbar = context.reads.dropFirst().firstIndex(where: { $0.role == "AXToolbar" }), toolbar > 0 {
        let item = toolbar - 1
        let hasChevron = elementAttribute(context.chain[toolbar].element, "AXOverflowButton") != nil
        if inOverflow(item: context.reads[item].frame, toolbar: context.reads[toolbar].frame, hasOverflowButton: hasChevron) {
            more.append("overflow")
            shown = Visibility()
        }
    }

    var facts: WindowFacts?
    if role == "AXWindow" {
        let focusedWindow = elementAttribute(AXUIElementCreateApplication(context.pid), kAXFocusedWindowAttribute)
        facts = windowFacts(context.element, focused: focusedWindow)
        if facts?.minimized == true { shown = Visibility() }
    }
    var scroll: ScrollPosition?
    if role == "AXScrollArea" {
        var cache: [AXHandle: AXRead] = [:]
        scroll = readScroll(read, cache: &cache)
    }
    var windowRef: String?
    if let index = context.windowIndex, index > 0 {
        windowRef = giveRef(context.chain[index].element, context.fingerprint(at: index), reader: reader)
    }

    var node = nodeObject(ref: ref, element: context.element, read: read, shown: shown, depth: context.depth,
                          window: windowRef, full: full, more: more, scroll: scroll, facts: facts)
    var covered: [String: Any]?
    var hit: HitTest?
    if doHit, let vis = shown.vis, isHitTested(role: role) {
        let test = hitTest(at: CGPoint(x: vis.midX, y: vis.midY), target: context)
        hit = test
        covered = coverer(of: context, hit: test, reader: reader)
        if let covered { node["covered"] = covered }
    }
    return Look(ref: ref, element: context.element, reResolved: reResolved, context: context, shown: shown,
                covered: covered, hit: hit, node: node)
}

/// What a hit test found in the target's place, as a node's `covered`: named by the nearest of
/// the hit and its parents that the reader has a ref for (what the reader has seen), else by
/// the hit itself, given a ref, or by its window when it is another app's; `where` says whether it is in the target's window, another
/// window of its app, or another app (then `app` names it). Nil when the hit is the target, is
/// inside it or is one of its parents, or when the hit test found nothing.
func coverer(of target: ElementContext, hit: HitTest, reader: String) -> [String: Any]? {
    guard hit.relation == .other, hit.hit != nil,
          let at = covererIndex(hit: hit.chain, isListed: { knownRef($0.element, reader: reader) != nil }) else { return nil }
    let pid = hit.chain[at].pid
    var covered: [String: Any] = [:]
    if let context = elementContext(hit.chain[at].element).context {
        // Another app's element is named by its window, which is what the reader has to deal
        // with (and what a snapshot's `attention` lists).
        let named = pid != target.pid ? (context.windowIndex ?? 0) : 0
        let element = context.chain[named].element
        let read = context.reads[named]
        let ref = knownRef(element, reader: reader) ?? giveRef(element, context.fingerprint(at: named), reader: reader)
        covered = ["by": ref, "role": wireRole(read.role.isEmpty ? "AXUnknown" : read.role)]
        if !read.name.isEmpty { covered["name"] = cut(read.name).text }
    }
    let sameProcess = pid == target.pid
    let sameWindow = sameProcess && target.window.map { hit.chain.contains(AXHandle($0)) } == true
    covered["where"] = coverWhere(sameProcess: sameProcess, sameWindow: sameWindow)
    if !sameProcess {
        let name = processName(pid)
        if !name.isEmpty { covered["app"] = name }
    }
    return covered
}
