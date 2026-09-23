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

/// Jump the Screen tab to the first frame at or after `step` (ADR 0008).
/// `nonce` makes a repeated click on the same row seek again.
struct SeekRequest: Hashable, Sendable {
    var runId: String
    var step: Int
    var nonce: Int
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
    var lastError: String?
    var selectedRunId: String?
    var seekRequest: SeekRequest?

    let client: DaemonClient

    private let frameCache = FrameCache()
    private var streamTask: Task<Void, Never>?
    private var seekNonce = 0
    private var pilots: [String: ControlPilot] = [:]

    init(client: DaemonClient = DaemonClient()) {
        self.client = client
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
            case .runs: runs = try await client.runs()
            case .detail(let runId): details[runId] = try await client.run(runId)
            case .messages(let runId): messages[runId] = try await client.messages(runId)
            case .steps(let runId): steps[runId] = try await client.steps(runId)
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
            guard var held = messages[runId] else { return .nothing }
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
            steps[runId] = try await client.steps(runId)
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
        let pilot = ControlPilot(runId: runId, client: client, host: self)
        pilots[runId] = pilot
        return pilot
    }

    /// A fresh live screen (ADR 0011); the caller starts it and must stop it.
    func liveScreen(for runId: String) -> LiveScreen {
        LiveScreen(runId: runId, source: client)
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

    func requestSeek(runId: String, step: Int) {
        seekNonce += 1
        seekRequest = SeekRequest(runId: runId, step: step, nonce: seekNonce)
    }

    // MARK: - Derived

    func run(_ runId: String) -> RunSummary? {
        runs.first { $0.runId == runId }
    }

    func verdict(_ runId: String) -> VerdictState? {
        details[runId]?.verdict ?? run(runId)?.verdict
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
