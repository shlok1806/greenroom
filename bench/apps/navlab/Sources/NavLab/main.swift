import AppKit
import SwiftUI

// NavLab: a window of navigation hazards for the verifier bench (bench/README.md, the navigation
// tier; docs/21 section 8). Each scene reproduces one way a verifier lost its way on a real
// desktop: a control drawn under a floating list, nested scroll areas, a label the window cuts
// off, a field that drops keys, a late result, a button enabled late, a thin splitter, dialogs
// and menus, and a second window over the first.
//
// Mutant patches edit these files, so keep them plain: one statement a line, no clever helpers.
// The app is AppKit at the top (so it decides when and where its windows appear) and SwiftUI
// inside them, with AppKit views where SwiftUI cannot do the job.

/// The scenes, in sidebar order. The raw value is the sidebar button's label and the scene's title.
enum LabScene: String, CaseIterable, Identifiable {
    case overlay = "Overlay"
    case nestedScroll = "Nested scroll"
    case longLabel = "Long label"
    case amount = "Amount field"
    case delayed = "Delayed result"
    case prepare = "Prepare and continue"
    case splitter = "Splitter"
    case dialogs = "Dialogs and menus"
    case secondWindow = "Second window"

    var id: String { rawValue }
}

/// State the windows share: the Inspector writes the project name the main window shows, and
/// the Tools menu marks the lab reviewed.
@MainActor
final class LabModel: ObservableObject {
    @Published var scene = LabScene.overlay
    @Published var projectName = "Untitled"
    @Published var reviewed = false
}

@main
@MainActor
final class NavLabApp: NSObject, NSApplicationDelegate {
    let model = LabModel()
    var splash: NSWindow?
    var mainWindow: NSWindow?
    var inspector: NSPanel?

    static func main() {
        let app = NSApplication.shared
        let delegate = NavLabApp()
        app.delegate = delegate
        app.setActivationPolicy(.regular)
        withExtendedLifetime(delegate) {
            app.run()
        }
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.mainMenu = makeMenu()
        showSplash()
        NSApp.activate()
        // NavLab loads for a few seconds before its window appears, as many real apps do. A
        // look taken right after launching it sees the loading window, not the scenes: the
        // app-launch hazard of docs/21 section 1.4 case 5.
        DispatchQueue.main.asyncAfter(deadline: .now() + 3) {
            self.showMain()
        }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
    }

    func showSplash() {
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 320, height: 120), styleMask: [.titled], backing: .buffered, defer: false)
        window.title = "NavLab"
        window.isReleasedWhenClosed = false
        window.contentView = NSHostingView(rootView: SplashView())
        window.center()
        window.makeKeyAndOrderFront(nil)
        splash = window
    }

    func showMain() {
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 900, height: 620), styleMask: [.titled, .closable, .miniaturizable], backing: .buffered, defer: false)
        window.title = "NavLab"
        window.isReleasedWhenClosed = false
        window.contentView = NSHostingView(rootView: ShellView(model: model, lab: self))
        window.center()
        window.makeKeyAndOrderFront(nil)
        mainWindow = window
        splash?.orderOut(nil)
        splash = nil
    }

    /// Opens the Inspector, a floating panel placed over the main window's Project header and
    /// Note field, as an inspector sits over what it edits. It takes the keyboard when it opens.
    func openInspector() {
        if let inspector {
            inspector.makeKeyAndOrderFront(nil)
            return
        }
        let panel = NSPanel(contentRect: NSRect(x: 0, y: 0, width: 340, height: 240), styleMask: [.titled, .closable, .utilityWindow], backing: .buffered, defer: false)
        panel.title = "Inspector"
        panel.isFloatingPanel = true
        panel.hidesOnDeactivate = false
        panel.isReleasedWhenClosed = false
        panel.contentView = NSHostingView(rootView: InspectorView(model: model))
        if let main = mainWindow {
            panel.setFrameTopLeftPoint(NSPoint(x: main.frame.minX + 190, y: main.frame.maxY - 60))
        }
        panel.makeKeyAndOrderFront(nil)
        inspector = panel
    }

    func closeInspector() {
        inspector?.close()
    }

    /// Shows a save panel as a sheet on the main window and writes a small file where it says.
    func export(done: @escaping (String) -> Void) {
        guard let window = mainWindow else {
            return
        }
        let panel = NSSavePanel()
        panel.nameFieldStringValue = "navlab-export.txt"
        panel.message = "Export the lab notes"
        panel.beginSheetModal(for: window) { response in
            guard response == .OK, let url = panel.url else {
                done("cancelled")
                return
            }
            do {
                try "NavLab export\n".write(to: url, atomically: true, encoding: .utf8)
                done(url.path)
            } catch {
                done("error: \(error.localizedDescription)")
            }
        }
    }

    /// The menu bar: Quit, the standard Edit commands text fields rely on, and a Tools menu whose
    /// Mark Reviewed command exists only there (a menu-only command for docs/21 wave 2).
    func makeMenu() -> NSMenu {
        let bar = NSMenu()
        let appItem = NSMenuItem()
        let appMenu = NSMenu(title: "NavLab")
        appMenu.addItem(withTitle: "Quit NavLab", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        appItem.submenu = appMenu
        bar.addItem(appItem)
        let editItem = NSMenuItem()
        let editMenu = NSMenu(title: "Edit")
        editMenu.addItem(withTitle: "Undo", action: Selector(("undo:")), keyEquivalent: "z")
        editMenu.addItem(withTitle: "Redo", action: Selector(("redo:")), keyEquivalent: "Z")
        editMenu.addItem(NSMenuItem.separator())
        editMenu.addItem(withTitle: "Cut", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        editMenu.addItem(withTitle: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        editMenu.addItem(withTitle: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        editMenu.addItem(withTitle: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        editItem.submenu = editMenu
        bar.addItem(editItem)
        let toolsItem = NSMenuItem()
        let toolsMenu = NSMenu(title: "Tools")
        let review = NSMenuItem(title: "Mark Reviewed", action: #selector(markReviewed), keyEquivalent: "r")
        review.keyEquivalentModifierMask = [.command, .shift]
        review.target = self
        toolsMenu.addItem(review)
        toolsItem.submenu = toolsMenu
        bar.addItem(toolsItem)
        return bar
    }

    @objc func markReviewed() {
        model.reviewed = true
    }
}

struct SplashView: View {
    var body: some View {
        VStack(spacing: 12) {
            Text("Loading NavLab...")
                .font(.headline)
            ProgressView()
                .progressViewStyle(.linear)
                .frame(width: 200)
                .accessibilityLabel("Loading")
        }
        .frame(width: 320, height: 120)
    }
}

/// The main window: a sidebar of scene buttons and the chosen scene.
struct ShellView: View {
    @ObservedObject var model: LabModel
    let lab: NavLabApp

    var body: some View {
        HStack(spacing: 0) {
            VStack(alignment: .leading, spacing: 2) {
                Text("Scenes")
                    .font(.headline)
                    .padding(.bottom, 6)
                ForEach(LabScene.allCases) { scene in
                    SceneButton(scene: scene, selected: model.scene == scene) {
                        model.scene = scene
                    }
                }
                Spacer()
            }
            .padding(12)
            .frame(width: 180)
            .frame(maxHeight: .infinity)
            .accessibilityElement(children: .contain)
            .accessibilityLabel("Scenes")
            Divider()
            sceneView
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        }
        .frame(width: 900, height: 620)
    }

    @ViewBuilder
    var sceneView: some View {
        switch model.scene {
        case .overlay:
            OverlayScene()
        case .nestedScroll:
            NestedScrollScene()
        case .longLabel:
            LongLabelScene()
        case .amount:
            AmountScene()
        case .delayed:
            DelayedScene()
        case .prepare:
            PrepareScene()
        case .splitter:
            SplitterScene()
        case .dialogs:
            DialogsScene(model: model, lab: lab)
        case .secondWindow:
            SecondWindowScene(model: model, lab: lab)
        }
    }
}

struct SceneButton: View {
    let scene: LabScene
    let selected: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Text(scene.rawValue)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.vertical, 5)
                .padding(.horizontal, 8)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .background(selected ? Color.accentColor.opacity(0.25) : Color.clear, in: RoundedRectangle(cornerRadius: 6))
        .accessibilityLabel(scene.rawValue)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

struct SceneTitle: View {
    let title: String

    init(_ title: String) {
        self.title = title
    }

    var body: some View {
        Text(title)
            .font(.system(size: 24, weight: .bold))
            .accessibilityAddTraits(.isHeader)
    }
}
