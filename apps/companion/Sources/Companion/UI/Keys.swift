import AppKit
import SwiftUI

/// The window's keys (companion ADR 0019 point 6), all in this file: Up and Down move between
/// runs, J and K between checks, Left and Right between frames, Space plays the recording,
/// F plays it at 1x or 4x, N jumps to the next failure (Shift-N the one before), Z shows the
/// picture alone, A opens Activity, M the composer, E the evidence, C captures a screenshot,
/// T takes control, U undoes an accept or reject, ? and Cmd-K the palette, / searches the runs,
/// Ctrl-Cmd-S hides or shows the runs, Cmd-L back to live, Cmd-R reads everything again,
/// Cmd-Return the primary action, Cmd-Delete Reject, Cmd-Shift-S saves the recording (a menu
/// item's key), Esc closes what is open. Every one is also a labeled button and a menu item or palette row. While a
/// text field has the keyboard only Esc and Cmd-K are ours; while the person drives the Mac,
/// every key goes to the Mac.
@MainActor
final class Keys {
    private let shell: ShellModel
    private var monitor: Any?

    init(shell: ShellModel) {
        self.shell = shell
    }

    func install() {
        guard monitor == nil else { return }
        monitor = NSEvent.addLocalMonitorForEvents(matching: [.keyDown, .scrollWheel]) { [weak self] event in
            // A scroll anywhere in the window hides the scrub bar's preview and goes on as
            // usual (companion ADR 0023).
            if event.type == .scrollWheel {
                MainActor.assumeIsolated { self?.shell.dismissScrubPreview() }
                return event
            }
            let ours = MainActor.assumeIsolated { self?.handle(event, responder: event.window?.firstResponder) ?? false }
            return ours ? nil : event
        }
    }

    func remove() {
        if let monitor { NSEvent.removeMonitor(monitor) }
        monitor = nil
    }

    /// What a key does; true when it was ours. Pure over the shell, for tests.
    @discardableResult
    func handle(_ event: NSEvent, responder: NSResponder?) -> Bool {
        let flags = event.modifierFlags.intersection([.command, .option, .control, .shift])
        let key = event.charactersIgnoringModifiers?.lowercased() ?? ""
        let typing = responder is NSText || responder is NSTextView
        if responder is InputSurfaceView, shell.driving { return false }
        if shell.dropdowns.isOpen {
            return shell.dropdowns.handle(keyCode: event.keyCode, characters: event.charactersIgnoringModifiers ?? "", flags: flags)
        }

        if flags == .command, key == "k" {
            shell.paletteOpen.toggle()
            return true
        }
        if event.keyCode == 53 { // esc
            if typing, let window = event.window {
                window.makeFirstResponder(nil)
            }
            return shell.closeOverlays() || typing
        }
        guard !typing, !shell.paletteOpen else { return false }

        if flags == .command, event.keyCode == 36 || event.keyCode == 76 { // return, enter
            if let s = shell.summary, let primary = shell.actions(for: s).primary {
                shell.perform(primary)
                return true
            }
            return false
        }
        if flags == .command, event.keyCode == 51 { // delete
            if let s = shell.summary, let reject = shell.actions(for: s).secondary.first(where: { $0.id == SummaryAction.reject }) {
                shell.perform(reject)
                return true
            }
            return false
        }
        if flags == .command, key == "l" {
            shell.goLive()
            return true
        }
        if flags == .command, key == "r" {
            shell.refresh()
            return true
        }
        if flags == [.control, .command], key == "s" {
            shell.toggleSidebar()
            return true
        }
        if flags == .shift, key == "n" {
            shell.jumpToFailure(forward: false)
            return true
        }
        if key == "?", flags.subtracting(.shift).isEmpty {
            shell.paletteOpen = true
            return true
        }
        guard flags.isEmpty else { return false }
        switch event.keyCode {
        case 125: shell.moveRun(by: 1); return true   // down
        case 126: shell.moveRun(by: -1); return true  // up
        case 123: shell.moveFrame(by: -1); return true // left
        case 124: shell.moveFrame(by: 1); return true  // right
        case 49: shell.togglePlay(); return true       // space
        default: break
        }
        switch key {
        case "j": shell.moveCheck(by: 1); return true
        case "k": shell.moveCheck(by: -1); return true
        case "a": shell.toggleActivity(); return true
        case "m": shell.openComposer(.message); return true
        case "e": shell.evidenceOpen.toggle(); return true
        case "n": shell.jumpToFailure(); return true
        case "/": shell.focusSearch(); return true
        case "l": shell.goLive(); return true
        case "z": shell.toggleZoom(); return true
        case "f": shell.toggleSpeed(); return true
        case "c":
            guard shell.canCapture else { return false }
            shell.capture()
            return true
        case "u":
            guard shell.store.verdictUndo.pending != nil else { return false }
            shell.undoVerdictChoice()
            return true
        case "t":
            guard let s = shell.summary else { return false }
            if let take = ([shell.actions(for: s).primary].compactMap { $0 } + shell.actions(for: s).secondary)
                .first(where: { $0.id == SummaryAction.takeControl }) {
                shell.perform(take)
                return true
            }
            guard shell.canTakeControl(s) else { return false }
            shell.perform(SummaryAction(id: SummaryAction.takeControl, label: "Take control"))
            return true
        default: return false
        }
    }

    // MARK: - How actions show their keys

    /// A tooltip for an action's button.
    static func hint(for action: SummaryAction) -> String {
        shortcut(for: action).map { "\(action.label) (\($0))" } ?? action.label
    }

    /// The key shown beside an action in the palette and menus.
    static func shortcut(for action: SummaryAction) -> String? {
        switch action.id {
        case SummaryAction.takeControl: "T"
        case SummaryAction.accept, SummaryAction.continue, SummaryAction.giveBack,
             SummaryAction.restart, SummaryAction.answer, SummaryAction.recheck, SummaryAction.keepWaiting:
            nil
        case SummaryAction.reject: "⌘⌫"
        default: nil
        }
    }

    static func icon(for action: SummaryAction) -> Icon {
        switch action.id {
        case SummaryAction.accept: .check
        case SummaryAction.reject: .close
        case SummaryAction.continue: .play
        case SummaryAction.restart, SummaryAction.recheck: .restart
        case SummaryAction.takeControl, SummaryAction.giveBack: .pointer
        case SummaryAction.answer: .message
        default: .check
        }
    }
}

/// The menu bar: the run's actions and the panels, with the Command chords the keys use. Bare
/// keys are not given to menu items (they would fire while a person types); the palette shows them.
struct ShellCommands: Commands {
    let shell: ShellModel
    /// The same setting the Settings sheet's Appearance control writes.
    @AppStorage("appearance", store: AppDefaults.shared) private var appearance = "system"

    var body: some Commands {
        CommandGroup(replacing: .appSettings) {
            Button("Settings…") { shell.settingsOpen = true }
                .keyboardShortcut(",", modifiers: .command)
        }
        CommandGroup(replacing: .sidebar) {
            Button(shell.sidebarHidden ? "Show Runs" : "Hide Runs") { shell.toggleSidebar() }
                .keyboardShortcut("s", modifiers: [.control, .command])
            Button("Search Runs") { shell.focusSearch() }
            // macOS's own menu bar: a picker here is a submenu of checkmarked items.
            Picker("Appearance", selection: $appearance) {
                Text("System").tag("system")
                Text("Light").tag("light")
                Text("Dark").tag("dark")
            }
            Divider()
        }
        CommandMenu("Run") {
            if let s = shell.summary {
                let (primary, secondary) = shell.actions(for: s)
                if let primary {
                    Button(primary.label) { shell.perform(primary) }
                        .keyboardShortcut(.return, modifiers: .command)
                }
                // Take control has its own item below, always there.
                ForEach(secondary.filter { $0.id != SummaryAction.takeControl }, id: \.id) { action in
                    if action.id == SummaryAction.reject {
                        Button(action.label) { shell.perform(action) }.keyboardShortcut(.delete, modifiers: .command)
                    } else {
                        Button(action.label) { shell.perform(action) }
                    }
                }
                Divider()
            }
            Button(undoTitle) { shell.undoVerdictChoice() }
                .disabled(shell.store.verdictUndo.pending == nil)
            if shell.driving {
                if shell.summary.map({ shell.actions(for: $0).primary?.id != SummaryAction.giveBack }) ?? true {
                    Button("Give Back Control") { shell.perform(SummaryAction(id: SummaryAction.giveBack, label: "Give control back")) }
                }
            } else if shell.summary.map({ shell.actions(for: $0).primary?.id != SummaryAction.takeControl }) ?? true {
                Button("Take Control") { shell.perform(SummaryAction(id: SummaryAction.takeControl, label: "Take control")) }
                    .disabled(!canTakeControl)
            }
            Divider()
            Button("Activity") { shell.toggleActivity() }
            Button("Message the Verifier") { shell.openComposer(.message) }
            Button("New Task") { shell.openComposer(.task) }
            Button("Evidence") { shell.evidenceOpen.toggle() }
            Button(shell.zoomed ? "Show Checks and Activity" : "Picture Only") { shell.toggleZoom() }
            Divider()
            Button("Next Check") { shell.moveCheck(by: 1) }.disabled(shell.checks.isEmpty)
            Button("Previous Check") { shell.moveCheck(by: -1) }.disabled(shell.checks.isEmpty)
            Divider()
            Button(shell.playing ? "Pause Recording" : "Play Recording") { shell.togglePlay() }
            Button("Play at \(shell.speed >= 4 ? 1 : Int(shell.speed) * 2)×") { shell.toggleSpeed() }
            Button("Next Failure") { shell.jumpToFailure() }
            Button("Previous Failure") { shell.jumpToFailure(forward: false) }
            Button("Back to Live") { shell.goLive() }
                .keyboardShortcut("l", modifiers: .command)
            Divider()
            Button("Capture Screenshot") { shell.capture() }.disabled(!shell.canCapture)
            Button("Save Recording…") { shell.exportRecording() }.disabled(!shell.canExport)
                .keyboardShortcut("s", modifiers: [.command, .shift])
            Button("Copy Run ID") { shell.copyRunID() }.disabled(shell.runId == nil)
            Button("Destroy the Mac…") { shell.confirmingDestroy = true }.disabled(!shell.canDestroy)
            Divider()
            Button("Refresh") { shell.refresh() }
                .keyboardShortcut("r", modifiers: .command)
            Divider()
            Button("Command Palette") { shell.paletteOpen.toggle() }
                .keyboardShortcut("k", modifiers: .command)
        }
    }

    /// Names what U would take back.
    private var undoTitle: String {
        switch shell.store.verdictUndo.pending?.payload.kind {
        case .accept?: "Undo Accept"
        case .dispute?: "Undo Reject"
        case nil: "Undo Accept or Reject"
        }
    }

    /// Take control, whether the header offers it or only the toolbar does.
    private var canTakeControl: Bool {
        guard let s = shell.summary else { return false }
        if shell.canTakeControl(s) { return true }
        let (primary, secondary) = shell.actions(for: s)
        return ([primary].compactMap { $0 } + secondary).contains { $0.id == SummaryAction.takeControl }
    }
}
