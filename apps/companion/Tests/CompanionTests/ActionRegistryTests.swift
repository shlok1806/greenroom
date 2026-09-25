import Foundation
import XCTest

@testable import Companion

/// The one action registry and the keyboard rules of companion ADR 0005: one list, no
/// key twice in a context, nothing destructive on a bare key, typing and driving own the
/// keyboard, accept and dispute only on a verdict open for review.
final class ActionRegistryTests: XCTestCase {
    private func chord(_ c: Character) -> KeyChord { .char(c) }

    /// A run open on its Screen, the stage focused: the usual place a key lands.
    private func onScreen(_ change: (inout ActionState) -> Void = { _ in }) -> ActionState {
        var s = ActionState()
        s.runOpen = true
        s.runCount = 3
        s.pane = .stage
        s.stage = .screen
        s.available = [
            HandlerKey(id: .play, context: .screen), HandlerKey(id: .nextFrame, context: .screen),
            HandlerKey(id: .previousFrame, context: .screen), HandlerKey(id: .goSteps, context: .run),
            HandlerKey(id: .goScreen, context: .run), HandlerKey(id: .goTranscript, context: .run),
            HandlerKey(id: .compose, context: .run),
        ]
        change(&s)
        return s
    }

    // MARK: - The registry

    func testEveryActionHasOneEntry() {
        XCTAssertEqual(ActionRegistry.all.count, ActionID.allCases.count)
        XCTAssertEqual(Set(ActionRegistry.all.map(\.id)).count, ActionRegistry.all.count, "an action has two entries")
        for id in ActionID.allCases { XCTAssertEqual(ActionRegistry.spec(id).id, id) }
    }

    /// Two entries on one key in one context would make the key mean whichever came first.
    func testNoKeyIsBoundTwiceInOneContext() {
        for context in ActionContext.allCases {
            var seen: [[KeyChord]: ActionID] = [:]
            for spec in ActionRegistry.all where spec.contexts.contains(context) {
                for binding in spec.keys {
                    if let other = seen[binding.chords] {
                        XCTFail("\(binding.label) is \(other) and \(spec.id) in \(context)")
                    }
                    seen[binding.chords] = spec.id
                }
            }
        }
    }

    /// A key that starts a sequence (`g`) cannot also be an action on its own where the
    /// sequence works, or the sequence could never be typed.
    func testNoSequencePrefixIsAlsoAKeyOfItsOwn() {
        for spec in ActionRegistry.all {
            for binding in spec.keys where binding.chords.count > 1 {
                let prefix = binding.chords[0]
                for other in ActionRegistry.all where other.contexts.contains(where: spec.contexts.contains) {
                    XCTAssertFalse(other.keys.contains { $0.chords == [prefix] },
                                   "\(prefix.label) starts \(spec.id) but is \(other.id) on its own")
                }
            }
        }
    }

    /// ADR 0005's first rule: destroy is Cmd-Backspace, never a key a stray press could hit.
    func testNothingDestructiveHasABareKey() {
        let destructive = ActionRegistry.all.filter(\.destructive)
        XCTAssertTrue(destructive.contains { $0.id == .destroy })
        for spec in destructive where spec.contexts != [.confirm] {
            XCTAssertFalse(spec.keys.isEmpty, "\(spec.id) has no key")
            for binding in spec.keys {
                XCTAssertFalse(binding.isBare, "\(spec.id) is on the bare key \(binding.label)")
            }
        }
    }

    /// The keys ADR 0005 fixes, so a registry change cannot move them unnoticed.
    func testTheDecisionRecordsKeysAreTheRegistrys() {
        let expected: [ActionID: String] = [
            .moveDown: "j", .moveUp: "k", .open: "⏎", .search: "/", .capture: "c", .takeControl: "t",
            .accept: "a", .dispute: "d", .undo: "u", .play: "space", .help: "?", .back: "esc",
            .palette: "⌘K", .destroy: "⌘⌫", .goSteps: "g s", .goTranscript: "g t", .goRuns: "g r",
            .previousFrame: "←", .nextFrame: "→", .refresh: "⌘R", .zoom: "z",
        ]
        for (id, label) in expected {
            XCTAssertEqual(ActionRegistry.spec(id).keyLabel, label, "\(id)")
        }
        XCTAssertTrue(ActionRegistry.spec(.moveDown).keys.contains(KeyBinding(.down)))
        XCTAssertTrue(ActionRegistry.spec(.moveUp).keys.contains(KeyBinding(.up)))
    }

    /// Menus are built from entries; each Command chord on the menu bar is used once.
    func testTheMenuBarIsBuiltFromEntriesWithDistinctChords() {
        let menu = ActionRegistry.menu(.view) + ActionRegistry.menu(.run)
        XCTAssertFalse(menu.isEmpty)
        XCTAssertTrue(menu.contains { $0.id == .destroy })
        XCTAssertTrue(menu.contains { $0.id == .toggleSidebar })
        let chords = menu.compactMap { spec -> [KeyChord]? in
            guard let binding = spec.keys.first, !binding.isBare else { return nil }
            return binding.chords
        }
        XCTAssertEqual(chords.count, Set(chords).count, "a menu chord is used twice")
    }

    // MARK: - Shortcuts live only in the registry

    private var sources: URL {
        URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
            .appendingPathComponent("Sources/Companion")
    }

    private func swiftFiles() throws -> [(name: String, text: String)] {
        let enumerator = try XCTUnwrap(FileManager.default.enumerator(at: sources, includingPropertiesForKeys: nil))
        var out: [(String, String)] = []
        for case let url as URL in enumerator where url.pathExtension == "swift" {
            out.append((url.path.replacingOccurrences(of: sources.path + "/", with: ""), try String(contentsOf: url, encoding: .utf8)))
        }
        XCTAssertGreaterThan(out.count, 20, "found no sources at \(sources.path)")
        return out
    }

    /// ADR 0005: "a test fails if any shortcut is declared outside it". Every key a SwiftUI
    /// view could answer to goes through `Views/Keyboard.swift`, which builds it from an
    /// entry; the guest's own keys stay in `InputSurface.swift`.
    func testEveryShortcutInTheAppComesFromTheRegistry() throws {
        let patterns = [
            #"\.keyboardShortcut\((?!for:)"#, #"\.onKeyPress\("#, #"onExitCommand"#, #"onMoveCommand"#,
            #"onCommand\("#, #"keyEquivalent\s*=(?!=)"#, #"addLocalMonitorForEvents"#, #"addGlobalMonitorForEvents"#,
        ].map { try! NSRegularExpression(pattern: $0) }
        let allowed: [String: Set<String>] = [
            // The registry's own builders: `keyboardShortcut(for:)` and `sendOnReturn`.
            "Views/Keyboard.swift": [#"\.keyboardShortcut\((?!for:)"#, #"\.onKeyPress\("#],
            // The one key monitor.
            "Views/KeyRouter.swift": [#"addLocalMonitorForEvents"#],
        ]
        for file in try swiftFiles() {
            for pattern in patterns where !(allowed[file.name]?.contains(pattern.pattern) ?? false) {
                let range = NSRange(file.text.startIndex..., in: file.text)
                if let hit = pattern.firstMatch(in: file.text, range: range) {
                    let line = file.text[..<Range(hit.range, in: file.text)!.lowerBound].split(separator: "\n", omittingEmptySubsequences: false).count
                    XCTFail("\(file.name):\(line) declares a key outside the registry (\(pattern.pattern))")
                }
            }
        }
    }

    /// The menu bar has no item of its own: every `Button` in the Commands is an entry.
    func testEveryMenuItemComesFromTheRegistry() throws {
        let app = try XCTUnwrap(swiftFiles().first { $0.name == "CompanionApp.swift" }).text
        let commands = try XCTUnwrap(app.range(of: "struct RunMenuCommands"))
        let body = app[commands.lowerBound...]
        XCTAssertNil(body.range(of: #"Button\(""#, options: .regularExpression), "a menu item with its own title")
        XCTAssertNotNil(body.range(of: "ActionRegistry.menu(.view)"))
        XCTAssertNotNil(body.range(of: "ActionRegistry.menu(.run)"))
        XCTAssertNotNil(body.range(of: ".keyboardShortcut(for: spec.id)"))
    }

    // MARK: - Chords from AppKit

    func testAKeyPressBecomesTheRegistrysChord() {
        func stroke(_ chars: String, _ ignoring: String? = nil, code: UInt16 = 50, cmd: Bool = false, shift: Bool = false) -> KeyChord {
            KeyChord(stroke: KeyStroke(characters: chars, charactersIgnoringModifiers: ignoring ?? chars, keyCode: code,
                                       command: cmd, shift: shift))
        }
        XCTAssertEqual(stroke("j"), .char("j"))
        // Shift is in the typed character: `?` and `G`, not shift-/ and shift-g.
        XCTAssertEqual(stroke("?", "/", shift: true), .char("?"))
        XCTAssertEqual(stroke("G", "g", shift: true), .char("G"))
        XCTAssertEqual(stroke("k", cmd: true), .cmd("k"))
        XCTAssertEqual(stroke("E", "e", cmd: true, shift: true), .cmd("e", shift: true))
        XCTAssertEqual(stroke("\r", code: 36), .enter)
        XCTAssertEqual(stroke("\r", code: 36, shift: true), KeyChord(key: .enter, shift: true))
        XCTAssertEqual(stroke("\u{1b}", code: 53), .escape)
        XCTAssertEqual(stroke(" ", code: 49), .space)
        XCTAssertEqual(stroke("\u{F701}", code: 125), .down)
        XCTAssertEqual(stroke("\u{7f}", code: 51, cmd: true), KeyChord(key: .delete, command: true))
    }

    func testLabelsSpellKeysTheWayTheMacDoes() {
        XCTAssertEqual(KeyChord.cmd("k").label, "⌘K")
        XCTAssertEqual(KeyChord.cmd("e", shift: true).label, "⇧⌘E")
        XCTAssertEqual(KeyChord(key: .character("s"), command: true, control: true).label, "⌃⌘S")
        XCTAssertEqual(KeyBinding(.char("g"), .char("s")).label, "g s")
        XCTAssertEqual(KeyChord(key: .enter, shift: true).label, "⇧⏎")
    }

    // MARK: - Contexts and modes

    func testTheLiveContextsGoFromTheFocusedPaneOutwards() {
        XCTAssertEqual(ActionRules.contexts(ActionState()), [.sidebar, .global])
        XCTAssertEqual(ActionRules.contexts(onScreen()), [.screen, .run, .global])
        XCTAssertEqual(ActionRules.contexts(onScreen { $0.stage = .steps }), [.steps, .run, .global])
        XCTAssertEqual(ActionRules.contexts(onScreen { $0.pane = .conversation }), [.conversation, .run, .global])
        XCTAssertEqual(ActionRules.contexts(onScreen { $0.verdictOpenForReview = true }), [.screen, .verdict, .run, .global])
    }

    func testAModeOwnsTheKeyboardAlone() {
        XCTAssertEqual(ActionRules.contexts(onScreen { $0.responder = .text }), [.composer])
        XCTAssertEqual(ActionRules.contexts(onScreen { $0.paletteOpen = true }), [.palette])
        XCTAssertEqual(ActionRules.contexts(onScreen { $0.confirmingDestroy = true }), [.confirm])
        XCTAssertEqual(ActionRules.contexts(onScreen { $0.driving = true; $0.responder = .guest }), [.driving])
        // Driving with the composer clicked: keys follow focus, into the field.
        XCTAssertEqual(ActionRules.contexts(onScreen { $0.driving = true; $0.responder = .text }), [.composer])
    }

    /// The player's speed has its own key, on the screen only, offered like play.
    func testTheSpeedIsAKeyOnTheScreen() {
        let spec = ActionRegistry.spec(.speed)
        XCTAssertEqual(spec.keyLabel, "f")
        XCTAssertEqual(spec.contexts, [.screen])
        XCTAssertFalse(spec.destructive)
        let offered = onScreen { $0.available.insert(HandlerKey(id: .speed, context: .screen)) }
        XCTAssertEqual(KeyResolver.resolve(chord("f"), offered), .perform(.speed, .screen))
        // Not offered (no recording to play): taken, doing nothing.
        XCTAssertEqual(KeyResolver.resolve(chord("f"), onScreen()), .swallow)
        XCTAssertEqual(ActionRules.whyDisabled(.speed, onScreen()), "Show the screen, with a recording")
    }

    // MARK: - Resolving keys

    func testABareKeyOnAPaneActs() {
        XCTAssertEqual(KeyResolver.resolve(.space, onScreen()), .perform(.play, .screen))
        XCTAssertEqual(KeyResolver.resolve(.right, onScreen()), .perform(.nextFrame, .screen))
        XCTAssertEqual(KeyResolver.resolve(chord("?"), onScreen()), .perform(.help, .global))
        XCTAssertEqual(KeyResolver.resolve(.cmd("k"), onScreen()), .perform(.palette, .global))
        XCTAssertEqual(KeyResolver.resolve(chord("/"), onScreen()), .perform(.search, .global))
    }

    /// ADR 0005: a typing context swallows bare keys; they are the field's to type.
    func testATypingContextLetsTheFieldHaveBareKeys() {
        let typing = onScreen { $0.responder = .text; $0.verdictOpenForReview = true }
        for key in ["j", "k", "a", "d", "t", "c", "u", "g", "?", "/", "G", " ", "r"] {
            XCTAssertEqual(KeyResolver.resolve(chord(Character(key)), typing), .pass, "\(key) acted while typing")
        }
        XCTAssertEqual(KeyResolver.resolve(.space, typing), .pass)
        // Return and Shift-Return are the field's own (`sendOnReturn`), built from the registry.
        XCTAssertEqual(KeyResolver.resolve(.enter, typing), .pass)
        XCTAssertEqual(KeyResolver.resolve(KeyChord(key: .enter, shift: true), typing), .pass)
        // The two keys a typing context owns: leaving it, and the palette.
        XCTAssertEqual(KeyResolver.resolve(.escape, typing), .perform(.leave, .composer))
        XCTAssertEqual(KeyResolver.resolve(.cmd("k"), typing), .perform(.palette, .composer))
    }

    /// Driving is a hard mode: every key, Cmd-Q and esc included, goes to the guest.
    func testWhileDrivingEveryKeyGoesToTheGuest() {
        let driving = onScreen { $0.driving = true; $0.responder = .guest; $0.machineReady = true; $0.machineExists = true }
        let keys: [KeyChord] = [
            .char("t"), .char("a"), .char("?"), .escape, .enter, .space, .tab, .cmd("k"), .cmd("q"), .cmd("w"),
            .cmd("r"), KeyChord(key: .delete, command: true), .char("g"),
        ]
        for key in keys {
            XCTAssertEqual(KeyResolver.resolve(key, driving), .pass, "\(key.label) did not reach the guest")
        }
        XCTAssertEqual(HintBar.content(driving).mode, .driving)
    }

    /// ADR 0005: accept and dispute act only on a verdict open for review, and not again
    /// while a choice waits out its undo.
    func testAcceptAndDisputeActOnlyOnAVerdictOpenForReview() {
        XCTAssertEqual(KeyResolver.resolve(chord("a"), onScreen()), .pass, "no verdict: a is nobody's key")
        let open = onScreen { $0.verdictOpenForReview = true }
        XCTAssertEqual(KeyResolver.resolve(chord("a"), open), .perform(.accept, .verdict))
        XCTAssertEqual(KeyResolver.resolve(chord("d"), open), .perform(.dispute, .verdict))
        let waiting = onScreen { $0.verdictOpenForReview = true; $0.undoSeconds = 3; $0.undoWord = "accepting" }
        XCTAssertEqual(KeyResolver.resolve(chord("a"), waiting), .swallow)
        XCTAssertEqual(KeyResolver.resolve(chord("u"), waiting), .perform(.undo, .global))
        XCTAssertEqual(KeyResolver.resolve(chord("u"), open), .swallow, "undo with nothing held")
    }

    func testGThenAKeyGoesSomewhere() {
        let s = onScreen()
        XCTAssertEqual(KeyResolver.resolve(chord("g"), s), .prefix(.char("g")))
        let waiting = onScreen { $0.pendingPrefix = .char("g") }
        XCTAssertEqual(KeyResolver.resolve(chord("s"), waiting), .perform(.goSteps, .run))
        XCTAssertEqual(KeyResolver.resolve(chord("t"), waiting), .perform(.goTranscript, .run))
        XCTAssertEqual(KeyResolver.resolve(chord("r"), waiting), .perform(.goRuns, .global))
        // A sequence that goes nowhere ends without its second key doing anything else.
        XCTAssertEqual(KeyResolver.resolve(chord("c"), onScreen { $0.pendingPrefix = .char("g"); $0.available = [] }), .swallow)
        XCTAssertEqual(KeyResolver.resolve(chord("x"), waiting), .swallow)
    }

    /// Cmd-Backspace asks; Return destroys; any other key keeps the machine.
    func testDestroyAsksInlineAndOnlyReturnConfirms() {
        let withMachine = onScreen { $0.machineExists = true }
        XCTAssertEqual(KeyResolver.resolve(KeyChord(key: .delete, command: true), withMachine), .perform(.destroy, .run))
        XCTAssertEqual(KeyResolver.resolve(KeyChord(key: .delete, command: true), onScreen()), .swallow, "no machine to destroy")
        XCTAssertEqual(KeyResolver.resolve(KeyChord(key: .delete), withMachine), .pass, "a bare Backspace destroyed")
        let asking = onScreen { $0.machineExists = true; $0.confirmingDestroy = true }
        XCTAssertEqual(KeyResolver.resolve(.enter, asking), .perform(.confirmDestroy, .confirm))
        for key: KeyChord in [.escape, .char("y"), .space, .cmd("k"), KeyChord(key: .delete, command: true)] {
            XCTAssertEqual(KeyResolver.resolve(key, asking), .perform(.cancelDestroy, .confirm), key.label)
        }
    }

    func testThePaletteTakesItsKeysAndTheFieldTypesTheRest() {
        let open = onScreen { $0.paletteOpen = true; $0.responder = .text }
        XCTAssertEqual(KeyResolver.resolve(.down, open), .perform(.paletteDown, .palette))
        XCTAssertEqual(KeyResolver.resolve(.up, open), .perform(.paletteUp, .palette))
        XCTAssertEqual(KeyResolver.resolve(.enter, open), .perform(.paletteRun, .palette))
        XCTAssertEqual(KeyResolver.resolve(.escape, open), .perform(.paletteClose, .palette))
        XCTAssertEqual(KeyResolver.resolve(.cmd("k"), open), .perform(.paletteClose, .palette))
        XCTAssertEqual(KeyResolver.resolve(chord("j"), open), .pass)
        XCTAssertEqual(KeyResolver.resolve(chord("a"), open), .pass)
    }

    /// A view's action is enabled only while a view on screen offers it.
    func testAViewsActionNeedsItsOffer() {
        XCTAssertEqual(KeyResolver.resolve(.space, onScreen { $0.available = [] }), .swallow)
        XCTAssertEqual(KeyResolver.resolve(chord("c"), onScreen()), .swallow, "capture with no machine")
        XCTAssertEqual(KeyResolver.resolve(chord("c"), onScreen { $0.available.insert(HandlerKey(id: .capture, context: .run)) }),
                       .perform(.capture, .run))
    }

    func testEscBacksOutOneLevel() {
        XCTAssertEqual(KeyResolver.resolve(.escape, ActionState()), .swallow, "nothing to back out of")
        XCTAssertEqual(KeyResolver.resolve(.escape, onScreen()), .perform(.back, .global))
        XCTAssertEqual(KeyResolver.resolve(.escape, onScreen { $0.pane = .sidebar; $0.helpOpen = true }), .perform(.back, .global))
    }

    // MARK: - The hint bar

    func testTheHintBarShowsOnlyWhatWorksInTheRunsList() {
        var s = ActionState()
        s.runCount = 4
        s.available = [HandlerKey(id: .moveDown, context: .sidebar), HandlerKey(id: .moveUp, context: .sidebar)]
        let bar = HintBar.content(s)
        XCTAssertEqual(bar.mode, .normal)
        XCTAssertEqual(bar.context, "runs")
        XCTAssertEqual(bar.hints.map(\.key), ["↑↓", "⏎", "/", "?"])
        XCTAssertEqual(bar.hints.map(\.title), ["runs", "open", "search", "more"])
        XCTAssertEqual(HintBar.trailing(s)?.key, "⌘K")
    }

    func testTheHintBarOnTheScreen() {
        let bar = HintBar.content(onScreen())
        XCTAssertEqual(bar.context, "screen")
        let keys = bar.hints.map(\.key)
        XCTAssertEqual(keys, ["space", "← →", "z", "/", "tab", "esc", "?"])
        XCTAssertFalse(keys.contains("a"), "accept shown with no verdict to review")
        XCTAssertFalse(keys.contains("t"), "take control shown with no machine")
        XCTAssertEqual(keys.last, "?")

        let reviewing = HintBar.content(onScreen { $0.verdictOpenForReview = true })
        // A verdict to review comes first: it is what the person is there to do.
        XCTAssertEqual(Array(reviewing.hints.map(\.key).prefix(2)), ["a", "d"])
    }

    func testTheHintBarWhileTyping() {
        let bar = HintBar.content(onScreen { $0.responder = .text })
        XCTAssertEqual(bar.mode, .typing)
        XCTAssertEqual(bar.hints.map(\.key), ["⏎", "⇧⏎", "esc"])
        XCTAssertEqual(bar.hints.map(\.title), ["send", "newline", "leave"])
    }

    /// The run search and a dispute's reason do not send on Return: only leaving is offered.
    func testAFieldThatDoesNotSendOffersOnlyLeaving() {
        let bar = HintBar.content(onScreen { $0.responder = .text; $0.typingSends = false })
        XCTAssertEqual(bar.mode, .typing)
        XCTAssertEqual(bar.hints.map(\.key), ["esc"])
    }

    func testTheHintBarAsksBeforeDestroyingAndCountsDownAnUndo() {
        let asking = HintBar.content(onScreen { $0.confirmingDestroy = true })
        XCTAssertEqual(asking.mode, .confirm)
        XCTAssertEqual(asking.hints.map(\.key), ["⏎", "esc"])

        let undo = HintBar.content(onScreen { $0.undoSeconds = 4; $0.undoWord = "accepting" })
        XCTAssertEqual(undo.undo, UndoHint(word: "accepting", seconds: 4))

        let waitingForG = HintBar.content(onScreen { $0.pendingPrefix = .char("g") })
        XCTAssertEqual(waitingForG.mode, .prefix)
        XCTAssertEqual(Set(waitingForG.hints.map(\.key)), ["v", "s", "t", "r", "c"])
    }

    // MARK: - Zoom (layer 3)

    /// `z` zooms the focused pane, in every pane, and only with a run open.
    func testZZoomsTheFocusedPane() {
        let spec = ActionRegistry.spec(.zoom)
        XCTAssertEqual(spec.keyLabel, "z")
        XCTAssertEqual(Set(spec.contexts), [.sidebar, .screen, .steps, .conversation])
        XCTAssertFalse(spec.destructive)
        XCTAssertEqual(spec.menu, .view)
        XCTAssertTrue(spec.inPalette)
        XCTAssertEqual(KeyResolver.resolve(chord("z"), onScreen()), .perform(.zoom, .screen))
        XCTAssertEqual(KeyResolver.resolve(chord("z"), onScreen { $0.stage = .steps }), .perform(.zoom, .steps))
        XCTAssertEqual(KeyResolver.resolve(chord("z"), onScreen { $0.pane = .conversation }), .perform(.zoom, .conversation))
        // No run open: nothing to zoom, the key does nothing.
        var runs = ActionState()
        runs.runCount = 3
        XCTAssertEqual(KeyResolver.resolve(chord("z"), runs), .swallow)
        XCTAssertEqual(ActionRules.whyDisabled(.zoom, runs), "Open a run first")
    }

    /// While zoomed the hint bar says `z restore` and names the zoomed pane; esc restores
    /// before anything else it would do.
    func testTheHintBarWhileZoomed() {
        let zoomed = onScreen { $0.zoomed = .screen }
        let bar = HintBar.content(zoomed)
        XCTAssertEqual(bar.context, "screen zoomed")
        XCTAssertEqual(bar.hints.first { $0.id == .zoom }?.title, "restore")
        XCTAssertEqual(HintBar.content(onScreen()).hints.first { $0.id == .zoom }?.title, "zoom")
        XCTAssertTrue(ActionRules.canGoBack(zoomed))
        XCTAssertEqual(KeyResolver.resolve(.escape, zoomed), .perform(.back, .global))
        XCTAssertEqual(KeyResolver.resolve(chord("z"), zoomed), .perform(.zoom, .screen))
        // Typing and driving own their keys: `z` types, or goes to the guest.
        XCTAssertEqual(KeyResolver.resolve(chord("z"), onScreen { $0.responder = .text }), .pass)
        XCTAssertEqual(KeyResolver.resolve(chord("z"), onScreen { $0.driving = true; $0.responder = .guest }), .pass)
    }

    /// A narrow window shows one pane: the hint bar leads with it and `tab` names the next.
    func testTheNarrowHintBarNamesThePaneAndTheNext() {
        let narrow = onScreen { $0.widthClass = .narrow }
        let bar = HintBar.content(narrow)
        XCTAssertEqual(bar.context, "screen")
        XCTAssertEqual(bar.hints.first { $0.id == .nextPane }?.title, "→ conversation")
        let onConversation = HintBar.content(onScreen { $0.widthClass = .narrow; $0.pane = .conversation })
        XCTAssertEqual(onConversation.hints.first { $0.id == .nextPane }?.title, "→ runs")
        // Wide windows keep the short word.
        XCTAssertEqual(HintBar.content(onScreen()).hints.first { $0.id == .nextPane }?.title, "pane")
    }

    /// The runs opened over a folded window: esc puts them away.
    func testEscPutsAwayTheRunsOpenedOverTheRun() {
        let overlay = onScreen { $0.widthClass = .medium; $0.pane = .sidebar }
        XCTAssertTrue(overlay.runsOverlay)
        XCTAssertTrue(ActionRules.canGoBack(overlay))
    }

    func testHelpToggleReadsMoreOrLess() {
        XCTAssertEqual(HintBar.content(onScreen()).hints.last?.title, "more")
        XCTAssertEqual(HintBar.content(onScreen { $0.helpOpen = true }).hints.last?.title, "less")
    }

    // MARK: - Help

    func testHelpListsEveryKeyByGroupDimWhereItDoesNothing() {
        let groups = KeyHelp.groups(onScreen())
        XCTAssertEqual(groups.map(\.group), ActionGroup.allCases)
        let hints = groups.flatMap(\.hints)
        XCTAssertEqual(hints.first { $0.id == .play }?.enabled, true)
        XCTAssertEqual(hints.first { $0.id == .accept }?.enabled, false)
        XCTAssertNil(hints.first { $0.id == .paletteDown }, "the palette's own keys are in the palette")
        // Every key a person can press outside a mode is in the help.
        for spec in ActionRegistry.all where !spec.keys.isEmpty && spec.contexts != [.palette] && spec.contexts != [.confirm] {
            if [.moveUp, .previousFrame].contains(spec.id) { continue }
            XCTAssertTrue(hints.contains { $0.id == spec.id }, "\(spec.id) is missing from the help")
        }
    }

    // MARK: - Fuzzy and the palette

    func testFuzzyMatchesCharactersInOrder() {
        XCTAssertNotNil(Fuzzy.score("tc", in: "Take control"))
        XCTAssertNotNil(Fuzzy.score("TAKE", in: "take control"), "matching ignores case")
        XCTAssertNotNil(Fuzzy.score("take ctl", in: "Take control"), "spaces in the query are ignored")
        XCTAssertNil(Fuzzy.score("tc", in: "Accept"), "out of order")
        XCTAssertNil(Fuzzy.score("xyz", in: "Take control"))
        XCTAssertEqual(Fuzzy.score("", in: "anything"), 0)
    }

    func testFuzzyRanksRunsAndWordStartsAboveScatteredLetters() throws {
        let run = try XCTUnwrap(Fuzzy.score("cap", in: "Capture a screenshot"))
        let scattered = try XCTUnwrap(Fuzzy.score("cap", in: "Clear a pane"))
        XCTAssertGreaterThan(run, scattered)
        let wordStarts = try XCTUnwrap(Fuzzy.score("gs", in: "Go to the steps"))
        let inside = try XCTUnwrap(Fuzzy.score("gs", in: "Things"))
        XCTAssertGreaterThan(wordStarts, inside)
    }

    func testThePaletteListsEveryCommandEnabledFirstWithWhyNot() {
        let items = PaletteModel.items(query: "", onScreen())
        let listed = Set(items.map(\.id))
        XCTAssertEqual(listed, Set(ActionRegistry.all.filter(\.inPalette).map(\.id)))
        XCTAssertFalse(listed.contains(.paletteRun))
        XCTAssertFalse(listed.contains(.palette))
        XCTAssertFalse(listed.contains(.giveBack), "giving back control is a click, by design")
        let firstDisabled = try? XCTUnwrap(items.firstIndex { !$0.enabled })
        let lastEnabled = items.lastIndex { $0.enabled }
        if let firstDisabled, let lastEnabled { XCTAssertLessThan(lastEnabled, firstDisabled, "a disabled command above an enabled one") }
        for item in items where !item.enabled {
            XCTAssertNotNil(item.reason, "\(item.id) is dim without saying why")
        }
        XCTAssertEqual(items.first { $0.id == .accept }?.reason, "No verdict is open for review")
    }

    func testThePaletteFiltersAsYouType() {
        let items = PaletteModel.items(query: "steps", onScreen())
        XCTAssertEqual(items.first?.id, .goSteps)
        XCTAssertTrue(items.allSatisfy { Fuzzy.score("steps", in: $0.title) != nil })
        XCTAssertEqual(PaletteModel.items(query: "zqxw", onScreen()), [], "the empty state")
        XCTAssertEqual(items.first?.key, "g s", "the key is shown, to teach it")
    }

    func testThePaletteSelectionStaysOnTheList() {
        XCTAssertEqual(PaletteModel.move(0, by: -1, count: 5), 0)
        XCTAssertEqual(PaletteModel.move(4, by: 1, count: 5), 4)
        XCTAssertEqual(PaletteModel.move(2, by: 1, count: 5), 3)
        XCTAssertEqual(PaletteModel.move(3, by: 1, count: 0), 0)
    }
}

/// The accept and dispute undo window, on a clock the test holds (ADR 0005).
final class UndoWindowTests: XCTestCase {
    private let start = Date(timeIntervalSince1970: 1_000)

    func testTheWindowIsTheTokensFiveSeconds() {
        XCTAssertEqual(UndoWindow<Int>.length, 5)
        XCTAssertEqual(DesignData.shared.tokens.motion.undoMs, 5000)
    }

    func testNothingIsDueUntilTheWindowEnds() {
        var window = UndoWindow<Int>()
        XCTAssertNil(window.start(1, now: start))
        XCTAssertNil(window.takeDue(now: start))
        XCTAssertNil(window.takeDue(now: start.addingTimeInterval(4.9)))
        XCTAssertNotNil(window.pending, "taking early dropped the choice")
        XCTAssertEqual(window.takeDue(now: start.addingTimeInterval(5)), 1)
        XCTAssertNil(window.pending)
        XCTAssertNil(window.takeDue(now: start.addingTimeInterval(9)), "sent twice")
    }

    func testTheCountdownReadsFiveToOne() {
        var window = UndoWindow<Int>()
        window.start(1, now: start)
        XCTAssertEqual(window.seconds(now: start), 5)
        XCTAssertEqual(window.seconds(now: start.addingTimeInterval(0.2)), 5)
        XCTAssertEqual(window.seconds(now: start.addingTimeInterval(1.0)), 4)
        XCTAssertEqual(window.seconds(now: start.addingTimeInterval(4.5)), 1)
        XCTAssertNil(window.seconds(now: start.addingTimeInterval(5)))
        XCTAssertNil(UndoWindow<Int>().seconds(now: start))
    }

    func testUndoTakesTheChoiceBackAndNothingIsEverDue() {
        var window = UndoWindow<Int>()
        window.start(7, now: start)
        XCTAssertEqual(window.undo(), 7)
        XCTAssertNil(window.undo(), "undone twice")
        XCTAssertNil(window.takeDue(now: start.addingTimeInterval(60)))
    }

    /// A second choice ends the first one's window: the first is handed back to send now.
    func testASecondChoiceSendsTheFirst() {
        var window = UndoWindow<Int>()
        window.start(1, now: start)
        XCTAssertEqual(window.start(2, now: start.addingTimeInterval(1)), 1)
        XCTAssertEqual(window.pending?.payload, 2)
        XCTAssertEqual(window.seconds(now: start.addingTimeInterval(1)), 5)
    }
}
