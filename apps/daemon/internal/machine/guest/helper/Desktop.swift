// What is on the screen and what runs, for the dialog check (desktopcheck.go, ADR 0018).

import AppKit
import CoreGraphics
import Foundation

/// Every on-screen window and every regular running application. Owners and
/// layers need no permission; titles need screen capture, which the guest
/// agent holds and this binary inherits.
func desktop() -> [String: Any] {
    let info = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] ?? []
    let windows: [[String: Any]] = info.map { w in
        let b = w[kCGWindowBounds as String] as? [String: Any] ?? [:]
        return [
            "owner": w[kCGWindowOwnerName as String] as? String ?? "",
            "pid": w[kCGWindowOwnerPID as String] as? Int ?? 0,
            "name": w[kCGWindowName as String] as? String ?? "",
            "layer": w[kCGWindowLayer as String] as? Int ?? 0,
            "alpha": w[kCGWindowAlpha as String] as? Double ?? 1,
            "x": b["X"] as? Double ?? 0, "y": b["Y"] as? Double ?? 0,
            "width": b["Width"] as? Double ?? 0, "height": b["Height"] as? Double ?? 0,
        ]
    }
    // Regular apps only: the boot check compares them with an allowlist of bundle ids, and the
    // guest runs dozens of accessory agents. `targetApp` reads accessory apps by name.
    let apps: [[String: Any]] = NSWorkspace.shared.runningApplications
        .filter { $0.activationPolicy == .regular }
        .map { ["name": $0.localizedName ?? "", "bundleId": $0.bundleIdentifier ?? "", "pid": Int($0.processIdentifier)] }
    return ["windows": windows, "apps": apps]
}
