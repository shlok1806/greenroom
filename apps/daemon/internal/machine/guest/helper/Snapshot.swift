// The perception ops of the toolkit (daemon ADR 0006, "Ops and results"): `snapshot`, `find`,
// and the `tree` the actions read before and after their input. The walk is Walker.swift; this
// file picks what to walk, adds what sits around it (the screen, the frontmost app, the focus,
// what wants attention) and answers.
//
// For the actions (station S3): `appTree` is the before and after, `treeSignature` the cheap
// settle check, `refQueueKey` the read queue of a request that names a ref.

import AppKit
import ApplicationServices
import Foundation

/// The most nodes a `tree` lists. A tree is read twice per action, so it is bounded below a
/// snapshot's maximum; the diff only needs what an action can have changed, which is on screen.
let treeLimit = 500

private struct SnapshotArgs: Decodable {
    var app: String?
    var window: String?
    var ref: String?
    var mode: String?
    var limit: Int?
    var fullText: [String]?
}

private struct FindArgs: Decodable {
    var text: String?
    var role: String?
    var app: String?
    var includeOffscreen: Bool?
    var limit: Int?
}

func registerSnapshotOps() {
    register("snapshot", .read(queueKey: refQueueKey)) { call in
        let args = try call.args(SnapshotArgs.self)
        try requireAccessibility()
        return try busy("ax") { try snapshot(args, call: call) }
    }
    register("find", .read(queueKey: appQueueKey)) { call in
        let args = try call.args(FindArgs.self)
        try requireAccessibility()
        return try busy("ax") { try find(args, call: call) }
    }
}

/// Refuses a read when the agent may not use accessibility, in the words the daemon passes on.
func requireAccessibility() throws {
    guard AXIsProcessTrusted() else {
        throw AgentFailure("not_trusted", "this machine has not granted Accessibility to the guest agent, so its UI cannot be read or used")
    }
}

/// The read queue of a request that may name a ref (`ref`, or a `window` given as a ref): the
/// queue of the ref's app, so reads of one hung app queue only behind each other; else
/// `appQueueKey`'s. Actions that take a ref use it too.
func refQueueKey(_ call: Call) -> String {
    struct Target: Decodable {
        var ref: String?
        var window: String?
    }
    let target = try? call.args(Target.self)
    for ref in [target?.ref, target?.window].compactMap({ $0 }) where refNumber(ref) != nil {
        if let pid = refPid(ref, reader: call.reader) { return "pid:\(pid)" }
    }
    return appQueueKey(call)
}

/// How long a ref may take to be found again inside a request.
private func resolveDeadline(_ call: Call) -> DispatchTime {
    min(call.deadline - .milliseconds(500), .now() + .seconds(3))
}

// MARK: - snapshot

private func snapshot(_ args: SnapshotArgs, call: Call) throws -> [String: Any] {
    let modeName = meaningful(args.mode)?.lowercased() ?? WalkMode.interactive.rawValue
    guard let mode = WalkMode(rawValue: modeName) else {
        throw AgentFailure("bad_request", "mode \(modeName) is not one of interactive, all, text")
    }
    var options = WalkOptions()
    options.mode = mode
    options.limit = min(max(args.limit ?? 250, 1), 1000)
    options.fullText = Set(args.fullText ?? [])

    var result: [String: Any] = ["screen": screenInfo()]
    let front = frontmostInfo()
    if !front.isEmpty { result["frontmost"] = front }

    // What to walk: a ref's subtree, one window, or every window of the app.
    var subtree: ElementContext?
    let app: AppTarget
    if let ref = meaningful(args.ref) {
        let resolved = try resolveRef(ref, reader: call.reader, until: resolveDeadline(call))
        guard let context = elementContext(resolved.element).context, let owner = appTarget(pid: resolved.pid) else {
            throw AgentFailure("stale_ref", "\(ref) went away while it was being read; take a new machine_snapshot", detail: ["ref": ref, "reason": "gone"])
        }
        subtree = context
        app = owner
        if resolved.reResolved { result["reResolved"] = [ref] }
    } else {
        app = try appTarget(named: args.app)
    }
    result["app"] = app.info

    let listing = appWindows(app)
    if listing.error == .cannotComplete {
        result["responding"] = false
        return result
    }
    if listing.error == .invalidUIElement {
        throw AgentFailure("not_found", "\(app.name.isEmpty ? "the application" : app.name) quit while it was being read; take a new machine_snapshot")
    }
    var window: AXUIElement?
    if subtree == nil, let wanted = meaningful(args.window) {
        window = try findWindow(wanted, among: listing.windows, app: app, call: call)
    }

    let walk = Walk(app: app, reader: call.reader, options: options, call: call)
    walk.focusedWindow = listing.focused
    if let subtree {
        walk.walk(subtree: subtree)
    } else if let window {
        walk.walk(windows: [window])
    } else if listing.windows.isEmpty {
        walk.walkRootChildren()
    } else {
        walk.walk(windows: listing.windows)
    }
    walk.findCoverers()
    try call.check()

    result["nodes"] = walk.nodes
    let wholeApp = subtree == nil && window == nil
    let attention = attentionItems(app, walk: walk, walkedAll: wholeApp, windows: listing.windows, reader: call.reader, call: call)
    if !attention.isEmpty { result["attention"] = attention }
    if let focused = focusedRef(in: app, reader: call.reader) { result["focused"] = focused }
    if let truncated = walk.truncatedBy { result["truncatedBy"] = truncated }
    result["responding"] = walk.responding
    return result
}

/// The window a snapshot's `window` names: a ref of a window, or a title (whole, whatever the
/// case, else the one title that contains it).
private func findWindow(_ wanted: String, among windows: [AXUIElement], app: AppTarget, call: Call) throws -> AXUIElement {
    if refNumber(wanted) != nil {
        let resolved = try resolveRef(wanted, reader: call.reader, until: resolveDeadline(call))
        guard resolved.fingerprint.role == "AXWindow" || readElement(resolved.element).role == "AXWindow" else {
            throw AgentFailure("bad_request", "\(wanted) is not a window; pass a window's ref (a node with role Window) or its title")
        }
        guard resolved.pid == app.pid else {
            throw AgentFailure("bad_request", "\(wanted) is a window of \(processName(resolved.pid)), not of \(app.name); leave out app, or pass one of its windows")
        }
        return resolved.element
    }
    let titled = windows.map { (window: $0, title: textAttribute($0, kAXTitleAttribute) ?? "") }
    if let exact = titled.first(where: { $0.title.caseInsensitiveCompare(wanted) == .orderedSame }) { return exact.window }
    let partial = titled.filter { $0.title.range(of: wanted, options: .caseInsensitive) != nil }
    if partial.count == 1 { return partial[0].window }
    let titles = titled.map { "\"\(cut($0.title, limit: 60).text)\"" }.joined(separator: ", ")
    if partial.isEmpty {
        throw AgentFailure("not_found", "\(app.name) has no window titled \"\(wanted)\"; its windows are \(titles.isEmpty ? "none" : titles)")
    }
    throw AgentFailure("bad_request", "\(partial.count) windows of \(app.name) have \"\(wanted)\" in their title (\(titles)); pass the whole title or the window's ref")
}

/// The reader's ref of the element with the keyboard focus, when it is in the target app.
func focusedRef(in app: AppTarget, reader: String) -> String? {
    guard let element = focusedElement(), AXHandle(element).pid == app.pid else { return nil }
    if let ref = knownRef(element, reader: reader) { return ref }
    guard let context = elementContext(element).context else { return nil }
    return giveRef(element, context.fingerprint(), reader: reader)
}

// MARK: - find

private func find(_ args: FindArgs, call: Call) throws -> [String: Any] {
    guard let text = args.text, !text.isEmpty else {
        throw AgentFailure("bad_request", "find needs text: the words to look for, or a /regular expression/")
    }
    let pattern: TextPattern
    do {
        pattern = try textPattern(text)
    } catch let error as PatternError {
        throw AgentFailure("bad_request", error.message)
    }
    let role = meaningful(args.role)
    let app = try appTarget(named: args.app)

    var options = WalkOptions()
    options.mode = .all
    options.limit = min(max(args.limit ?? 50, 1), 1000)
    options.includeOffscreen = args.includeOffscreen ?? true
    options.accept = { read in
        findMatches(pattern, role: role, FindFields(
            role: read.role, title: read.title ?? "", value: read.value ?? "", desc: read.description ?? "",
            help: read.help ?? "", placeholder: read.placeholder ?? "", identifier: read.identifier ?? "",
            secret: read.secret))
    }

    var result: [String: Any] = ["app": app.info]
    let listing = appWindows(app)
    if listing.error == .cannotComplete {
        result["responding"] = false
        result["searched"] = 0
        return result
    }
    let walk = Walk(app: app, reader: call.reader, options: options, call: call)
    walk.focusedWindow = listing.focused
    if listing.windows.isEmpty {
        walk.walkRootChildren()
    } else {
        walk.walk(windows: listing.windows)
    }
    walk.findCoverers()
    try call.check()
    result["matches"] = walk.nodes
    result["searched"] = walk.visited
    if let truncated = walk.truncatedBy { result["truncatedBy"] = truncated }
    if !walk.responding { result["responding"] = false }
    return result
}

// MARK: - tree, for the actions

/// An app's `tree` (daemon ADR 0006): its windows in interactive mode, with what wants
/// attention, as the before and after of an action or a wait.
struct AppTree {
    /// The wire's `tree`: `nodes`, `attention`, `windows`, `app`.
    let object: [String: Any]
    /// What the walk read, for comparing with a later `treeSignature`.
    let signature: Signature
    /// False when the app stopped answering accessibility; the tree is then what was read
    /// before it did, maybe nothing.
    let responding: Bool
}

/// Reads an app's tree for a reader, giving refs as a snapshot does. `budget` bounds the walk
/// (the request's own deadline bounds it too).
func appTree(_ app: AppTarget, reader: String, call: Call?, budget: TimeInterval = walkBudget) -> AppTree {
    var object: [String: Any] = ["app": app.info]
    let listing = appWindows(app)
    if listing.error != .success {
        return AppTree(object: object, signature: Signature(), responding: listing.error != .cannotComplete)
    }
    var options = WalkOptions()
    options.limit = treeLimit
    options.budget = budget
    let walk = Walk(app: app, reader: reader, options: options, call: call)
    walk.focusedWindow = listing.focused
    if listing.windows.isEmpty {
        walk.walkRootChildren()
    } else {
        walk.walk(windows: listing.windows)
    }
    walk.findCoverers()
    if !walk.nodes.isEmpty { object["nodes"] = walk.nodes }
    if !walk.windowRefs.isEmpty { object["windows"] = walk.windowRefs }
    let attention = attentionItems(app, walk: walk, walkedAll: true, windows: listing.windows, reader: reader, call: call)
    if !attention.isEmpty { object["attention"] = attention }
    var signature = walk.signature
    signOnScreenWindows(of: app.pid, into: &signature)
    return AppTree(object: object, signature: signature, responding: walk.responding)
}

/// The signature of an app's windows without building a tree: no refs, no nodes, no hit tests.
/// Equal signatures read some time apart mean nothing a person would see changed in between
/// (the settle of daemon ADR 0006 point 5). Nil when the app does not answer or is gone.
func treeSignature(_ app: AppTarget, call: Call?, budget: TimeInterval = 1) -> Signature? {
    let listing = appWindows(app)
    guard listing.error == .success else { return nil }
    var options = WalkOptions()
    options.nodes = false
    options.hitTest = false
    options.budget = budget
    let walk = Walk(app: app, reader: "", options: options, call: call)
    walk.focusedWindow = listing.focused
    if listing.windows.isEmpty {
        walk.walkRootChildren()
    } else {
        walk.walk(windows: listing.windows)
    }
    guard walk.responding else { return nil }
    var signature = walk.signature
    signOnScreenWindows(of: app.pid, into: &signature)
    return signature
}

/// Adds an app's on-screen windows as the window server sees them: a menu or a popover that
/// opens is a window of its own that the AX windows list does not always carry.
private func signOnScreenWindows(of pid: pid_t, into signature: inout Signature) {
    for window in screenWindowList() where window.pid == pid {
        signature.add(window.number)
        signature.add(window.layer)
        signature.add(window.bounds)
    }
}

// MARK: - Attention

private func attentionItem(ref: String?, kind: String, role: String, name: String, app: String, pid: pid_t) -> [String: Any] {
    var item: [String: Any] = ["kind": kind, "role": wireRole(role), "pid": Int(pid)]
    if let ref { item["ref"] = ref }
    if !name.isEmpty { item["name"] = cut(name).text }
    if !app.isEmpty { item["app"] = app }
    return item
}

/// What wants handling before the target app's windows (daemon ADR 0006's `attention`): its
/// open menus, then its sheets, dialogs, alerts and popovers, then the windows of other
/// processes in front of its windows, front to back. A walk that covered every window has met
/// the app's own items; otherwise its windows are looked at on their own.
func attentionItems(_ app: AppTarget, walk: Walk, walkedAll: Bool, windows: [AXUIElement], reader: String, call: Call?) -> [[String: Any]] {
    var items: [[String: Any]] = []
    let listed = screenWindowList()
    let screen = bounds
    func timeLeft() -> Bool { call.map { $0.remaining > 0.3 && !$0.cancelled } ?? true }

    for menu in openMenus(of: app.pid, in: listed, screen: screen) where timeLeft() {
        let found = axSurface(at: menu, roles: ["AXMenu"], reader: reader)
        items.append(attentionItem(ref: found?.ref, kind: "menu", role: "AXMenu", name: found?.name ?? "", app: app.name, pid: app.pid))
    }

    if walkedAll {
        items += walk.attention
    } else if timeLeft() {
        items += surfaceAttention(app, windows: windows, reader: reader, call: call)
    }

    for window in windowsOver(target: app.pid, own: getpid(), in: listed, screen: screen) where timeLeft() {
        let found = axSurface(at: window, roles: ["AXWindow"], reader: reader)
        let name = meaningful(window.name) ?? found?.name ?? ""
        items.append(attentionItem(ref: found?.ref, kind: "window", role: found?.role ?? "AXWindow", name: name,
                                   app: window.owner, pid: window.pid))
    }
    return items
}

/// The AX element of an on-screen window, found by hit-testing the middle of what shows of it
/// and climbing to the nearest element with one of `roles` (else the outermost the climb met),
/// with the reader's ref. Nil when the hit lands in another process's window (something in
/// front of it) or on nothing AX knows.
private func axSurface(at window: ScreenWindow, roles: Set<String>, reader: String) -> (ref: String, role: String, name: String)? {
    let shows = window.bounds.intersection(bounds)
    guard rectShows(shows), let hit = elementAt(CGPoint(x: shows.midX, y: shows.midY)),
          AXHandle(hit).pid == window.pid, let context = elementContext(hit).context else { return nil }
    let at = context.reads.firstIndex { roles.contains($0.role) } ?? (context.windowIndex ?? context.chain.count - 1)
    let read = context.reads[at]
    let ref = giveRef(context.chain[at].element, context.fingerprint(at: at), reader: reader)
    return (ref, read.role.isEmpty ? "AXUnknown" : read.role, read.name)
}

/// The app's sheets, dialogs, alerts and popovers from its windows and their children alone,
/// for a snapshot that walked less than the whole app.
private func surfaceAttention(_ app: AppTarget, windows: [AXUIElement], reader: String, call: Call?) -> [[String: Any]] {
    var items: [[String: Any]] = []
    func add(_ element: AXUIElement, _ read: AXRead, kind: String) {
        guard let context = elementContext(element, read: read).context else { return }
        let ref = giveRef(element, context.fingerprint(), reader: reader)
        items.append(attentionItem(ref: ref, kind: kind, role: read.role, name: read.name, app: app.name, pid: app.pid))
    }
    for window in windows {
        if let call, call.remaining < 0.3 || call.cancelled { break }
        let read = readElement(window)
        // A minimized window says AXDialog (Preview's do), and wants nothing until it is back.
        guard read.ok, flagAttribute(window, kAXMinimizedAttribute) != true else { continue }
        if let kind = attentionKind(role: read.role) ?? attentionKind(windowSubrole: read.subrole) { add(window, read, kind: kind) }
        for child in read.children.prefix(64) {
            let childRead = readElement(child)
            if childRead.ok, let kind = attentionKind(role: childRead.role) { add(child, childRead, kind: kind) }
        }
    }
    return items
}
