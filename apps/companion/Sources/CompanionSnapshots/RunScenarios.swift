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
