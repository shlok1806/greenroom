import AppKit
import SwiftUI
@testable import Companion

/// The run window's states (companion ADR 0019; Figma Mockups and Wireframes 01 to 15), each
/// rendered from a real store. Run records (steps, frames, screenshots) come from a daemon
/// serving copied runs at `GREENROOM_URL` (`greenroom serve -root <scratch> -tart
/// /usr/bin/false`, never at VMs) holding the TipSplit fail run and the WordCount pass run.
/// Summaries are the daemon's golden board, edited into each state by `StateFixtures`.
@MainActor
enum RunScenarios {
    typealias F = StateFixtures

    static func scenarios(base: URL?) -> [RedesignHarness.Scenario] {
        typealias S = RedesignHarness.Scenario
        let all: [RedesignHarness.Size] = [.regular, .compact, .wide]
        func at(_ state: F.State) -> Date { F.start.addingTimeInterval(state.at) }
        func stateScenario(_ name: String, _ state: F.State, sizes: [RedesignHarness.Size] = [.regular], dark: Bool = false,
                           boot: [String] = []) -> S {
            S(name: name, sizes: sizes, dark: dark, budget: state.budget, countFrom: 248) { size in
                await window(size, base: base, state: state, now: at(state), boot: boot)
            }
        }
        return [
            S(name: "r01-home-wide", sizes: [.wide], budget: .runVerdictWaiting, countFrom: 272) { size in
                await window(size, base: base, state: .failed, now: at(.failed))
            },
            stateScenario("r03-failed", .failed, sizes: all, dark: true),
            stateScenario("r02-live", .live, dark: true),
            S(name: "r04-passed", sizes: [.regular], budget: .runVerdictWaiting, countFrom: 248) { size in
                await window(size, base: base, state: .failed, select: F.wordCount, now: F.start.addingTimeInterval(6 * 60), stage: { board in
                    F.edit(&board, F.wordCount) { s in
                        s.checks.items = s.checks.items.map { item in
                            var item = item
                            item.picture = SummaryPicture(kind: "screenshot", file: "032-screenshot.png", step: 32)
                            return item
                        }
                    }
                })
            },
            stateScenario("r05-paused", .paused),
            stateScenario("r06-starting", .starting, boot: ["clone", "start", "agent", "ip"]),
            stateScenario("r07a-not-answering", .notAnswering),
            stateScenario("r07b-restarting", .restarting, boot: ["start"]),
            stateScenario("r08-resource-warning", .warning),
            S(name: "r09-take-control", sizes: [.regular], countFrom: 0) { size in
                await window(size, base: base, state: .live, now: at(.live), drive: true)
            },
            S(name: "r10-activity", sizes: [.regular], dark: true, countFrom: 248) { size in
                await window(size, base: base, state: .failed, now: at(.failed), prepare: { $0.activityOpen = true })
            },
            S(name: "r10b-composer", sizes: [.regular], countFrom: 248) { size in
                await window(size, base: base, state: .failed, now: at(.failed), prepare: { $0.openComposer(.reject) })
            },
            S(name: "r11-evidence", sizes: [.regular], countFrom: 248) { size in
                await window(size, base: base, state: .failed, now: at(.failed), prepare: { $0.evidenceOpen = true })
            },
            S(name: "r12-palette", sizes: [.regular], countFrom: 248) { size in
                await window(size, base: base, state: .failed, now: at(.failed), prepare: { $0.paletteOpen = true })
            },
            S(name: "r13a-no-runs", sizes: [.regular], budget: .firstLaunch, countFrom: 248) { size in
                await window(size, base: base, state: .failed, now: F.start, empty: true)
            },
            S(name: "r13b-offline", sizes: [.regular], budget: .firstLaunch, countFrom: 248) { size in
                await window(size, base: nil, state: .failed, now: F.start, unreachable: true)
            },
            S(name: "r14-settings", sizes: [.regular], countFrom: 248) { size in
                await window(size, base: base, state: .failed, now: at(.failed), prepare: { $0.settingsOpen = true })
            },
            stateScenario("r15-done", .done),
            // Redesign 7: the player and the inspector.
            S(name: "r16-more-menu", sizes: [.regular], dark: true, countFrom: 248) { size in
                await window(size, base: base, state: .failed, now: at(.failed), prepare: { shell in
                    shell.dropdowns.toggle("more", anchor: CGRect(x: size.width - 52, y: 12, width: 28, height: 28),
                                           items: moreItems(shell), width: 260)
                    shell.dropdowns.move(by: 1)
                })
            },
            S(name: "r17-speed-menu", sizes: [.regular], countFrom: 248) { size in
                await window(size, base: base, state: .failed, now: at(.failed), prepare: { shell in
                    shell.setSpeed(2)
                    shell.dropdowns.toggle("speed", anchor: CGRect(x: size.width - 470, y: size.height - 60, width: 30, height: 22),
                                           items: [1.0, 2, 4].map { v in DropdownItem(id: "s\(Int(v))", title: "\(Int(v))× speed", checked: v == 2) {} },
                                           width: 150)
                })
            },
            S(name: "r18-message-markdown", sizes: [.regular], dark: true, countFrom: 248) { size in
                await window(size, base: base, state: .failed, now: at(.failed), prepare: { shell in
                    shell.store.messages[F.tipSplit] = markdownConversation()
                    shell.show(.message)
                    shell.composer = .message
                })
            },
            S(name: "r19-message-streaming", sizes: [.regular], countFrom: 248) { size in
                await window(size, base: base, state: .live, now: at(.live), prepare: { shell in
                    let all = markdownConversation()
                    shell.store.messages[F.tipSplit] = Array(all.dropLast())
                    shell.noteMessages(Array(all.dropLast()), runId: F.tipSplit)
                    shell.store.messages[F.tipSplit] = all
                    shell.noteMessages(all, runId: F.tipSplit, now: Date().addingTimeInterval(0.4))
                    shell.show(.message)
                })
            },
            S(name: "r20-picture-only", sizes: [.regular, .compact], countFrom: 248) { size in
                await window(size, base: base, state: .failed, now: at(.failed), prepare: { $0.zoomed = true })
            },
            S(name: "r21-details", sizes: [.regular], countFrom: 248) { size in
                await window(size, base: base, state: .failed, now: at(.failed), prepare: { $0.detailsOpen = true })
            },
            S(name: "r22-scrubbed", sizes: [.regular, .compact], dark: true, countFrom: 248) { size in
                await window(size, base: base, state: .live, now: at(.live), prepare: { shell in
                    shell.seek(toStep: 9)
                })
            },
            S(name: "r23-activity-log", sizes: [.regular], countFrom: 248) { size in
                await window(size, base: base, state: .failed, now: at(.failed), prepare: { shell in
                    shell.activityOpen = true
                    shell.seek(toStep: 13)
                })
            },
            S(name: "r24-destroy-undo", sizes: [.regular], countFrom: 248) { size in
                await window(size, base: base, state: .failed, now: at(.failed), prepare: { shell in
                    shell.confirmingDestroy = true
                    shell.store.lastError = "greenroom answered 409: the Mac is busy restarting"
                })
            },
        ]
    }

    /// The More menu's rows as the toolbar builds them (the harness cannot click).
    static func moreItems(_ shell: ShellModel) -> [DropdownItem] {
        [
            DropdownItem(id: "details", title: "Run details", icon: .info) {},
            DropdownItem(id: "evidence", title: "Open the evidence", icon: .video, keys: "E") {},
            DropdownItem(id: "zoom", title: "Picture only", icon: .expand, keys: "Z") {},
            DropdownItem(id: "task", title: "New task for the verifier", icon: .message) {},
            DropdownItem(id: "palette", title: "Command palette", icon: .search, keys: "⌘K") {},
            DropdownItem(id: "capture", title: "Capture a screenshot", icon: .camera, keys: "C", separated: true) {},
            DropdownItem(id: "export", title: "Save the recording…", icon: .download) {},
            DropdownItem(id: "copy", title: "Copy run ID", icon: .copy) {},
            DropdownItem(id: "restart", title: "Restart the Mac", icon: .restart, separated: true) {},
            DropdownItem(id: "destroy", title: "Destroy the Mac…", icon: .trash, destructive: true) {},
        ]
    }

    /// A conversation with headings, lists, code, a table, a quote and a link.
    static func markdownConversation() -> [Message] {
        let at = F.start
        return [
            Message(seq: 1, at: at, from: .coder, kind: .task,
                    text: "Check **TipSplit** on screen: Bill 120, Tip 20%, People 3. *Each pays* should be `$48.00`."),
            Message(seq: 5, at: at.addingTimeInterval(90), from: .human, kind: .note, text: "Also try the 25% button, please."),
            Message(seq: 9, at: at.addingTimeInterval(200), from: .verifier, kind: .reply, text: """
            ## What I saw
            1. The tip reads **$24.00** at 20%, as expected.
            2. *Each pays* reads `$8.00`, not `$48.00`.

            | Field | Expected | Saw |
            | --- | --- | --- |
            | Tip | $24.00 | $24.00 |
            | Each pays | $48.00 | $8.00 |

            > The total is divided before the tip is added.

            ```swift
            let each = bill / Double(people) + tip   // should be (bill + tip) / people
            ```
            See [the source](https://github.com/shlok1806/greenroom) and step 13.
            """),
        ]
    }

    /// A window over a real store: the run records from the daemon, the summaries staged.
    static func window(_ size: RedesignHarness.Size, base: URL?, state: F.State, select: String = F.tipSplit, now: Date,
                       boot: [String] = [], drive: Bool = false, empty: Bool = false, unreachable: Bool = false,
                       prepare: @MainActor (ShellModel) -> Void = { _ in },
                       stage: (inout SummaryBoard) -> Void = { _ in }) async -> AnyView {
        let client: DaemonClient
        if unreachable || base == nil {
            let config = URLSessionConfiguration.ephemeral
            config.protocolClasses = [RefusingURLProtocol.self]
            client = DaemonClient(baseURL: DaemonClient.defaultBaseURL, session: URLSession(configuration: config))
        } else {
            client = DaemonClient(baseURL: base!)
        }
        let store = RunStore(client: client, controlClient: GrantingControlClient(), screenSource: SilentScreen())
        await store.resync()
        store.connected = store.reachable == true
        if !unreachable {
            if empty {
                store.board = SummaryBoard(groups: SummaryGroup.allCases.map { .init(id: $0, runs: []) },
                                           macs: SummaryMacs(free: 3, total: 3, text: "3 Macs free"))
            } else if let golden = RedesignHarness.goldenBoard() {
                var board = F.board(golden, state: state)
                stage(&board)
                store.board = board
                store.selectedRunId = select
                await store.select(select)
                if !boot.isEmpty {
                    store.details[select]?.machine = Machine(runId: select, name: "gr", image: "", status: .booting,
                                                             createdAt: now, dir: "",
                                                             boot: boot.map { BootPhase(phase: BootPhaseName(text: $0), at: now, seconds: $0 == boot.last ? nil : 3) })
                }
            }
        }
        let shell = ShellModel(store: store)
        if drive { await store.pilot(for: select).take() }
        prepare(shell)
        return AnyView(CompanionShell(shell: shell)
            .frame(width: size.width, height: size.height)
            .ignoresSafeArea()
            .environment(\.frozenNow, now))
    }
}
