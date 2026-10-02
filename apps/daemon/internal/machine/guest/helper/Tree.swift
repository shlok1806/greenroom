// The accessibility tree of --ui-base64 (ADR 0012).

import AppKit
import ApplicationServices
import Foundation

// MARK: - UI tree (ADR 0012)

struct UIRequest: Decodable {
    var app: String?
    var limit: Int?
}

/// One attribute of an element, or nil when it has none or will not say.
func attribute(_ element: AXUIElement, _ name: String) -> CFTypeRef? {
    var value: CFTypeRef?
    guard AXUIElementCopyAttributeValue(element, name as CFString, &value) == .success else { return nil }
    return value
}

func text(_ element: AXUIElement, _ name: String) -> String? {
    guard let value = attribute(element, name) else { return nil }
    let out: String
    if let s = value as? String {
        out = s
    } else if let n = value as? NSNumber {
        out = n.stringValue
    } else {
        return nil
    }
    let trimmed = out.trimmingCharacters(in: .whitespacesAndNewlines)
    if trimmed.isEmpty { return nil }
    // A text view's whole document is not a label.
    return trimmed.count > 120 ? String(trimmed.prefix(120)) + "..." : trimmed
}

func flag(_ element: AXUIElement, _ name: String) -> Bool? {
    (attribute(element, name) as? NSNumber)?.boolValue
}

/// The element's frame in global points, top-left origin: the space CGEvent posts in.
func frame(_ element: AXUIElement) -> CGRect? {
    guard let p = attribute(element, kAXPositionAttribute), let s = attribute(element, kAXSizeAttribute),
          CFGetTypeID(p) == AXValueGetTypeID(), CFGetTypeID(s) == AXValueGetTypeID()
    else { return nil }
    var origin = CGPoint.zero
    var size = CGSize.zero
    // swiftlint:disable:next force_cast
    guard AXValueGetValue(p as! AXValue, .cgPoint, &origin), AXValueGetValue(s as! AXValue, .cgSize, &size) else { return nil }
    return CGRect(origin: origin, size: size)
}

func children(_ element: AXUIElement) -> [AXUIElement] {
    (attribute(element, kAXChildrenAttribute) as? [AXUIElement]) ?? []
}

/// Roles that are only layout. They are walked through, and listed only when
/// they carry text of their own.
let containerRoles: Set<String> = [
    "AXGroup", "AXScrollArea", "AXSplitGroup", "AXLayoutArea", "AXLayoutItem", "AXUnknown",
    "AXSplitter", "AXMatte", "AXGrowArea", "AXRow", "AXColumn", "AXCell",
]

let toggleRoles: Set<String> = ["AXRadioButton", "AXCheckBox", "AXSwitch", "AXToggle"]

/// A running application as `logic/AppChoice.swift` weighs it.
func candidate(_ app: NSRunningApplication) -> AppCandidate {
    let policy: AppPolicy
    switch app.activationPolicy {
    case .regular: policy = .regular
    case .accessory: policy = .accessory
    default: policy = .prohibited
    }
    return AppCandidate(pid: app.processIdentifier, name: app.localizedName ?? "", bundleId: app.bundleIdentifier ?? "", policy: policy)
}

/// Finds the application to read: the one a name or bundle id names, whatever its activation
/// policy (a menu bar app is `LSUIElement`, #223), else the one a person sees in front
/// (`defaultApp`, #209).
func targetApp(_ name: String?) throws -> NSRunningApplication {
    let running = NSWorkspace.shared.runningApplications.filter { !$0.isTerminated }
    let candidates = running.map(candidate)
    if let name, !name.isEmpty {
        if let match = matchApp(name, in: candidates),
           let app = running.first(where: { $0.processIdentifier == match.pid }) {
            return app
        }
        // Owners and layers need no permission (Desktop.swift).
        let windows = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] ?? []
        let owners = Set(windows.filter { $0[kCGWindowLayer as String] as? Int == 0 }.compactMap { $0[kCGWindowOwnerPID as String] as? Int32 })
        let listed = candidates.map { app in
            var app = app
            app.hasWindow = owners.contains(app.pid)
            return app
        }
        throw Failure("no running application named \(name); running: \(runningAppsList(listed))")
    }
    // The system-wide element knows focus now; NSWorkspace can lag a launch.
    var focusedApp: NSRunningApplication?
    var focused: CFTypeRef?
    if AXUIElementCopyAttributeValue(AXUIElementCreateSystemWide(), kAXFocusedApplicationAttribute as CFString, &focused) == .success,
       let focused, CFGetTypeID(focused) == AXUIElementGetTypeID() {
        var pid: pid_t = 0
        // swiftlint:disable:next force_cast
        if AXUIElementGetPid(focused as! AXUIElement, &pid) == .success {
            focusedApp = NSRunningApplication(processIdentifier: pid)
        }
    }
    let menuBarOwner = NSWorkspace.shared.menuBarOwningApplication
    let frontmost = NSWorkspace.shared.frontmostApplication
    guard let chosen = defaultApp(focused: focusedApp.map(candidate), menuBarOwner: menuBarOwner.map(candidate),
                                  frontmost: frontmost.map(candidate))
    else { throw Failure("no frontmost application") }
    for app in [focusedApp, menuBarOwner, frontmost] {
        if let app, app.processIdentifier == chosen.pid { return app }
    }
    throw Failure("no frontmost application")
}

/// Walks the target's windows depth first and lists what a person could see
/// or use: every element with a frame on the screen, minus bare layout. The
/// menu bar is left out; it is the same for every app and eats the budget.
func uiTree(_ request: UIRequest) throws -> [String: Any] {
    guard AXIsProcessTrusted() else {
        throw Failure("this machine has not granted Accessibility to the guest agent, so the UI tree cannot be read")
    }
    let app = try targetApp(request.app)
    let root = AXUIElementCreateApplication(app.processIdentifier)
    AXUIElementSetMessagingTimeout(root, 1.0)
    // Chromium and Electron build their tree only when asked.
    AXUIElementSetAttributeValue(root, "AXManualAccessibility" as CFString, kCFBooleanTrue)

    let limit = max(1, min(request.limit ?? 250, 1000))
    let screen = bounds
    var elements: [[String: Any]] = []
    var visited = 0
    // Why the walk stopped early, if it did: the element limit, or the caps on
    // elements visited and depth that keep a pathological tree from hanging it.
    var truncatedBy: String? = nil

    func visit(_ element: AXUIElement, depth: Int, clip: CGRect) {
        if elements.count >= limit {
            truncatedBy = truncatedBy ?? "limit"
            return
        }
        if visited >= 5000 {
            truncatedBy = truncatedBy ?? "visited"
            return
        }
        if depth > 40 {
            truncatedBy = truncatedBy ?? "depth"
            return
        }
        visited += 1
        let role = text(element, kAXRoleAttribute) ?? "AXUnknown"
        if role == "AXMenuBar" { return }
        var nextClip = clip
        var listed = false
        if let f = frame(element) {
            let visible = f.intersection(clip)
            // Hidden, collapsed or scrolled out of view: nothing to click, nor
            // anything below it.
            if f.width < 1 || f.height < 1 || visible.isNull || visible.width < 1 || visible.height < 1 {
                if role != "AXApplication" { return }
            } else {
                if role == "AXScrollArea" || role == "AXWindow" { nextClip = visible }
                let title = text(element, kAXTitleAttribute)
                let label = text(element, kAXDescriptionAttribute)
                let value = text(element, kAXValueAttribute)
                let help = text(element, kAXHelpAttribute)
                let identifier = text(element, kAXIdentifierAttribute)
                let hasText = title != nil || label != nil || value != nil || identifier != nil
                if !containerRoles.contains(role) || hasText {
                    var item: [String: Any] = [
                        "role": role,
                        "depth": depth,
                        "frame": ["x": visible.minX, "y": visible.minY, "w": visible.width, "h": visible.height],
                    ]
                    if let s = text(element, kAXSubroleAttribute), s != "AXUnknown" { item["subrole"] = s }
                    if let title { item["title"] = title }
                    if let label, label != title { item["label"] = label }
                    // A radio button's or checkbox's value is 0 or 1: say which.
                    if toggleRoles.contains(role), let value, value == "0" || value == "1" {
                        if value == "1" { item["selected"] = true }
                    } else if let value, value != title {
                        item["value"] = value
                    }
                    if let help, title == nil, label == nil { item["help"] = help }
                    if let identifier, !identifier.hasPrefix("_NS:") { item["identifier"] = identifier }
                    if flag(element, kAXEnabledAttribute) == false { item["enabled"] = false }
                    if flag(element, kAXSelectedAttribute) == true { item["selected"] = true }
                    if flag(element, kAXFocusedAttribute) == true { item["focused"] = true }
                    elements.append(item)
                    listed = true
                }
            }
        }
        for child in children(element) {
            visit(child, depth: listed ? depth + 1 : depth, clip: nextClip)
        }
    }

    // Windows first, focused one first, so a cap cuts the background.
    var windows = (attribute(root, kAXWindowsAttribute) as? [AXUIElement]) ?? []
    if let focused = attribute(root, kAXFocusedWindowAttribute), CFGetTypeID(focused) == AXUIElementGetTypeID() {
        // swiftlint:disable:next force_cast
        let f = focused as! AXUIElement
        if let i = windows.firstIndex(where: { CFEqual($0, f) }) {
            windows.insert(windows.remove(at: i), at: 0)
        }
    }
    if windows.isEmpty { windows = children(root) }
    for window in windows {
        visit(window, depth: 0, clip: screen)
    }

    return [
        "app": ["name": app.localizedName ?? "", "bundleId": app.bundleIdentifier ?? "", "pid": Int(app.processIdentifier)],
        // Regular apps only: the verifier judges an app gone when it leaves this list (ADR
        // 0028), so an accessory app read by name is never judged gone, and the guest's many
        // agents stay out of every outline. A not-found error lists accessory apps instead.
        "apps": NSWorkspace.shared.runningApplications.filter { $0.activationPolicy == .regular }.compactMap(\.localizedName),
        "screen": ["width": Int(screen.width), "height": Int(screen.height)],
        "elements": elements,
        "truncated": truncatedBy != nil,
        "truncatedBy": truncatedBy ?? "",
    ]
}
