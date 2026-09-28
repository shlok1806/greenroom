// The waiting ops of the toolkit (daemon ADR 0006; docs/21 section 5.5): `waitFor`, which waits
// for a target to reach a state, and `expect`, which polls a property until it passes. Both are
// reads: they run on the concurrent wait queue and hop onto their app's read queue for each
// poll only, so a long wait never holds up a snapshot of the app it watches. Notifications
// (Observer.swift) wake a wait early; every wait also polls. A wait that times out is a normal
// result with `satisfied: false`, and an expectation that never passed is `passed: false`. The
// decisions are logic/WaitChecks.swift.

import AppKit
import ApplicationServices
import Foundation

func registerWaitOps() {
    register("waitFor", .wait) { call in
        let args = try call.args(WaitForArgs.self)
        try requireAccessibility()
        return try waitFor(args, call: call)
    }
    register("expect", .wait) { call in
        let args = try call.args(ExpectArgs.self)
        try requireAccessibility()
        return try expect(args, call: call)
    }
}

// MARK: - Arguments

/// What a wait or an expectation watches: a ref, an element by text (and role), a window by title,
/// an app, or the app going idle. The daemon sends an object; a bare ref or "idle" is taken too.
private struct TargetArgs: Decodable {
    var ref: String?
    var text: String?
    var role: String?
    var app: String?
    var window: String?
    var idle: Bool?

    private enum Keys: String, CodingKey {
        case ref, text, role, app, window, idle
    }

    init(from decoder: Decoder) throws {
        if let single = try? decoder.singleValueContainer(), let word = try? single.decode(String.self) {
            if word == "idle" { idle = true } else { ref = word }
            return
        }
        let container = try decoder.container(keyedBy: Keys.self)
        ref = try container.decodeIfPresent(String.self, forKey: .ref)
        text = try container.decodeIfPresent(String.self, forKey: .text)
        role = try container.decodeIfPresent(String.self, forKey: .role)
        app = try container.decodeIfPresent(String.self, forKey: .app)
        window = try container.decodeIfPresent(String.self, forKey: .window)
        idle = try container.decodeIfPresent(Bool.self, forKey: .idle)
    }
}

private struct ValueArgs: Decodable {
    var op: String?
    var expected: String
}

private struct WaitForArgs: Decodable {
    var target: TargetArgs
    var state: String?
    var value: ValueArgs?
    var timeoutMs: Int?
}

/// `expected` as a string, a bool or a whole number.
private struct ExpectedArg: Decodable {
    let value: Expected

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let flag = try? container.decode(Bool.self) {
            value = .flag(flag)
        } else if let number = try? container.decode(Double.self) {
            guard number.isFinite, number == number.rounded(), abs(number) < 1e9 else {
                throw DecodingError.dataCorruptedError(in: container, debugDescription: "a count is a whole number")
            }
            value = .count(Int(number))
        } else if let text = try? container.decode(String.self) {
            value = .text(text)
        } else {
            throw DecodingError.dataCorruptedError(in: container, debugDescription: "expected is a string, a bool or a whole number")
        }
    }
}

private struct ExpectArgs: Decodable {
    var target: TargetArgs
    var property: String
    var op: String?
    var expected: ExpectedArg?
    var timeoutMs: Int?
}

// MARK: - Targets

private enum Target {
    case ref(String)
    case text(TextPattern, role: String?, app: String?)
    case window(String, app: String?)
    case app(String)
    case idle(app: String?)

    /// The target as a sentence names it.
    var label: String {
        switch self {
        case let .ref(ref): return ref
        case .text: return "the element"
        case let .window(title, _): return "window \"\(title)\""
        case let .app(name): return "app \"\(name)\""
        case .idle: return "the app"
        }
    }
}

private func target(_ args: TargetArgs) throws -> Target {
    let named = [meaningful(args.ref), meaningful(args.text), meaningful(args.window), args.idle == true ? "idle" : nil].compactMap { $0 }
    if named.count > 1 {
        throw AgentFailure("bad_request", "target names more than one thing; pass one of ref, text, window or idle (app may scope text, window or idle)")
    }
    if let ref = meaningful(args.ref) {
        guard refNumber(ref) != nil else {
            throw AgentFailure("bad_request", "\(ref) is not a ref; refs look like e17 and come from machine_snapshot or machine_find")
        }
        return .ref(ref)
    }
    if let text = args.text, !text.isEmpty {
        do {
            return .text(try textPattern(text), role: meaningful(args.role), app: meaningful(args.app))
        } catch let error as PatternError {
            throw AgentFailure("bad_request", error.message)
        }
    }
    if let window = meaningful(args.window) { return .window(window, app: meaningful(args.app)) }
    if args.idle == true { return .idle(app: meaningful(args.app)) }
    if let app = meaningful(args.app) { return .app(app) }
    throw AgentFailure("bad_request", "target is missing; pass a ref such as \"e62\", {\"text\": \"Done\", \"role\": \"Button\"}, {\"app\": \"TipSplit\"}, {\"window\": \"Settings\"} or {\"idle\": true}")
}

/// What one poll saw of a target. For a text target that matched several elements, the facts of
/// one element are left out and `candidates` lists them.
private struct Seen {
    var exists = false
    var shows = false
    var covered = false
    var enabled: Bool?
    var focused: Bool?
    var selected: Bool?
    var name: String?
    var value: String?
    var secret = false
    var count = 0
    var node: [String: Any]?
    var candidates: [[String: Any]] = []
    var tree: Signature?

    var ambiguous: Bool { count > 1 }

    var wait: WaitObservation {
        WaitObservation(exists: exists, shows: shows, enabled: enabled, focused: focused, name: name,
                        value: secret ? value.map { secretText($0.count) } : value, tree: tree)
    }

    var expectation: ExpectObservation {
        ExpectObservation(exists: exists, visible: shows && !covered, enabled: enabled, selected: selected,
                          name: name, value: value, count: count, secret: secret)
    }
}

/// The facts of one element a look saw.
private func seen(_ look: Look) -> Seen {
    let states = look.node["states"] as? [String] ?? []
    var out = Seen()
    out.exists = true
    out.shows = look.shown.vis != nil
    out.covered = look.covered != nil
    out.enabled = !states.contains("disabled")
    out.focused = states.contains("focused")
    out.selected = states.contains("selected")
    out.name = look.read.name
    out.secret = look.read.secret
    if look.role == "AXWindow" {
        out.value = nil
    } else {
        out.value = look.read.value ?? ""
    }
    out.count = 1
    out.node = look.node
    return out
}

/// The app a target lives in, when it can be named now: a ref's (from the ref table, touching
/// nothing), else the one it names, else the frontmost. Nil for an app that is not running.
private func targetPid(_ target: Target, reader: String) -> pid_t? {
    switch target {
    case let .ref(ref):
        return refPid(ref, reader: reader)
    case let .text(_, _, app), let .window(_, app), let .idle(app):
        return (try? targetApp(app))?.processIdentifier
    case let .app(name):
        return (try? targetApp(name))?.processIdentifier
    }
}

/// Where an app's reads queue, as `appQueueKey` names it.
private func queueKey(_ pid: pid_t?) -> String {
    pid.map { "pid:\($0)" } ?? "app:"
}

/// The running application a name or bundle id names, nil when none does.
private func runningApp(_ name: String) -> NSRunningApplication? {
    try? targetApp(name)
}

/// One poll of a target, run on its app's read queue. `tree` asks for the app's signature too
/// (a `changes` of an app, and `idle`). Nil when the app did not answer this time.
private func poll(_ target: Target, reader: String, call: Call, tree: Bool) throws -> Seen? {
    switch target {
    case let .ref(ref):
        do {
            let found = try look(ref: ref, reader: reader, hitTest: true, until: min(call.deadline, .now() + .seconds(1)))
            var out = seen(found)
            if tree, let app = appTarget(pid: found.pid) { out.tree = treeSignature(app, call: call, budget: 0.5) }
            return out
        } catch let failure as AgentFailure where failure.code == "stale_ref" {
            // Gone: which is what a wait for `disappears` waits for. Only a ref the reader
            // never had is an error.
            if (failure.detail?["reason"] as? String) == "unknown" { throw failure }
            return Seen()
        } catch let failure as AgentFailure where failure.code == "not_responding" {
            return nil
        }

    case let .text(pattern, role, appName):
        guard let app = appName.map({ try? appTarget(named: $0) }) ?? (try? appTarget(named: nil)) else { return Seen() }
        let listing = appWindows(app)
        if listing.error == .cannotComplete { return nil }
        var options = WalkOptions()
        options.mode = .all
        options.limit = 50
        options.budget = 1
        options.accept = { read in
            findMatches(pattern, role: role, FindFields(
                role: read.role, title: read.title ?? "", value: read.value ?? "", desc: read.description ?? "",
                help: read.help ?? "", placeholder: read.placeholder ?? "", identifier: read.identifier ?? "",
                secret: read.secret))
        }
        let walk = Walk(app: app, reader: reader, options: options, call: call)
        walk.focusedWindow = listing.focused
        if listing.windows.isEmpty {
            walk.walkRootChildren()
        } else {
            walk.walk(windows: listing.windows)
        }
        walk.findCoverers()
        if !walk.responding { return nil }
        let matches = walk.nodes
        var out = Seen()
        out.count = matches.count
        out.exists = !matches.isEmpty
        out.shows = matches.contains { $0["vis"] != nil }
        out.covered = !matches.contains { $0["vis"] != nil && $0["covered"] == nil }
        if matches.count == 1, let ref = matches[0]["ref"] as? String,
           let found = try? look(ref: ref, reader: reader, hitTest: true, until: min(call.deadline, .now() + .seconds(1))) {
            // One match: read it whole, since a node cuts long text.
            out = seen(found)
        } else if matches.count > 1 {
            out.candidates = Array(matches.prefix(10))
            out.node = matches.first { $0["vis"] != nil } ?? matches.first
        }
        if tree { out.tree = treeSignature(app, call: call, budget: 0.5) }
        return out

    case let .window(title, appName):
        guard let app = appName.map({ try? appTarget(named: $0) }) ?? (try? appTarget(named: nil)) else { return Seen() }
        let listing = appWindows(app)
        if listing.error == .cannotComplete { return nil }
        let titled = listing.windows.map { (window: $0, title: textAttribute($0, kAXTitleAttribute) ?? "") }
        var matching = titled.filter { $0.title.caseInsensitiveCompare(title) == .orderedSame }
        if matching.isEmpty { matching = titled.filter { $0.title.range(of: title, options: .caseInsensitive) != nil } }
        var out = Seen()
        out.count = matching.count
        if matching.count == 1, let found = look(element: matching[0].window, reader: reader, hitTest: false) {
            out = seen(found)
        } else if matching.count > 1 {
            out.exists = true
            out.candidates = matching.prefix(10).compactMap { look(element: $0.window, reader: reader, hitTest: false)?.node }
            out.shows = true
        }
        if tree { out.tree = treeSignature(app, call: call, budget: 0.5) }
        return out

    case let .app(name):
        var out = Seen()
        guard let running = runningApp(name), !running.isTerminated else { return out }
        out.exists = true
        out.count = 1
        out.name = running.localizedName
        out.focused = running.isActive || NSWorkspace.shared.frontmostApplication?.processIdentifier == running.processIdentifier
        let pid = running.processIdentifier
        // An app has appeared once a window of it is on screen, as a person sees it launch.
        out.shows = screenWindowList().contains { $0.pid == pid && $0.layer == 0 && rectShows($0.bounds.intersection(bounds)) }
        if tree, let app = appTarget(pid: pid) { out.tree = treeSignature(app, call: call, budget: 0.5) }
        return out

    case let .idle(appName):
        guard let app = appName.map({ try? appTarget(named: $0) }) ?? (try? appTarget(named: nil)) else { return Seen() }
        var out = Seen()
        out.exists = true
        out.tree = treeSignature(app, call: call, budget: 0.5)
        return out.tree == nil ? nil : out
    }
}

/// Polls a target on its app's read queue, bounded by `end`.
private func pollQueued(_ target: Target, call: Call, tree: Bool, until end: DispatchTime) throws -> Seen? {
    let pid = targetPid(target, reader: call.reader)
    let limit = min(call.deadline, max(end, .now() + .milliseconds(500)))
    do {
        return try onReadQueue(queueKey(pid), until: limit) {
            try busy("ax") { try poll(target, reader: call.reader, call: call, tree: tree) }
        }
    } catch let failure as AgentFailure where failure.code == "not_responding" {
        return nil
    }
}

/// The tree of a target's app, read on its queue, for a wait's before and after; nil when the
/// app is not running or does not answer.
private func treeQueued(_ target: Target, call: Call, budget: TimeInterval) -> [String: Any]? {
    guard let pid = targetPid(target, reader: call.reader), let app = appTarget(pid: pid) else { return nil }
    let limit = min(call.deadline, .now() + .milliseconds(Int(budget * 1000) + 500))
    let tree = try? onReadQueue(queueKey(pid), until: limit) {
        busy("ax") { appTree(app, reader: call.reader, call: call, budget: budget) }
    }
    guard let tree, tree.responding else { return nil }
    return tree.object
}

private func ambiguous(_ target: Target, _ seen: Seen) -> AgentFailure {
    AgentFailure("ambiguous", "\(seen.count) elements match the text, and this compares one element; pass the ref of the one you mean",
                 detail: ["candidates": seen.candidates])
}

// MARK: - waitFor

/// What is kept of the call's deadline for the after tree and the answer.
private let waitReserve: TimeInterval = 2

private func waitFor(_ args: WaitForArgs, call: Call) throws -> [String: Any] {
    let target = try target(args.target)
    var state: WaitState?
    var matcher: TextMatcher?
    if case .idle = target {
        if let spelled = meaningful(args.state), spelled != "changes" {
            throw AgentFailure("bad_request", "an idle wait has no state; leave state out")
        }
    } else {
        let spelled = meaningful(args.state) ?? WaitState.appears.rawValue
        guard let parsed = WaitState(rawValue: spelled) else {
            throw AgentFailure("bad_request", "state \(spelled) is not one of \(WaitState.allCases.map(\.rawValue).joined(separator: ", "))")
        }
        state = parsed
        if parsed == .value {
            guard let value = args.value else {
                throw AgentFailure("bad_request", "state \"value\" needs value: {\"op\": \"equals\", \"expected\": \"42\"}")
            }
            do {
                matcher = try TextMatcher(op: value.op, expected: value.expected)
            } catch let error as PatternError {
                throw AgentFailure("bad_request", error.message)
            }
        }
        if case .app = target, [.enabled, .disabled, .value].contains(parsed) {
            throw AgentFailure("bad_request", "an app has no \(parsed.rawValue) state; wait for it to appear, disappear, be focused (frontmost) or change")
        }
    }
    let timeoutMs = waitTimeoutMs(args.timeoutMs, default: defaultWaitTimeoutMs)
    let start = DispatchTime.now()
    var end = start + .milliseconds(timeoutMs)
    let latest = call.deadline - .milliseconds(Int(waitReserve * 1000))
    if latest < end { end = max(latest, start) }
    func elapsedMs() -> Int { Int((DispatchTime.now().uptimeNanoseconds - start.uptimeNanoseconds) / 1_000_000) }

    var pid = targetPid(target, reader: call.reader)
    if let pid { watch(pid) }
    let before = treeQueued(target, call: call, budget: 1.2)
    let wantsTree: Bool = {
        if case .idle = target { return true }
        if case .app = target { return state == .changes }
        return false
    }()

    var first: Seen?
    var last: Seen?
    var satisfied = false
    var quietSince = DispatchTime.now()
    while true {
        let stamp = UIWaker.shared.stamp(pid)
        if let now = try pollQueued(target, call: call, tree: wantsTree, until: end) {
            if case .idle = target {
                if let previous = last?.tree, previous != now.tree { quietSince = DispatchTime.now() }
                last = now
                let quiet = Int((DispatchTime.now().uptimeNanoseconds - quietSince.uptimeNanoseconds) / 1_000_000)
                if quiet >= settleQuietMs { satisfied = true }
            } else if let state {
                if now.ambiguous, ![.appears, .disappears, .changes].contains(state) { throw ambiguous(target, now) }
                if state == .value, now.secret {
                    throw AgentFailure("bad_request", "\(now.node?["ref"] as? String ?? target.label) is a secure text field, whose value is never read; wait for it to appear, be enabled or be focused instead")
                }
                if first == nil { first = now }
                last = now
                satisfied = waitSatisfied(state, now: now.wait, first: first?.wait ?? now.wait, matcher: matcher)
            }
        } else if case .idle = target {
            quietSince = DispatchTime.now() // Not answering is not idle.
        }
        if satisfied || DispatchTime.now() >= end { break }
        if pid == nil, let found = targetPid(target, reader: call.reader) {
            pid = found
            watch(found)
        }
        let nowStamp = UIWaker.shared.stamp(pid)
        if nowStamp == stamp {
            nap(waitPollMs, for: pid, since: stamp, until: end)
        } else if case .idle = target {
            quietSince = DispatchTime.now() // Notified while polling: something moved.
        }
        try call.check()
    }

    var result: [String: Any] = ["satisfied": satisfied, "elapsedMs": elapsedMs()]
    if let node = last?.node { result["node"] = node }
    if let value = last?.wait.value, last?.exists == true { result["value"] = value }
    if let before { result["before"] = before }
    if let after = treeQueued(target, call: call, budget: max(0.2, min(1.2, call.remaining - 0.5))) { result["after"] = after }
    return result
}

// MARK: - expect

private func expect(_ args: ExpectArgs, call: Call) throws -> [String: Any] {
    let target = try target(args.target)
    if case .idle = target {
        throw AgentFailure("bad_request", "idle is for waitFor; expect needs an element, a window or an app")
    }
    guard let property = ExpectProperty(rawValue: args.property) else {
        throw AgentFailure("bad_request", "property \(args.property) is not one of \(ExpectProperty.allCases.map(\.rawValue).joined(separator: ", "))")
    }
    let op = meaningful(args.op) ?? property.ops[0]
    let expected: Expected
    var matcher: TextMatcher?
    do {
        expected = try expectedFor(property, op: op, expected: args.expected?.value)
        if case let .text(text) = expected { matcher = try TextMatcher(op: op, expected: text) }
    } catch let error as PatternError {
        throw AgentFailure("bad_request", error.message)
    }
    if case .app = target, [.value, .enabled, .selected, .visible].contains(property) {
        throw AgentFailure("bad_request", "an app has no \(property.rawValue); expect exists, name or count of it, or name one of its elements")
    }

    let timeoutMs = waitTimeoutMs(args.timeoutMs, default: defaultExpectTimeoutMs)
    let start = DispatchTime.now()
    var end = start + .milliseconds(timeoutMs)
    let latest = call.deadline - .milliseconds(500)
    if latest < end { end = max(latest, start) }
    func elapsedMs() -> Int { Int((DispatchTime.now().uptimeNanoseconds - start.uptimeNanoseconds) / 1_000_000) }
    let single: Set<ExpectProperty> = [.value, .name, .enabled, .selected]

    var last: Seen?
    var passed = false
    var attempt = 0
    while true {
        if let now = try pollQueued(target, call: call, tree: false, until: end) {
            if now.ambiguous, single.contains(property) { throw ambiguous(target, now) }
            last = now
            // A secure field's value is never read, so waiting for it cannot help.
            if property == .value, now.secret { break }
            passed = expectPasses(property, op: op, expected: expected, matcher: matcher, now.expectation)
        }
        if passed { break }
        let now = DispatchTime.now()
        if now >= end { break }
        var next = now + .milliseconds(expectPollWait(after: attempt))
        if next > end { next = end }
        attempt += 1
        usleep(UInt32((next.uptimeNanoseconds - now.uptimeNanoseconds) / 1000))
        try call.check()
    }

    var result: [String: Any] = [
        "passed": passed, "elapsedMs": elapsedMs(),
        "observed": observedValue(property, last?.expectation ?? ExpectObservation()),
    ]
    if let node = last?.node { result["node"] = node }
    return result
}
