import Foundation
import Observation

/// What the store must fetch after applying an event. Returned rather than
/// performed so the merge itself stays testable without a daemon.
enum Followup: Hashable, Sendable {
    case nothing
    case steps(String)
    case run(String)
}

/// Everything the windows read. It owns one `DaemonClient`, one event stream
/// and the last state each run was seen in. Views never fetch for themselves.
@Observable
@MainActor
final class RunStore {
    var runs: [RunSummary] = []
    var details: [String: RunDetail] = [:]
    var messages: [String: [Message]] = [:]
    var steps: [String: [Step]] = [:]
    var connected = false
    var lastError: String?
    var selectedRunId: String?

    let client: DaemonClient

    private var streamTask: Task<Void, Never>?
    private var started = false

    init(client: DaemonClient = DaemonClient()) {
        self.client = client
    }

    // MARK: - Lifecycle

    /// Loads the run list and keeps a live event stream open, reconnecting with
    /// a backoff and re-syncing from the API after every drop. The files on
    /// disk are the truth; the stream is only a hint (ADR 0007).
    func start() {
        guard !started else { return }
        started = true
        streamTask = Task { [weak self] in
            await self?.runStream()
        }
    }

    func stop() {
        streamTask?.cancel()
        streamTask = nil
        started = false
        connected = false
    }

    private func runStream() async {
        var backoff: UInt64 = 1
        while !Task.isCancelled {
            await refresh()
            await resync()
            do {
                connected = true
                for try await event in client.events(runId: nil) {
                    await handle(event)
                }
                connected = false
            } catch {
                connected = false
                if Task.isCancelled { return }
                report(error)
            }
            if Task.isCancelled { return }
            try? await Task.sleep(for: .seconds(Double(backoff)))
            backoff = min(backoff * 2, 10)
        }
    }

    // MARK: - Fetching

    /// Reloads the run list.
    func refresh() async {
        do {
            runs = try await client.runs()
            if lastError != nil { lastError = nil }
        } catch {
            report(error)
        }
    }

    /// Reloads everything already open, which is what a reconnect needs.
    private func resync() async {
        for runId in details.keys.sorted() {
            await select(runId)
        }
    }

    /// Loads the detail, the transcript and the steps of one run.
    func select(_ runId: String) async {
        async let detail = client.run(runId)
        async let page = client.messages(runId, after: 0)
        async let stepList = client.steps(runId)
        do {
            details[runId] = try await detail
            messages[runId] = try await page.messages
            steps[runId] = try await stepList
            if lastError != nil { lastError = nil }
        } catch {
            report(error)
        }
    }

    // MARK: - Events

    private func handle(_ event: ServerEvent) async {
        switch apply(event) {
        case .nothing:
            break
        case .steps(let runId):
            do {
                steps[runId] = try await client.steps(runId)
            } catch {
                report(error)
            }
        case .run(let runId):
            await refresh()
            if details[runId] != nil || selectedRunId == runId {
                do {
                    details[runId] = try await client.run(runId)
                } catch {
                    report(error)
                }
            }
        }
    }

    /// Merges one event into what is held and says what still has to be
    /// fetched. No networking happens here on purpose.
    @discardableResult
    func apply(_ event: ServerEvent) -> Followup {
        switch event {
        case .message(let runId, let message):
            guard var held = messages[runId] else { return .nothing }
            if let last = held.last, message.seq <= last.seq { return .nothing }
            held.append(message)
            messages[runId] = held
            if let index = runs.firstIndex(where: { $0.runId == runId }) {
                runs[index].messages = max(runs[index].messages, message.seq)
                runs[index].lastActivity = max(runs[index].lastActivity, message.at)
            }
            // A verdict or its closing changes the badge, so re-read the run.
            switch message.kind {
            case .verdict, .accept, .dispute: return .run(runId)
            default: return .nothing
            }
        case .step(let runId, let seq, let step):
            if let index = runs.firstIndex(where: { $0.runId == runId }) {
                runs[index].steps = max(runs[index].steps, seq)
                if let at = step?.at {
                    runs[index].lastActivity = max(runs[index].lastActivity, at)
                }
            }
            guard steps[runId] != nil else { return .nothing }
            return .steps(runId)
        case .run(let lifecycle):
            if let machine = lifecycle.machine, var detail = details[lifecycle.runId] {
                detail.machine = machine
                details[lifecycle.runId] = detail
            }
            return .run(lifecycle.runId)
        }
    }

    // MARK: - Actions

    /// The human seat in the conversation (ADR 0006).
    func send(runId: String, kind: MessageKind, text: String, replyTo: Int? = nil) async {
        do {
            _ = try await client.send(runId: runId, kind: kind, text: text, replyTo: replyTo)
            await reloadMessages(runId)
            await refresh()
            if lastError != nil { lastError = nil }
        } catch {
            report(error)
        }
    }

    func screenshot(runId: String) async {
        do {
            _ = try await client.screenshot(runId: runId)
            steps[runId] = try await client.steps(runId)
            if lastError != nil { lastError = nil }
        } catch {
            report(error)
        }
    }

    func destroy(runId: String) async {
        do {
            try await client.destroy(runId: runId)
            await refresh()
            details[runId] = try await client.run(runId)
            await reloadMessages(runId)
            if lastError != nil { lastError = nil }
        } catch {
            report(error)
        }
    }

    func artifact(runId: String, name: String) async -> Data? {
        do {
            return try await client.artifact(runId: runId, name: name)
        } catch {
            report(error)
            return nil
        }
    }

    // MARK: - Derived

    func run(_ runId: String) -> RunSummary? {
        runs.first { $0.runId == runId }
    }

    /// The verdict state of a run, from its detail when that is loaded.
    func verdict(_ runId: String) -> VerdictState? {
        details[runId]?.verdict ?? run(runId)?.verdict
    }

    /// The newest screenshot artifact of a run, if any step took one.
    func latestScreenshot(_ runId: String) -> (step: Int, name: String)? {
        guard let list = steps[runId] else { return nil }
        for step in list.reversed() {
            if let name = step.screenshotArtifact { return (step.seq, name) }
        }
        return nil
    }

    /// True while the verifier owes the transcript an answer: the newest thing
    /// said by a human or the coder starts a turn (ADR 0006, "A human is always
    /// answered") and nothing from the verifier has closed it yet. Progress is
    /// the verifier working, not its answer, and a system event is not an
    /// answer either, so neither ends the wait.
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

    /// Which messages hand the turn to the verifier. A coder `note` is context
    /// it reads at its next turn; a human `note` is a question to answer.
    nonisolated static func startsTurn(_ message: Message) -> Bool {
        switch message.kind {
        case .task, .answer, .dispute: return true
        case .note: return message.from == .human
        default: return false
        }
    }

    private func reloadMessages(_ runId: String) async {
        do {
            messages[runId] = try await client.messages(runId, after: 0).messages
        } catch {
            report(error)
        }
    }

    private func report(_ error: Error) {
        lastError = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
    }
}
