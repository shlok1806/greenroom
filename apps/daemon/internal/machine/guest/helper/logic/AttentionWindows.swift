// Which windows of other processes sit over the target app (daemon ADR 0006's `attention`, kind
// `window`), decided over the window server's list, and which of the target's own windows are
// open menus. Pure: the guest reads the list (Attention.swift), the host tests
// run recorded ones.

import CoreGraphics
import Foundation

/// One on-screen window as the window server lists it.
struct ScreenWindow: Equatable {
    var number: Int
    var pid: Int32
    var owner: String
    var name: String
    var layer: Int
    var alpha: Double
    var bounds: CGRect
}

/// The window server's list as values. An entry without a number, an owner pid or bounds is
/// dropped: nothing can be said about it.
func screenWindows(from list: [[String: Any]]) -> [ScreenWindow] {
    list.compactMap { entry in
        guard let number = (entry["kCGWindowNumber"] as? NSNumber)?.intValue,
              let pid = (entry["kCGWindowOwnerPID"] as? NSNumber)?.int32Value,
              let box = entry["kCGWindowBounds"] as? [String: Any],
              let x = (box["X"] as? NSNumber)?.doubleValue, let y = (box["Y"] as? NSNumber)?.doubleValue,
              let width = (box["Width"] as? NSNumber)?.doubleValue, let height = (box["Height"] as? NSNumber)?.doubleValue
        else { return nil }
        return ScreenWindow(
            number: number, pid: pid,
            owner: entry["kCGWindowOwnerName"] as? String ?? "",
            name: entry["kCGWindowName"] as? String ?? "",
            layer: (entry["kCGWindowLayer"] as? NSNumber)?.intValue ?? 0,
            alpha: (entry["kCGWindowAlpha"] as? NSNumber)?.doubleValue ?? 1,
            bounds: CGRect(x: x, y: y, width: width, height: height)
        )
    }
}

/// Processes whose windows are the desktop's own furniture and never something to handle.
let furnitureOwners: Set<String> = ["Dock", "Window Server", "Control Center", "WindowManager", "Wallpaper"]

/// The window levels of the menu bar and of the items in it.
private let menuBarLayers: Set<Int> = [24, 25]

/// The window level menus open at (kCGPopUpMenuWindowLevel).
let menuLayer = 101

/// The most foreign windows a snapshot names: a desktop with more than this over one app is
/// not something a list helps with.
let attentionWindowLimit = 16

private func isOnScreen(_ window: ScreenWindow, _ screen: CGRect) -> Bool {
    guard let bounds = finiteRect(window.bounds) else { return false }
    return window.alpha > 0 && rectShows(bounds.intersection(screen))
}

/// The windows of other processes that are in front of a window of `target` and overlap it,
/// front to back as listed. `windows` is the window server's list, front to back; `own` is the
/// agent's pid, whose windows (it has none, but a helper might) are never reported.
func windowsOver(target: Int32, own: Int32, in windows: [ScreenWindow], screen: CGRect) -> [ScreenWindow] {
    var over: [ScreenWindow] = []
    for (index, window) in windows.enumerated() {
        if over.count >= attentionWindowLimit { break }
        if window.pid == target || window.pid == own { continue }
        if furnitureOwners.contains(window.owner) || menuBarLayers.contains(window.layer) { continue }
        if !isOnScreen(window, screen) { continue }
        let covers = windows[(index + 1)...].contains { behind in
            behind.pid == target && isOnScreen(behind, screen)
                && rectShows(behind.bounds.intersection(window.bounds).intersection(screen))
        }
        if covers { over.append(window) }
    }
    return over
}

/// The target's open menus: its windows at the menu level, front to back.
func openMenus(of target: Int32, in windows: [ScreenWindow], screen: CGRect) -> [ScreenWindow] {
    windows.filter { $0.pid == target && $0.layer == menuLayer && isOnScreen($0, screen) }
}

/// The kind of attention a window of the target app is by its subrole, nil for an ordinary one.
func attentionKind(windowSubrole: String) -> String? {
    switch windowSubrole {
    case "AXDialog": return "dialog"
    case "AXSystemDialog": return "alert"
    default: return nil
    }
}

/// The kind of attention an element inside a window is by its role, nil for most.
func attentionKind(role: String) -> String? {
    switch role {
    case "AXSheet": return "sheet"
    case "AXPopover": return "popover"
    default: return nil
    }
}
