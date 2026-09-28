// The guest agent's ops that keep the one-shot modes' shapes (daemon ADR 0005 point 12):
// screen, ui, desktop and input here, capture in Capture.swift, sh in AgentShell.swift.

import AppKit
import ApplicationServices
import Foundation

/// Every op the agent answers. A new op file adds its register function here, and that is the
/// whole change to the dispatcher.
func registerAgentOps() {
    registerCoreOps()
    registerCaptureOp()
    registerShellOp()
    registerSnapshotOps()
}

/// The read queue of the app a request targets: `args.app` if it names one, else the
/// frontmost, by pid, so reads of one hung app queue only behind each other.
func appQueueKey(_ call: Call) -> String {
    struct Target: Decodable { var app: String? }
    let name = (try? call.args(Target.self))?.app
    if let app = try? targetApp(name) { return "pid:\(app.processIdentifier)" }
    // No such app: the op answers that itself, from a queue of its own.
    return "app:\(name ?? "")"
}

private func registerCoreOps() {
    register("screen", .read(queueKey: { _ in "screen" })) { _ in screenInfo() }

    register("desktop", .read(queueKey: { _ in "desktop" })) { _ in desktop() }

    register("ui", .read(queueKey: appQueueKey)) { call in
        let request = try call.args(UIRequest.self)
        guard AXIsProcessTrusted() else {
            throw AgentFailure("not_trusted", "this machine has not granted Accessibility to the guest agent, so the UI tree cannot be read")
        }
        return try busy("ax") {
            do {
                return try uiTree(request)
            } catch let failure as Failure {
                // targetApp's two failures: a name that matches nothing, or no app in front.
                throw AgentFailure("not_found", failure.message)
            }
        }
    }

    register("input", .input) { call in
        let actions = try call.args(Request.self).actions
        try validate(actions)
        try perform(actions, before: { index in
            do {
                try call.check()
            } catch var failure as AgentFailure {
                // Posted events are never undone: say how far the batch got.
                failure.detail = (failure.detail ?? [:]).merging(["posted": index, "actions": actions.count]) { $1 }
                throw failure
            }
        })
        return [
            "ok": true,
            "actions": actions.count,
            "screen": ["width": Int(bounds.width), "height": Int(bounds.height)],
        ]
    }
}

private let actionTypes: Set<String> = ["move", "click", "down", "up", "scroll", "type", "key", "sleep"]

/// Refuses a batch the one-shot helper would fail part way through, before anything of it is
/// posted.
private func validate(_ actions: [Action]) throws {
    for (index, action) in actions.enumerated() {
        let type = action.type.lowercased()
        guard actionTypes.contains(type) else {
            throw AgentFailure("bad_request", "actions[\(index)]: unknown action \(action.type); use one of \(actionTypes.sorted().joined(separator: ", "))")
        }
        if type == "key", keyCode(for: action.key ?? "") == nil {
            throw AgentFailure("bad_request", "actions[\(index)]: unknown key \(action.key ?? "")")
        }
    }
}
