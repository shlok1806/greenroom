// Bringing an app to the front before input, and clicking the part of an element that shows
// (daemon ADR 0009). Keys go to the frontmost app and a click to whatever is on top at its point;
// an app started through machine_exec opens behind the frontmost one, and the Dock covers the
// bottom of the screen.

import AppKit
import ApplicationServices
import Foundation

/// How long `focus` waits for the window server to put the app in front.
let focusWait: TimeInterval = 1

/// The `focus` action: unhides the app, sets it frontmost through accessibility, un-minimizes and
/// raises its window (the one under the action's point, else its main one) and waits until it is
/// in front. Throws, naming the app that stayed in front, when it cannot.
func focus(_ action: Action) throws {
    let running: NSRunningApplication
    if let pid = action.pid {
        guard let app = NSRunningApplication(processIdentifier: pid_t(pid)), !app.isTerminated else {
            throw Failure("focus: no running application with pid \(pid)")
        }
        running = app
    } else if let name = action.app, !name.isEmpty {
        running = try targetApp(name)
    } else {
        throw Failure("focus needs app or pid")
    }
    let pid = running.processIdentifier
    let name = running.localizedName ?? processName(pid)
    if running.isHidden { running.unhide() }

    let app = AXUIElementCreateApplication(pid)
    AXUIElementSetMessagingTimeout(app, 1)
    if let window = focusWindow(app, at: action.x.flatMap { x in action.y.map { CGPoint(x: x, y: $0) } }) {
        var minimized: CFTypeRef?
        if AXUIElementCopyAttributeValue(window, kAXMinimizedAttribute as CFString, &minimized) == .success,
           (minimized as? Bool) == true {
            AXUIElementSetAttributeValue(window, kAXMinimizedAttribute as CFString, kCFBooleanFalse)
        }
        AXUIElementPerformAction(window, kAXRaiseAction as CFString)
        AXUIElementSetAttributeValue(window, kAXMainAttribute as CFString, kCFBooleanTrue)
    }
    // NSWorkspace's frontmost application can be stale in the long-lived agent (`isInFront`).
    let frontAtStart = NSWorkspace.shared.frontmostApplication?.processIdentifier
    if frontAtStart == pid && running.isActive { return }
    // Through accessibility first: a background process's NSRunningApplication.activate is
    // declined by the cooperative activation of macOS 14 and later.
    AXUIElementSetAttributeValue(app, kAXFrontmostAttribute as CFString, kCFBooleanTrue)
    let deadline = Date().addingTimeInterval(focusWait)
    var asked = false
    while Date() < deadline {
        if isInFront(pid: pid, isActive: running.isActive, frontNow: NSWorkspace.shared.frontmostApplication?.processIdentifier,
                     frontAtStart: frontAtStart) {
            usleep(50_000) // the window server orders the raised window with the activation
            return
        }
        if !asked, Date().addingTimeInterval(focusWait / 2) >= deadline {
            running.activate(options: [])
            asked = true
        }
        // NSWorkspace's frontmost application is updated on the main run loop.
        RunLoop.current.run(until: Date().addingTimeInterval(0.02))
    }
    let front = NSWorkspace.shared.frontmostApplication?.localizedName ?? "another application"
    throw Failure("could not bring \(name) to the front within \(Int(focusWait)) s; \(front) stayed in front")
}

/// The window to raise: the app's window under point when it has one there, else its main,
/// focused or first window.
private func focusWindow(_ app: AXUIElement, at point: CGPoint?) -> AXUIElement? {
    var value: CFTypeRef?
    guard AXUIElementCopyAttributeValue(app, kAXWindowsAttribute as CFString, &value) == .success,
          let windows = value as? [AXUIElement], !windows.isEmpty
    else { return nil }
    if let point, let under = windows.first(where: { frame(of: $0)?.contains(point) == true }) {
        return under
    }
    for attribute in [kAXMainWindowAttribute, kAXFocusedWindowAttribute] {
        var window: CFTypeRef?
        if AXUIElementCopyAttributeValue(app, attribute as CFString, &window) == .success, let window,
           CFGetTypeID(window) == AXUIElementGetTypeID() {
            // swiftlint:disable:next force_cast
            return (window as! AXUIElement)
        }
    }
    return windows.first
}

/// An accessibility element's frame in points, top-left origin, or nil.
private func frame(of element: AXUIElement) -> CGRect? {
    var position: CFTypeRef?
    var size: CFTypeRef?
    guard AXUIElementCopyAttributeValue(element, kAXPositionAttribute as CFString, &position) == .success,
          AXUIElementCopyAttributeValue(element, kAXSizeAttribute as CFString, &size) == .success,
          let position, let size
    else { return nil }
    var origin = CGPoint.zero
    var extent = CGSize.zero
    // swiftlint:disable force_cast
    AXValueGetValue(position as! AXValue, .cgPoint, &origin)
    AXValueGetValue(size as! AXValue, .cgSize, &extent)
    // swiftlint:enable force_cast
    return CGRect(origin: origin, size: extent)
}

/// The point to click for a click by element: its center when the app owns what the window
/// server finds there, else the owned point of its frame nearest the center. Throws, naming what
/// covers the element, when no point of it shows.
func ownedPoint(of action: Action, pid: pid_t, center: CGPoint) throws -> CGPoint {
    let owner = { (point: CGPoint) -> pid_t? in
        guard let hit = elementAt(point) else { return nil }
        var found: pid_t = 0
        return AXUIElementGetPid(hit, &found) == .success ? found : nil
    }
    let at = owner(center)
    // Nothing answered the hit test (no accessibility, a window without it): click as aimed.
    if at == nil || at == pid { return center }
    let size = CGSize(width: action.w ?? 0, height: action.h ?? 0)
    let element = CGRect(x: center.x - size.width / 2, y: center.y - size.height / 2, width: size.width, height: size.height)
    let points = clickPoints(in: element).map(clamp)
    if let point = firstOwned(Array(points.dropFirst()), owns: { owner($0) == pid }) {
        return point
    }
    let cover = at.map(processName) ?? "another window"
    throw Failure("the element at (\(Int(center.x)), \(Int(center.y))) is covered by \(cover.isEmpty ? "another window" : cover) at every point; " +
        "move its window, scroll it into view, or use the keyboard")
}
