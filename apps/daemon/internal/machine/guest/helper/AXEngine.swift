// Access to accessibility for the toolkit's ops (daemon ADR 0006; docs/21 section 4.5's
// AXEngine): one element's attributes in one message, the application a request targets, a
// process's identity, and a window's number. Everything here talks to other processes, so
// everything here is bounded by AX's messaging timeout.

import AppKit
import ApplicationServices
import Foundation

/// How long one AX message to an app may take while walking it (daemon ADR 0005 point 5).
let walkMessagingTimeout: Float = 1.0

/// How long one AX message may take on an action's path.
let actionMessagingTimeout: Float = 0.5

/// An AXUIElement as a dictionary key: hashed with CFHash, compared with CFEqual, which is how
/// AX says two references are one element.
struct AXHandle: Hashable {
    let element: AXUIElement

    init(_ element: AXUIElement) {
        self.element = element
    }

    static func == (a: AXHandle, b: AXHandle) -> Bool {
        CFEqual(a.element, b.element)
    }

    func hash(into hasher: inout Hasher) {
        hasher.combine(CFHash(element))
    }

    var pid: pid_t {
        var pid: pid_t = 0
        return AXUIElementGetPid(element, &pid) == .success ? pid : 0
    }
}

// MARK: - One element in one message

/// The attributes a node is built from, in the order `readElement` asks for them. One
/// AXUIElementCopyMultipleAttributeValues call reads them all, so a node costs one message to
/// its app and not twenty.
private let batchAttributes: [String] = [
    kAXRoleAttribute, kAXSubroleAttribute, kAXTitleAttribute, kAXDescriptionAttribute,
    kAXValueAttribute, kAXHelpAttribute, kAXIdentifierAttribute, kAXPlaceholderValueAttribute,
    kAXEnabledAttribute, kAXSelectedAttribute, kAXFocusedAttribute, kAXExpandedAttribute,
    kAXElementBusyAttribute, kAXPositionAttribute, kAXSizeAttribute, kAXChildrenAttribute,
    kAXParentAttribute, kAXOrientationAttribute, kAXVerticalScrollBarAttribute,
    kAXHorizontalScrollBarAttribute,
]

/// What one element said about itself.
struct AXRead {
    /// `.success`, or why nothing could be read: `.invalidUIElement` for an element that is
    /// gone, `.cannotComplete` for an app that did not answer in time.
    var error: AXError = .success
    var role = ""
    var subrole = ""
    var title: String?
    var description: String?
    /// The value when it is text or a number; nil for a value of any other kind.
    var value: String?
    var help: String?
    var identifier: String?
    var placeholder: String?
    var enabled: Bool?
    var selected: Bool?
    var focused: Bool?
    var expanded: Bool?
    var busy: Bool?
    /// Global points, top-left origin: the space CGEvent posts in.
    var frame: CGRect?
    var children: [AXUIElement] = []
    var parent: AXUIElement?
    var orientation: String?
    var verticalBar: AXUIElement?
    var horizontalBar: AXUIElement?

    var ok: Bool { error == .success }

    /// The element's name as a node and a fingerprint carry it.
    var name: String { nodeName(title: title, description: description, placeholder: placeholder) }

    var secret: Bool { isSecure(role: role, subrole: subrole) }
}

private func axString(_ value: AnyObject) -> String? {
    if let text = value as? String { return text }
    if let text = value as? NSAttributedString { return text.string }
    return nil
}

private func axText(_ value: AnyObject) -> String? {
    if let text = axString(value) { return text }
    // A slider's 0.5, a checkbox's 1. CFBoolean is a number here too, which is what the toggle
    // roles rely on.
    if let number = value as? NSNumber { return number.stringValue }
    if let url = value as? URL { return url.absoluteString }
    return nil
}

private func axFlag(_ value: AnyObject) -> Bool? {
    guard CFGetTypeID(value) == CFBooleanGetTypeID() || value is NSNumber else { return nil }
    return (value as? NSNumber)?.boolValue
}

private func axElement(_ value: AnyObject) -> AXUIElement? {
    guard CFGetTypeID(value) == AXUIElementGetTypeID() else { return nil }
    // swiftlint:disable:next force_cast
    return (value as! AXUIElement)
}

private func axPoint(_ value: AnyObject) -> CGPoint? {
    guard CFGetTypeID(value) == AXValueGetTypeID() else { return nil }
    var point = CGPoint.zero
    // swiftlint:disable:next force_cast
    return AXValueGetValue(value as! AXValue, .cgPoint, &point) ? point : nil
}

private func axSize(_ value: AnyObject) -> CGSize? {
    guard CFGetTypeID(value) == AXValueGetTypeID() else { return nil }
    var size = CGSize.zero
    // swiftlint:disable:next force_cast
    return AXValueGetValue(value as! AXValue, .cgSize, &size) ? size : nil
}

/// Reads an element. An attribute the element does not have comes back as an error value in its
/// slot, which every reader above turns into nil.
func readElement(_ element: AXUIElement) -> AXRead {
    var values: CFArray?
    let error = AXUIElementCopyMultipleAttributeValues(
        element, batchAttributes as CFArray, AXCopyMultipleAttributeOptions(rawValue: 0), &values)
    guard error == .success else { return AXRead(error: error) }
    guard let list = values as? [AnyObject], list.count == batchAttributes.count else {
        return AXRead(error: .failure)
    }
    var read = AXRead()
    read.role = axString(list[0]) ?? ""
    read.subrole = axString(list[1]) ?? ""
    read.title = axString(list[2])
    read.description = axString(list[3])
    read.value = axText(list[4])
    read.help = axString(list[5])
    read.identifier = axString(list[6])
    read.placeholder = axString(list[7])
    read.enabled = axFlag(list[8])
    read.selected = axFlag(list[9])
    read.focused = axFlag(list[10])
    read.expanded = axFlag(list[11])
    read.busy = axFlag(list[12])
    if let origin = axPoint(list[13]), let size = axSize(list[14]) {
        read.frame = finiteRect(CGRect(origin: origin, size: size))
    }
    if let children = list[15] as? [AnyObject] {
        read.children = children.compactMap(axElement)
    }
    read.parent = axElement(list[16])
    read.orientation = axString(list[17])
    read.verticalBar = axElement(list[18])
    read.horizontalBar = axElement(list[19])
    return read
}

/// One attribute with its error, for the few reads that are not part of the batch.
func readAttribute(_ element: AXUIElement, _ name: String) -> (value: AnyObject?, error: AXError) {
    var value: CFTypeRef?
    let error = AXUIElementCopyAttributeValue(element, name as CFString, &value)
    return (error == .success ? value : nil, error)
}

func elementAttribute(_ element: AXUIElement, _ name: String) -> AXUIElement? {
    readAttribute(element, name).value.flatMap(axElement)
}

func elementsAttribute(_ element: AXUIElement, _ name: String) -> [AXUIElement] {
    (readAttribute(element, name).value as? [AnyObject])?.compactMap(axElement) ?? []
}

func textAttribute(_ element: AXUIElement, _ name: String) -> String? {
    readAttribute(element, name).value.flatMap(axText)
}

func flagAttribute(_ element: AXUIElement, _ name: String) -> Bool? {
    readAttribute(element, name).value.flatMap(axFlag)
}

/// A scroll bar's value, 0 to 1.
func numberAttribute(_ element: AXUIElement, _ name: String) -> Double? {
    (readAttribute(element, name).value as? NSNumber)?.doubleValue
}

/// Whether an attribute can be set, false when the element will not say.
func isSettable(_ element: AXUIElement, _ name: String) -> Bool {
    var settable: DarwinBoolean = false
    return AXUIElementIsAttributeSettable(element, name as CFString, &settable) == .success && settable.boolValue
}

// MARK: - Processes

/// When a process started, as `seconds.microseconds` since 1970: with its pid, the process's
/// identity (a recycled pid has another start). Nil when there is no such process, or it is one
/// this user may not ask about.
func processStart(_ pid: pid_t) -> String? {
    guard pid > 0 else { return nil }
    var info = proc_bsdinfo()
    let size = Int32(MemoryLayout<proc_bsdinfo>.size)
    guard proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, size) == size else { return nil }
    return String(format: "%llu.%06llu", UInt64(info.pbi_start_tvsec), UInt64(info.pbi_start_tvusec))
}

/// Whether the process a fingerprint names is still the one running under its pid.
func processLives(pid: pid_t, started: String) -> Bool {
    if let now = processStart(pid) { return started.isEmpty || now == started }
    // A process of another user: its start cannot be read, only that it exists.
    guard started.isEmpty, pid > 0 else { return false }
    return kill(pid, 0) == 0 || errno == EPERM
}

/// The application a request reads.
struct AppTarget {
    let pid: pid_t
    /// Its start time, empty when it cannot be read.
    let started: String
    let name: String
    let bundleId: String
    let root: AXUIElement

    /// The wire's `app` object.
    var info: [String: Any] {
        var info: [String: Any] = ["pid": Int(pid)]
        if !name.isEmpty { info["name"] = name }
        if !bundleId.isEmpty { info["bundleId"] = bundleId }
        if !started.isEmpty { info["started"] = started }
        return info
    }
}

private let primedLock = NSLock()
private var primed: Set<String> = []

/// The application running under `pid`, ready to be read, or nil when nothing runs there.
func appTarget(pid: pid_t) -> AppTarget? {
    guard pid > 0 else { return nil }
    let running = NSRunningApplication(processIdentifier: pid)
    let started = processStart(pid) ?? ""
    if running == nil, started.isEmpty, !processLives(pid: pid, started: "") { return nil }
    let root = AXUIElementCreateApplication(pid)
    AXUIElementSetMessagingTimeout(root, walkMessagingTimeout)

    // Chromium and Electron build their tree only when asked, and asking is one message: once
    // per process is enough. Never AXEnhancedUserInterface, which changes how AppKit apps lay
    // out and animate (docs/13 3.5).
    let key = "\(pid):\(started)"
    primedLock.lock()
    let first = primed.insert(key).inserted
    if primed.count > 512 { primed = [key] }
    primedLock.unlock()
    if first {
        AXUIElementSetAttributeValue(root, "AXManualAccessibility" as CFString, kCFBooleanTrue)
    }
    return AppTarget(
        pid: pid, started: started,
        name: running?.localizedName ?? "",
        bundleId: running?.bundleIdentifier ?? "",
        root: root)
}

/// The application a request names (by name or bundle id, whatever its activation policy), else
/// the one in front (`targetApp`). `not_found` names the applications there are.
func appTarget(named name: String?) throws -> AppTarget {
    let running: NSRunningApplication
    do {
        running = try targetApp(meaningful(name))
    } catch let failure as Failure {
        throw AgentFailure("not_found", failure.message)
    }
    guard let app = appTarget(pid: running.processIdentifier) else {
        throw AgentFailure("not_found", "\(running.localizedName ?? "the application") quit while it was being read; take a new machine_snapshot")
    }
    return app
}

/// The frontmost application as the wire's `frontmost` object, empty when there is none.
func frontmostInfo() -> [String: Any] {
    guard let running = try? targetApp(nil) else { return [:] }
    var info: [String: Any] = ["pid": Int(running.processIdentifier)]
    if let name = running.localizedName, !name.isEmpty { info["name"] = name }
    if let bundle = running.bundleIdentifier, !bundle.isEmpty { info["bundleId"] = bundle }
    return info
}

/// A process's name for a sentence, from the running applications or the process table.
func processName(_ pid: pid_t) -> String {
    if let name = NSRunningApplication(processIdentifier: pid)?.localizedName, !name.isEmpty { return name }
    var buffer = [CChar](repeating: 0, count: 256)
    let length = proc_name(pid, &buffer, UInt32(buffer.count))
    return length > 0 ? String(cString: buffer) : ""
}

// MARK: - Windows

/// An app's windows, the focused one first, so a walk that runs out of budget has read the
/// window in use. `error` is the root's answer: `.cannotComplete` is an app that is not
/// responding.
func appWindows(_ app: AppTarget) -> (windows: [AXUIElement], focused: AXUIElement?, error: AXError) {
    let (value, error) = readAttribute(app.root, kAXWindowsAttribute)
    if error == .cannotComplete || error == .invalidUIElement { return ([], nil, error) }
    var windows = (value as? [AnyObject])?.compactMap(axElement) ?? []
    let focused = elementAttribute(app.root, kAXFocusedWindowAttribute)
    if let focused {
        if let index = windows.firstIndex(where: { CFEqual($0, focused) }) {
            windows.insert(windows.remove(at: index), at: 0)
        } else {
            // A window AX lists as focused but not among the windows (a panel): it is still
            // what the person is looking at.
            windows.insert(focused, at: 0)
        }
    }
    return (windows, focused, .success)
}

/// The window server's on-screen windows, front to back.
func screenWindowList() -> [ScreenWindow] {
    let list = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID)
    return screenWindows(from: list as? [[String: Any]] ?? [])
}

private typealias WindowNumberFunction = @convention(c) (AXUIElement, UnsafeMutablePointer<CGWindowID>) -> AXError

/// `_AXUIElementGetWindow`, which says which window server window an AX window is. It is not
/// in the headers, so it is looked up when the agent starts using it rather than linked: on a
/// system without it the helper still compiles and runs, and windows are told apart by their
/// frames instead.
private let windowNumberFunction: WindowNumberFunction? = {
    guard let symbol = dlsym(UnsafeMutableRawPointer(bitPattern: -2), "_AXUIElementGetWindow") else { return nil }
    return unsafeBitCast(symbol, to: WindowNumberFunction.self)
}()

/// The window server's number of an AX window, 0 when it cannot be told. `listed` is the window
/// server's list, for the fallback: the one window of the process with the same frame.
func windowNumber(of window: AXUIElement, frame: CGRect?, pid: pid_t, among listed: () -> [ScreenWindow]) -> Int {
    if let function = windowNumberFunction {
        var number: CGWindowID = 0
        if function(window, &number) == .success, number != 0 { return Int(number) }
    }
    guard let frame else { return 0 }
    let same = listed().filter { candidate in
        candidate.pid == pid && abs(candidate.bounds.minX - frame.minX) <= 1 && abs(candidate.bounds.minY - frame.minY) <= 1
            && abs(candidate.bounds.width - frame.width) <= 1 && abs(candidate.bounds.height - frame.height) <= 1
    }
    return same.count == 1 ? same[0].number : 0
}

/// The element the window server's hit test finds at a point of the screen, in any app.
func elementAt(_ point: CGPoint) -> AXUIElement? {
    var hit: AXUIElement?
    let error = AXUIElementCopyElementAtPosition(systemWide, Float(point.x), Float(point.y), &hit)
    return error == .success ? hit : nil
}

/// The system-wide element. Its messaging timeout is the process's default, set when the agent
/// starts (AgentChannel.swift).
let systemWide: AXUIElement = AXUIElementCreateSystemWide()

/// The element with the keyboard focus, in whatever app has it.
func focusedElement() -> AXUIElement? {
    elementAttribute(systemWide, kAXFocusedUIElementAttribute)
}
