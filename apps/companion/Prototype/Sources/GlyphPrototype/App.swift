import AppKit
import SwiftUI

// PROTOTYPE entry point: a plain AppKit window with custom chrome hosting the SwiftUI grid.

@main
@MainActor
enum GlyphPrototypeMain {
    static func main() {
        PrototypeFonts.register()
        let app = NSApplication.shared
        let delegate = PrototypeAppDelegate()
        app.delegate = delegate
        app.setActivationPolicy(.regular)
        app.run()
        _ = delegate
    }
}

@MainActor
final class PrototypeAppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    let model = PrototypeModel()
    var window: NSWindow!
    private var monitor: Any?
    private var themeObservation: Task<Void, Never>?

    func applicationDidFinishLaunching(_ notification: Notification) {
        let env = ProcessInfo.processInfo.environment
        let cols = Int(env["PROTO_COLS"] ?? "") ?? 212
        let rows = Int(env["PROTO_ROWS"] ?? "") ?? 56
        let size = G.size(cols, rows)
        window = NSWindow(
            contentRect: NSRect(origin: .zero, size: size),
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered, defer: false
        )
        window.title = "GlyphPrototype (throwaway)"
        window.titleVisibility = .hidden
        window.titlebarAppearsTransparent = true
        window.isMovableByWindowBackground = false
        window.contentResizeIncrements = NSSize(width: G.cellW, height: G.cellH)
        window.contentMinSize = G.size(72, 24)
        window.delegate = self
        window.contentView = NSHostingView(rootView: RootView().environment(model))
        window.center()
        if let x = env["PROTO_X"].flatMap(Double.init), let y = env["PROTO_Y"].flatMap(Double.init) {
            window.setFrameTopLeftPoint(NSPoint(x: x, y: y))
        }
        applyTheme()
        window.makeKeyAndOrderFront(nil)
        placeTrafficLights()
        if env["PROTO_SHOTS"] == nil { NSApp.activate() }
        scheduleScript(env)
        model.start()
        installKeys()
        buildMenu()
        themeObservation = Task { @MainActor [weak self] in
            var last: ThemeID?
            while !Task.isCancelled {
                guard let self else { return }
                if self.model.theme != last { last = self.model.theme; self.applyTheme() }
                try? await Task.sleep(for: .milliseconds(100))
            }
        }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }

    func windowDidResize(_ notification: Notification) { placeTrafficLights() }
    func windowDidBecomeKey(_ notification: Notification) { placeTrafficLights() }

    private func applyTheme() {
        window.backgroundColor = NSColor(model.palette.background)
        window.appearance = NSAppearance(named: model.theme.isDark ? .darkAqua : .aqua)
        placeTrafficLights()
    }

    /// Centres the traffic lights on grid row 0, so they sit on the chrome line.
    private func placeTrafficLights() {
        let kinds: [NSWindow.ButtonType] = [.closeButton, .miniaturizeButton, .zoomButton]
        for (i, kind) in kinds.enumerated() {
            guard let b = window.standardWindowButton(kind), let sup = b.superview else { continue }
            let y = sup.frame.height - G.cellH / 2 - b.frame.height / 2 - 1
            b.setFrameOrigin(NSPoint(x: G.cellW + CGFloat(i) * 20, y: y.rounded()))
        }
    }

    private func buildMenu() {
        let main = NSMenu()
        let appItem = NSMenuItem()
        main.addItem(appItem)
        let appMenu = NSMenu()
        appMenu.addItem(withTitle: "Quit GlyphPrototype", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        appItem.submenu = appMenu
        NSApp.mainMenu = main
    }

    // MARK: scripted shots (for design review; the window renders itself, no screen capture)

    /// `PROTO_ACTIONS="1.5:scene.fail,3:theme.light"` runs registry actions by id at times;
    /// `PROTO_KEYS="2:⌘k,2.2:t"` presses keys; `PROTO_SHOTS="2:/tmp/a.png,4:/tmp/b.png"`
    /// writes the window to PNG; the app quits after the last shot.
    private func scheduleScript(_ env: [String: String]) {
        func pairs(_ key: String) -> [(Double, String)] {
            (env[key] ?? "").split(separator: ",").compactMap { item in
                let parts = item.split(separator: ":", maxSplits: 1)
                guard parts.count == 2, let t = Double(parts[0]) else { return nil }
                return (t, String(parts[1]))
            }
        }
        for (t, id) in pairs("PROTO_ACTIONS") {
            after(t) { [weak self] in
                guard let self, let a = Registry.all.first(where: { $0.id == id }) else { return }
                a.perform(self.model)
            }
        }
        for (t, key) in pairs("PROTO_KEYS") {
            after(t) { [weak self] in
                guard let self else { return }
                let named = key.count > 1 && !key.hasPrefix("⌘") || key.hasPrefix("⌘")
                _ = self.handleKey(key, chars: named ? "" : key)
            }
        }
        let shots = pairs("PROTO_SHOTS")
        for (t, path) in shots {
            after(t) { [weak self] in self?.snapshot(to: path) }
        }
        if let last = shots.map(\.0).max() {
            after(last + 0.3) { NSApp.terminate(nil) }
        }
    }

    private func after(_ t: Double, _ body: @escaping @MainActor () -> Void) {
        Task { @MainActor in
            try? await Task.sleep(for: .seconds(t))
            body()
        }
    }

    private func snapshot(to path: String) {
        guard let view = window.contentView?.superview else { return }
        let bounds = view.bounds
        guard let rep = view.bitmapImageRepForCachingDisplay(in: bounds) else { return }
        view.cacheDisplay(in: bounds, to: rep)
        try? rep.representation(using: .png, properties: [:])?.write(to: URL(fileURLWithPath: path))
    }

    // MARK: keys

    private func installKeys() {
        monitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
            guard let self else { return event }
            let handled = MainActor.assumeIsolated { self.handle(event) }
            return handled ? nil : event
        }
    }

    private func handle(_ e: NSEvent) -> Bool {
        let chars = e.modifierFlags.contains(.command) ? "" : (e.characters ?? "")
        return handleKey(KeyName.of(e), chars: chars)
    }

    /// One key path for real events and scripted keys. `chars` is the typed text (empty
    /// for command chords and named keys).
    func handleKey(_ key: String, chars: String) -> Bool {
        let m = model
        let printable = !chars.isEmpty && chars.unicodeScalars.allSatisfy { $0.value >= 32 && $0.value != 127 && $0.value < 0xF700 }

        // Driving is a hard mode: every key, Cmd-Q too, goes to the guest.
        if m.driving {
            m.sendToMachine(key: key, chars: chars)
            return true
        }
        if m.confirmDestroy {
            if key == "⏎" { m.powerDown() } else { m.confirmDestroy = false }
            return true
        }
        if m.paletteOpen {
            let items = Registry.paletteItems(m)
            switch key {
            case "esc", "⌘k": m.paletteOpen = false
            case "↓", "tab": m.paletteIndex = min(max(0, items.count - 1), m.paletteIndex + 1)
            case "↑", "⇧tab": m.paletteIndex = max(0, m.paletteIndex - 1)
            case "⏎":
                if items.indices.contains(m.paletteIndex) { m.runPalette(items[m.paletteIndex]) }
            case "⌫":
                if !m.paletteQuery.isEmpty { m.paletteQuery.removeLast(); m.paletteIndex = 0 }
            default:
                if printable { m.paletteQuery += chars; m.paletteIndex = 0 }
            }
            return true
        }
        if m.composerActive {
            if Registry.handle(key, m) { return true }
            if key == "⌫" {
                if !m.composer.isEmpty { m.composer.removeLast() }
            } else if key == "⌘k" {
                m.composerActive = false
                _ = Registry.handle(key, m)
            } else if printable {
                m.composer += chars
            }
            return true
        }
        if key == "⌘q" { return false }
        if m.pendingG {
            m.pendingG = false
            _ = Registry.handle("g " + key, m)
            return true
        }
        if key == "g" { m.pendingG = true; return true }
        if Registry.handle(key, m) { return true }
        return !key.hasPrefix("⌘")
    }
}

extension PrototypeModel {
    /// While driving, keys go to the guest. The prototype shows them landing.
    func sendToMachine(key: String, chars: String) {
        let shown: String
        switch key {
        case "space": shown = "␣"
        case "⏎", "esc", "tab", "⌫", "↑", "↓", "←", "→": shown = "‹\(key)›"
        default: shown = key.count == 1 ? chars : "‹\(key)›"
        }
        drivenKeys = String((drivenKeys + shown).suffix(40))
        if clickMarks, !reduceMotion { ripples.append(Ripple(point: pointer, start: now, driving: true)) }
    }
}
