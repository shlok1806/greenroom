import AppKit
import Observation
import SwiftUI

/// The window's state beside the store (companion ADR 0019): which check and frame are
/// selected, which panel is open, and every action the header, the menus, the palette and the
/// keys perform. The run state is the daemon's summary (`RunStore.board`); this only holds
/// what the person is looking at.
@Observable
@MainActor
final class ShellModel {
    let store: RunStore

    /// The selected check per run; nil picks `CheckSelection.initial`.
    private(set) var checkSelection: [String: String] = [:]
    /// A frame the person picked in the filmstrip, per run; nil shows the check's proof.
    private(set) var frameSelection: [String: String] = [:]
    var activityOpen = false
    var composer: ComposerMode?
    /// Bumped to move the keyboard into the composer.
    private(set) var composerFocus = 0
    var composerDraft = ""
    var sending = false
    var evidenceOpen = false
    var paletteOpen = false
    var settingsOpen = false
    /// Runs whose "not answering" the person chose to wait out.
    private(set) var keptWaiting: Set<String> = []
    /// Runs whose resource warning the person dismissed.
    private(set) var dismissedWarnings: Set<String> = []
    /// The action on its way, so its button shows the spinner and cannot go twice.
    private(set) var busy: String?

    init(store: RunStore) {
        self.store = store
    }

    // MARK: - What is open

    var runId: String? { store.selectedRunId }

    var summary: Summary? { runId.flatMap { store.board?.summary($0) } }

    var checks: [SummaryCheck] { summary?.checks.items ?? [] }

    var selectedCheckID: String? {
        guard let runId else { return nil }
        if let chosen = checkSelection[runId], checks.contains(where: { $0.id == chosen }) { return chosen }
        return CheckSelection.initial(checks)
    }

    var selectedCheck: SummaryCheck? { checks.first { $0.id == selectedCheckID } }

    var selectedFrame: String? { runId.flatMap { frameSelection[$0] } }

    /// Whether the person holds this run's screen.
    var driving: Bool {
        guard let runId else { return false }
        return store.existingPilot(runId)?.active == true
    }

    /// The run's live screen should show: an open run with no outcome yet, unless the person
    /// picked a frame to look at.
    var showsLive: Bool {
        guard let s = summary, selectedFrame == nil else { return false }
        return s.machine.status == "on" && (s.state == .checking || s.state == .paused)
    }

    // MARK: - Moving

    func select(run id: String) {
        guard id != store.selectedRunId else { return }
        store.selectedRunId = id
        composer = nil
        Task { await store.select(id) }
    }

    /// Opens a run when none is: the first that needs you, else the newest.
    func selectDefaultRunIfNeeded() {
        guard store.selectedRunId == nil, let board = store.board else { return }
        if let first = board.runs.first { select(run: first.runId) }
    }

    func moveRun(by delta: Int) {
        guard let board = store.board else { return }
        let items = SidebarLayout.items(board, expanded: [.done], selected: runId)
        if let next = SidebarLayout.step(from: runId, by: delta, in: items) { select(run: next) }
    }

    func select(check id: String) {
        guard let runId else { return }
        checkSelection[runId] = id
        frameSelection[runId] = nil
    }

    func moveCheck(by delta: Int) {
        if let next = CheckSelection.step(from: selectedCheckID, by: delta, in: checks) { select(check: next) }
    }

    func select(frame file: String?) {
        guard let runId else { return }
        frameSelection[runId] = file
    }

    /// Left and Right step through the filmstrip's frames.
    func moveFrame(by delta: Int, in files: [String]) {
        guard !files.isEmpty else { return }
        let current = selectedFrame.flatMap { files.firstIndex(of: $0) } ?? (delta > 0 ? -1 : files.count)
        select(frame: files[min(max(0, current + delta), files.count - 1)])
    }

    // MARK: - Panels

    func toggleActivity() {
        activityOpen.toggle()
        if !activityOpen { composer = nil }
    }

    func openComposer(_ mode: ComposerMode = .message) {
        activityOpen = true
        if composer != mode { composerDraft = "" }
        composer = mode
        composerFocus += 1
    }

    func closeOverlays() -> Bool {
        if paletteOpen { paletteOpen = false; return true }
        if settingsOpen { settingsOpen = false; return true }
        if evidenceOpen { evidenceOpen = false; return true }
        if composer != nil { composer = nil; return true }
        if activityOpen { activityOpen = false; return true }
        return false
    }

    // MARK: - Actions

    /// Performs a summary action: the header's buttons, the menus, the palette and the keys all
    /// come here.
    func perform(_ action: SummaryAction) {
        guard let runId, busy == nil else { return }
        switch action.id {
        case SummaryAction.accept:
            run(action.id) {
                if await self.store.acceptVerdict(runId: runId) { self.advanceAfterDecision(from: runId) }
            }
        case SummaryAction.reject:
            openComposer(.reject)
        case SummaryAction.continue:
            run(action.id) { await self.store.continueVerifier(runId: runId) }
        case SummaryAction.answer:
            openComposer(.answer)
        case SummaryAction.recheck:
            openComposer(.recheck)
        case SummaryAction.restart:
            run(action.id) { await self.store.reboot(runId: runId) }
        case SummaryAction.keepWaiting:
            keptWaiting.insert(runId)
        case SummaryAction.takeControl:
            run(action.id) { await self.store.pilot(for: runId).take() }
        case SummaryAction.giveBack:
            run(action.id) { await self.store.pilot(for: runId).release() }
        default:
            break
        }
    }

    /// The header's buttons, after the person chose to wait out a stuck screen: Restart the Mac
    /// stays, plain.
    func actions(for s: Summary) -> (primary: SummaryAction?, secondary: [SummaryAction]) {
        var primary = s.primaryAction
        var secondary = s.secondaryActions
        if s.state == .notAnswering, keptWaiting.contains(s.runId) {
            secondary = [primary].compactMap { $0 }
            primary = nil
        }
        if driving {
            primary = SummaryAction(id: SummaryAction.giveBack, label: "Give control back")
        }
        return (primary, secondary.filter { $0.id != SummaryAction.keepWaiting || !keptWaiting.contains(s.runId) })
    }

    func dismissWarning() {
        if let runId { dismissedWarnings.insert(runId) }
    }

    func warning(for s: Summary) -> String? {
        dismissedWarnings.contains(s.runId) ? nil : s.machine.warning
    }

    /// Sends what the composer holds, as the mode says.
    func sendComposer() {
        guard let runId, let mode = composer else { return }
        let text = composerDraft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, !sending else { return }
        sending = true
        Task {
            let sent: Bool
            switch mode {
            case .message:
                sent = await store.send(runId: runId, kind: .note, text: text)
            case .reject:
                let seq = store.verdict(runId)?.seq
                sent = await store.send(runId: runId, kind: .dispute, text: text, replyTo: seq)
            case .answer:
                let question = (store.messages[runId] ?? []).last { $0.from == .verifier && $0.kind == .question }
                sent = await store.send(runId: runId, kind: .answer, text: text, replyTo: question?.seq)
            case .recheck:
                sent = await store.send(runId: runId, kind: .task, text: "Look again before this is trusted: \(text)")
            }
            sending = false
            if sent {
                composerDraft = ""
                if mode != .message { composer = nil }
                if mode == .reject { advanceAfterDecision(from: runId) }
            }
        }
    }

    /// Accept or Reject moves on to the next run that needs you, so a reviewer walks the list.
    private func advanceAfterDecision(from runId: String) {
        guard store.selectedRunId == runId, let board = store.board else { return }
        if let next = board.groups.first(where: { $0.id == .needsYou })?.runs.first(where: { $0.runId != runId }) {
            select(run: next.runId)
        }
    }

    private func run(_ id: String, _ work: @escaping @MainActor () async -> Void) {
        busy = id
        Task {
            await work()
            busy = nil
        }
    }
}

/// What the composer sends.
enum ComposerMode: Hashable, Sendable {
    /// A note to the verifier; it answers.
    case message
    /// A dispute of the current verdict, with the reason.
    case reject
    /// The answer to the verifier's question.
    case answer
    /// A task asking the verifier to look again at an accepted verdict.
    case recheck

    var placeholder: String {
        switch self {
        case .message: "Message the verifier. A new task continues the run."
        case .reject: "Why is it wrong? The verifier looks again."
        case .answer: "Answer the verifier"
        case .recheck: "What should it look at again?"
        }
    }

    var send: String {
        switch self {
        case .message, .answer: "Send"
        case .reject: "Reject"
        case .recheck: "Ask"
        }
    }
}

extension RunStore {
    /// Restarts the run's Mac on the same disk (daemon ADR 0004). True once the daemon took it.
    @discardableResult
    func reboot(runId: String) async -> Bool {
        do {
            try await client.reboot(runId: runId)
        } catch {
            report(error)
            return false
        }
        clearError()
        return true
    }
}
