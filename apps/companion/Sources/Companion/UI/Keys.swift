import AppKit
import SwiftUI

/// The window's keys (companion ADR 0019 point 6), all in this file: Up and Down move between
/// runs, J and K between checks, Left and Right between frames, A opens Activity, M the
/// composer, E the evidence, Cmd-K the palette, Cmd-Return the primary action, Cmd-Delete
/// Reject, Esc closes what is open. Every one is also a labeled button and a menu item or
/// palette row. While a text field has the keyboard only Esc and Cmd-K are ours; while the
/// person drives the Mac, every key goes to the Mac.
@MainActor
final class Keys {
    private let shell: ShellModel
    private var monitor: Any?

    init(shell: ShellModel) {
        self.shell = shell
    }

    func install() {
        guard monitor == nil else { return }
        monitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
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
        guard flags.isEmpty else { return false }
        switch event.keyCode {
        case 125: shell.moveRun(by: 1); return true   // down
        case 126: shell.moveRun(by: -1); return true  // up
        case 123: shell.moveFrame(by: -1, in: filmstrip()); return true // left
        case 124: shell.moveFrame(by: 1, in: filmstrip()); return true  // right
        default: break
        }
        switch key {
        case "j": shell.moveCheck(by: 1); return true
        case "k": shell.moveCheck(by: -1); return true
        case "a": shell.toggleActivity(); return true
        case "m": shell.openComposer(.message); return true
        case "e": shell.evidenceOpen.toggle(); return true
        default: return false
        }
    }

    private func filmstrip() -> [String] {
        guard let s = shell.summary else { return [] }
        return Filmstrip.items(shell.store.frames[s.runId] ?? [], checks: s.checks.items).map(\.file)
    }

    // MARK: - How actions show their keys

    /// A tooltip for an action's button.
    static func hint(for action: SummaryAction) -> String {
        shortcut(for: action).map { "\(action.label) (\($0))" } ?? action.label
    }

    /// The key shown beside an action in the palette and menus.
    static func shortcut(for action: SummaryAction) -> String? {
        switch action.id {
        case SummaryAction.accept, SummaryAction.continue, SummaryAction.takeControl, SummaryAction.giveBack,
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

    var body: some Commands {
        CommandGroup(replacing: .appSettings) {
            Button("Settings…") { shell.settingsOpen = true }
                .keyboardShortcut(",", modifiers: .command)
        }
        CommandMenu("Run") {
            if let s = shell.summary {
                let (primary, secondary) = shell.actions(for: s)
                if let primary {
                    Button(primary.label) { shell.perform(primary) }
                        .keyboardShortcut(.return, modifiers: .command)
                }
                ForEach(secondary, id: \.id) { action in
                    if action.id == SummaryAction.reject {
                        Button(action.label) { shell.perform(action) }.keyboardShortcut(.delete, modifiers: .command)
                    } else {
                        Button(action.label) { shell.perform(action) }
                    }
                }
                Divider()
            }
            Button("Activity") { shell.toggleActivity() }
            Button("Message the Verifier") { shell.openComposer(.message) }
            Button("Evidence") { shell.evidenceOpen.toggle() }
            Divider()
            Button("Command Palette") { shell.paletteOpen.toggle() }
                .keyboardShortcut("k", modifiers: .command)
        }
    }
}
