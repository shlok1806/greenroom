// The `scroll` op (daemon ADR 0006; docs/21 section 5.3, #190), and scrolling an element into view
// for the actionability checks. Into view: AXScrollToVisible when the element lists it, else
// wheel events at a point that hit-tests to the container itself (not a nested scroll area, not a
// control that takes the wheel), in steps sized from the distance, until the element shows or
// the position stops moving; the scroll bar's value is the last resort. The plan is
// logic/ScrollPlan.swift; this file posts and measures.

import AppKit
import ApplicationServices
import CoreGraphics
import Foundation

/// Where a scroll goes: an element into view, an end, or a distance.
enum ScrollDestination: Decodable {
    case ref(String)
    case top
    case bottom
    case pages(Double)
    case by(Double)

    private struct Object: Decodable {
        var ref: String?
        var pages: Double?
        var by: Double?
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let word = try? container.decode(String.self) {
            switch word {
            case "top": self = .top
            case "bottom": self = .bottom
            default:
                guard refNumber(word) != nil else {
                    throw DecodingError.dataCorruptedError(in: container, debugDescription: "to is \"top\", \"bottom\", {\"ref\": \"e45\"}, {\"pages\": n} or {\"by\": points}")
                }
                self = .ref(word)
            }
            return
        }
        let object = try container.decode(Object.self)
        switch (object.ref, object.pages, object.by) {
        case let (ref?, nil, nil): self = .ref(ref)
        case let (nil, pages?, nil) where pages.isFinite && pages != 0: self = .pages(pages)
        case let (nil, nil, by?) where by.isFinite && by != 0: self = .by(by)
        default:
            throw DecodingError.dataCorruptedError(in: container, debugDescription: "to is \"top\", \"bottom\", {\"ref\": \"e45\"}, {\"pages\": n} or {\"by\": points}, one of them and not zero")
        }
    }
}

private struct ScrollArgs: Decodable {
    var ref: String
    var to: ScrollDestination
    var timeoutMs: Int?
}

func registerScrollOp() {
    register("scroll", .input) { call in
        let args = try call.args(ScrollArgs.self)
        try requireAccessibility()
        return try scroll(args, call: call)
    }
}

// MARK: - Measuring

/// Where a scroll area is now: its scroll bars' values and the origin of its content.
private func scrollMarker(_ container: AXUIElement) -> ScrollMarker {
    let read = readElement(container)
    var marker = ScrollMarker()
    marker.y = read.verticalBar.flatMap { numberAttribute($0, kAXValueAttribute) }
    marker.x = read.horizontalBar.flatMap { numberAttribute($0, kAXValueAttribute) }
    for child in read.children.prefix(8) {
        let childRead = readElement(child)
        if childRead.ok, childRead.role != "AXScrollBar", let frame = childRead.frame {
            marker.origin = frame.origin
            break
        }
    }
    return marker
}

/// A scroll area's position as the wire's `{x, y}`, fractions or null.
private func positionObject(_ position: ScrollPosition?) -> [String: Any] {
    ["x": position?.x.map { $0 as Any } ?? NSNull(), "y": position?.y.map { $0 as Any } ?? NSNull()]
}

private func position(of container: AXUIElement) -> ScrollPosition? {
    var cache: [AXHandle: AXRead] = [:]
    let read = readElement(container)
    guard read.ok else { return nil }
    return readScroll(read, cache: &cache)
}

/// The part of a container that shows: its frame cut by what is around it.
private func viewRect(of container: AXUIElement, reader: String) -> CGRect? {
    guard let context = elementContext(container).context else { return nil }
    return context.shown(to: reader).vis ?? context.read.frame.flatMap { rectShows($0) ? $0 : nil }
}

// MARK: - Posting

/// Posts one wheel event at a point (the pointer moves there first: a wheel goes to what is under
/// the pointer) and waits for the window server to apply it. `dx` and `dy` are in Greenroom's
/// sign.
private func postWheel(at point: CGPoint, dx: CGFloat, dy: CGFloat) {
    let base = sessionEvents()
    let first = posted
    if cursor != point {
        cursor = point
        post(CGEvent(mouseEventSource: source, mouseType: .mouseMoved, mouseCursorPosition: point, mouseButton: .left))
    }
    if let event = CGEvent(scrollWheelEvent2Source: source, units: .pixel, wheelCount: 2,
                           wheel1: wheelValue(dy), wheel2: wheelValue(dx), wheel3: 0) {
        event.location = point
        post(event)
    }
    try? settle(since: base, want: posted &- first, limit: 1)
    // The view moves when its app handles the event, a moment after the window server applied it.
    usleep(30_000)
}

/// A point of the container's view where a wheel event scrolls the container itself, or what
/// is in the way at its center when there is none.
private func wheelPoint(for container: AXUIElement, view: CGRect, reader: String, until end: DispatchTime)
    -> (point: CGPoint?, blocker: [String: Any]?) {
    let handle = AXHandle(container)
    var blocker: [String: Any]?
    for point in wheelPoints(in: view) {
        if DispatchTime.now() >= end { break }
        guard let hit = elementAt(point), let context = elementContext(hit).context else { continue }
        let roles = context.reads.map { $0.role }
        switch wheelHit(roles: roles, containerIndex: context.chain.firstIndex(of: handle)) {
        case .container:
            return (point, nil)
        case let .nested(at), let .swallowed(at):
            if blocker == nil {
                let element = context.chain[at].element
                var named: [String: Any] = ["role": wireRole(context.reads[at].role), "where": "window"]
                named["ref"] = knownRef(element, reader: reader) ?? giveRef(element, context.fingerprint(at: at), reader: reader)
                if !context.reads[at].name.isEmpty { named["name"] = cut(context.reads[at].name).text }
                blocker = named
            }
        case .elsewhere:
            if blocker == nil, let containerContext = elementContext(container).context {
                let test = HitTest(hit: hit, relation: .other, chain: context.chain)
                if let cover = coverer(of: containerContext, hit: test, reader: reader) {
                    var named: [String: Any] = [:]
                    if let by = cover["by"] { named["ref"] = by }
                    for key in ["role", "name", "where", "app"] { if let value = cover[key] { named[key] = value } }
                    blocker = named
                }
            }
        }
    }
    return (nil, blocker)
}

// MARK: - Into view

/// The screen's visible frame in top-left points: the main display less the menu bar and the
/// Dock (AppKit's `visibleFrame`, read on the main thread, whose run loop the agent runs). The
/// whole display when AppKit does not answer in time. Kept for a second: the checks ask on every
/// try, and the Dock seldom moves.
func screenVisibleFrame() -> CGRect {
    visibleFrameLock.lock()
    if let cached = visibleFrameCache, DispatchTime.now() < cached.until {
        visibleFrameLock.unlock()
        return cached.frame
    }
    visibleFrameLock.unlock()

    func read() -> CGRect? {
        guard let primary = NSScreen.screens.first else { return nil }
        return topLeftFrame(primary.visibleFrame, primaryHeight: primary.frame.height)
    }
    var frame: CGRect?
    if Thread.isMainThread {
        frame = read()
    } else {
        final class Box: @unchecked Sendable { var frame: CGRect? }
        let box = Box()
        let done = DispatchSemaphore(value: 0)
        DispatchQueue.main.async {
            box.frame = read()
            done.signal()
        }
        if done.wait(timeout: .now() + .milliseconds(500)) == .success { frame = box.frame }
    }
    guard let frame, !frame.isEmpty else { return bounds }
    visibleFrameLock.lock()
    visibleFrameCache = (frame, .now() + .seconds(1))
    visibleFrameLock.unlock()
    return frame
}

private let visibleFrameLock = NSLock()
private var visibleFrameCache: (frame: CGRect, until: DispatchTime)?

struct IntoView {
    var steps = 0
    /// `axScrollToVisible`, `wheel` or `scrollBar`; nil when nothing was done.
    var via: String?
    var visible = false
    /// The element is inside the container's view but not where the screen shows it: the part
    /// of the view it could come to is under the Dock or the menu bar (its window extends past
    /// the screen's visible frame), so no scroll of this container shows it.
    var pastScreenEdge = false
    /// What stopped the wheel from reaching the container, when nothing could.
    var blocker: [String: Any]?
}

/// Scrolls `container` until `target` shows inside it and inside the screen's visible frame
/// (`reachableView`), bounded by `end` and `maxScrollSteps`.
func scrollIntoView(_ target: AXUIElement, container: AXUIElement, reader: String, call: Call, until end: DispatchTime) -> IntoView {
    var out = IntoView()
    AXUIElementSetMessagingTimeout(target, actionMessagingTimeout)
    AXUIElementSetMessagingTimeout(container, actionMessagingTimeout)
    guard var view = viewRect(of: container, reader: reader) else { return out }
    let screen = screenVisibleFrame()
    // Where the element must come to. A view wholly under the Dock has nowhere to bring it; the
    // view itself is then the goal, and `pastScreenEdge` says that is not enough.
    var reach = reachableView(view, screenVisible: screen) ?? view
    func shows() -> Bool {
        guard let frame = readElement(target).frame else { return false }
        return insideView(frame, reach) && reach.intersects(screen)
    }
    func finish() -> IntoView {
        if !out.visible, let frame = readElement(target).frame, insideView(frame, view) { out.pastScreenEdge = true }
        return out
    }
    if shows() {
        out.visible = true
        return out
    }

    if actionNames(target).contains("AXScrollToVisible") {
        if AXUIElementPerformAction(target, "AXScrollToVisible" as CFString) == .success {
            out.via = "axScrollToVisible"
            usleep(50_000)
            if shows() {
                out.visible = true
                return out
            }
        }
    }

    let found = wheelPoint(for: container, view: reach, reader: reader, until: end)
    if let point = found.point {
        var progress = ScrollProgress()
        _ = progress.record(scrollMarker(container))
        while out.steps < maxScrollSteps, DispatchTime.now() < end, !call.cancelled, pausedBy(call) == nil {
            if shows() {
                out.visible = true
                break
            }
            guard let frame = readElement(target).frame else { break }
            let distance = intoViewDistance(frame: frame, view: reach)
            let dy = wheelStep(distance: distance.dy, viewLength: reach.height)
            let dx = wheelStep(distance: distance.dx, viewLength: reach.width)
            if dx == 0, dy == 0 { break }
            postWheel(at: point, dx: dx, dy: dy)
            out.steps += 1
            out.via = "wheel"
            if progress.record(scrollMarker(container)) { break }
        }
        if !out.visible, shows() { out.visible = true }
    } else {
        out.blocker = found.blocker
    }
    if out.visible { return out }

    // The last resort: the scroll bar's value, when the bar takes one.
    if DispatchTime.now() < end, let bar = readElement(container).verticalBar, isSettable(bar, kAXValueAttribute),
       let current = numberAttribute(bar, kAXValueAttribute), let frame = readElement(target).frame {
        var cache: [AXHandle: AXRead] = [:]
        let children = readElement(container).children.prefix(64).compactMap { child -> CGRect? in
            let read = cache[AXHandle(child)] ?? readElement(child)
            cache[AXHandle(child)] = read
            return read.ok && read.role != "AXScrollBar" ? read.frame : nil
        }
        let distance = intoViewDistance(frame: frame, view: reach).dy
        if let content = contentFrame(of: Array(children)),
           let value = scrollBarValue(current: current, distance: distance, contentLength: content.height, viewLength: view.height) {
            if AXUIElementSetAttributeValue(bar, kAXValueAttribute as CFString, NSNumber(value: value)) == .success {
                out.via = "scrollBar"
                usleep(50_000)
                if let again = viewRect(of: container, reader: reader) {
                    view = again
                    reach = reachableView(view, screenVisible: screen) ?? view
                }
                out.visible = shows()
            }
        }
    }
    return finish()
}

/// The actionability checks' scroll: brings a target a scroll area hides into its view.
func scrollTargetIntoView(_ look: Look, scrollerRef: String, call: Call, until end: DispatchTime) -> IntoView {
    guard let scroller = try? resolveRef(scrollerRef, reader: call.reader, until: end) else { return IntoView() }
    return scrollIntoView(look.element, container: scroller.element, reader: call.reader, call: call, until: end)
}

// MARK: - The op

private func scroll(_ args: ScrollArgs, call: Call) throws -> [String: Any] {
    let timeoutMs = actionTimeoutMs(args.timeoutMs)
    let start = DispatchTime.now()
    var end = start + .milliseconds(timeoutMs)
    // Kept for the settle and the after tree.
    let latest = call.deadline - .milliseconds(3500)
    if latest < end { end = max(latest, start) }

    let given = try busy("ax") { try look(ref: args.ref, reader: call.reader, hitTest: false, until: end) }
    let container: AXUIElement
    let containerRef: String
    if given.role == "AXScrollArea" {
        container = given.element
        containerRef = given.ref
    } else if let nearest = given.context.scrollAreas.first, let index = given.context.chain.firstIndex(of: AXHandle(nearest)) {
        container = nearest
        containerRef = giveRef(nearest, given.context.fingerprint(at: index), reader: call.reader)
    } else {
        throw AgentFailure("bad_request", "\(targetLabel(given)) is not a scroll area and is in none; pass the ref of a scroll area, or of an element inside one")
    }
    AXUIElementSetMessagingTimeout(container, actionMessagingTimeout)

    // The element to bring into view, which must be inside the container.
    var target: Look?
    if case let .ref(ref) = args.to {
        let found = try busy("ax") { try look(ref: ref, reader: call.reader, hitTest: false, until: end) }
        guard found.context.chain.contains(AXHandle(container)) else {
            throw AgentFailure("bad_request", "\(targetLabel(found)) is not inside \(containerRef); pass the scroll area that holds it, or the element itself as ref")
        }
        target = found
    }

    guard let app = appTarget(pid: given.pid) else {
        throw AgentFailure("stale_ref", "\(args.ref)'s app is no longer running (it quit or crashed); take a new machine_snapshot",
                           detail: ["ref": args.ref, "reason": "app_quit"])
    }
    watch(app.pid)
    let action = ActionRun(app: app, call: call)
    action.readBefore()

    let from = busy("ax") { position(of: container) }
    var steps = 0
    var via: String?
    var down = true
    var visible: Bool?
    var notes: [String] = []
    try call.check()

    switch args.to {
    case .ref:
        guard let target else { break }
        let done = busy("ax") { scrollIntoView(target.element, container: container, reader: call.reader, call: call, until: end) }
        steps = done.steps
        via = done.via
        visible = done.visible
        if done.pastScreenEdge {
            notes.append("\(target.ref) is inside \(containerRef)'s view, but that part of the view is under the Dock or the menu bar (the window extends past the screen's visible area), so no scroll of \(containerRef) shows it; move or resize the window first")
        }
        if let holder = pausedBy(call) { throw pausedAfter(holder, steps: steps) }
        if let frame = target.read.frame, let view = viewRect(of: container, reader: call.reader) {
            down = intoViewDistance(frame: frame, view: view).dy >= 0
        }
        if !done.visible, done.via == nil, let blocker = done.blocker {
            throw refusedScroll(containerRef, blocker: blocker, target: target.node, start: start)
        }
    case .top, .bottom, .pages, .by:
        guard let view = viewRect(of: container, reader: call.reader) else {
            throw AgentFailure("refused", "\(containerRef) shows nothing on screen, so there is nothing to scroll; bring its window into view first",
                               detail: ["reason": "hidden", "waitedMs": elapsed(since: start)])
        }
        let found = busy("ax") { wheelPoint(for: container, view: view, reader: call.reader, until: end) }
        guard let point = found.point else {
            throw refusedScroll(containerRef, blocker: found.blocker ?? [:], target: nil, start: start)
        }
        var planned: [CGFloat] = []
        var edge = false
        switch args.to {
        case .top: edge = true; down = false
        case .bottom: edge = true; down = true
        case let .pages(pages): planned = relativeSteps(total: pageDistance(pages: pages, viewLength: view.height), viewLength: view.height)
        case let .by(by): planned = relativeSteps(total: CGFloat(by), viewLength: view.height)
        case .ref: break
        }
        if !edge { down = (planned.first ?? 0) >= 0 }
        var progress = ScrollProgress()
        _ = progress.record(busy("ax") { scrollMarker(container) })
        while steps < maxScrollSteps, DispatchTime.now() < end, !call.cancelled {
            let step: CGFloat
            if edge {
                if atEnd(busy("ax") { position(of: container) }, down: down) { break }
                step = edgeStep(viewLength: view.height, down: down)
            } else {
                guard steps < planned.count else { break }
                step = planned[steps]
            }
            if let holder = pausedBy(call) { throw pausedAfter(holder, steps: steps) }
            postWheel(at: point, dx: 0, dy: step)
            steps += 1
            via = "wheel"
            if busy("ax", { progress.record(scrollMarker(container)) }) { break }
        }
        // The last resort for an end: the scroll bar's value.
        if edge, !atEnd(busy("ax") { position(of: container) }, down: down), DispatchTime.now() < end,
           let bar = readElement(container).verticalBar, isSettable(bar, kAXValueAttribute),
           AXUIElementSetAttributeValue(bar, kAXValueAttribute as CFString, NSNumber(value: down ? 1.0 : 0.0)) == .success {
            via = "scrollBar"
            usleep(50_000)
        }
    }

    let settled = action.settle()
    var result = action.finish(settled)
    let to = busy("ax") { position(of: container) }
    if let containerLook = busy("ax", { look(element: container, reader: call.reader, hitTest: false) }) {
        result["container"] = containerLook.node
    } else {
        result["container"] = ["ref": containerRef]
    }
    result["from"] = positionObject(from)
    result["to"] = positionObject(to)
    result["atEnd"] = atEnd(to, down: down)
    result["steps"] = steps
    if let via { result["via"] = via }
    if let target {
        let after = busy("ax") { look(element: target.element, reader: call.reader) }
        if let after {
            result["target"] = after.node
            // Visible where the screen shows it (not only inside the container, which may run
            // under the Dock); what covers it is in the node, for the daemon to say.
            result["visible"] = (visible ?? false) && after.shown.vis != nil
        } else {
            result["visible"] = visible ?? false
        }
    }
    if !notes.isEmpty { result["notes"] = notes }
    return result
}

/// The holder that paused the agent, when that stops this call's input: a person took the screen,
/// and the action ends with `paused` (daemon ADR 0006 point 11, takeover).
func pausedBy(_ call: Call) -> String? {
    guard call.input, let holder = pauseHolder(), holder != call.reader else { return nil }
    return holder
}

private func pausedAfter(_ holder: String, steps: Int) -> AgentFailure {
    var failure = pausedFailure(holder)
    failure.detail = (failure.detail ?? [:]).merging(["posted": steps]) { $1 }
    return failure
}

private func elapsed(since start: DispatchTime) -> Int {
    Int((DispatchTime.now().uptimeNanoseconds - start.uptimeNanoseconds) / 1_000_000)
}

private func refusedScroll(_ containerRef: String, blocker: [String: Any], target: [String: Any]?, start: DispatchTime) -> AgentFailure {
    var detail: [String: Any] = ["reason": "covered", "waitedMs": elapsed(since: start)]
    if !blocker.isEmpty { detail["by"] = blocker }
    if let target { detail["target"] = target }
    return AgentFailure("refused", "no point of \(containerRef) takes a wheel event for it: \(blocker.isEmpty ? "something covers it" : describeCause(blocker)) is in the way everywhere; close or move it, or scroll that one instead",
                        detail: detail)
}
