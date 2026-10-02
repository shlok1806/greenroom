import Foundation

private let finder = AppCandidate(pid: 400, name: "Finder", bundleId: "com.apple.finder", policy: .regular)
private let terminal = AppCandidate(pid: 410, name: "Terminal", bundleId: "com.apple.Terminal", policy: .regular)
private let axServer = AppCandidate(pid: 300, name: "AccessibilityUIServer", bundleId: "com.apple.AccessibilityUIServer", policy: .accessory)
private let menuApp = AppCandidate(pid: 1665, name: "Vorssaint", bundleId: "com.vorssaint.utils", policy: .accessory)
private let daemon = AppCandidate(pid: 90, name: "universalaccessd", bundleId: "com.apple.universalaccessd", policy: .prohibited)

func testANameOrBundleIDMatchesAnAppOfAnyPolicy() {
    let apps = [finder, axServer, menuApp, terminal, daemon]
    expectEqual(matchApp("Vorssaint", in: apps), menuApp, "an accessory app by name (#223)")
    expectEqual(matchApp("com.vorssaint.utils", in: apps), menuApp, "and by bundle id")
    expectEqual(matchApp("VORSSAINT", in: apps), menuApp, "case does not matter")
    expectEqual(matchApp("universalaccessd", in: apps), daemon, "an exact name of a background process")
    expectEqual(matchApp("finder", in: apps), finder, "a regular app as before")
    expectEqual(matchApp("Safari", in: apps), nil, "nothing runs by that name")
    expectEqual(matchApp("", in: apps), nil, "an empty name names nothing")
}

func testAPartialNameNeverLandsOnABackgroundProcess() {
    let apps = [daemon, menuApp, terminal]
    expectEqual(matchApp("Vors", in: apps), menuApp, "an accessory app by part of its name")
    expectEqual(matchApp("universal", in: apps), nil, "a background process needs its exact name")
    expectEqual(matchApp("term", in: apps), terminal)
}

func testARegularAppWinsANameItShares() {
    let helper = AppCandidate(pid: 20, name: "Safari Web Content", bundleId: "com.apple.WebKit.WebContent", policy: .accessory)
    let safari = AppCandidate(pid: 30, name: "Safari", bundleId: "com.apple.Safari", policy: .regular)
    let notesAgent = AppCandidate(pid: 40, name: "Notes", bundleId: "com.example.notes-agent", policy: .accessory)
    let notes = AppCandidate(pid: 50, name: "Notes", bundleId: "com.apple.Notes", policy: .regular)
    expectEqual(matchApp("safari", in: [helper, safari]), safari, "exact before contains")
    expectEqual(matchApp("saf", in: [helper, safari]), safari, "regular before accessory among contains")
    expectEqual(matchApp("notes", in: [notesAgent, notes]), notes, "regular before accessory among exact names")
}

func testWithNoNameTheFocusedAppCountsOnlyWhenARegularAppOrTheMenuBarOwner() {
    // #209: AccessibilityUIServer held AX focus while Finder was in front.
    expectEqual(defaultApp(focused: axServer, menuBarOwner: finder, frontmost: finder), finder, "an agent's focus is passed over")
    expectEqual(defaultApp(focused: axServer, menuBarOwner: nil, frontmost: finder), finder, "then the frontmost")
    expectEqual(defaultApp(focused: terminal, menuBarOwner: finder, frontmost: finder), terminal, "a regular focused app is fresher than NSWorkspace")
    expectEqual(defaultApp(focused: menuApp, menuBarOwner: menuApp, frontmost: finder), menuApp, "an accessory app that owns the menu bar")
    expectEqual(defaultApp(focused: nil, menuBarOwner: finder, frontmost: terminal), finder, "no focus: the menu bar's owner")
    expectEqual(defaultApp(focused: nil, menuBarOwner: nil, frontmost: terminal), terminal, "else the frontmost")
    expectEqual(defaultApp(focused: nil, menuBarOwner: nil, frontmost: nil), nil, "else none")
}

func testTheRunningListMarksAccessoryApps() {
    let dup = AppCandidate(pid: 1, name: "Vorssaint", bundleId: "com.vorssaint.utils", policy: .accessory)
    expectEqual(runningAppsList([menuApp, finder, daemon, dup, terminal]),
                "Finder, Terminal, Vorssaint (accessory)", "regular first, each name once, no background process")
    let shared = AppCandidate(pid: 2, name: "Finder", bundleId: "x", policy: .accessory)
    expectEqual(runningAppsList([shared, finder]), "Finder", "a name a regular app has is not marked")
    let many = (0 ..< maxListedAccessoryApps + 3).map {
        AppCandidate(pid: Int32(1000 + $0), name: "Agent \($0)", bundleId: "", policy: .accessory)
    }
    let list = runningAppsList([finder] + many)
    expect(list.hasPrefix("Finder, Agent 0 (accessory)"), list)
    expect(list.hasSuffix("Agent \(maxListedAccessoryApps - 1) (accessory), and 3 more accessory apps"), list)
    // The app under test launched last, after dozens of agents, still makes the list: it has a window.
    var windowed = menuApp
    windowed.hasWindow = true
    let withWindow = runningAppsList([finder] + many + [windowed])
    expect(withWindow.hasPrefix("Finder, Vorssaint (accessory), Agent 0 (accessory)"), withWindow)
    expect(withWindow.hasSuffix("and 4 more accessory apps"), withWindow)
}
