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

    @AppStorage(ThemePreference.key, store: AppDefaults.shared) private var preference: ThemePreference = .system
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

/// `GREENROOM_SNAPSHOT=<dir>` (debug builds only, `SnapshotMode`): once the run list is
/// in, open `GREENROOM_SNAPSHOT_RUN` (a run id; a run the daemon does not list fails the
/// snapshot) if given, on `GREENROOM_SNAPSHOT_PANE` (`screen` or `steps`) if given, press
/// `GREENROOM_SNAPSHOT_KEYS` if given (comma-separated: `g`, `?`, `cmd+k`, `esc`,
/// `enter`, `tab`, `shift+enter`, `text:words`), then write the window to
/// `<dir>/<GREENROOM_SNAPSHOT_NAME or "window">-<theme>.png` in each theme of
/// `GREENROOM_SNAPSHOT_THEMES` (default all four) and quit. The window draws itself
/// (`cacheDisplay`), so it needs no screen-recording permission.
///
/// A snapshot is a camera, never a seat. The app runs `.prohibited` and its window can
/// never be key or main, so a person typing elsewhere is never typing into it; the key
/// router swallows every real event; the daemon client refuses every write; settings go
/// to a scratch domain thrown away at the end. Anything that breaks one of these, or a
/// requested run that is not there, fails the snapshot with a message and exit status 1.
@MainActor
enum SnapshotHook {
    static func runIfAsked(store: RunStore, router: KeyRouter) {
        let environment = ProcessInfo.processInfo.environment
        guard SnapshotMode.isActive, let directory = environment["GREENROOM_SNAPSHOT"] else { return }
        let name = environment["GREENROOM_SNAPSHOT_NAME"] ?? "window"
        let themes = (environment["GREENROOM_SNAPSHOT_THEMES"] ?? "dark,light,dark-hc,light-hc")
            .split(separator: ",").compactMap { ThemePreference(rawValue: String($0)) }
        let run = environment["GREENROOM_SNAPSHOT_RUN"].flatMap { $0.isEmpty ? nil : $0 }
        let pane = environment["GREENROOM_SNAPSHOT_PANE"].flatMap(StagePane.init(rawValue:))
        let keys = (environment["GREENROOM_SNAPSHOT_KEYS"] ?? "").split(separator: ",").map(String.init)
        Task {
            // The scratch domain (`AppDefaults`): the person's own settings are never written.
            let defaults = AppDefaults.shared
            if let pane { defaults.set(pane.rawValue, forKey: "stagePane") }
            // So the first selection is already the requested run, not the newest or the
            // one that needs the person.
            if let run { defaults.set(run, forKey: "selectedRunId") }

            var window: NSWindow?
            for _ in 0..<100 {
                window = NSApp.windows.first(where: AppDelegate.isRunWindow)
                if window != nil { break }
                try? await Task.sleep(for: .milliseconds(50))
            }
            guard let window else { return fail(store, "the run window never opened") }
            seal(window)
            do { try assertNotAPerson(window) } catch { return fail(store, "\(error)") }

            for _ in 0..<150 where store.reachable != true { try? await Task.sleep(for: .milliseconds(100)) }
            if let breach = open(run, in: store) { return fail(store, "\(breach)") }
            // `GREENROOM_SNAPSHOT_SIZE=820x560`: the window's content at that size.
            let size = (environment["GREENROOM_SNAPSHOT_SIZE"] ?? "").split(separator: "x").compactMap { Double($0) }
            if size.count == 2 { window.setContentSize(CGSize(width: size[0], height: size[1])) }
            try? await Task.sleep(for: .seconds(4))
            // Real key events handed to the router as typed ones would be; the window
            // stays unkeyed, so the person's own keys go where they were going.
            if !keys.isEmpty {
                for token in keys {
                    let presses = token.hasPrefix("text:") ? token.dropFirst(5).map { String($0) } : [token]
                    for press in presses {
                        if let event = keyEvent(press, in: window) { router.inject(event, into: window) }
                        try? await Task.sleep(for: .milliseconds(250))
                    }
                }
                try? await Task.sleep(for: .seconds(1))
            }
            try? FileManager.default.createDirectory(atPath: directory, withIntermediateDirectories: true)
            for theme in themes {
                defaults.set(theme.rawValue, forKey: ThemePreference.key)
                try? await Task.sleep(for: .seconds(1.5))
                // A key may open another run (`j`), which is the snapshot's own doing; only a
                // selection that drifted with no keys pressed is a fault.
                if let run, keys.isEmpty, store.selectedRunId != run {
                    return fail(store, "the window left run \(run) for \(store.selectedRunId ?? "no run")")
                }
                do { try assertNotAPerson(window) } catch { return fail(store, "\(error)") }
                // A window under others gets no display pass of its own, so the new
                // theme would reach the picture one capture late: draw it now.
                window.contentView?.layoutSubtreeIfNeeded()
                window.displayIfNeeded()
                try? await Task.sleep(for: .milliseconds(300))
                let file = URL(fileURLWithPath: directory).appendingPathComponent("\(name)-\(theme.rawValue).png")
                write(window, to: file)
            }
            if environment["GREENROOM_SNAPSHOT_MENU"] != nil { printMenus() }
            finish(store)
            NSApp.terminate(nil)
        }
    }

    struct Breach: Error, CustomStringConvertible {
        var description: String
    }

    /// Opens the requested run once the list is in, or says why the snapshot cannot go
    /// on: the list never answered, or it does not hold the run. Another run is never
    /// shown in its place.
    static func open(_ run: String?, in store: RunStore) -> Breach? {
        guard store.reachable == true else {
            return Breach(description: "greenroom at \(store.client.baseURL.absoluteString) did not answer the run list")
        }
        guard let run else { return nil }
        guard store.runs.contains(where: { $0.runId == run }) else {
            return Breach(description: "run \(run) is not in greenroom's list of \(store.runs.count) runs; no other run is shown in its place")
        }
        store.selectedRunId = run
        return nil
    }

    /// The window can never be key or main: its class answers no, for every window of it
    /// in this process (a snapshot process has only this one). Its frame and state are not
    /// saved, so the person's window comes back where they left it.
    static func seal(_ window: NSWindow) {
        let never: @convention(block) (AnyObject) -> Bool = { _ in false }
        let implementation = imp_implementationWithBlock(never)
        let windowClass: AnyClass = window.classForCoder
        for selector in [#selector(getter: NSWindow.canBecomeKey), #selector(getter: NSWindow.canBecomeMain)] {
            guard let method = class_getInstanceMethod(windowClass, selector) else { continue }
            class_replaceMethod(windowClass, selector, implementation, method_getTypeEncoding(method))
        }
        window.setFrameAutosaveName("")
        window.isRestorable = false
    }

    /// Throws when the app is active or its window key or main: then a person's keys could
    /// land in it.
    static func assertNotAPerson(_ window: NSWindow) throws {
        if window.canBecomeKey || window.canBecomeMain {
            throw Breach(description: "the snapshot window could become key")
        }
        if NSApp.isActive || window.isKeyWindow || window.isMainWindow || NSApp.keyWindow != nil {
            throw Breach(description: "the snapshot app became active or its window key; a person's keys could reach it")
        }
    }

    /// Nothing held goes out, and the scratch settings go.
    private static func finish(_ store: RunStore) {
        store.undoVerdictChoice()
        AppDefaults.discardScratch()
    }

    /// Stops the snapshot loudly: a message on stderr and exit status 1.
    private static func fail(_ store: RunStore, _ reason: String) {
        finish(store)
        FileHandle.standardError.write(Data("snapshot failed: \(reason)\n".utf8))
        exit(1)
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
    static func keyEvent(_ token: String, in window: NSWindow) -> NSEvent? {
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
        return NSEvent.keyEvent(
            with: .keyDown, location: .zero, modifierFlags: flags,
            timestamp: ProcessInfo.processInfo.systemUptime, windowNumber: window.windowNumber, context: nil,
            characters: characters, charactersIgnoringModifiers: characters.lowercased(), isARepeat: false, keyCode: code
        )
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
