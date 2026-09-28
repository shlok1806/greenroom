import CoreGraphics
import Foundation

private let screen = CGRect(x: 0, y: 0, width: 1024, height: 768)

private func window(_ number: Int, _ owner: String, pid: Int32, name: String = "", layer: Int = 0, alpha: Double = 1,
                    _ bounds: CGRect) -> ScreenWindow {
    ScreenWindow(number: number, pid: pid, owner: owner, name: name, layer: layer, alpha: alpha, bounds: bounds)
}

// A recorded desktop, front to back: TipSplit (pid 812) with a permission prompt and a
// notification over it, and Finder behind it.
private let tipSplit = CGRect(x: 272, y: 204, width: 480, height: 360)
private let desktop: [ScreenWindow] = [
    window(90, "Window Server", pid: 140, name: "StatusIndicator", layer: 2_147_483_630, CGRect(x: 0, y: 0, width: 1024, height: 768)),
    window(81, "Control Center", pid: 410, name: "Clock", layer: 25, CGRect(x: 900, y: 0, width: 120, height: 24)),
    window(80, "Window Server", pid: 140, name: "Menubar", layer: 24, CGRect(x: 0, y: 0, width: 1024, height: 24)),
    window(70, "NotificationCenter", pid: 500, name: "Notification Center", layer: 23, CGRect(x: 660, y: 40, width: 360, height: 80)),
    window(60, "UserNotificationCenter", pid: 610, name: "", layer: 8, CGRect(x: 380, y: 250, width: 260, height: 300)),
    window(55, "Overlay", pid: 620, name: "invisible", layer: 3, alpha: 0, CGRect(x: 0, y: 0, width: 1024, height: 768)),
    window(51, "Dock", pid: 300, name: "Dock", layer: 20, CGRect(x: 0, y: 700, width: 1024, height: 68)),
    window(41, "TipSplit", pid: 812, name: "TipSplit", tipSplit),
    window(30, "Finder", pid: 330, name: "Documents", CGRect(x: 100, y: 100, width: 700, height: 500)),
    window(20, "Dock", pid: 300, name: "Desktop Picture", layer: -2_147_483_624, CGRect(x: 0, y: 0, width: 1024, height: 768)),
]

func testWindowsOverTheTargetAreNamedFrontToBack() {
    let over = windowsOver(target: 812, own: 999, in: desktop, screen: screen)
    // The prompt overlaps TipSplit. The notification is in front but beside it, Finder overlaps
    // but is behind it, and the desktop's furniture never counts.
    expectEqual(over.map(\.number), [60])
    expectEqual(over.first?.owner, "UserNotificationCenter")
}

func testANotificationOverTheTargetCounts() {
    var moved = desktop
    moved[7].bounds = CGRect(x: 650, y: 60, width: 370, height: 300) // TipSplit, now under the banner
    expectEqual(windowsOver(target: 812, own: 999, in: moved, screen: screen).map(\.number), [70])
}

func testTheFrontmostAppsWindowsCoverABackgroundTarget() {
    // Finder is the target: the banner, the prompt and TipSplit are all in front of it and over it.
    expectEqual(windowsOver(target: 330, own: 999, in: desktop, screen: screen).map(\.number), [70, 60, 41])
}

func testTransparentOffscreenAndOwnWindowsAreSkipped() {
    let list = [
        window(9, "greenroom-input", pid: 999, layer: 3, CGRect(x: 0, y: 0, width: 1024, height: 768)),
        window(8, "Ghost", pid: 700, alpha: 0, CGRect(x: 0, y: 0, width: 1024, height: 768)),
        window(7, "Parked", pid: 701, CGRect(x: -40000, y: 40000, width: 1280, height: 800)),
        window(6, "Sliver", pid: 702, CGRect(x: 0, y: 0, width: 272.5, height: 768)),
        window(5, "WindowManager", pid: 703, CGRect(x: 0, y: 0, width: 1024, height: 768)),
        window(4, "Faint", pid: 704, alpha: 0.01, CGRect(x: 300, y: 300, width: 100, height: 100)),
        window(3, "TipSplit", pid: 812, tipSplit),
    ]
    // Half a point of overlap is none. A window that is nearly transparent still takes the
    // clicks meant for what is under it, so it counts.
    expectEqual(windowsOver(target: 812, own: 999, in: list, screen: screen).map(\.number), [4])
    expect(windowsOver(target: 812, own: 999, in: [], screen: screen).isEmpty, "an empty list")
    expect(windowsOver(target: 4242, own: 999, in: list, screen: screen).isEmpty, "a target with no window has nothing over it")
}

func testTheListOfWindowsOverATargetIsBounded() {
    var crowd = (0..<40).map { window(100 + $0, "App \($0)", pid: Int32(2000 + $0), tipSplit) }
    crowd.append(window(3, "TipSplit", pid: 812, tipSplit))
    expectEqual(windowsOver(target: 812, own: 999, in: crowd, screen: screen).count, attentionWindowLimit)
}

func testOpenMenusAreTheTargetsWindowsAtTheMenuLevel() {
    let list = [
        window(12, "TipSplit", pid: 812, layer: 101, CGRect(x: 300, y: 24, width: 200, height: 300)),
        window(11, "Finder", pid: 330, layer: 101, CGRect(x: 10, y: 24, width: 200, height: 300)),
        window(3, "TipSplit", pid: 812, tipSplit),
    ]
    expectEqual(openMenus(of: 812, in: list, screen: screen).map(\.number), [12])
}

func testTheWindowServersListBecomesValues() {
    let list: [[String: Any]] = [
        [
            "kCGWindowNumber": 41, "kCGWindowOwnerPID": 812, "kCGWindowOwnerName": "TipSplit", "kCGWindowName": "TipSplit",
            "kCGWindowLayer": 0, "kCGWindowAlpha": 1.0,
            "kCGWindowBounds": ["X": 272, "Y": 204, "Width": 480, "Height": 360],
        ],
        // No name and no alpha: a window of a process that does not share them.
        ["kCGWindowNumber": 60, "kCGWindowOwnerPID": 610, "kCGWindowBounds": ["X": 0, "Y": 0, "Width": 10, "Height": 10]],
        ["kCGWindowNumber": 61, "kCGWindowOwnerPID": 611], // no bounds
        ["kCGWindowOwnerPID": 612, "kCGWindowBounds": ["X": 0, "Y": 0, "Width": 10, "Height": 10]], // no number
    ]
    let windows = screenWindows(from: list)
    expectEqual(windows.count, 2)
    expectEqual(windows.first, window(41, "TipSplit", pid: 812, name: "TipSplit", tipSplit))
    expectEqual(windows.last, window(60, "", pid: 610, CGRect(x: 0, y: 0, width: 10, height: 10)))
}

func testDialogsSheetsAndPopoversAreAttention() {
    expectEqual(attentionKind(windowSubrole: "AXDialog"), "dialog")
    expectEqual(attentionKind(windowSubrole: "AXSystemDialog"), "alert")
    expect(attentionKind(windowSubrole: "AXStandardWindow") == nil, "an ordinary window")
    expectEqual(attentionKind(role: "AXSheet"), "sheet")
    expectEqual(attentionKind(role: "AXPopover"), "popover")
    expect(attentionKind(role: "AXButton") == nil, "a control")
}
