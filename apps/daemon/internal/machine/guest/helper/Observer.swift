// What wakes the agent's waits (daemon ADR 0006 point 5; docs/21 section 4.5's Observer): one
// AXObserver per watched app on the main run loop (the agent's main thread runs CFRunLoopRun),
// and NSWorkspace's launch, terminate and activate notifications. They only wake waiters: every
// wait also polls, since SwiftUI drops notifications. And the settle every action ends with.

import AppKit
import ApplicationServices
import Foundation

/// The notifications an app's observer asks for.
private let watchedNotifications: [String] = [
    kAXValueChangedNotification, kAXUIElementDestroyedNotification, kAXCreatedNotification,
    kAXFocusedUIElementChangedNotification, kAXWindowCreatedNotification, kAXSheetCreatedNotification,
    kAXMenuOpenedNotification, kAXLayoutChangedNotification, kAXTitleChangedNotification,
]

/// The most apps watched at once; the least recently watched is dropped first.
private let maxWatchedApps = 32

/// Counts what happened, per app and on the desktop as a whole, and wakes whoever waits for it.
final class UIWaker {
    static let shared = UIWaker()

    private let condition = NSCondition()
    private var perApp: [pid_t: UInt64] = [:]
    private var desktop: UInt64 = 0

    /// Something happened in an app.
    func bump(_ pid: pid_t) {
        condition.lock()
        perApp[pid, default: 0] &+= 1
        condition.broadcast()
        condition.unlock()
    }

    /// Something happened to the desktop: an app launched, quit or came to the front.
    func bumpDesktop() {
        condition.lock()
        desktop &+= 1
        condition.broadcast()
        condition.unlock()
    }

    /// A number that grows whenever something happens in the app (or on the desktop): two equal
    /// stamps mean nothing was notified in between.
    func stamp(_ pid: pid_t?) -> UInt64 {
        condition.lock()
        defer { condition.unlock() }
        return stampLocked(pid)
    }

    private func stampLocked(_ pid: pid_t?) -> UInt64 {
        (pid.map { perApp[$0] ?? 0 } ?? perApp.values.reduce(0, &+)) &+ desktop
    }

    /// Sleeps until the stamp moves on from `since` or `deadline` passes; true when it moved.
    @discardableResult
    func wait(_ pid: pid_t?, since: UInt64, until deadline: DispatchTime) -> Bool {
        condition.lock()
        defer { condition.unlock() }
        while stampLocked(pid) == since {
            let now = DispatchTime.now()
            if now >= deadline { return false }
            let seconds = Double(deadline.uptimeNanoseconds - now.uptimeNanoseconds) / 1e9
            _ = condition.wait(until: Date().addingTimeInterval(seconds))
        }
        return true
    }
}

/// Sleeps `ms` or until the app (or the desktop) notifies something, whichever is first.
func nap(_ ms: Int, for pid: pid_t?, since stamp: UInt64, until limit: DispatchTime? = nil) {
    var end = DispatchTime.now() + .milliseconds(max(ms, 0))
    if let limit, limit < end { end = limit }
    UIWaker.shared.wait(pid, since: stamp, until: end)
}

// MARK: - AXObservers

private struct Watched {
    let observer: AXObserver
    let started: String
    var usedAt: UInt64
}

private let watchLock = NSLock()
private var watched: [pid_t: Watched] = [:]
private var watchTick: UInt64 = 0

/// The observer callback: runs on the main run loop and only counts.
private let observerCallback: AXObserverCallback = { _, _, _, refcon in
    guard let refcon else { return }
    UIWaker.shared.bump(pid_t(truncatingIfNeeded: Int(bitPattern: refcon)))
}

/// NSWorkspace's app notifications, registered once, on first use.
private let workspaceWatch: Void = {
    let center = NSWorkspace.shared.notificationCenter
    for name in [NSWorkspace.didLaunchApplicationNotification, NSWorkspace.didTerminateApplicationNotification,
                 NSWorkspace.didActivateApplicationNotification] {
        center.addObserver(forName: name, object: nil, queue: nil) { note in
            if name == NSWorkspace.didTerminateApplicationNotification,
               let app = note.userInfo?[NSWorkspace.applicationUserInfoKey] as? NSRunningApplication {
                unwatch(app.processIdentifier)
            }
            UIWaker.shared.bumpDesktop()
        }
    }
}()

/// Watches an app for the notifications waits and settles wake on. Idempotent; best effort: an
/// app that will not take an observer is still polled.
func watch(_ pid: pid_t) {
    _ = workspaceWatch
    guard pid > 0 else { return }
    let started = processStart(pid) ?? ""
    watchLock.lock()
    watchTick &+= 1
    if var known = watched[pid], known.started == started {
        known.usedAt = watchTick
        watched[pid] = known
        watchLock.unlock()
        return
    }
    watchLock.unlock()

    var created: AXObserver?
    guard AXObserverCreate(pid, observerCallback, &created) == .success, let observer = created else { return }
    let app = AXUIElementCreateApplication(pid)
    AXUIElementSetMessagingTimeout(app, actionMessagingTimeout)
    let refcon = UnsafeMutableRawPointer(bitPattern: Int(pid))
    for name in watchedNotifications {
        // An app refuses some notifications (no windows yet, an old toolkit); the rest still come.
        _ = AXObserverAddNotification(observer, app, name as CFString, refcon)
    }
    CFRunLoopAddSource(CFRunLoopGetMain(), AXObserverGetRunLoopSource(observer), .defaultMode)
    CFRunLoopWakeUp(CFRunLoopGetMain())

    watchLock.lock()
    var dropped: [AXObserver] = []
    if let old = watched[pid] { dropped.append(old.observer) }
    watched[pid] = Watched(observer: observer, started: started, usedAt: watchTick)
    while watched.count > maxWatchedApps, let oldest = watched.min(by: { $0.value.usedAt < $1.value.usedAt }) {
        dropped.append(oldest.value.observer)
        watched[oldest.key] = nil
    }
    watchLock.unlock()
    for observer in dropped {
        CFRunLoopRemoveSource(CFRunLoopGetMain(), AXObserverGetRunLoopSource(observer), .defaultMode)
    }
}

private func unwatch(_ pid: pid_t) {
    watchLock.lock()
    let gone = watched.removeValue(forKey: pid)
    watchLock.unlock()
    if let gone {
        CFRunLoopRemoveSource(CFRunLoopGetMain(), AXObserverGetRunLoopSource(gone.observer), .defaultMode)
    }
}

// MARK: - Settle (daemon ADR 0006 point 5)

/// How long an app must stay quiet to count as settled, and the longest a settle waits.
let settleQuietMs = 300
let settleLimitMs = 2000

struct Settled {
    /// True when the app stayed quiet for `settleQuietMs`; false when it was still changing at
    /// the limit, stopped answering, or the call ran out of time.
    let settled: Bool
    /// How long the settle took.
    let ms: Int
    /// The app's process ended meanwhile.
    let gone: Bool
}

/// Waits until an app's UI settles: no AX notification and no change of its tree's signature for
/// 300 ms, at most 2 s. A notification restarts the quiet time at once; the signature, which
/// catches what SwiftUI does not notify, is read after every 150 ms without one. Never throws: an
/// input already posted is answered with its effect, so a cancel or the call's deadline only
/// ends the settle early (`reserve` is kept for the after tree).
func settleUI(_ app: AppTarget, call: Call, limitMs: Int = settleLimitMs, reserve: TimeInterval = 1) -> Settled {
    let start = DispatchTime.now()
    func ms(since time: DispatchTime) -> Int { Int((DispatchTime.now().uptimeNanoseconds - time.uptimeNanoseconds) / 1_000_000) }
    var seen = UIWaker.shared.stamp(app.pid)
    var signature = busy("ax") { treeSignature(app, call: call, budget: 0.5) }
    var quietSince = DispatchTime.now()
    while true {
        if !processLives(pid: app.pid, started: app.started) { return Settled(settled: false, ms: ms(since: start), gone: true) }
        let quiet = ms(since: quietSince)
        if quiet >= settleQuietMs, signature != nil { return Settled(settled: true, ms: ms(since: start), gone: false) }
        if ms(since: start) >= limitMs || call.cancelled || call.remaining <= reserve {
            return Settled(settled: false, ms: ms(since: start), gone: false)
        }
        let wake = min(waitPollMs, max(settleQuietMs - quiet, 10), max(limitMs - ms(since: start), 1))
        if UIWaker.shared.wait(app.pid, since: seen, until: .now() + .milliseconds(wake)) {
            seen = UIWaker.shared.stamp(app.pid)
            quietSince = DispatchTime.now()
            continue
        }
        let now = busy("ax") { treeSignature(app, call: call, budget: 0.5) }
        if now == nil || now != signature { quietSince = DispatchTime.now() }
        signature = now
    }
}
