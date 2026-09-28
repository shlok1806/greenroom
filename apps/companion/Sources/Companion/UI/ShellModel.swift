import AppKit
import Observation
import SwiftUI
import UniformTypeIdentifiers

/// The window's state beside the store (companion ADR 0019): which check is selected, where
/// the recording's playhead is, which panel is open, and every action the header, the menus,
/// the palette and the keys perform. The run state is the daemon's summary (`RunStore.board`);
/// this only holds what the person is looking at.
@Observable
@MainActor
final class ShellModel {
    let store: RunStore
    /// The app's own dropdowns (no NSMenu): the one open, drawn over the window.
    let dropdowns = DropdownCenter()

    /// The selected check per run; nil picks `CheckSelection.initial`.
    private(set) var checkSelection: [String: String] = [:]
    /// The recording's playhead per run, in seconds from the run's start; nil shows what the
    /// run shows by itself (the live screen, or the selected check's proof).
    private(set) var playheads: [String: TimeInterval] = [:]
    /// The recording plays (Space); it stops at the end, or goes back to live on a live run.
    private(set) var playing = false
    /// Runs whose live screen the person asked for (Live, L) although the run has an outcome:
    /// the Mac is still on, so its screen can be watched.
    private(set) var liveRequested: Set<String> = []
    /// 1x, 2x or 4x (F).
    private(set) var speed: Double = 1
    /// What the inspector column at the right of the player shows (redesign 7): the checks by
    /// default, Activity (A) or the conversation and composer (M). The player always stays.
    var inspectorTab: InspectorTab = .checks
    /// The player alone: the inspector folded away (Z).
    var zoomed = false
    var composer: ComposerMode?
    /// Bumped to move the keyboard into the composer.
    private(set) var composerFocus = 0
    var composerDraft = ""
    var sending = false
    var evidenceOpen = false
    var paletteOpen = false
    var settingsOpen = false
    /// The run's details popover.
    var detailsOpen = false
    /// Destroying the Mac asks first, inline under the header (never a dialog).
    var confirmingDestroy = false
    /// Runs whose "not answering" the person chose to wait out.
    private(set) var keptWaiting: Set<String> = []
    /// Runs whose resource warning the person dismissed.
    private(set) var dismissedWarnings: Set<String> = []
    /// The action on its way, so its button shows the spinner and cannot go twice.
    private(set) var busy: String?

    /// Per run, the newest message already on screen when the conversation was first shown:
    /// those show whole; newer ones stream in once.
    private(set) var messageBaseline: [String: Int] = [:]
    /// Messages streaming in now, by "run#seq", from when they arrived.
    private(set) var streamStarts: [String: Date] = [:]

    @ObservationIgnored private var playTask: Task<Void, Never>?
    @ObservationIgnored private var timelineCache: (key: String, value: RecordingTimeline)?

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
        let open: Set<SummaryState> = [.checking, .paused, .notAnswering, .restarting]
        return CheckSelection.initial(checks, checking: summary.map { open.contains($0.state) } ?? false)
    }

    var selectedCheck: SummaryCheck? { checks.first { $0.id == selectedCheckID } }

    /// The playhead the person set, in seconds.
    var playhead: TimeInterval? { runId.flatMap { playheads[$0] } }

    /// The frame under the playhead, when the person moved it; nil shows the default picture.
    var selectedFrame: String? {
        guard let playhead else { return nil }
        return timeline().frame(at: playhead)?.file
    }

    /// Whether the person holds this run's screen.
    var driving: Bool {
        guard let runId else { return false }
        return store.existingPilot(runId)?.active == true
    }

    /// The run's live screen should show: an open run with no outcome yet, or any run whose
    /// Mac is on once the person pressed Live; never while they scrub the recording.
    var showsLive: Bool {
        guard let s = summary, playhead == nil, s.machine.status == "on" else { return false }
        return s.state == .checking || s.state == .paused || liveRequested.contains(s.runId)
    }

    // MARK: - Moving

    func select(run id: String) {
        guard id != store.selectedRunId else { return }
        pause()
        store.selectedRunId = id
        composer = nil
        confirmingDestroy = false
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

    /// Shows a check's proof: its picture with its mark, the playhead at its step.
    func select(check id: String) {
        guard let runId else { return }
        pause()
        checkSelection[runId] = id
        playheads[runId] = nil
        liveRequested.remove(runId)
    }

    func moveCheck(by delta: Int) {
        if let next = CheckSelection.step(from: selectedCheckID, by: delta, in: checks) { select(check: next) }
    }

    // MARK: - The recording

    /// Whether the run's Mac is on: its bar grows to now and the transport offers Live.
    static func isLive(_ s: Summary) -> Bool {
        s.machine.status == "on" && s.endedAt == nil
    }

    /// The open run's recording timeline, as of `now`. Built once per change of the record
    /// (and once a second while live), since playback asks for it ten times a second.
    func timeline(now: Date = Date()) -> RecordingTimeline {
        guard let s = summary else { return .empty }
        let frames = store.frames[s.runId] ?? []
        let steps = store.steps[s.runId] ?? []
        let live = Self.isLive(s)
        let key = "\(s.runId)|\(frames.count)|\(frames.last?.file ?? "")|\(steps.count)|\(steps.last?.seq ?? 0)|"
            + "\(s.checks.items.hashValue)|\(live)|\(s.startedAt.timeIntervalSince1970)|\(live ? Int(now.timeIntervalSince1970) : 0)"
        if let cached = timelineCache, cached.key == key { return cached.value }
        let made = RecordingTimeline.make(frames: frames, steps: steps, checks: s.checks.items,
                                          start: s.startedAt > .epoch ? s.startedAt : nil, live: live, now: now)
        timelineCache = (key, made)
        return made
    }

    /// Where the bar's playhead is: the person's, else the moment the picture shows (the end
    /// while live, the selected check's proof, else the newest frame).
    func currentSeconds(_ t: RecordingTimeline) -> TimeInterval {
        if let playhead { return min(playhead, t.duration) }
        if showsLive { return t.duration }
        if let step = selectedCheck?.picture?.step ?? selectedCheck?.step, let at = t.seconds(ofStep: step) { return at }
        if let file = summary?.lastFrame?.file, let at = t.seconds(ofFrame: file) { return at }
        return t.duration
    }

    /// The step under the playhead: the Activity row that shows as current.
    func currentStep(_ t: RecordingTimeline) -> Int? {
        if playhead == nil, showsLive { return t.marks.last?.step }
        return t.step(at: currentSeconds(t))
    }

    /// Moves the playhead; the picture follows.
    func seek(to seconds: TimeInterval) {
        guard let runId else { return }
        playheads[runId] = min(max(0, seconds), timeline().duration)
    }

    /// Shows a step: the picture at its start, paused. Activity's rows and the bar's marks.
    func seek(toStep step: Int) {
        pause()
        if let at = timeline().seconds(ofStep: step) { seek(to: at) }
    }

    /// Left and Right: one recorded frame along, paused.
    func moveFrame(by delta: Int) {
        let t = timeline()
        guard let next = t.stepFrame(from: currentSeconds(t), by: delta) else { return }
        pause()
        seek(to: next)
    }

    /// N: the next failure along the bar, wrapping; shift-N the one before. A failed check's
    /// proof selects the check, so its picture shows with its mark.
    func jumpToFailure(forward: Bool = true) {
        let t = timeline()
        let now = currentSeconds(t)
        guard let mark = forward ? t.nextFailure(after: now) : t.previousFailure(before: now) else { return }
        pause()
        if let number = mark.check, checks.indices.contains(number - 1) {
            let check = checks[number - 1]
            if check.state == .fail, (check.picture?.step ?? check.step) == mark.step {
                select(check: check.id)
                return
            }
        }
        seek(to: mark.at)
    }

    /// Back to the live edge: the live screen while the Mac is on, else the run's own picture.
    func goLive() {
        pause()
        guard let runId else { return }
        playheads[runId] = nil
        if let s = summary, Self.isLive(s) { liveRequested.insert(runId) }
    }

    func togglePlay() {
        if playing { pause() } else { play() }
    }

    /// Plays the recording from the playhead (from the start when it is at the end or live),
    /// skipping stretches where nothing happened.
    func play() {
        guard let runId else { return }
        let t = timeline()
        guard !t.frames.isEmpty else { return }
        var start = currentSeconds(t)
        if (playhead == nil && showsLive) || start >= t.duration - 0.5 { start = 0 }
        playheads[runId] = start
        playing = true
        playTask?.cancel()
        playTask = Task { [weak self] in
            var last = Date()
            while !Task.isCancelled {
                try? await Task.sleep(for: .milliseconds(100))
                guard let self, self.playing, self.runId == runId else { return }
                let now = Date()
                let elapsed = now.timeIntervalSince(last)
                last = now
                let t = self.timeline(now: now)
                if let next = t.advance(from: self.playheads[runId] ?? 0, by: elapsed, speed: self.speed) {
                    self.playheads[runId] = next
                } else {
                    self.playing = false
                    self.playheads[runId] = t.live ? nil : t.duration
                    return
                }
            }
        }
    }

    func pause() {
        playing = false
        playTask?.cancel()
        playTask = nil
    }

    func setSpeed(_ value: Double) {
        speed = [1, 2, 4].contains(value) ? value : 1
    }

    /// 1x, 2x, 4x, then 1x again.
    func toggleSpeed() {
        speed = speed >= 4 ? 1 : speed * 2
    }

    // MARK: - The conversation

    /// Notes the messages the conversation shows: the first time for a run, all of them are
    /// already seen; after that, an agent's new message streams in once.
    func noteMessages(_ messages: [Message], runId: String, now: Date = Date()) {
        let top = messages.map(\.seq).max() ?? 0
        guard let base = messageBaseline[runId] else {
            messageBaseline[runId] = top
            return
        }
        for message in messages where message.seq > base && (message.from == .verifier || message.from == .coder) {
            let key = "\(runId)#\(message.seq)"
            if streamStarts[key] == nil { streamStarts[key] = now }
        }
        if top > base { messageBaseline[runId] = top }
    }

    /// When a message began streaming in; nil shows it whole.
    func streamStart(runId: String, seq: Int) -> Date? { streamStarts["\(runId)#\(seq)"] }

    /// Ends a message's reveal (it finished, or the person clicked it).
    func finishStream(runId: String, seq: Int) {
        streamStarts["\(runId)#\(seq)"] = nil
    }

    // MARK: - Panels

    /// Activity shows in the inspector (the player stays beside it).
    var activityOpen: Bool {
        get { inspectorTab == .activity && !zoomed }
        set {
            inspectorTab = newValue ? .activity : .checks
            if newValue { zoomed = false }
        }
    }

    /// A: Activity in the inspector, or back to the checks.
    func toggleActivity() {
        activityOpen.toggle()
        if !activityOpen { composer = nil }
    }

    func show(_ tab: InspectorTab) {
        inspectorTab = tab
        zoomed = false
        if tab == .message, composer == nil { openComposer(.message) }
    }

    /// Z: the player alone, or the inspector back.
    func toggleZoom() {
        zoomed.toggle()
    }

    func openComposer(_ mode: ComposerMode = .message) {
        inspectorTab = .message
        zoomed = false
        if composer != mode { composerDraft = "" }
        composer = mode
        composerFocus += 1
    }

    func closeOverlays() -> Bool {
        if dropdowns.isOpen { dropdowns.close(); return true }
        if detailsOpen { detailsOpen = false; return true }
        if paletteOpen { paletteOpen = false; return true }
        if settingsOpen { settingsOpen = false; return true }
        if confirmingDestroy { confirmingDestroy = false; return true }
        if evidenceOpen { evidenceOpen = false; return true }
        if zoomed { zoomed = false; return true }
        if composer != nil, composer != .message { composer = .message; composerDraft = ""; return true }
        if inspectorTab != .checks { inspectorTab = .checks; composer = nil; return true }
        return false
    }

    // MARK: - Actions

    /// Performs a summary action: the header's buttons, the menus, the palette and the keys all
    /// come here. Accept and Reject are held for their undo (`U`) before they go out.
    func perform(_ action: SummaryAction) {
        guard let runId, busy == nil else { return }
        switch action.id {
        case SummaryAction.accept:
            run(action.id) {
                await self.store.holdAccept(runId: runId)
                if self.store.heldVerdictChoice(runId) != nil { self.advanceAfterDecision(from: runId) }
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
    /// stays, plain. Take control shows on any run whose Mac is on, as the old window had it.
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

    /// Take control, when the header does not already offer it: any run whose Mac is on.
    func canTakeControl(_ s: Summary) -> Bool {
        guard s.machine.status == "on", !driving else { return false }
        let (primary, secondary) = actions(for: s)
        return !([primary].compactMap { $0 } + secondary).contains { $0.id == SummaryAction.takeControl }
    }

    func dismissWarning() {
        if let runId { dismissedWarnings.insert(runId) }
    }

    func warning(for s: Summary) -> String? {
        dismissedWarnings.contains(s.runId) ? nil : s.machine.warning
    }

    /// Sends what the composer holds, as the mode says. A rejection is held for its undo.
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
            case .task:
                sent = await store.send(runId: runId, kind: .task, text: text)
            case .reject:
                store.updateVerdictDraft(runId) {
                    $0.action = .reject
                    $0.reason = text
                }
                await store.submitVerdictAction(runId: runId)
                sent = store.heldVerdictChoice(runId) != nil
            case .answer:
                let question = (store.messages[runId] ?? []).last { $0.from == .verifier && $0.kind == .question }
                sent = await store.send(runId: runId, kind: .answer, text: text, replyTo: question?.seq)
            case .recheck:
                sent = await store.send(runId: runId, kind: .task, text: "Look again before this is trusted: \(text)")
            }
            sending = false
            if sent {
                composerDraft = ""
                if mode != .message && mode != .task { composer = nil }
                if mode == .reject { advanceAfterDecision(from: runId) }
            }
        }
    }

    /// The accept or rejection waiting out its undo, with its run's name and seconds left.
    func heldChoice(now: Date) -> (choice: PendingVerdictChoice, name: String, seconds: Int)? {
        guard let held = store.verdictUndo.pending?.payload, let seconds = store.verdictUndo.seconds(now: now) else { return nil }
        let name = store.board?.summary(held.runId)?.name ?? "the run"
        return (held, name, seconds)
    }

    /// U: takes back the accept or rejection before it goes out.
    func undoVerdictChoice() {
        guard let held = store.undoVerdictChoice() else { return }
        if held.runId != runId { select(run: held.runId) }
    }

    // MARK: - The Mac and the record (the old window's More menu)

    /// Capture a screenshot now (C): it lands in the record as a step.
    var canCapture: Bool { summary?.machine.status == "on" }

    func capture() {
        guard let runId, canCapture else { return }
        run("capture") { await self.store.screenshot(runId: runId) }
    }

    /// Whether the run recorded a video to save.
    var canExport: Bool { summary?.lastFrame != nil || !(runId.flatMap { store.frames[$0] } ?? []).isEmpty }

    /// Saves the run's recording as an .mp4 where the person picks.
    func exportRecording() {
        guard let runId, canExport else { return }
        run("export") {
            guard let data = await self.store.recording(runId: runId) else { return }
            let panel = NSSavePanel()
            panel.nameFieldStringValue = "\(runId).mp4"
            panel.allowedContentTypes = [.mpeg4Movie]
            guard panel.runModal() == .OK, let url = panel.url else { return }
            do {
                try data.write(to: url)
            } catch {
                self.store.report(error)
            }
        }
    }

    /// Whether the run still has a Mac to destroy.
    var canDestroy: Bool {
        guard let s = summary else { return false }
        return s.machine.isUp || s.machine.status == "not running"
    }

    func destroy() {
        guard let runId else { return }
        confirmingDestroy = false
        if driving { Task { await store.pilot(for: runId).release() } }
        run("destroy") { await self.store.destroy(runId: runId) }
    }

    /// Reads everything again from Greenroom.
    func refresh() {
        Task { _ = await store.resync() }
    }

    func copyRunID() {
        guard let runId else { return }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(runId, forType: .string)
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

/// The inspector column's tabs.
enum InspectorTab: String, CaseIterable, Sendable {
    case checks, activity, message

    var title: String {
        switch self {
        case .checks: "Checks"
        case .activity: "Activity"
        case .message: "Message"
        }
    }
}

/// What the composer sends.
enum ComposerMode: Hashable, Sendable {
    /// A note to the verifier; it answers.
    case message
    /// A new task: the verifier starts a turn on it.
    case task
    /// A dispute of the current verdict, with the reason.
    case reject
    /// The answer to the verifier's question.
    case answer
    /// A task asking the verifier to look again at an accepted verdict.
    case recheck

    var placeholder: String {
        switch self {
        case .message: "Message the verifier"
        case .task: "A new task for the verifier"
        case .reject: "Why is it wrong? The verifier looks again."
        case .answer: "Answer the verifier"
        case .recheck: "What should it look at again?"
        }
    }

    var send: String {
        switch self {
        case .message, .answer: "Send"
        case .task: "Start"
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
