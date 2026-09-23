import AppKit
import Foundation
import Observation

/// What the store must fetch after applying an event. Returned rather than
/// performed so `apply` stays testable without a daemon.
enum Followup: Hashable, Sendable {
    case nothing
    case steps(String)
    case run(String)
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
    var lastError: String?
    var selectedRunId: String?
    var seekRequest: SeekRequest?

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
    private var streamTask: Task<Void, Never>?
    private var seekNonce = 0
    private var pilots: [String: ControlPilot] = [:]

    init(
        client: DaemonClient = DaemonClient(),
        controlClient: (any ControlClient)? = nil,
        screenSource: (any ScreenSource)? = nil
    ) {
        self.client = client
        self.controlClient = controlClient ?? client
        self.screenSource = screenSource ?? client
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
        connected = false
    }

    private func runStream() async {
        var backoff = Backoff()
        while !Task.isCancelled {
            // URLSession holds an SSE response back until the first bytes (a
            // ping, up to 15 s), so a daemon that just answered counts as live.
            connected = await resync()
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
        var failures: [Error] = []
        var reached = true
        for fetch in RunStore.resyncPlan(selected: selectedRunId) {
            guard let error = await attempt(fetch) else { continue }
            failures.append(error)
            if fetch == .runs { reached = false }
        }
        settle(failures)
        return reached
    }

    private func perform(_ fetch: Fetch) async {
        settle([await attempt(fetch)].compactMap { $0 })
    }

    /// Loads and stores one piece; `nil` on success.
    private func attempt(_ fetch: Fetch) async -> Error? {
        do {
            switch fetch {
            case .runs:
                do {
                    runs = withLearnedTasks(try await client.runs())
                    reachable = true
                    learnMissingTasks()
                } catch {
                    if !RunStore.isCancellation(error) { reachable = false }
                    throw error
                }
            case .detail(let runId): details[runId] = try await client.run(runId)
            case .messages(let runId):
                let held = try await client.messages(runId)
                messages[runId] = held
                learn(task: RunTitle.task(in: held), for: runId)
            case .steps(let runId): steps[runId] = StepLog.normalized(try await client.steps(runId))
            case .frames(let runId): frames[runId] = try await client.frames(runId)
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
            if let last = held.last, message.seq <= last.seq { return .nothing }
            held.append(message)
            messages[runId] = held
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
            if let machine = lifecycle.machine, details[lifecycle.runId] != nil {
                details[lifecycle.runId]?.machine = machine
            }
            return .run(lifecycle.runId)
        case .frame(let runId, let frame):
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
        ConnectionState.derive(reachable: reachable, hasData: !runs.isEmpty)
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
    /// must not hide the one the list now has.
    func verdict(_ runId: String) -> VerdictState? {
        let detail = details[runId]?.verdict
        let listed = run(runId)?.verdict
        guard let detail, detail.status != .none else { return listed ?? detail }
        guard let listed else { return detail }
        return (listed.seq ?? 0) > (detail.seq ?? 0) ? listed : detail
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
            case .system, .unknown:
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
