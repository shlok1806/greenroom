import Foundation
import Observation

/// The three lease routes, so `ControlPilot` is testable without a daemon.
protocol ControlClient: Sendable {
    func takeControl(runId: String) async throws -> ControlResponse
    /// Extends the lease this app holds and never takes a new one (#100).
    func renewControl(runId: String) async throws -> ControlResponse
    func releaseControl(runId: String) async throws
    func input(runId: String, actions: [InputAction]) async throws -> InputResult
}

extension DaemonClient: ControlClient {}

/// What a pilot reports back to; `RunStore` in the app.
@MainActor
protocol PilotHost: AnyObject {
    func report(_ error: Error)
    func clearError()
    func reloadTranscript(_ runId: String) async
    /// Control broke under the person; the machine may have stopped.
    func reloadRun(_ runId: String) async
}

enum ControlError: Error, LocalizedError, Equatable {
    /// A 2xx answer without a lease.
    case refused

    var errorDescription: String? {
        "greenroom did not hand over the screen."
    }
}

/// Holds one run's screen-control lease and posts what the person does with
/// it (ADR 0009). The daemon lends the screen for a minute at a time, so the
/// lease is renewed while held and must be given back on every way out.
///
/// Main-actor isolation is what makes the queue safe: input appends to
/// `pending` while a request is in flight and the running drain picks it up,
/// so a slow daemon means bigger batches, not a backlog of requests.
@Observable
@MainActor
final class ControlPilot {
    private(set) var active = false
    /// For the badge only; the app sends fractions, never pixels.
    private(set) var screen: GuestScreen?
    private(set) var taking = false
    private(set) var releasing = false
    /// Why control last ended without the person asking, for the Screen stage.
    private(set) var endedReason: String?

    var busy: Bool { taking || releasing }

    let runId: String
    private let client: any ControlClient
    private weak var host: (any PilotHost)?
    private let renewal: Duration

    private var pending: [InputAction] = []
    private var drain: Task<Void, Never>?
    private var heartbeat: Task<Void, Never>?
    /// Set when a release arrives while the take is still in flight.
    private var abandonTake = false

    /// A press whose release never arrives leaves the guest dragging for the
    /// rest of the run, so every way out lets go of the button first.
    private var heldButton: String?
    private var lastPoint: (x: Double, y: Double)?

    /// Every posted action also renews the daemon's one-minute lease; the
    /// heartbeat covers a person watching without touching.
    init(runId: String, client: any ControlClient, host: any PilotHost, renewal: Duration = .seconds(20)) {
        self.runId = runId
        self.client = client
        self.host = host
        self.renewal = renewal
    }

    // MARK: - The lease

    func take() async {
        guard !active, !busy else { return }
        taking = true
        abandonTake = false
        endedReason = nil
        let taken = await acquire()
        taking = false
        guard taken else { return }
        if abandonTake {
            await release()
            return
        }
        // The handover is a message in the run's conversation.
        await host?.reloadTranscript(runId)
    }

    private func acquire() async -> Bool {
        do {
            let answer = try await client.takeControl(runId: runId)
            guard answer.control != nil else { throw ControlError.refused }
            screen = answer.screen
            active = true
            host?.clearError()
            startHeartbeat()
            return true
        } catch {
            host?.report(error)
            return false
        }
    }

    /// Safe to call when nothing is held, and while a take is in flight.
    func release() async {
        if taking {
            abandonTake = true
            return
        }
        guard active, !releasing else { return }
        releasing = true
        defer { releasing = false }
        stopHeartbeat()
        // Queued input lands first, so a queued button-up is never dropped.
        await drain?.value
        // A failing drain has already given the screen back.
        guard active else { return }
        await giveBack(reportFailure: true)
    }

    /// Lets go of a held button, then of the lease.
    private func giveBack(reportFailure: Bool) async {
        active = false
        pending = []
        if let button = heldButton {
            let up = InputAction(type: .up, x: lastPoint?.x, y: lastPoint?.y, button: button, clicks: 1)
            _ = try? await client.input(runId: runId, actions: [up])
        }
        heldButton = nil
        do {
            try await client.releaseControl(runId: runId)
            await host?.reloadTranscript(runId)
        } catch {
            // The daemon expires an unrenewed lease anyway.
            if reportFailure { host?.report(error) }
        }
    }

    /// The lease or the machine is gone: stop driving, say why, and still try
    /// to let go in case the failure was transient.
    private func fail(_ error: Error) async {
        guard active else { return }
        stopHeartbeat()
        endedReason = Self.reason(error)
        await giveBack(reportFailure: false)
        await host?.reloadRun(runId)
        // Last, so the transcript reload inside `giveBack` does not clear it.
        host?.report(error)
    }

    /// The daemon's own words, e.g. "VM ... is not running", without the status prefix.
    static func reason(_ error: Error) -> String {
        if case DaemonError.status(_, let body) = error {
            let trimmed = body.trimmingCharacters(in: .whitespacesAndNewlines)
            if !trimmed.isEmpty { return trimmed }
        }
        return (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
    }

    private func startHeartbeat() {
        heartbeat?.cancel()
        heartbeat = Task { [weak self, renewal] in
            while !Task.isCancelled {
                try? await Task.sleep(for: renewal)
                guard let self, !Task.isCancelled, self.active else { return }
                do {
                    // A renewal, not a take: another window of the same seat may have given the
                    // screen back, and taking it again would undo that silently (#100).
                    let answer = try await self.client.renewControl(runId: self.runId)
                    guard answer.control != nil else { throw ControlError.refused }
                } catch {
                    if Task.isCancelled { return }
                    // Cleared first so `fail` does not cancel the task running it.
                    self.heartbeat = nil
                    await self.fail(error)
                    return
                }
            }
        }
    }

    private func stopHeartbeat() {
        heartbeat?.cancel()
        heartbeat = nil
    }

    // MARK: - Driving

    /// Queues actions and returns at once; a click must not wait on the network.
    func send(_ actions: [InputAction]) {
        guard active, !releasing, !actions.isEmpty else { return }
        for action in actions {
            if let x = action.x, let y = action.y { lastPoint = (x, y) }
            switch action.type {
            case .down: heldButton = action.button ?? "left"
            case .up: heldButton = nil
            default: break
            }
        }
        pending.append(contentsOf: actions)
        if drain == nil {
            drain = Task { [weak self] in await self?.flush() }
        }
    }

    /// The only sender of input; it re-reads `pending` after every request,
    /// so nothing queued meanwhile is lost.
    private func flush() async {
        defer { drain = nil }
        while active, !pending.isEmpty {
            let batch = InputBatch.coalesced(pending)
            pending = []
            do {
                screen = try await client.input(runId: runId, actions: batch).screen
                host?.clearError()
            } catch {
                await fail(error)
                return
            }
        }
    }
}
