import Foundation
import XCTest

@testable import Companion

/// The top bar's More menu (companion ADR 0016): drawn by the app, its rows from the one
/// registry (ADR 0005), Builds and Updates with its news, Destroy set apart, and its own
/// keyboard while open.
@MainActor
final class MoreMenuTests: XCTestCase {
    /// A live run on its screen: a ready machine to capture and destroy, a recording to export.
    private func live(_ change: (inout ActionState) -> Void = { _ in }) -> ActionState {
        var s = ActionState()
        s.runOpen = true
        s.runCount = 2
        s.pane = .stage
        s.machineReady = true
        s.machineExists = true
        s.available = [
            HandlerKey(id: .capture, context: .run), HandlerKey(id: .exportRecording, context: .run),
            HandlerKey(id: .toggleConversation, context: .run), HandlerKey(id: .play, context: .screen),
        ]
        change(&s)
        return s
    }

    /// A finished run: its machine is gone and it has no recording.
    private func finished() -> ActionState {
        live {
            $0.machineReady = false
            $0.machineExists = false
            $0.available = [HandlerKey(id: .toggleConversation, context: .run)]
        }
    }

    // MARK: - The rows come from the registry

    func testALiveRunsMenuIsTheRegistrysMoreEntriesInSectionOrder() {
        let items = MoreMenu.items(live())
        XCTAssertEqual(items.map(\.id), [.capture, .exportRecording, .toggleConversation, .greenroom, .destroy])
        XCTAssertEqual(items.map(\.title),
                       ["Capture Screenshot", "Export Recording...", "Hide Conversation", "Builds and Updates...", "Destroy Machine..."])
        XCTAssertEqual(items.map(\.key), ["c", "e", "", "", "⌘⌫"])
        XCTAssertEqual(items.map(\.section), [.run, .run, .view, .app, .destructive])
        XCTAssertEqual(items.filter(\.destructive).map(\.id), [.destroy])
    }

    /// No second list: every row is a registry entry with `more`, titled and keyed as the
    /// menu bar and the palette show it.
    func testEveryRowIsARegistryEntryNamedAsTheMenuBarNamesIt() {
        let entries = ActionRegistry.all.filter { $0.more != nil }
        XCTAssertEqual(Set(ActionRegistry.moreEntries.map(\.id)), Set(entries.map(\.id)))
        let state = live()
        for item in MoreMenu.items(state) {
            let spec = ActionRegistry.spec(item.id)
            XCTAssertEqual(item.title, MenuTitles.title(spec, state), "\(item.id)")
            XCTAssertEqual(item.key, spec.keyLabel, "\(item.id)")
            XCTAssertNotNil(spec.menu, "\(item.id) is in the More menu but not the menu bar")
            XCTAssertTrue(spec.inPalette, "\(item.id) is in the More menu but not Cmd-K")
        }
    }

    func testTheMenuBarAndTheMenuFollowTheConversationTheSameWay() {
        let hidden = live { $0.conversationShown = false }
        XCTAssertEqual(MoreMenu.items(hidden).first { $0.id == .toggleConversation }?.title, "Show Conversation")
        XCTAssertEqual(MenuTitles.title(ActionRegistry.spec(.toggleConversation), hidden), "Show Conversation")
    }

    func testAFinishedRunOffersOnlyWhatStillApplies() {
        XCTAssertEqual(MoreMenu.items(finished()).map(\.id), [.toggleConversation, .greenroom])
    }

    /// A narrow window names the conversation in its pane switch.
    func testANarrowWindowLeavesTheConversationToThePaneSwitch() {
        let narrow = live { $0.widthClass = .narrow }
        XCTAssertFalse(MoreMenu.items(narrow).contains { $0.id == .toggleConversation })
    }

    func testALineGoesWhereTheSectionChanges() {
        let items = MoreMenu.items(live())
        XCTAssertEqual(items.indices.filter { MoreMenu.startsSection($0, in: items) }, [2, 3, 4])
    }

    // MARK: - Builds and Updates' news

    private func summary(_ headline: BuildsSummary.Headline, mismatch: Bool = false) -> BuildsSummary {
        BuildsSummary(headline: headline, detail: nil, mismatch: mismatch, canUpdate: true, whyNot: nil)
    }

    func testTheBadgeSaysUpdatesThenRebuildThenMismatch() {
        XCTAssertEqual(MoreBadge.of(summary(.updates(3))), .updates(3))
        XCTAssertEqual(MoreBadge.of(summary(.updates(3), mismatch: true)), .updates(3))
        XCTAssertEqual(MoreBadge.of(summary(.rebuild)), .rebuild)
        XCTAssertEqual(MoreBadge.of(summary(.upToDate, mismatch: true)), .mismatch)
        XCTAssertNil(MoreBadge.of(summary(.upToDate)))
        XCTAssertNil(MoreBadge.of(summary(.checking)))
        XCTAssertNil(MoreBadge.of(summary(.unknown("No source checkout is recorded."))))
        XCTAssertEqual([MoreBadge.updates(3), .updates(1), .rebuild, .mismatch].map(\.text), ["3 new", "1 new", "rebuild", "mismatch"])
        XCTAssertEqual(MoreBadge.updates(1).spoken, "1 update available")
    }

    func testOnlyBuildsAndUpdatesCarriesTheBadge() {
        let items = MoreMenu.items(live(), badge: .rebuild)
        XCTAssertEqual(items.filter { $0.badge != nil }.map(\.id), [.greenroom])
        XCTAssertEqual(items.first { $0.id == .greenroom }?.badge, .rebuild)
    }

    // MARK: - Keys

    func testDotOpensItOnARun() {
        XCTAssertEqual(KeyResolver.resolve(.char("."), live()), .perform(.more, .run))
        XCTAssertEqual(ActionRegistry.label(.more), ".")
        XCTAssertFalse(ActionRules.isEnabledAnywhere(.more, ActionState()), "no run, no More button")
    }

    func testWhileOpenItOwnsTheKeyboard() {
        let open = live { $0.moreOpen = true }
        XCTAssertEqual(ActionRules.contexts(open), [.more])
        XCTAssertEqual(KeyResolver.resolve(.down, open), .perform(.moreDown, .more))
        XCTAssertEqual(KeyResolver.resolve(KeyChord(key: .character("n"), control: true), open), .perform(.moreDown, .more))
        XCTAssertEqual(KeyResolver.resolve(.up, open), .perform(.moreUp, .more))
        XCTAssertEqual(KeyResolver.resolve(.enter, open), .perform(.moreRun, .more))
        XCTAssertEqual(KeyResolver.resolve(.space, open), .perform(.moreRun, .more), "not the screen's play")
        XCTAssertEqual(KeyResolver.resolve(.escape, open), .perform(.moreClose, .more))
        XCTAssertEqual(KeyResolver.resolve(.char("."), open), .perform(.moreClose, .more))
        XCTAssertEqual(KeyResolver.resolve(.char("t"), open), .swallow, "take control does not act under it")
        XCTAssertEqual(KeyResolver.resolve(.cmd("q"), open), .pass, "the menu bar still quits")
    }

    /// An item's own key runs it, as a native menu's key equivalent would.
    func testAnItemsKeyRunsIt() {
        let open = live { $0.moreOpen = true }
        XCTAssertEqual(KeyResolver.resolve(.char("c"), open), .perform(.capture, .run))
        XCTAssertEqual(KeyResolver.resolve(.char("e"), open), .perform(.exportRecording, .run))
        XCTAssertEqual(KeyResolver.resolve(KeyChord(key: .delete, command: true), open), .perform(.destroy, .run))
        // Not in the menu, so not a key there.
        let noMachine = live {
            $0.moreOpen = true
            $0.machineExists = false
            $0.available.remove(HandlerKey(id: .capture, context: .run))
        }
        XCTAssertEqual(KeyResolver.resolve(.char("c"), noMachine), .swallow)
        XCTAssertEqual(KeyResolver.resolve(KeyChord(key: .delete, command: true), noMachine), .pass, "a chord reaches the menu bar")
    }

    func testTheMovesStayOnTheList() {
        let items = MoreMenu.items(live())
        XCTAssertEqual(MoreMenu.move(nil, by: 1, in: items), .capture)
        XCTAssertEqual(MoreMenu.move(nil, by: -1, in: items), .destroy)
        XCTAssertEqual(MoreMenu.move(.capture, by: -1, in: items), .capture)
        XCTAssertEqual(MoreMenu.move(.destroy, by: 1, in: items), .destroy)
        XCTAssertEqual(MoreMenu.move(.exportRecording, by: 1, in: items), .toggleConversation)
        XCTAssertNil(MoreMenu.move(nil, by: 1, in: []))
        // A row that appears while open (the machine became ready) keeps the selection on its row.
        let later = MoreMenu.items(live())
        let before = MoreMenu.items(finished())
        XCTAssertEqual(MoreMenu.move(.greenroom, by: 1, in: before), .greenroom)
        XCTAssertEqual(MoreMenu.move(.greenroom, by: 1, in: later), .destroy)
        XCTAssertEqual(MoreMenu.move(.capture, by: 1, in: before), .toggleConversation, "a row that went away starts over")
    }

    func testTheHintBarShowsItsKeys() {
        let open = live { $0.moreOpen = true }
        let bar = HintBar.content(open)
        XCTAssertEqual(bar.context, "more")
        XCTAssertEqual(bar.hints.map(\.key), ["↑↓", "⏎", "esc"])
        XCTAssertEqual(bar.hints.map(\.title), ["move", "run", "close"])
        XCTAssertNil(HintBar.trailing(open))
    }

    func testTheHelpListsItsKeyButNotItsInsideKeys() {
        let keys = KeyHelp.groups(live()).flatMap(\.hints).compactMap(\.id)
        XCTAssertTrue(keys.contains(.more))
        XCTAssertFalse(keys.contains(.moreDown))
        XCTAssertFalse(keys.contains(.moreClose))
        XCTAssertFalse(PaletteModel.items(query: "", live()).contains { [.more, .moreRun].contains($0.id) })
    }

    // MARK: - The model

    /// A run open whose view offers the conversation, as `RunView` does.
    private func keyboard() -> KeyboardModel {
        let store = RunStore(client: StubURLProtocol.client { _ in .json("[]") })
        store.selectedRunId = "20260926-120000-aaaaaaaaaaaaaaaa"
        let keyboard = KeyboardModel(store: store)
        keyboard.pane = .stage
        keyboard.offer(UUID(), context: .run, ids: [.toggleConversation], perform: { _ in })
        return keyboard
    }

    func testByKeyTheFirstRowIsSelectedByClickNone() {
        let model = keyboard()
        XCTAssertTrue(model.handle(.char("."), responder: .other))
        XCTAssertTrue(model.moreOpen)
        XCTAssertEqual(model.moreSelection, .toggleConversation)
        XCTAssertTrue(model.handle(.escape, responder: .other))
        XCTAssertFalse(model.moreOpen)

        model.openMore(byKey: false)
        XCTAssertNil(model.moreSelection)
        model.perform(.moreUp)
        XCTAssertEqual(model.moreSelection, .greenroom, "up from none is the last row")
    }

    func testReturnRunsTheSelectionAndTheMenuCloses() {
        let model = keyboard()
        model.openMore(byKey: true)
        let items = model.moreItems()
        XCTAssertEqual(items.map(\.id), [.toggleConversation, .greenroom])
        model.perform(.moreDown)
        XCTAssertEqual(model.moreSelection, .greenroom)
        model.perform(.moreDown)
        XCTAssertEqual(model.moreSelection, .greenroom, "stays on the list")
        XCTAssertTrue(model.handle(.enter, responder: .other))
        XCTAssertFalse(model.moreOpen)
        XCTAssertTrue(model.greenroomOpen, "Builds and Updates ran")
    }

    func testDestroyFromTheMenuStillAsksInline() {
        let model = keyboard()
        let runId = model.store.selectedRunId
        // Destroy needs a machine; the menu's own rule is the registry's.
        XCTAssertFalse(model.moreItems().contains { $0.id == .destroy })
        model.openMore(byKey: true)
        model.perform(.destroy)
        XCTAssertFalse(model.moreOpen)
        XCTAssertEqual(model.confirmingDestroy, runId)
    }

    func testAnythingElseThatRunsPutsItAway() {
        let model = keyboard()
        model.openMore(byKey: true)
        model.perform(.palette)
        XCTAssertFalse(model.moreOpen)
        XCTAssertTrue(model.paletteOpen)
        model.closePalette()
        model.openMore(byKey: true)
        XCTAssertFalse(model.paletteOpen)
        model.focusPane(at: .zero)
        XCTAssertTrue(model.moreOpen, "a click outside only closes it, through the layer under it")
    }

    /// Opening it with no run open does nothing: there is no More button to hang from.
    func testNoRunNoMenu() {
        let model = KeyboardModel(store: RunStore(client: StubURLProtocol.client { _ in .json("[]") }))
        model.openMore(byKey: true)
        XCTAssertFalse(model.moreOpen)
    }

    // MARK: - No native menu in the window

    /// ADR 0016: a menu inside the window is drawn by the app. Only the menu bar
    /// (`CompanionApp.swift`) is AppKit's.
    func testNoViewDrawsANativeMenu() throws {
        let views = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
            .appendingPathComponent("Sources/Companion/Views")
        let files = try FileManager.default.contentsOfDirectory(at: views, includingPropertiesForKeys: nil)
            .filter { $0.pathExtension == "swift" }
        XCTAssertGreaterThan(files.count, 10)
        let pattern = try NSRegularExpression(pattern: #"\bMenu\s*[({]|\.contextMenu\b|\bNSMenu\b|\.menuStyle\("#)
        for file in files {
            // Code only: the doc comments may name what they replaced.
            let text = try String(contentsOf: file, encoding: .utf8)
                .split(separator: "\n").filter { !$0.trimmingCharacters(in: .whitespaces).hasPrefix("//") }
                .joined(separator: "\n")
            if pattern.firstMatch(in: text, range: NSRange(text.startIndex..., in: text)) != nil {
                XCTFail("\(file.lastPathComponent) draws a native menu in the window")
            }
        }
    }
}
