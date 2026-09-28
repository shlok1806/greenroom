// Refs in the guest (daemon ADR 0006 point 3): each reader's table of live elements, where an
// element sits (its parents, window, scroll areas and fingerprint), and turning a ref back into
// an element, by its fingerprint when the element itself is gone. The table and the matching
// are logic/RefTable.swift and logic/Fingerprint.swift; this file does their AX.
//
// For the actions (station S3): `resolveRef` is the way from a ref to an element, and
// `elementContext` the way from an element to everything around it.

import AppKit
import ApplicationServices
import Foundation

// MARK: - The tables

private let refLock = NSLock()
private var refTables: [String: RefTable<AXHandle>] = [:]

/// The agent's clock for last-seen times: seconds since boot, which never steps.
func refClock() -> Double {
    Double(DispatchTime.now().uptimeNanoseconds) / 1e9
}

/// Runs `body` on a reader's table. The lock is held meanwhile, so `body` makes no AX call:
/// one hung app must never hold every reader's refs.
func withRefs<T>(_ reader: String, _ body: (inout RefTable<AXHandle>) -> T) -> T {
    refLock.lock()
    defer { refLock.unlock() }
    return body(&refTables[reader, default: RefTable()])
}

private struct RefsArgs: Decodable {
    var reader: String?
    var next: Int
}

/// The `refs` op: raises a reader's counter so its next new ref is at least `e<next>`. The daemon
/// sends it before a reader's first toolkit call on a new connection, with one past the highest
/// ref it saw that reader hold on this machine, so refs never repeat across connections: an old
/// ref is then "not a ref this reader holds" instead of the name of another element.
func registerRefsOp() {
    register("refs", .read(queueKey: { _ in "refs" })) { call in
        let args = try call.args(RefsArgs.self)
        let reader = meaningful(args.reader) ?? call.reader
        guard !reader.isEmpty else { throw AgentFailure("bad_request", "refs: name the reader whose refs to raise") }
        guard args.next >= 1 else { throw AgentFailure("bad_request", "refs: next must be 1 or more") }
        let next = withRefs(reader) { $0.raise(next: args.next) }
        return ["reader": reader, "next": next]
    }
}

/// The element's ref for a reader: the one it has, or a new one.
func giveRef(_ element: AXUIElement, _ fingerprint: Fingerprint, reader: String) -> String {
    withRefs(reader) { $0.see(AXHandle(element), fingerprint, at: refClock()) }
}

/// The ref a reader already has for an element, nil when it has none. Used for the rule that a
/// point is refused when what it hits has a ref (daemon ADR 0006 point 6).
func knownRef(_ element: AXUIElement, reader: String) -> String? {
    withRefs(reader) { $0.ref(of: AXHandle(element)) }
}

/// The pid a ref's element belongs to, without touching the element: where a request that
/// names a ref queues.
func refPid(_ ref: String, reader: String) -> pid_t? {
    withRefs(reader) { $0.entry(ref)?.fingerprint.pid }
}

// MARK: - Where an element sits

/// The most parents a climb follows. Trees deeper than a walk goes (40) are cut there too.
private let climbLimit = 64

/// An element with everything above it, read by climbing its parents: one message per level.
struct ElementContext {
    let element: AXUIElement
    /// The element, then its parent, and so on up to its application (or where the climb
    /// stopped).
    let chain: [AXHandle]
    /// What each element of `chain` said, by the same index. `reads[0]` is the element's own.
    let reads: [AXRead]
    /// The index in `chain` of the element's window: the outermost AXWindow above it (or
    /// itself), nil for an element that is in none.
    let windowIndex: Int?
    let windowNumber: Int
    /// Each element's role path under the window, by the index in `chain`, for the elements
    /// at or under the window.
    let paths: [String]
    let pid: pid_t
    let started: String

    var read: AXRead { reads[0] }
    var role: String { reads[0].role.isEmpty ? "AXUnknown" : reads[0].role }
    var window: AXUIElement? { windowIndex.map { chain[$0].element } }
    var isWindow: Bool { windowIndex == 0 }

    /// How many levels the element is under its window.
    var depth: Int { windowIndex ?? max(chain.count - 1, 0) }

    /// The fingerprint of the element at `index` of the chain.
    func fingerprint(at index: Int = 0) -> Fingerprint {
        let read = reads[index]
        return Fingerprint(
            pid: pid, started: started, window: windowNumber,
            path: index < paths.count ? paths[index] : "",
            identifier: stableIdentifier(read.identifier),
            role: read.role.isEmpty ? "AXUnknown" : read.role,
            name: read.name)
    }

    /// The scroll areas above the element, nearest first, as far up as its surface (its
    /// window, sheet or popover): the containers `scroll` works on.
    var scrollAreas: [AXUIElement] {
        var areas: [AXUIElement] = []
        for index in chain.indices.dropFirst() {
            if surfaceRoles.contains(reads[index].role) { break }
            if reads[index].role == "AXScrollArea" { areas.append(chain[index].element) }
        }
        return areas
    }

    /// What the element is inside: its window and the scroll areas above it, with the reader's
    /// refs for those.
    func clip(reader: String) -> ClipContext {
        var clip = ClipContext(screen: bounds)
        for index in chain.indices.dropFirst().reversed() {
            clip = clipBelow(clip, role: reads[index].role, frame: reads[index].frame) {
                giveRef(chain[index].element, fingerprint(at: index), reader: reader)
            }
        }
        return clip
    }

    /// What of the element shows.
    func shown(to reader: String) -> Visibility {
        visibility(of: read.frame, in: clipAround(role: role, in: clip(reader: reader)))
    }
}

/// Reads an element and everything above it. Nil when the element itself cannot be read (it is
/// gone, or its app does not answer); `error` then says which.
func elementContext(_ element: AXUIElement, read first: AXRead? = nil) -> (context: ElementContext?, error: AXError) {
    let own = first ?? readElement(element)
    guard own.ok else { return (nil, own.error) }
    var chain = [AXHandle(element)]
    var reads = [own]
    while chain.count < climbLimit, reads[reads.count - 1].role != "AXApplication",
          let parent = reads[reads.count - 1].parent {
        let handle = AXHandle(parent)
        // An app that names an element as its own ancestor would climb forever.
        if chain.contains(handle) { break }
        let read = readElement(parent)
        guard read.ok else { break }
        chain.append(handle)
        reads.append(read)
    }

    let windowIndex = reads.lastIndex { $0.role == "AXWindow" }
    // Paths run from the window down; an element in no window is placed under its app.
    let top = windowIndex ?? (chain.count - 1)
    var paths = [String](repeating: "", count: top + 1)
    if top > 0 {
        for index in stride(from: top - 1, through: 0, by: -1) {
            let place = reads[index + 1].children.firstIndex { CFEqual($0, chain[index].element) } ?? -1
            let role = reads[index].role.isEmpty ? "AXUnknown" : reads[index].role
            paths[index] = pathAppending(paths[index + 1], role: role, index: place)
        }
    }

    let pid = chain[0].pid
    var number = 0
    if let windowIndex {
        number = windowNumber(of: chain[windowIndex].element, frame: reads[windowIndex].frame, pid: pid, among: screenWindowList)
    }
    return (ElementContext(
        element: element, chain: chain, reads: reads, windowIndex: windowIndex, windowNumber: number,
        paths: paths, pid: pid, started: processStart(pid) ?? ""), .success)
}

/// The nearest scroll area above an element, nil when it is in none: the container `scroll`
/// moves when it is given the element.
func nearestScrollArea(of element: AXUIElement) -> AXUIElement? {
    elementContext(element).context?.scrollAreas.first
}

// MARK: - From a ref to an element

/// A ref turned back into an element.
struct ResolvedRef {
    let ref: String
    let element: AXUIElement
    let fingerprint: Fingerprint
    /// True when the ref's own element was gone and its fingerprint found this one in its
    /// place; the result of the action then says `reResolved`.
    let reResolved: Bool

    var pid: pid_t { fingerprint.pid }
}

private func stale(_ ref: String, _ reason: String, _ message: String) -> AgentFailure {
    AgentFailure("stale_ref", message, detail: ["ref": ref, "reason": reason])
}

/// The element a reader's ref names (daemon ADR 0006 point 3):
/// - its own element while that lives;
/// - else the one element of its window (or of its app, for an element in no window) that has
///   its fingerprint, with `reResolved` set, and the ref then names that element from now on;
/// - else `stale_ref`, whose message says what happened (its app quit, its window closed, it is
///   gone from its window) and whose `detail.reason` is `app_quit`, `window_closed`, `gone`,
///   `ambiguous` or `unknown`.
/// An app that does not answer is `not_responding`, never a stale ref: the element may be fine.
/// `until` bounds the search for a dead element's replacement (3 s when nil).
func resolveRef(_ ref: String, reader: String, until: DispatchTime? = nil) throws -> ResolvedRef {
    guard refNumber(ref) != nil else {
        throw AgentFailure("bad_request", "\(ref) is not a ref; refs look like e17 and come from machine_snapshot or machine_find")
    }
    guard let entry = withRefs(reader, { $0.entry(ref) }) else {
        throw stale(ref, "unknown", "\(ref) is not a ref this reader holds (refs are per reader, and the least recently seen are dropped past \(refTableCapacity)); take a new machine_snapshot")
    }
    let wanted = entry.fingerprint
    guard processLives(pid: wanted.pid, started: wanted.started) else {
        throw stale(ref, "app_quit", "\(ref)'s app is no longer running (it quit or crashed); take a new machine_snapshot to see what is on screen now")
    }

    let element = entry.handle.element
    AXUIElementSetMessagingTimeout(element, actionMessagingTimeout)
    switch readAttribute(element, kAXRoleAttribute).error {
    case .invalidUIElement:
        break
    case .cannotComplete:
        throw AgentFailure("not_responding", "\(ref)'s app (\(processName(wanted.pid))) is not answering accessibility; try again shortly")
    default:
        withRefs(reader) { $0.touch(ref, at: refClock()) }
        return ResolvedRef(ref: ref, element: element, fingerprint: wanted, reResolved: false)
    }

    let deadline = until ?? (.now() + .seconds(3))
    guard let app = appTarget(pid: wanted.pid) else {
        throw stale(ref, "app_quit", "\(ref)'s app is no longer running (it quit or crashed); take a new machine_snapshot to see what is on screen now")
    }
    let listing = appWindows(app)
    if listing.error == .cannotComplete {
        throw AgentFailure("not_responding", "\(ref)'s app (\(app.name)) is not answering accessibility; try again shortly")
    }
    var roots: [(window: AXUIElement, number: Int)] = []
    var listed: [ScreenWindow]?
    for window in listing.windows {
        let number = windowNumber(of: window, frame: frame(window), pid: app.pid) {
            if listed == nil { listed = screenWindowList() }
            return listed ?? []
        }
        if wanted.window == 0 || number == wanted.window { roots.append((window, number)) }
    }
    if wanted.window != 0, roots.isEmpty {
        let what = wanted.role == "AXWindow" ? "\(ref) (a window) closed" : "\(ref)'s window closed"
        throw stale(ref, "window_closed", "\(what); take a new machine_snapshot to see what is there now")
    }

    var candidates: [(candidate: AXUIElement, fingerprint: Fingerprint)] = []
    for root in roots {
        guard let found = fingerprints(under: root.window, like: wanted, window: root.number, until: deadline) else {
            // The search ran out of time or visits: what it did not see might match too, so
            // nothing it saw can be called the only match.
            throw stale(ref, "gone", "\(ref) is gone, and its window is too large or too slow to look for it again; take a new machine_snapshot")
        }
        candidates += found
    }
    switch rematch(wanted, among: candidates) {
    case let .unique(found):
        guard let match = candidates.first(where: { CFEqual($0.candidate, found) }) else { break }
        withRefs(reader) { $0.rebind(ref, to: AXHandle(found), match.fingerprint, at: refClock()) }
        return ResolvedRef(ref: ref, element: found, fingerprint: match.fingerprint, reResolved: true)
    case let .ambiguous(count):
        throw stale(ref, "ambiguous", "\(ref) is gone and \(count) elements now look like it, so none was taken; take a new machine_snapshot and use the ref of the one you mean")
    case .none:
        break
    }
    let place = wanted.window == 0 ? "its app" : "its window"
    throw stale(ref, "gone", "\(ref) is gone from \(place) (it was removed, or the view was rebuilt without it); take a new machine_snapshot")
}

/// The fingerprints of the elements under a window that have the wanted role, or nil when the
/// search could not finish (the visit or depth cap, or the deadline).
private func fingerprints(under window: AXUIElement, like wanted: Fingerprint, window number: Int, until deadline: DispatchTime)
    -> [(candidate: AXUIElement, fingerprint: Fingerprint)]? {
    var found: [(candidate: AXUIElement, fingerprint: Fingerprint)] = []
    var stack: [(element: AXUIElement, place: Int, parentPath: String?, depth: Int)] = [(window, 0, nil, 0)]
    var visited = 0
    while let next = stack.popLast() {
        visited += 1
        if visited > 5000 || next.depth > 40 || DispatchTime.now() > deadline { return nil }
        let read = readElement(next.element)
        if read.error == .cannotComplete { return nil }
        guard read.ok else { continue }
        let role = read.role.isEmpty ? "AXUnknown" : read.role
        let path = next.parentPath.map { pathAppending($0, role: role, index: next.place) } ?? ""
        if role == wanted.role {
            found.append((next.element, Fingerprint(
                pid: wanted.pid, started: wanted.started, window: number, path: path,
                identifier: stableIdentifier(read.identifier), role: role, name: read.name)))
        }
        for (place, child) in read.children.enumerated().reversed() {
            stack.append((child, place, path, next.depth + 1))
        }
    }
    return found
}
