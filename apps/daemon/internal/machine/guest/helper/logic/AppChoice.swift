// Which running application a request means (issues #209 and #223): a name or bundle id
// matches any running app whatever its activation policy, and with no name the read goes to the
// app a person sees in front. Pure, so the host tests pin both; `targetApp` in Tree.swift feeds
// it NSWorkspace's apps.

import Foundation

/// NSApplication.ActivationPolicy without AppKit: regular apps have a Dock icon and a menu
/// bar, accessory apps (`LSUIElement`: menu bar apps, agents) have neither but can show
/// windows, prohibited ones are background processes with no UI.
enum AppPolicy: Equatable {
    case regular, accessory, prohibited

    /// Regular first: a name both kinds share means the app with the Dock icon.
    var rank: Int {
        switch self {
        case .regular: return 0
        case .accessory: return 1
        case .prohibited: return 2
        }
    }
}

struct AppCandidate: Equatable {
    var pid: Int32
    var name: String
    var bundleId: String
    var policy: AppPolicy
    /// It has a window on the screen, so there is something to read. Only the not-found list
    /// uses it.
    var hasWindow = false
}

/// The apps regular first, then accessory, then prohibited, each kind in the order given.
private func byPolicy(_ apps: [AppCandidate]) -> [AppCandidate] {
    apps.enumerated().sorted { a, b in
        a.element.policy.rank != b.element.policy.rank ? a.element.policy.rank < b.element.policy.rank : a.offset < b.offset
    }.map(\.element)
}

/// The app `wanted` names: an exact name or bundle id of any policy, else the first name that
/// contains it among regular and accessory apps (a background process has nothing to read, and
/// a loose match should not land on one). Regular apps win a tie, then the order given.
func matchApp(_ wanted: String, in apps: [AppCandidate]) -> AppCandidate? {
    let key = wanted.lowercased()
    if key.isEmpty { return nil }
    let ordered = byPolicy(apps)
    if let exact = ordered.first(where: { $0.name.lowercased() == key || $0.bundleId.lowercased() == key }) {
        return exact
    }
    return ordered.first { $0.policy != .prohibited && $0.name.lowercased().contains(key) }
}

/// The app to read when none is named (#209). The focused app is the freshest answer
/// (NSWorkspace can lag a launch), but an accessory or agent process can hold accessibility
/// focus while a regular app is in front (AccessibilityUIServer did on greenroom-lean-a), so
/// it counts only when it is regular or owns the menu bar. Else the menu bar's owner, the app a
/// person sees as active, then NSWorkspace's frontmost.
func defaultApp(focused: AppCandidate?, menuBarOwner: AppCandidate?, frontmost: AppCandidate?) -> AppCandidate? {
    if let focused, focused.policy == .regular || focused.pid == menuBarOwner?.pid {
        return focused
    }
    return menuBarOwner ?? frontmost
}

/// The most accessory apps a "running:" list names; a guest runs few, a desktop dozens.
let maxListedAccessoryApps = 20

/// The apps a "no running application" error names: regular ones, then accessory ones marked
/// "(accessory)", those with a window on the screen first (a guest runs dozens of windowless
/// agents, and the app under test must not fall past the cap), each name once, background
/// processes left out.
func runningAppsList(_ apps: [AppCandidate]) -> String {
    var seen = Set<String>()
    var regular: [String] = []
    var accessory: [String] = []
    let windowsFirst = byPolicy(apps).enumerated().sorted { a, b in
        a.element.policy != b.element.policy || a.element.hasWindow == b.element.hasWindow ? a.offset < b.offset : a.element.hasWindow
    }.map(\.element)
    for app in windowsFirst where !app.name.isEmpty && app.policy != .prohibited {
        guard seen.insert(app.name).inserted else { continue }
        if app.policy == .regular {
            regular.append(app.name)
        } else {
            accessory.append(app.name)
        }
    }
    var names = regular + accessory.prefix(maxListedAccessoryApps).map { "\($0) (accessory)" }
    if accessory.count > maxListedAccessoryApps {
        names.append("and \(accessory.count - maxListedAccessoryApps) more accessory apps")
    }
    return names.joined(separator: ", ")
}
