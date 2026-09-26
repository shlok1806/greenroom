import AppKit
import Foundation
import Observation

/// What the store must fetch after applying an event. Returned rather than
/// performed so `apply` stays testable without a daemon.
enum Followup: Hashable, Sendable {
    case nothing
    case steps(String)
    case run(String)
    /// The held transcript no longer matches the daemon's: read it again.
    case messages(String)
}

/// One fetch needed to resync with the daemon.
enum Fetch: Hashable, Sendable {
    case runs
    case detail(String)
    case messages(String)
    case steps(String)
    case frames(String)
}

/// Jump the Screen stage to the first frame at or after `step` (ADR 0008).
/// `nonce` makes a repeated click on the same row seek again.
struct SeekRequest: Hashable, Sendable {
    var runId: String
    var step: Int
    var nonce: Int
    /// Raised from the verdict's evidence, so the stage offers the way back.
    var fromVerdict = false
    /// Show the step's record in Steps rather than its frame on the Screen.
    var inSteps = false
}

/// A verdict landing on screen: which run and verdict, and when the moment began.
struct VerdictMoment: Equatable, Sendable {
    let runId: String
    let seq: Int
    let start: Date
    /// Whether the decode and the draw play; under Reduce Motion the card shows at once.
    let plays: Bool
}

/// What a person has started on one run's verdict. The store holds it, not the card, so
/// the card can be rebuilt by the very change an action causes (accepting redraws it)
/// without losing the draft or orphaning a dialog. It belongs to one verdict as it stood:
/// a new verdict, or the same one closing, starts it empty, so a reason typed for one is
/// never sent against another, nor against a verdict that can no longer take it.
struct VerdictDraft: Equatable, Sendable {
    enum Action: Equatable, Sendable { case reject, recheck }

    var verdictSeq: Int?
    /// Whether the verdict was accepted or rejected when the draft began.
    var verdictClosed = false
    var action: Action?
    var reason = ""
    /// Accepting without having opened any cited evidence asks first.
    var openedEvidence = false
    /// The inline "accept anyway?" row is showing.
    var confirmingAccept = false
    /// The card's evidence is open. Per run and verdict and never saved, so a card opened
    /// on one verdict does not open every other one, nor come back open after a relaunch.
    var expanded = false
}

/// An LRU of decoded frame images, so scrubbing never refetches a frame.
@MainActor
final class FrameCache {
    private let capacity: Int
    private var order: [String] = []
    private var images: [String: NSImage] = [:]

    init(capacity: Int = 60) {
        self.capacity = capacity
    }

    func image(runId: String, file: String) -> NSImage? {
        let key = Self.key(runId, file)
        guard let image = images[key] else { return nil }
        touch(key)
        return image
    }

    func store(_ image: NSImage, runId: String, file: String) {
        let key = Self.key(runId, file)
        images[key] = image
        touch(key)
        while order.count > capacity {
            images.removeValue(forKey: order.removeFirst())
        }
    }

    private func touch(_ key: String) {
        order.removeAll { $0 == key }
        order.append(key)
    }

    private static func key(_ runId: String, _ file: String) -> String { "\(runId)/\(file)" }
}

/// Reconnect delays: 1, 2, 4 ... 10 s, back to 1 once a connection opens.
struct Backoff: Equatable, Sendable {
    static let ceiling = 10
    private(set) var seconds = 1

    /// The delay to wait now; the next one doubles.
    mutating func next() -> Int {
        defer { seconds = min(seconds * 2, Backoff.ceiling) }
        return seconds
    }

    mutating func reset() { seconds = 1 }
}

/// The single source of truth the views read. Views never fetch for themselves.
@Observable
@MainActor
final class RunStore: PilotHost {
    var runs: [RunSummary] = []
    var details: [String: RunDetail] = [:]
    var messages: [String: [Message]] = [:]
    var steps: [String: [Step]] = [:]
    var frames: [String: [Frame]] = [:]
    var connected = false
    /// Whether the last read of the run list answered; nil before the first one finishes.
    var reachable: Bool?
    /// The daemon's words when it answered that read with an HTTP error: it is running
    /// and refused, which is not the same as nothing answering.
    var refusal: String?
    var lastError: String?
    var selectedRunId: String? {
        didSet { if selectedRunId != nil, goneRun != nil { goneRun = nil } }
    }
    /// The title of the run that was open when the daemon came back without it, until
    /// another run is opened.
    private(set) var goneRun: String?
    var seekRequest: SeekRequest?
    /// Per run, the seq of the newest verdict whose message came in through the event
    /// stream while its transcript was held: what `VerdictLanding.lands` asks, so a verdict
    /// read by a resync or on opening the run never plays the moment.
    private(set) var liveVerdicts: [String: Int] = [:]
    /// The verdict-lands moment playing now (ADR 0006), started by the run view that saw
    /// the verdict land. The cards read it, so a card rebuilt mid-way carries on, not over.
    var verdictMoment: VerdictMoment?
    private(set) var verdictDrafts: [String: VerdictDraft] = [:]
    /// An accept or dispute shown as made but not yet sent: it waits out its undo
    /// (companion ADR 0005), since the daemon cannot take a recorded message back.
    private(set) var verdictUndo = UndoWindow<PendingVerdictChoice>()
    /// Sends the held choice when its window ends.
    @ObservationIgnored private var undoTimer: Task<Void, Never>?

    let client: DaemonClient
    /// The lease routes; the daemon client unless a test lends the screen without one.
    private let controlClient: any ControlClient
    /// The live screen route; the daemon client unless a test stands in for it.
    private let screenSource: any ScreenSource

    /// First task messages learned from transcripts, for a daemon whose run list has no
    /// `task` (before `RunSummary.task`). Merged into every run list read.
    private var learnedTasks: [String: String] = [:]
    /// Runs whose transcript is being read only to learn the task, so each is read once.
    private var taskFetches: Set<String> = []

    private let frameCache = FrameCache()
    /// The runs list's glyph thumbnails (the power-down still, ADR 0006).
    let thumbnails: RunThumbnails
    private var streamTask: Task<Void, Never>?
    /// Whether the event stream is up, so a failed read of the list is retried rather
    /// than left until the next drop.
    private var streamOpen = false
    /// The one retry of the run list, while the stream is up and the last read failed.
    private var listRetry: Task<Void, Never>?
    private var seekNonce = 0
    private var pilots: [String: ControlPilot] = [:]
    /// The newest read started for each piece, so an older answer landing late never
    /// replaces a newer one.
    private var reads: [Fetch: Int] = [:]
    private var readCounter = 0

    init(
        client: DaemonClient = DaemonClient(),
        controlClient: (any ControlClient)? = nil,
        screenSource: (any ScreenSource)? = nil
    ) {
        self.client = client
        self.controlClient = controlClient ?? client
        self.screenSource = screenSource ?? client
        thumbnails = RunThumbnails { [client] runId, file in try await client.frame(runId: runId, file: file) }
    }

    // MARK: - Lifecycle

    /// Keeps the event stream open, resyncing from the API after every drop:
    /// the stream is only a hint (ADR 0007).
    func start() {
        guard streamTask == nil else { return }
        streamTask = Task { [weak self] in
            await self?.runStream()
        }
    }

    func stop() {
        streamTask?.cancel()
        streamTask = nil
        endListRetries()
        connected = false
    }

    private func runStream() async {
        var backoff = Backoff()
        while !Task.isCancelled {
            // URLSession holds an SSE response back until the first bytes (a
            // ping, up to 15 s), so a daemon that just answered counts as live.
            connected = await resync()
            // The stream may stay open for hours; a failed read of the list must not
            // leave the window saying the daemon is down all that time.
            streamOpen = true
            retryTheListIfItFailed()
            defer { endListRetries() }
            do {
                for try await item in client.events() {
                    switch item {
                    case .opened:
                        connected = true
                        backoff.reset()
                    case .event(let event):
                        await handle(event)
                    }
                }
                connected = false
            } catch {
                connected = false
                if Task.isCancelled { return }
                report(error)
            }
            if Task.isCancelled { return }
            try? await Task.sleep(for: .seconds(backoff.next()))
        }
    }

    /// Starts the retry of the run list when its last read failed while the stream is up,
    /// unless one is already running.
    private func retryTheListIfItFailed() {
        guard streamOpen, reachable == false, listRetry == nil else { return }
        listRetry = Task { [weak self] in await self?.resyncUntilTheListAnswers() }
    }

    /// The stream ended, which resyncs by itself.
    private func endListRetries() {
        streamOpen = false
        listRetry?.cancel()
        listRetry = nil
    }

    /// While the stream is up, resyncs on the reconnect backoff until a read of the run
    /// list answers.
    private func resyncUntilTheListAnswers() async {
        var backoff = Backoff()
        while reachable != true {
            try? await Task.sleep(for: .seconds(backoff.next()))
            if Task.isCancelled { return }
            if await resync() { connected = true }
        }
        if !Task.isCancelled { listRetry = nil }
    }

    // MARK: - Fetching

    /// The run list always, plus every piece of the selected run. Run after
    /// each reconnect and on foregrounding, since a socket can look alive
    /// across sleep while the daemon restarted.
    nonisolated static func resyncPlan(selected: String?) -> [Fetch] {
        guard let selected else { return [.runs] }
        return [.runs, .detail(selected), .messages(selected), .steps(selected), .frames(selected)]
    }

    /// True when the daemon answered the run list.
    @discardableResult
    func resync() async -> Bool {
        let selected = selectedRunId
        let title = selected.map { RunTitle.short(task: run($0)?.task ?? RunTitle.task(in: messages[$0] ?? []), runId: $0) }
        var failures: [(fetch: Fetch, error: Error)] = []
        var reached = true
        for fetch in RunStore.resyncPlan(selected: selected) {
            guard let error = await attempt(fetch) else { continue }
            failures.append((fetch, error))
            if fetch == .runs { reached = false }
        }
        if reached {
            if let selected, let title, selectedRunId == selected, isGone(selected, failures: failures) {
                // The daemon came back without the open run: showing its old record, or
                // inventing one, would let a person act on a run that does not exist.
                selectedRunId = nil
                goneRun = title
                failures.removeAll { $0.fetch != .runs }
            }
            forgetUnselected()
        }
        settle(failures.map(\.error))
        return reached
    }

    /// Gone only when both say so: the fresh list lacks it and the run itself answers
    /// 404. A list read from before the run existed misses it too, but the run answers.
    private func isGone(_ runId: String, failures: [(fetch: Fetch, error: Error)]) -> Bool {
        guard run(runId) == nil else { return false }
        return failures.contains { failure in
            guard failure.fetch == .detail(runId), case DaemonError.status(404, _)? = failure.error as? DaemonError else { return false }
            return true
        }
    }

    /// The daemon may have restarted with other data, so what is held for runs that are
    /// not open is dropped rather than shown; opening one reads it afresh.
    private func forgetUnselected() {
        let keep = selectedRunId
        details = details.filter { $0.key == keep }
        messages = messages.filter { $0.key == keep }
        steps = steps.filter { $0.key == keep }
        frames = frames.filter { $0.key == keep }
        liveVerdicts = liveVerdicts.filter { $0.key == keep }
    }

    private func perform(_ fetch: Fetch) async {
        settle([await attempt(fetch)].compactMap { $0 })
    }

    /// Loads and stores one piece; `nil` on success.
    private func attempt(_ fetch: Fetch) async -> Error? {
        readCounter += 1
        let read = readCounter
        reads[fetch] = read
        func current() -> Bool { reads[fetch] == read }
        do {
            switch fetch {
            case .runs:
                do {
                    runs = withLearnedTasks(try await client.runs())
                    let listed = Set(runs.map(\.runId))
                    verdictDrafts = verdictDrafts.filter { listed.contains($0.key) }
                    reachable = true
                    refusal = nil
                    learnMissingTasks()
                } catch {
                    if !RunStore.isCancellation(error) {
                        reachable = false
                        // An HTTP error status is an answer, in the daemon's own words.
                        if case DaemonError.status? = error as? DaemonError {
                            refusal = (error as? LocalizedError)?.errorDescription
                        } else {
                            refusal = nil
                        }
                        retryTheListIfItFailed()
                    }
                    throw error
                }
            case .detail(let runId):
                let detail = try await client.run(runId)
                if current() { details[runId] = detail }
            case .messages(let runId):
                let held = try await client.messages(runId)
                guard current() else { break }
                messages[runId] = held
                learn(task: RunTitle.task(in: held), for: runId)
            case .steps(let runId):
                let held = StepLog.normalized(try await client.steps(runId))
                if current() { steps[runId] = held }
            case .frames(let runId):
                let held = try await client.frames(runId)
                if current() { frames[runId] = held }
            }
            return nil
        } catch {
            return error
        }
    }

    /// A later success must not hide an earlier failure.
    private func settle(_ failures: [Error]) {
        if let first = failures.first { report(first) } else { clearError() }
    }

    /// Each piece lands on its own, so a run without frames still shows its transcript.
    func select(_ runId: String) async {
        async let detail = attempt(.detail(runId))
        async let messageList = attempt(.messages(runId))
        async let stepList = attempt(.steps(runId))
        async let frameList = attempt(.frames(runId))
        settle([await detail, await messageList, await stepList, await frameList].compactMap { $0 })
    }

    // MARK: - Events

    private func handle(_ event: ServerEvent) async {
        switch apply(event) {
        case .nothing:
            break
        case .steps(let runId):
            await perform(.steps(runId))
        case .messages(let runId):
            await perform(.messages(runId))
        case .run(let runId):
            await perform(.runs)
            if details[runId] != nil || selectedRunId == runId {
                await perform(.detail(runId))
            }
        }
    }

    /// Merges one event into what is held and says what still has to be
    /// fetched. No I/O.
    @discardableResult
    func apply(_ event: ServerEvent) -> Followup {
        switch event {
        case .message(let runId, let message):
            guard var held = messages[runId] else {
                // Not open: the row still counts it, and a verdict changes its badge now.
                if let index = runs.firstIndex(where: { $0.runId == runId }) {
                    runs[index].messages += 1
                    runs[index].lastActivity = max(runs[index].lastActivity, message.at)
                }
                if message.kind == .task { learn(task: message.text, for: runId) }
                switch message.kind {
                case .verdict, .accept, .dispute: return .run(runId)
                default: return .nothing
                }
            }
            if let last = held.last, message.seq <= last.seq {
                // A repeat out of a reconnect changes nothing; a different message at a
                // held seq means the daemon's transcript is not the one held.
                return held.first(where: { $0.seq == message.seq }) == message ? .nothing : .messages(runId)
            }
            held.append(message)
            messages[runId] = held
            if message.kind == .verdict { liveVerdicts[runId] = message.seq }
            if let index = runs.firstIndex(where: { $0.runId == runId }) {
                runs[index].messages += 1
                runs[index].lastActivity = max(runs[index].lastActivity, message.at)
            }
            // A verdict or its closing changes the badge.
            switch message.kind {
            case .verdict, .accept, .dispute: return .run(runId)
            default: return .nothing
            }
        case .step(let runId, let seq, let step):
            if let index = runs.firstIndex(where: { $0.runId == runId }) {
                if steps[runId]?.contains(where: { $0.seq == seq }) != true {
                    runs[index].steps += 1
                }
                if let at = step?.at {
                    runs[index].lastActivity = max(runs[index].lastActivity, at)
                }
            }
            return steps[runId] == nil ? .nothing : .steps(runId)
        case .run(let lifecycle):
            if var machine = lifecycle.machine, details[lifecycle.runId] != nil {
                // A daemon before boot phases sends none; keep what the boot events said.
                if machine.boot.isEmpty, let held = details[lifecycle.runId]?.machine?.boot {
                    machine.boot = held
                }
                details[lifecycle.runId]?.machine = machine
            }
            return .run(lifecycle.runId)
        case .boot(let runId, let phase):
            // Only onto a held machine: a run opened later reads every phase from its detail.
            guard let held = details[runId]?.machine?.boot else { return .nothing }
            details[runId]?.machine?.boot = held.merging(phase)
            return .nothing
        case .frame(let runId, let frame):
            // The row's thumbnail follows a live run, no faster than `liveRefresh`.
            if let index = runs.firstIndex(where: { $0.runId == runId }),
               RunThumbnails.advances(runs[index].lastFrame, to: frame) {
                runs[index].lastFrame = frame
            }
            // Unloaded runs fetch the whole list when opened.
            guard var held = frames[runId], !held.contains(where: { $0.file == frame.file }) else {
                return .nothing
            }
            held.append(frame)
            frames[runId] = held
            if let index = runs.firstIndex(where: { $0.runId == runId }) {
                // Not activity: the recorder captures an idle machine too, and the
                // daemon's lastActivity counts only steps and messages.
                runs[index].frames = (runs[index].frames ?? 0) + 1
            }
            return .nothing
        }
    }

    // MARK: - Actions

    /// The human seat in the conversation (ADR 0006). True once the daemon took it.
    @discardableResult
    func send(runId: String, kind: MessageKind, text: String, replyTo: Int? = nil) async -> Bool {
        do {
            try await client.send(runId: runId, kind: kind, text: text, replyTo: replyTo)
        } catch {
            report(error)
            return false
        }
        await reloadTranscript(runId)
        await perform(.runs)
        return true
    }

    // MARK: - Verdict actions

    /// The draft for the run's current verdict; empty when it was made for another one.
    func verdictDraft(_ runId: String) -> VerdictDraft {
        let current = verdict(runId)
        let seq = current?.seq
        let closed = current.map { $0.status == .accepted || $0.status == .rejected } ?? false
        if let held = verdictDrafts[runId], held.verdictSeq == seq, held.verdictClosed == closed { return held }
        return VerdictDraft(verdictSeq: seq, verdictClosed: closed)
    }

    func updateVerdictDraft(_ runId: String, _ change: (inout VerdictDraft) -> Void) {
        var draft = verdictDraft(runId)
        change(&draft)
        verdictDrafts[runId] = draft
    }

    /// Accept, or ask first when none of the cited evidence was opened. The accept is
    /// held for its undo, never sent at once.
    func requestAccept(runId: String) async {
        guard let verdict = verdict(runId), verdict.status.isOpen else { return }
        if verdictDraft(runId).openedEvidence || citedSteps(runId).isEmpty && (verdict.evidence ?? []).isEmpty {
            await holdAccept(runId: runId)
        } else {
            updateVerdictDraft(runId) { $0.confirmingAccept = true }
        }
    }

    /// Shows the accept as made and sends it when the undo window ends.
    func holdAccept(runId: String) async {
        updateVerdictDraft(runId) { $0.confirmingAccept = false }
        guard let verdict = verdict(runId), verdict.status.isOpen, let seq = verdict.seq else { return }
        await hold(PendingVerdictChoice(runId: runId, verdictSeq: seq, kind: .accept))
    }

    /// The card's Reject (a dispute) is held for its undo; a re-check is a task and goes now.
    func submitVerdictAction(runId: String) async {
        let draft = verdictDraft(runId)
        let text = draft.reason.trimmingCharacters(in: .whitespacesAndNewlines)
        switch draft.action {
        case .reject?:
            guard !text.isEmpty, let verdict = verdict(runId), verdict.status.isOpen, let seq = verdict.seq else { return }
            await hold(PendingVerdictChoice(runId: runId, verdictSeq: seq, kind: .dispute(reason: text)))
        case .recheck?:
            await sendVerdictAction(runId: runId)
        case nil:
            return
        }
    }

    /// The choice waiting out its undo on this run's current verdict, if any.
    func heldVerdictChoice(_ runId: String) -> PendingVerdictChoice? {
        guard let held = verdictUndo.pending?.payload, held.runId == runId,
              held.verdictSeq == verdict(runId)?.seq else { return nil }
        return held
    }

    /// Takes the held choice back: nothing was sent. A dispute's reason stays in its draft.
    @discardableResult
    func undoVerdictChoice() -> PendingVerdictChoice? {
        undoTimer?.cancel()
        undoTimer = nil
        return verdictUndo.undo()
    }

    /// Sends the held choice if its window has ended. The timer calls this; tests pass a
    /// later `now`.
    func sendHeldVerdictChoice(now: Date = Date()) async {
        guard let due = verdictUndo.takeDue(now: now) else { return }
        undoTimer?.cancel()
        undoTimer = nil
        await send(due)
    }

    private func hold(_ choice: PendingVerdictChoice) async {
        let displaced = verdictUndo.start(choice, now: Date())
        undoTimer?.cancel()
        undoTimer = Task { [weak self] in
            try? await Task.sleep(for: .seconds(UndoWindow<PendingVerdictChoice>.length))
            guard !Task.isCancelled else { return }
            await self?.sendHeldVerdictChoice()
        }
        // A second choice ends the first one's window: it goes now.
        if let displaced { await send(displaced) }
    }

    /// Sends a choice whose window ended, if the verdict it was made on is still the open one.
    private func send(_ choice: PendingVerdictChoice) async {
        guard let verdict = verdict(choice.runId), verdict.seq == choice.verdictSeq, verdict.status.isOpen else {
            lastError = "The verdict changed, so your choice did not go out."
            return
        }
        switch choice.kind {
        case .accept:
            await acceptVerdict(runId: choice.runId)
        case .dispute(let reason):
            if await send(runId: choice.runId, kind: .dispute, text: reason, replyTo: choice.verdictSeq) {
                verdictDrafts[choice.runId] = nil
            }
        }
    }

    @discardableResult
    func acceptVerdict(runId: String) async -> Bool {
        updateVerdictDraft(runId) { $0.confirmingAccept = false }
        guard let verdict = verdict(runId), verdict.status.isOpen, let seq = verdict.seq else { return false }
        let sent = await send(runId: runId, kind: .accept, text: "accepted", replyTo: seq)
        if sent { verdictDrafts[runId] = nil }
        return sent
    }

    /// Sends the draft's reject or re-check with its reason.
    @discardableResult
    func sendVerdictAction(runId: String) async -> Bool {
        let draft = verdictDraft(runId)
        let text = draft.reason.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let action = draft.action, !text.isEmpty, let verdict = verdict(runId), let seq = verdict.seq else { return false }
        switch action {
        case .reject: guard verdict.status.isOpen else { return false }
        case .recheck:
            // A newer task is the verifier's current work; a re-check of the older verdict
            // would pull the turn away from it (issue #89).
            guard verdict.status == .accepted,
                  VerdictReview.newerTask(than: seq, in: messages[runId] ?? []) == nil else { return false }
        }
        let sent: Bool
        switch action {
        case .reject:
            sent = await send(runId: runId, kind: .dispute, text: text, replyTo: seq)
        case .recheck:
            // A task starts a verifier turn; no reply-to, since the accepted verdict is closed.
            sent = await send(runId: runId, kind: .task,
                              text: "Re-check the \(Chrome.outcomeTitle(verdict.verdict).lowercased()) verdict (message \(seq)) before it is trusted: \(text)")
        }
        if sent { verdictDrafts[runId] = nil }
        return sent
    }

    func screenshot(runId: String) async {
        do {
            try await client.screenshot(runId: runId)
            steps[runId] = StepLog.normalized(try await client.steps(runId))
            // The daemon writes the capture into the conversation. Re-read it
            // rather than trust the stream, which may be what is broken.
            await reloadTranscript(runId)
            clearError()
        } catch {
            report(error)
        }
    }

    func destroy(runId: String) async {
        do {
            try await client.destroy(runId: runId)
            await perform(.runs)
            details[runId] = try await client.run(runId)
            await reloadTranscript(runId)
            clearError()
        } catch {
            report(error)
        }
    }

    /// A screenshot artifact, decoded like a frame.
    func artifactImage(runId: String, name: String) async -> NSImage? {
        do {
            return await Self.decode(try await client.artifact(runId: runId, name: name))
        } catch {
            report(error)
            return nil
        }
    }

    func frameImage(runId: String, file: String) async -> NSImage? {
        if let cached = frameCache.image(runId: runId, file: file) { return cached }
        do {
            let data = try await client.frame(runId: runId, file: file)
            guard let image = await Self.decode(data) else { return nil }
            frameCache.store(image, runId: runId, file: file)
            return image
        } catch {
            report(error)
            return nil
        }
    }

    /// Parses only the JPEG header off the main actor; the pixels still decode
    /// on first draw. Deliberate: forcing it would hold ~3.1MB per cached
    /// frame (~189MB at capacity 60), so the cache must be bounded in bytes in
    /// the same change.
    private nonisolated static func decode(_ data: Data) async -> NSImage? {
        await Task.detached(priority: .userInitiated) { () -> NSImage? in
            guard let rep = NSBitmapImageRep(data: data) else { return nil }
            let image = NSImage(size: NSSize(width: rep.pixelsWide, height: rep.pixelsHigh))
            image.addRepresentation(rep)
            return image
        }.value
    }

    /// `nil` on failure, with the daemon's text (e.g. "ffmpeg not found") in `lastError`.
    func recording(runId: String) async -> Data? {
        do {
            return try await client.recording(runId: runId)
        } catch {
            report(error)
            return nil
        }
    }

    // MARK: - Driving the screen (ADR 0009)

    /// One pilot per run, not per view: the lease belongs to the run, so a
    /// redrawn tab keeps it and two views never both ask for it.
    func pilot(for runId: String) -> ControlPilot {
        if let held = pilots[runId] { return held }
        let pilot = ControlPilot(runId: runId, client: controlClient, host: self)
        pilots[runId] = pilot
        return pilot
    }

    /// The run's pilot if one was ever made, without making one: safe to read while a
    /// view draws.
    func existingPilot(_ runId: String) -> ControlPilot? {
        pilots[runId]
    }

    /// A fresh live screen (ADR 0011); the caller starts it and must stop it.
    func liveScreen(for runId: String) -> LiveScreen {
        LiveScreen(runId: runId, source: screenSource)
    }

    var holdsControl: Bool {
        pilots.values.contains { $0.active || $0.busy }
    }

    func releaseAllControl() async {
        await withTaskGroup(of: Void.self) { group in
            for pilot in pilots.values {
                group.addTask { await pilot.release() }
            }
        }
    }

    func reloadTranscript(_ runId: String) async {
        await perform(.messages(runId))
    }

    /// Re-reads the run so a stopped machine shows as not ready. Unsettled, so
    /// the lease error that caused it stays on screen.
    func reloadRun(_ runId: String) async {
        _ = await attempt(.detail(runId))
        _ = await attempt(.runs)
    }

    func requestSeek(runId: String, step: Int, fromVerdict: Bool = false, inSteps: Bool = false) {
        seekNonce += 1
        seekRequest = SeekRequest(runId: runId, step: step, nonce: seekNonce, fromVerdict: fromVerdict, inSteps: inSteps)
    }

    /// The step the stage is pointing at, highlighted in Steps and named in the status
    /// line. Cleared by `clearFocus` (the way back to the verdict) or a run change.
    var focusedStep: SeekRequest? {
        guard let request = seekRequest, request.runId == selectedRunId else { return nil }
        return request
    }

    func clearFocus() {
        seekRequest = nil
    }

    // MARK: - Derived

    var connection: ConnectionState {
        ConnectionState.derive(reachable: reachable, refusal: refusal, hasData: !runs.isEmpty)
    }

    /// Where the daemon is, as a person would type it.
    var daemonAddress: String {
        let url = client.baseURL
        guard let host = url.host() else { return url.absoluteString }
        return url.port.map { "\(host):\($0)" } ?? host
    }

    /// The one derived state every view shows for a run (companion ADR 0002). Uses the
    /// held records when the run is open, so its row and its header agree.
    func facts(_ runId: String, now: Date = Date()) -> RunFacts {
        RunFacts.derive(
            summary: run(runId),
            detail: details[runId],
            messages: messages[runId],
            steps: steps[runId],
            verdict: verdict(runId),
            now: now
        )
    }

    func run(_ runId: String) -> RunSummary? {
        runs.first { $0.runId == runId }
    }

    /// The newer of the detail's and the list's: a detail read before the verdict arrived
    /// must not hide the one the list now has. Contesting, accepting and rejecting keep
    /// the seq, so on a tie the copy further along (`progress`) is the newer one.
    func verdict(_ runId: String) -> VerdictState? {
        let detail = details[runId]?.verdict
        let listed = run(runId)?.verdict
        guard let detail, detail.status != .none else { return listed ?? detail }
        guard let listed else { return detail }
        let listedSeq = listed.seq ?? 0, detailSeq = detail.seq ?? 0
        if listedSeq != detailSeq { return listedSeq > detailSeq ? listed : detail }
        return Self.progress(listed.status) > Self.progress(detail.status) ? listed : detail
    }

    /// The message that proposed the current verdict, once the transcript holds it.
    func verdictMessage(_ runId: String) -> Message? {
        guard let seq = verdict(runId)?.seq else { return nil }
        return messages[runId]?.first { $0.seq == seq }
    }

    /// The steps the current verdict cites: its checks' evidence (root ADR 0024) and the
    /// free list's steps. The state's own list stands in until the message is held.
    func citedSteps(_ runId: String) -> [Int] {
        if let message = verdictMessage(runId) { return message.citedSteps }
        return (verdict(runId)?.evidence ?? []).compactMap { Evidence.parse($0).step }
    }

    /// How far a verdict has gone at one seq: none, proposed, contested, then closed.
    private static func progress(_ status: VerdictStatus) -> Int {
        switch status {
        case .none, .unknown: 0
        case .proposed: 1
        case .contested: 2
        case .accepted, .rejected: 3
        }
    }

    // MARK: - Task titles

    private func withLearnedTasks(_ fetched: [RunSummary]) -> [RunSummary] {
        fetched.map { run in
            var run = run
            if run.task?.isEmpty ?? true, let known = learnedTasks[run.runId] { run.task = known }
            return run
        }
    }

    private func learn(task: String?, for runId: String) {
        guard let task, !task.isEmpty, learnedTasks[runId] == nil else { return }
        learnedTasks[runId] = task
        if let index = runs.firstIndex(where: { $0.runId == runId }), runs[index].task?.isEmpty ?? true {
            runs[index].task = task
        }
    }

    /// Reads, a few at a time and once each, the transcripts of runs the list did not name.
    private func learnMissingTasks() {
        let missing = runs.filter { ($0.task?.isEmpty ?? true) && $0.messages > 0 && !taskFetches.contains($0.runId) }
            .map(\.runId)
        guard !missing.isEmpty else { return }
        taskFetches.formUnion(missing)
        Task { [weak self] in
            for batch in stride(from: 0, to: missing.count, by: 4).map({ Array(missing[$0..<min($0 + 4, missing.count)]) }) {
                await withTaskGroup(of: (String, String?).self) { group in
                    for runId in batch {
                        group.addTask { [weak self] in
                            let held = try? await self?.client.messages(runId)
                            return (runId, held.flatMap { RunTitle.task(in: $0) })
                        }
                    }
                    for await (runId, task) in group {
                        self?.learn(task: task, for: runId)
                    }
                }
            }
        }
    }

    /// Whether the transcript shows "Verifier is working": it owes an answer and can still
    /// give one. A destroyed run's verifier has stopped, so its last human message waits
    /// for nothing, whatever the daemon posted after it.
    nonisolated static func verifierIsWorking(_ messages: [Message], verifierListens: Bool) -> Bool {
        verifierListens && awaitingVerifier(messages)
    }

    /// True while the verifier owes an answer (ADR 0006, "A human is always
    /// answered"). Verifier progress and system events do not end the wait.
    nonisolated static func awaitingVerifier(_ messages: [Message]) -> Bool {
        for message in messages.reversed() {
            switch message.from {
            case .verifier:
                if message.kind == .progress { continue }
                return false
            case .coder, .human:
                return startsTurn(message)
            case .system:
                // The daemon says when no verifier will answer: none is configured, or the
                // machine was destroyed with the task still open (#79).
                if message.kind == .event,
                   message.text.contains("nobody will answer") || message.text.contains("nothing will answer") {
                    return false
                }
                continue
            case .unknown:
                continue
            }
        }
        return false
    }

    /// A coder `note` is context for the next turn; a human `note` is a question.
    nonisolated static func startsTurn(_ message: Message) -> Bool {
        switch message.kind {
        case .task, .answer, .dispute: return true
        case .note: return message.from == .human
        default: return false
        }
    }

    // MARK: - Errors

    /// Guarded so an already-clear error does not notify observers.
    func clearError() {
        if lastError != nil { lastError = nil }
    }

    /// Cancellations are the app changing its mind, never shown.
    func report(_ error: Error) {
        guard !RunStore.isCancellation(error) else { return }
        lastError = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
    }

    nonisolated static func isCancellation(_ error: Error) -> Bool {
        if error is CancellationError { return true }
        if case DaemonError.cancelled = error { return true }
        if let url = error as? URLError, url.code == .cancelled { return true }
        return false
    }
}
