// The walk behind `snapshot`, `find` and the trees of actions (daemon ADR 0006, "Ops and
// results"): an app's windows depth first, each element read in one message, given a ref,
// measured against what clips it, and hit-tested for what covers it. The decisions are logic/
// (Visibility, NodeText, ScrollPosition); this file reads what they decide on.

import AppKit
import ApplicationServices
import Foundation

/// The wall budget of one walk (daemon ADR 0005 point 5): past it the walk answers what it has,
/// with `truncatedBy: "time"`.
let walkBudget: TimeInterval = 3

/// The caps that keep a pathological tree from holding a walk: elements visited, and depth,
/// counted in listed elements as a node's `depth` is.
let visitLimit = 5000
let depthLimit = 40

/// How deep a walk goes in elements of any kind. Layout nests far deeper than what is listed,
/// and the walk recurses, so this bounds its stack.
private let levelLimit = 160

/// How many of a scroll area's children are read to measure its content when it has no scroll
/// bars to ask.
private let contentSample = 64

struct WalkOptions {
    var mode: WalkMode = .interactive
    /// The most nodes listed.
    var limit = 250
    /// Refs whose text is sent whole.
    var fullText: Set<String> = []
    /// Whether listed elements are hit-tested for what covers them.
    var hitTest = true
    /// False leaves out elements of which nothing shows.
    var includeOffscreen = true
    /// `find`: only elements this accepts are listed, and `depth` counts every level, since
    /// the elements between two matches are not in the result.
    var accept: ((AXRead) -> Bool)?
    var budget: TimeInterval = walkBudget
    /// False reads only the signature: no refs are given and no nodes built.
    var nodes = true
}

// MARK: - One node

/// A frame on the wire: `[x, y, w, h]`, to the hundredth of a point.
func wireRect(_ rect: CGRect) -> [Double] {
    [rect.minX, rect.minY, rect.width, rect.height].map { (Double($0) * 100).rounded() / 100 }
}

/// A point on the wire: `[x, y]`.
func wirePoint(_ point: CGPoint) -> [Double] {
    [point.x, point.y].map { (Double($0) * 100).rounded() / 100 }
}

private let textInputRoles: Set<String> = ["AXTextField", "AXTextArea", "AXComboBox", "AXSecureTextField"]

/// What a window says that other elements do not (daemon ADR 0006, W16).
struct WindowFacts {
    var states: [String] = []
    var document: String?
    var edited = false
    var minimized = false
}

private let windowAttributes: [String] = [
    kAXMainAttribute, kAXMinimizedAttribute, kAXDocumentAttribute, "AXEdited", kAXModalAttribute,
]

func windowFacts(_ window: AXUIElement, focused: AXUIElement?) -> WindowFacts {
    var facts = WindowFacts()
    var values: CFArray?
    let error = AXUIElementCopyMultipleAttributeValues(
        window, windowAttributes as CFArray, AXCopyMultipleAttributeOptions(rawValue: 0), &values)
    if error == .success, let list = values as? [AnyObject], list.count == windowAttributes.count {
        if (list[0] as? NSNumber)?.boolValue == true { facts.states.append("main") }
        if (list[1] as? NSNumber)?.boolValue == true {
            facts.states.append("minimized")
            facts.minimized = true
        }
        if let document = list[2] as? String, !document.isEmpty {
            // AXDocument is a file URL; a path is what a person and a shell both read.
            if let url = URL(string: document), url.isFileURL {
                facts.document = url.path
            } else {
                facts.document = document
            }
        }
        facts.edited = (list[3] as? NSNumber)?.boolValue == true
        if (list[4] as? NSNumber)?.boolValue == true { facts.states.append("modal") }
    }
    if let focused, CFEqual(focused, window) { facts.states.append("focused") }
    return facts
}

/// A scroll area's position: from its scroll bars, else from the frames of its children.
/// `cache` keeps what it read of the children for the walk that visits them next.
func readScroll(_ read: AXRead, cache: inout [AXHandle: AXRead]) -> ScrollPosition? {
    guard let view = read.frame else { return nil }
    let vertical = read.verticalBar.flatMap { numberAttribute($0, kAXValueAttribute) }
    let horizontal = read.horizontalBar.flatMap { numberAttribute($0, kAXValueAttribute) }
    var content: CGRect?
    if vertical == nil, horizontal == nil {
        var frames: [CGRect] = []
        for child in read.children.prefix(contentSample) {
            let handle = AXHandle(child)
            let childRead = cache[handle] ?? readElement(child)
            cache[handle] = childRead
            if childRead.ok, childRead.role != "AXScrollBar", let frame = childRead.frame { frames.append(frame) }
        }
        content = contentFrame(of: frames)
    }
    let position = scrollPosition(vertical: vertical, horizontal: horizontal, view: view, content: content)
    return position.scrolls ? position : nil
}

private func wireScroll(_ position: ScrollPosition) -> [String: Any] {
    [
        "x": position.x.map { $0 as Any } ?? NSNull(),
        "y": position.y.map { $0 as Any } ?? NSNull(),
        "up": position.up, "down": position.down, "left": position.left, "right": position.right,
    ]
}

/// The states of an element, in the wire's words. `editable` costs one message, asked only of
/// the roles that take text.
private func states(of element: AXUIElement, _ read: AXRead, role: String) -> [String] {
    var states: [String] = []
    if read.enabled == false { states.append("disabled") }
    var selected = read.selected == true
    if toggleRoleNames.contains(role) {
        if read.value == "1" { selected = true }
        if read.value == "2" { states.append("mixed") }
    }
    if selected { states.append("selected") }
    if read.focused == true { states.append("focused") }
    if read.expanded == true { states.append("expanded") }
    if textInputRoles.contains(role) || read.subrole == "AXSearchField" {
        if read.enabled != false, isSettable(element, kAXValueAttribute) { states.append("editable") }
    }
    if read.busy == true { states.append("busy") }
    if read.secret { states.append("secret") }
    return states
}

/// One element as the wire's `node` (daemon ADR 0006). Empty fields are left out. `more` is
/// what only the caller knows: a window's states, `overflow`.
func nodeObject(ref: String, element: AXUIElement, read: AXRead, shown: Visibility, depth: Int, window: String?,
                full: Bool, more: [String] = [], scroll: ScrollPosition? = nil, facts: WindowFacts? = nil) -> [String: Any] {
    let role = read.role.isEmpty ? "AXUnknown" : read.role
    var node: [String: Any] = ["ref": ref, "role": wireRole(role), "depth": depth]
    if !read.subrole.isEmpty, read.subrole != "AXUnknown" { node["subrole"] = wireRole(read.subrole) }

    // A toggle's 0 or 1 is its selected state, not a value anyone reads.
    var value = read.value ?? ""
    if toggleRoleNames.contains(role), ["0", "1", "2"].contains(value) { value = "" }
    let name = read.name
    var desc = meaningful(read.description) ?? ""
    if desc.trimmingCharacters(in: .whitespacesAndNewlines) == name { desc = "" }
    var help = meaningful(read.help) ?? ""
    if help.trimmingCharacters(in: .whitespacesAndNewlines) == name { help = "" }
    let sent = sentText(NodeText(name: name, value: value, desc: desc, help: help), secret: read.secret, full: full)
    if !sent.text.name.isEmpty { node["name"] = sent.text.name }
    if !sent.text.value.isEmpty { node["value"] = sent.text.value }
    if !sent.text.desc.isEmpty { node["desc"] = sent.text.desc }
    if !sent.text.help.isEmpty { node["help"] = sent.text.help }
    if !sent.cut.isEmpty { node["cut"] = sent.cut }
    if let chars = sent.chars { node["chars"] = chars }
    let identifier = stableIdentifier(read.identifier)
    if !identifier.isEmpty { node["id"] = cut(identifier).text }

    // A window says `focused` both as an element and as the app's focused window: once is enough.
    var all: [String] = []
    for state in states(of: element, read, role: role) + (facts?.states ?? []) + more where !all.contains(state) {
        all.append(state)
    }
    if !all.isEmpty { node["states"] = all }
    if let document = facts?.document { node["document"] = document }
    if facts?.edited == true { node["edited"] = true }

    if let frame = read.frame { node["frame"] = wireRect(frame) }
    if let vis = shown.vis { node["vis"] = wireRect(vis) }
    if let offscreen = shown.offscreen { node["offscreen"] = offscreen }
    if let clipped = shown.clipped { node["clipped"] = clipped }
    if let scroller = shown.scroller { node["scroller"] = scroller }
    if let window { node["window"] = window }
    if let scroll { node["scroll"] = wireScroll(scroll) }
    return node
}

// MARK: - What covers an element

/// What a hit test at a point of an element found.
struct HitTest {
    /// The element the window server's hit test returned, nil when it returned none.
    let hit: AXUIElement?
    let relation: HitRelation
    /// The hit and its parents, as far as the climb went.
    let chain: [AXHandle]
}

/// How far a hit's parents are followed to see whether it is inside the target.
private let hitClimb = 24

/// Hit-tests a point of the screen against an element: what is there, and whether that is the
/// element (`same`), something inside it (`descendant`), something it is inside (`ancestor`)
/// or anything else (`other`, which covers it at that point). `known` answers an element's
/// parent chain without a message when the caller has walked it.
func hitTest(at point: CGPoint, target: [AXHandle], known: ((AXHandle) -> [AXHandle]?)? = nil) -> HitTest {
    guard let hit = elementAt(point) else { return HitTest(hit: nil, relation: .other, chain: []) }
    var chain: [AXHandle] = []
    var current: AXUIElement? = hit
    while let element = current, chain.count < hitClimb {
        let handle = AXHandle(element)
        if let rest = known?(handle) {
            chain += rest
            break
        }
        if chain.contains(handle) { break }
        chain.append(handle)
        // Inside the target already: the rest of the climb would change nothing.
        if handle == target.first { break }
        current = elementAttribute(element, kAXParentAttribute)
    }
    return HitTest(hit: hit, relation: hitRelation(target: target, hit: chain), chain: chain)
}

/// Hit-tests a point against an element with its context: the form actions use, for the
/// visible center and the other points of the 3x3 grid.
func hitTest(at point: CGPoint, target context: ElementContext) -> HitTest {
    hitTest(at: point, target: context.chain)
}

// MARK: - The walk

final class Walk {
    let app: AppTarget
    let reader: String
    let options: WalkOptions
    /// The app's focused window, which its node says.
    var focusedWindow: AXUIElement?
    /// Elements read before the walk reaches them (by the attention pass, by a scroll area
    /// measuring its content), so nothing is read twice.
    var readAhead: [AXHandle: AXRead] = [:]

    private(set) var nodes: [[String: Any]] = []
    /// The refs of the windows walked, in order.
    private(set) var windowRefs: [String] = []
    /// The sheets and popovers the walk met.
    private(set) var attention: [[String: Any]] = []
    private(set) var truncatedBy: String?
    private(set) var visited = 0
    /// False when the app stopped answering during the walk.
    private(set) var responding = true
    private(set) var signature = Signature()

    private struct Seen {
        let handle: AXHandle
        let parent: Int
        let role: String
        let name: String
        let fingerprint: Fingerprint
        /// The index of its window among the seen, -1 for an element in none.
        let window: Int
        var ref: String?
        var listed = false
    }

    private struct Place {
        /// The parent among the seen, -1 for a root.
        var parent = -1
        /// The index among the parent's children.
        var index = 0
        /// The parent's role path; nil for a root, whose own path is `rootPath`.
        var parentPath: String?
        var rootPath = ""
        var depth = 0
        var level = 0
        var clip: ClipContext
        var windowNumber = 0
        var windowSeen = -1
        var windowRef: String?
        var toolbar: (frame: CGRect?, overflow: Bool)?
        /// Inside a toolbar item that sits behind the overflow chevron.
        var overflow = false
    }

    private let call: Call?
    private let screen: CGRect
    private let deadline: DispatchTime
    private var seen: [Seen] = []
    private var index: [AXHandle: Int] = [:]
    private var stopped = false
    private var timeouts = 0
    private var hits: [(node: Int, seen: Int, point: CGPoint)] = []
    private var screenList: [ScreenWindow]?
    private var strangers: [AXHandle: [String: Any]] = [:]

    init(app: AppTarget, reader: String, options: WalkOptions, call: Call?) {
        self.app = app
        self.reader = reader
        self.options = options
        self.call = call
        screen = bounds
        var end = DispatchTime.now() + options.budget
        // The answer still has to be built and sent inside the request's own deadline.
        if let call, call.deadline - .milliseconds(250) < end { end = call.deadline - .milliseconds(250) }
        deadline = end
    }

    // MARK: Entry points

    /// Walks windows of the app, in the order given.
    func walk(windows: [AXUIElement]) {
        for window in windows {
            if stopped { return }
            let handle = AXHandle(window)
            let read = readAhead.removeValue(forKey: handle) ?? readElement(window)
            let number = read.ok ? windowNumber(of: window, frame: read.frame, pid: app.pid, among: listedWindows) : 0
            visit(window, read: read, Place(clip: ClipContext(screen: screen), windowNumber: number))
        }
    }

    /// Walks what an app with no windows has under its root (the menu bar is left out).
    func walkRootChildren() {
        let read = readElement(app.root)
        guard note(read) else { return }
        for (place, child) in read.children.enumerated() {
            if stopped { return }
            visit(child, read: nil, Place(index: place, parentPath: "", clip: ClipContext(screen: screen)))
        }
    }

    /// Walks the subtree under one element. Its parents are noted first, unlisted, so a hit
    /// test that lands on one of them is known for the ancestor it is.
    func walk(subtree context: ElementContext) {
        var parent = -1
        var windowSeen = -1
        for place in context.chain.indices.dropFirst().reversed() {
            let read = context.reads[place]
            let isWindow = context.windowIndex == place
            seen.append(Seen(
                handle: context.chain[place], parent: parent, role: read.role, name: read.name,
                fingerprint: context.fingerprint(at: place), window: isWindow ? seen.count : windowSeen))
            parent = seen.count - 1
            index[context.chain[place]] = parent
            if isWindow { windowSeen = parent }
        }
        var windowRef: String?
        if let windowIndex = context.windowIndex, windowIndex > 0 {
            windowRef = giveRef(context.chain[windowIndex].element, context.fingerprint(at: windowIndex), reader: reader)
        }
        var place = Place(parent: parent, clip: context.clip(reader: reader), windowNumber: context.windowNumber,
                          windowSeen: windowSeen, windowRef: windowRef)
        place.rootPath = context.paths.first ?? ""
        visit(context.element, read: context.read, place)
    }

    /// Hit-tests the listed elements that show, once the whole walk is known: a hit on
    /// something inside an element is only known for that when its inside has been walked.
    func findCoverers() {
        guard options.hitTest, options.nodes else { return }
        for item in hits {
            if call?.cancelled == true { return }
            if DispatchTime.now() > deadline {
                truncatedBy = truncatedBy ?? "time"
                return
            }
            let target = chain(of: item.seen)
            let test = hitTest(at: item.point, target: target) { [self] handle in
                index[handle].map(chain(of:))
            }
            guard test.relation == .other, test.hit != nil else { continue }
            if let covered = coverer(of: item.seen, hit: test.chain) { nodes[item.node]["covered"] = covered }
        }
    }

    // MARK: The visit

    private func listedWindows() -> [ScreenWindow] {
        if screenList == nil { screenList = screenWindowList() }
        return screenList ?? []
    }

    private func stop(_ why: String) {
        truncatedBy = truncatedBy ?? why
        stopped = true
    }

    /// Whether a read can be used. Three reads in a row that timed out are an app that has
    /// stopped answering: the walk ends there rather than spend a second on each element left.
    private func note(_ read: AXRead) -> Bool {
        if read.ok {
            timeouts = 0
            return true
        }
        if read.error == .cannotComplete {
            timeouts += 1
            if timeouts >= 3 {
                responding = false
                stopped = true
            }
        }
        return false
    }

    private func visit(_ element: AXUIElement, read given: AXRead?, _ place: Place) {
        if stopped { return }
        if options.nodes, nodes.count >= options.limit { return stop("limit") }
        if visited >= visitLimit { return stop("visited") }
        // `find` counts every level in `depth` (its matches have no listed parents between
        // them), so only the level bound applies to it.
        if (options.accept == nil && place.depth > depthLimit) || place.level > levelLimit {
            // Only this branch is cut: its siblings may be shallower.
            truncatedBy = truncatedBy ?? "depth"
            return
        }
        if DispatchTime.now() > deadline { return stop("time") }
        if call?.cancelled == true {
            stopped = true
            return
        }
        let handle = AXHandle(element)
        // An element listed under two parents is walked where it was met first.
        if index[handle] != nil { return }
        visited += 1
        let read = given ?? readAhead.removeValue(forKey: handle) ?? readElement(element)
        guard note(read) else { return }
        let role = read.role.isEmpty ? "AXUnknown" : read.role
        // The menu bar is the same for every window and eats the budget.
        if role == "AXMenuBar" { return }

        let isRoot = place.parentPath == nil
        let path = isRoot ? place.rootPath : pathAppending(place.parentPath ?? "", role: role, index: place.index)
        let isWindow = role == "AXWindow" && place.windowSeen < 0
        let name = read.name
        let fingerprint = Fingerprint(
            pid: app.pid, started: app.started, window: place.windowNumber, path: path,
            identifier: stableIdentifier(read.identifier), role: role, name: name)
        let me = seen.count
        seen.append(Seen(handle: handle, parent: place.parent, role: role, name: name, fingerprint: fingerprint,
                         window: isWindow ? me : place.windowSeen))
        index[handle] = me

        // What of it shows. An element with no frame at all is walked through; one with a frame
        // that has no area, or that lies outside its window, is hidden with all that is in it.
        // A frame with no area is treated as no frame: SwiftUI and web content report zero-sized
        // groups around children that do show, so only a frame with area can hide a subtree.
        let hasArea = read.frame.map(rectShows) ?? false
        var shown = visibility(of: read.frame, in: clipAround(role: role, in: place.clip))
        var more: [String] = []
        var behindChevron = place.overflow
        if !behindChevron, let toolbar = place.toolbar {
            behindChevron = inOverflow(item: read.frame, toolbar: toolbar.frame, hasOverflowButton: toolbar.overflow)
        }
        sign(read, role: role, name: name, shows: shown.vis != nil)
        if behindChevron {
            more.append("overflow")
            shown = Visibility()
        } else if hasArea, !shown.listed, role != "AXApplication" {
            return
        }
        var facts: WindowFacts?
        if role == "AXWindow" {
            facts = windowFacts(element, focused: focusedWindow)
            if facts?.minimized == true { shown = Visibility() }
        }
        let minimized = facts?.minimized == true

        var listed = shown.listed && (hasArea || behindChevron)
        if listed {
            if let accept = options.accept {
                listed = accept(read)
            } else {
                listed = isListed(
                    mode: options.mode, role: role, named: !name.isEmpty, valued: meaningful(read.value) != nil,
                    described: meaningful(read.description) != nil, identified: !fingerprint.identifier.isEmpty)
            }
        }
        if listed, !options.includeOffscreen, shown.vis == nil { listed = false }

        var windowRef = place.windowRef
        if options.nodes {
            if isWindow {
                // A window has a ref whether it is listed or not: its elements name it.
                windowRef = ref(me)
                windowRefs.append(windowRef ?? "")
                // A minimized window says AXDialog (Preview's do), and wants nothing until it is
                // brought back.
                if !minimized, let kind = attentionKind(windowSubrole: read.subrole) { attend(me, kind: kind) }
            }
            if let kind = attentionKind(role: role) { attend(me, kind: kind) }
            if listed {
                let own = ref(me)
                seen[me].listed = true
                let scroll = role == "AXScrollArea" ? readScroll(read, cache: &readAhead) : nil
                nodes.append(nodeObject(
                    ref: own, element: element, read: read, shown: shown, depth: place.depth,
                    window: isWindow ? nil : windowRef, full: options.fullText.contains(own),
                    more: more, scroll: scroll, facts: facts))
                if options.hitTest, let vis = shown.vis, isHitTested(role: role) {
                    hits.append((nodes.count - 1, me, CGPoint(x: vis.midX, y: vis.midY)))
                }
            }
        }
        // Nothing in a minimized window can be seen or pressed. What is behind the chevron is
        // walked, flagged, so the item's button is listed even when a group wraps it.
        if minimized { return }

        var below = Place(parent: me, clip: place.clip, windowNumber: place.windowNumber,
                          windowSeen: isWindow ? me : place.windowSeen, windowRef: windowRef)
        below.parentPath = path
        below.level = place.level + 1
        below.depth = (listed || options.accept != nil) ? place.depth + 1 : place.depth
        below.overflow = behindChevron
        below.clip = clipBelow(place.clip, role: role, frame: read.frame) { [self] in
            options.nodes ? ref(me) : "-"
        }
        if role == "AXToolbar" {
            below.toolbar = (read.frame, elementAttribute(element, "AXOverflowButton") != nil)
        }
        // A scroll bar's thumb and page buttons say again what its scroll area's `scroll`
        // says; only `all` lists them.
        if role == "AXScrollBar", options.mode != .all, options.accept == nil { return }
        for (childPlace, child) in read.children.enumerated() {
            if stopped { return }
            below.index = childPlace
            visit(child, read: nil, below)
        }
    }

    private func sign(_ read: AXRead, role: String, name: String, shows: Bool) {
        signature.add(element: SignedElement(
            role: role, name: name, value: read.value ?? "", secret: read.secret, enabled: read.enabled,
            selected: read.selected, focused: read.focused, expanded: read.expanded, frame: read.frame,
            children: read.children.count, shows: shows))
    }

    /// The ref of a seen element, given on first use.
    private func ref(_ at: Int) -> String {
        if let ref = seen[at].ref { return ref }
        let ref = giveRef(seen[at].handle.element, seen[at].fingerprint, reader: reader)
        seen[at].ref = ref
        return ref
    }

    private func attend(_ at: Int, kind: String) {
        var item: [String: Any] = ["ref": ref(at), "kind": kind, "role": wireRole(seen[at].role), "pid": Int(app.pid)]
        if !seen[at].name.isEmpty { item["name"] = cut(seen[at].name).text }
        if !app.name.isEmpty { item["app"] = app.name }
        attention.append(item)
    }

    /// A seen element and its parents, as far as the walk knows them.
    private func chain(of at: Int) -> [AXHandle] {
        var chain: [AXHandle] = []
        var next = at
        while next >= 0, chain.count <= levelLimit + climbRoom {
            chain.append(seen[next].handle)
            next = seen[next].parent
        }
        return chain
    }

    /// What covers a seen element, given the chain of what was hit at its center: named by the
    /// nearest of the hit and its parents that this walk lists, else by the hit itself.
    private func coverer(of target: Int, hit: [AXHandle]) -> [String: Any]? {
        guard let at = covererIndex(hit: hit, isListed: { [self] in index[$0].map { seen[$0].listed } ?? false }) else { return nil }
        let handle = hit[at]
        let pid = handle.pid
        let sameProcess = pid == app.pid
        var sameWindow = false
        if sameProcess, seen[target].window >= 0 {
            sameWindow = hit.contains(seen[seen[target].window].handle)
        }
        var covered: [String: Any]
        if let known = index[handle] {
            covered = ["by": ref(known), "role": wireRole(seen[known].role)]
            if !seen[known].name.isEmpty { covered["name"] = cut(seen[known].name).text }
        } else {
            covered = stranger(Array(hit[at...]))
        }
        covered["where"] = coverWhere(sameProcess: sameProcess, sameWindow: sameWindow)
        if !sameProcess {
            let name = processName(pid)
            if !name.isEmpty { covered["app"] = name }
        }
        return covered
    }

    /// An element this walk did not see (another app's, another window's), given a ref so the
    /// reader can look at it or act on it. `hit` is the element and the parents the hit test
    /// climbed. Another app's element is named by its window, which is what the reader has to
    /// deal with (and what `attention` lists); the answer is kept for every element up to what
    /// it names, so the other hits on that window cost no messages.
    private func stranger(_ hit: [AXHandle]) -> [String: Any] {
        if let known = hit.lazy.compactMap({ self.strangers[$0] }).first { return known }
        guard let first = hit.first, let context = elementContext(first.element).context else { return [:] }
        let at = context.pid != app.pid ? (context.windowIndex ?? 0) : 0
        let read = context.reads[at]
        var covered: [String: Any] = [
            "by": giveRef(context.chain[at].element, context.fingerprint(at: at), reader: reader),
            "role": wireRole(read.role.isEmpty ? "AXUnknown" : read.role),
        ]
        if !read.name.isEmpty { covered["name"] = cut(read.name).text }
        for handle in context.chain.prefix(at + 1) { strangers[handle] = covered }
        return covered
    }
}

/// Room for the parents a subtree walk notes above its root.
private let climbRoom = 64
