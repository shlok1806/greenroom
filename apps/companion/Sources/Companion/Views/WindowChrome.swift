import AppKit
import SwiftUI

// The window's own chrome (ADR 0004, 0008): no system title bar, toolbar or sidebar. The
// content fills the window in the theme's ground; the top bar, on the faint olive
// `chromeTint`, holds the traffic lights, the `greenroom█` wordmark and what the open
// view puts there.

/// Puts the theme into the environment, following the person's choice (View > Theme),
/// the Mac's appearance and Increase Contrast, and sets the window's defaults: the
/// reading face, secondary text in dim, quiet buttons and brand checkboxes.
struct ThemedRoot<Content: View>: View {
    @ViewBuilder var content: () -> Content

    @AppStorage(ThemePreference.key) private var preference: ThemePreference = .system
    @Environment(\.colorScheme) private var colorScheme
    @Environment(\.colorSchemeContrast) private var contrast

    var body: some View {
        let id = preference.resolve(systemIsDark: colorScheme == .dark, increasedContrast: contrast == .increased)
        let theme = DesignData.shared.theme(id)
        content()
            .readingStyle()
            .ground(.background)
            .tint(theme.brand)
            .buttonStyle(.quiet)
            .toggleStyle(.check)
            .background(theme.background)
            .background(WindowConfigurator(background: theme.backgroundRGB.nsColor))
            .environment(\.theme, theme)
            .preferredColorScheme(preference.colorScheme)
    }
}

// MARK: - The NSWindow

/// Sets up the hosting window: a transparent full-size title bar with no title, the
/// theme's ground, and the traffic lights centred in the top bar.
private struct WindowConfigurator: NSViewRepresentable {
    let background: NSColor

    func makeNSView(context: Context) -> ConfiguratorView { ConfiguratorView() }

    func updateNSView(_ view: ConfiguratorView, context: Context) {
        view.background = background
        view.apply()
    }

    final class ConfiguratorView: NSView {
        var background: NSColor = .windowBackgroundColor
        private var observers: [NSObjectProtocol] = []

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            for observer in observers { NotificationCenter.default.removeObserver(observer) }
            observers = []
            guard let window else { return }
            // AppKit lays the title bar out again on these and puts the buttons back.
            let names: [Notification.Name] = [
                NSWindow.didResizeNotification, NSWindow.didEndLiveResizeNotification,
                NSWindow.didBecomeKeyNotification, NSWindow.didResignKeyNotification,
                NSWindow.didBecomeMainNotification, NSWindow.didResignMainNotification,
                NSWindow.didExitFullScreenNotification, NSWindow.didChangeScreenNotification,
                // After every event the window handles: AppKit may put a button back on any
                // pass. Only a moved button is set, so this costs a comparison.
                NSWindow.didUpdateNotification,
            ]
            observers = names.map { name in
                NotificationCenter.default.addObserver(forName: name, object: window, queue: .main) { [weak self] _ in
                    MainActor.assumeIsolated { self?.placeTrafficLights() }
                }
            }
            for kind in [NSWindow.ButtonType.closeButton, .miniaturizeButton, .zoomButton] {
                guard let button = window.standardWindowButton(kind) else { continue }
                button.postsFrameChangedNotifications = true
                observers.append(NotificationCenter.default.addObserver(
                    forName: NSView.frameDidChangeNotification, object: button, queue: .main
                ) { [weak self] _ in
                    MainActor.assumeIsolated { self?.placeTrafficLights() }
                })
            }
            apply()
            // Once more after SwiftUI has finished setting the window up.
            DispatchQueue.main.async { [weak self] in self?.placeTrafficLights() }
        }

        func apply() {
            guard let window else { return }
            window.titleVisibility = .hidden
            window.titlebarAppearsTransparent = true
            window.titlebarSeparatorStyle = .none
            window.styleMask.insert(.fullSizeContentView)
            window.backgroundColor = background
            placeTrafficLights()
        }

        /// Close, minimise and zoom, centred on the top bar's height.
        func placeTrafficLights() {
            guard let window, !window.styleMask.contains(.fullScreen) else { return }
            let kinds: [NSWindow.ButtonType] = [.closeButton, .miniaturizeButton, .zoomButton]
            for (index, kind) in kinds.enumerated() {
                guard let button = window.standardWindowButton(kind), let container = button.superview else { continue }
                let middle = container.isFlipped
                    ? TopBar.height / 2
                    : container.bounds.height - TopBar.height / 2
                let y = (middle - button.frame.height / 2).rounded()
                let origin = NSPoint(x: TopBar.trafficLightInset + CGFloat(index) * TopBar.trafficLightPitch, y: y)
                if button.frame.origin != origin { button.setFrameOrigin(origin) }
            }
        }
    }
}

// MARK: - Top bar

/// What the open view puts in the top bar: after the wordmark, and at the right edge.
struct TopBarItems {
    let leading: AnyView?
    let trailing: AnyView?
}

struct TopBarItemsKey: PreferenceKey {
    static var defaultValue: TopBarItems? { nil }

    static func reduce(value: inout TopBarItems?, nextValue: () -> TopBarItems?) {
        value = nextValue() ?? value
    }
}

extension View {
    /// Puts views in the window's top bar while this view is on screen: `leading` after
    /// the wordmark, `trailing` at the right edge.
    func topBar<Leading: View, Trailing: View>(
        @ViewBuilder leading: () -> Leading = { EmptyView() },
        @ViewBuilder trailing: () -> Trailing
    ) -> some View {
        preference(key: TopBarItemsKey.self, value: TopBarItems(leading: AnyView(leading()), trailing: AnyView(trailing())))
    }
}

/// The top bar: the traffic lights' room, the wordmark, then what the open view put
/// there. On `chromeTint`, with a hairline under it.
struct TopBar: View {
    let items: TopBarItems?

    static let height: CGFloat = 44
    /// The traffic lights' spacing, as AppKit draws them, and the first one's inset.
    static let trafficLightPitch: CGFloat = 20
    static let trafficLightInset: CGFloat = 16
    /// Where the wordmark starts: past the three lights and a gap.
    static let wordmarkInset: CGFloat = 88

    @Environment(\.theme) private var theme

    var body: some View {
        HStack(spacing: Space.l) {
            Wordmark()
            if let leading = items?.leading {
                leading
            }
            Spacer(minLength: Space.s)
            if let trailing = items?.trailing {
                trailing
            }
        }
        .padding(.leading, Self.wordmarkInset)
        .padding(.trailing, Space.l)
        .frame(height: Self.height)
        .frame(maxWidth: .infinity)
        .ground(.chrome)
        .background(theme.chromeTint)
        .overlay(alignment: .bottom) { Hairline() }
    }
}

/// `greenroom█`: the product's name and its mark, the block cursor, in the brand.
struct Wordmark: View {
    var size: CGFloat = TypeScale.mono

    @Environment(\.theme) private var theme

    var body: some View {
        HStack(spacing: 1) {
            Text("greenroom")
                .font(Typeface.monoBold.font(size: size))
                .foregroundStyle(theme.foreground)
            Rectangle()
                .fill(theme.brand)
                .frame(width: (size * 0.6).rounded(), height: (size * 1.2).rounded())
        }
        .fixedSize()
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("greenroom")
    }
}

#if DEBUG
// MARK: - Design screenshots from the running app

/// `GREENROOM_SNAPSHOT=<dir>` (debug builds only): once the runs are in, open
/// `GREENROOM_SNAPSHOT_RUN` (a run id) if given, on `GREENROOM_SNAPSHOT_PANE` (`screen`
/// or `steps`) if given, press `GREENROOM_SNAPSHOT_KEYS` if given (comma-separated:
/// `g`, `?`, `cmd+k`, `esc`, `enter`, `tab`, `shift+enter`, `text:words`), then write the window to
/// `<dir>/<GREENROOM_SNAPSHOT_NAME or "window">-<theme>.png` in each theme of
/// `GREENROOM_SNAPSHOT_THEMES` (default all four), put the person's theme back and quit.
/// The window draws itself (`cacheDisplay`), so it needs no screen-recording permission.
@MainActor
enum SnapshotHook {
    static func runIfAsked(store: RunStore) {
        let environment = ProcessInfo.processInfo.environment
        guard let directory = environment["GREENROOM_SNAPSHOT"], !directory.isEmpty else { return }
        let name = environment["GREENROOM_SNAPSHOT_NAME"] ?? "window"
        let themes = (environment["GREENROOM_SNAPSHOT_THEMES"] ?? "dark,light,dark-hc,light-hc")
            .split(separator: ",").compactMap { ThemePreference(rawValue: String($0)) }
        let run = environment["GREENROOM_SNAPSHOT_RUN"]
        let pane = environment["GREENROOM_SNAPSHOT_PANE"].flatMap(StagePane.init(rawValue:))
        Task {
            let defaults = UserDefaults.standard
            let saved = defaults.string(forKey: ThemePreference.key)
            let savedPane = defaults.string(forKey: "stagePane")
            if let pane { defaults.set(pane.rawValue, forKey: "stagePane") }
            for _ in 0..<150 where store.runs.isEmpty { try? await Task.sleep(for: .milliseconds(100)) }
            if let run, !run.isEmpty { store.selectedRunId = run }
            // `GREENROOM_SNAPSHOT_SIZE=820x560`: the window's content at that size.
            let size = (environment["GREENROOM_SNAPSHOT_SIZE"] ?? "").split(separator: "x").compactMap { Double($0) }
            if size.count == 2, let window = NSApp.windows.first(where: AppDelegate.isRunWindow) {
                window.setContentSize(CGSize(width: size[0], height: size[1]))
            }
            try? await Task.sleep(for: .seconds(4))
            // Real key events through the app's own queue, so the router sees them as typed.
            let keys = (environment["GREENROOM_SNAPSHOT_KEYS"] ?? "").split(separator: ",").map(String.init)
            if let window = NSApp.windows.first(where: AppDelegate.isRunWindow), !keys.isEmpty {
                window.makeKeyAndOrderFront(nil)
                for token in keys {
                    let presses = token.hasPrefix("text:") ? token.dropFirst(5).map { String($0) } : [token]
                    for press in presses {
                        post(press, to: window)
                        try? await Task.sleep(for: .milliseconds(250))
                    }
                }
                try? await Task.sleep(for: .seconds(1))
            }
            try? FileManager.default.createDirectory(atPath: directory, withIntermediateDirectories: true)
            for theme in themes {
                defaults.set(theme.rawValue, forKey: ThemePreference.key)
                try? await Task.sleep(for: .seconds(1.5))
                guard let window = NSApp.windows.first(where: AppDelegate.isRunWindow) else { continue }
                let file = URL(fileURLWithPath: directory).appendingPathComponent("\(name)-\(theme.rawValue).png")
                write(window, to: file)
            }
            if environment["GREENROOM_SNAPSHOT_MENU"] != nil { printMenus() }
            if let saved { defaults.set(saved, forKey: ThemePreference.key) } else { defaults.removeObject(forKey: ThemePreference.key) }
            // Keys may have moved the stage's focus (`g s`); the person's own comes back.
            if let savedPane { defaults.set(savedPane, forKey: "stagePane") } else { defaults.removeObject(forKey: "stagePane") }
            NSApp.terminate(nil)
        }
    }

    /// The View and Run menus as AppKit holds them: title, key equivalent, enabled.
    private static func printMenus() {
        for top in NSApp.mainMenu?.items ?? [] where ["View", "Run"].contains(top.title) {
            // As when a person opens it: SwiftUI brings the items up to date then.
            if let menu = top.submenu {
                menu.delegate?.menuNeedsUpdate?(menu)
                menu.delegate?.menuWillOpen?(menu)
                menu.update()
            }
            for item in top.submenu?.items ?? [] where !item.isSeparatorItem {
                let mask = item.keyEquivalentModifierMask
                let mods = (mask.contains(.control) ? "⌃" : "") + (mask.contains(.option) ? "⌥" : "")
                    + (mask.contains(.shift) ? "⇧" : "") + (mask.contains(.command) ? "⌘" : "")
                let key = item.keyEquivalent.isEmpty ? "" : " [\(mods)\(item.keyEquivalent == "\u{8}" ? "⌫" : item.keyEquivalent.uppercased())]"
                print("menu \(top.title) > \(item.title)\(key)\(item.isEnabled ? "" : " (disabled)")")
            }
        }
    }

    /// A key press, as the keyboard would make it: `cmd+k`, `shift+enter`, `esc`, `j`.
    private static func post(_ token: String, to window: NSWindow) {
        var parts = token.split(separator: "+").map(String.init)
        let name = parts.popLast() ?? token
        var flags: NSEvent.ModifierFlags = []
        for modifier in parts {
            switch modifier {
            case "cmd": flags.insert(.command)
            case "shift": flags.insert(.shift)
            case "ctrl": flags.insert(.control)
            case "opt": flags.insert(.option)
            default: break
            }
        }
        let named: [String: (UInt16, String)] = [
            "enter": (36, "\r"), "esc": (53, "\u{1b}"), "tab": (48, "\t"), "space": (49, " "),
            "up": (126, "\u{F700}"), "down": (125, "\u{F701}"), "left": (123, "\u{F702}"),
            "right": (124, "\u{F703}"), "delete": (51, "\u{7f}"), "comma": (43, ","),
        ]
        // A typed character carries itself (KeyChord reads it from there); 50 is a key
        // code no rule names.
        let (code, characters) = named[name] ?? (50, name)
        guard let event = NSEvent.keyEvent(
            with: .keyDown, location: .zero, modifierFlags: flags,
            timestamp: ProcessInfo.processInfo.systemUptime, windowNumber: window.windowNumber, context: nil,
            characters: characters, charactersIgnoringModifiers: characters.lowercased(), isARepeat: false, keyCode: code
        ) else { return }
        NSApp.postEvent(event, atStart: false)
    }

    /// The whole window, title bar and traffic lights included, as the window draws it.
    static func write(_ window: NSWindow, to file: URL) {
        guard let view = window.contentView?.superview ?? window.contentView else { return }
        let bounds = view.bounds
        // At the screen's scale (2x on Retina), so type can be judged at its real sharpness.
        let scale = window.backingScaleFactor
        guard let bitmap = NSBitmapImageRep(
            bitmapDataPlanes: nil, pixelsWide: Int(bounds.width * scale), pixelsHigh: Int(bounds.height * scale),
            bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
            colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0
        ) else { return }
        bitmap.size = bounds.size
        (window.effectiveAppearance).performAsCurrentDrawingAppearance {
            view.cacheDisplay(in: bounds, to: bitmap)
        }
        try? bitmap.representation(using: .png, properties: [:])?.write(to: file)
        print("snapshot \(file.path) \(Int(bounds.width))x\(Int(bounds.height))")
    }
}
#endif
