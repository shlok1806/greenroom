import Foundation
import Observation

/// Holds one run's screen-control lease and posts what the person does with
/// it (ADR 0009). The daemon lends the screen for a minute at a time, so the
/// lease is renewed while held and must be given back on every way out.
///
/// Main-actor isolation is what makes the queue safe: input appends to
/// `pending` while a request is in flight and the running flush picks it up,
/// so a slow daemon means bigger batches, not a backlog of requests.
@Observable
@MainActor
final class ControlPilot {
    private(set) var active = false
    /// For the badge only; the app sends fractions, never pixels.
    private(set) var screen: GuestScreen?
    private(set) var busy = false

    let runId: String
    private let store: RunStore

    private var pending: [InputAction] = []
    private var flushing = false
    private var flushTask: Task<Void, Never>?
    private var heartbeat: Task<Void, Never>?

    /// A press whose release never arrives leaves the guest dragging for the
    /// rest of the run, so `release()` lets go of the button first.
    private var heldButton: String?
    private var lastPoint: (x: Double, y: Double)?

    /// Every posted action also renews the daemon's one-minute lease; this
    /// covers a person watching without touching.
    static let renewal: Duration = .seconds(20)

    init(runId: String, store: RunStore) {
        self.runId = runId
        self.store = store
    }

    // MARK: - The lease

    func take() async {
        guard !active else { return }
        busy = true
        defer { busy = false }
        do {
            let answer = try await store.client.takeControl(runId: runId)
            screen = answer.screen
            active = answer.control != nil
            store.clearError()
            startHeartbeat()
            // The handover is a message in the run's conversation.
            await store.reloadTranscript(runId)
        } catch {
            active = false
            store.report(error)
        }
    }

    /// Safe to call when nothing is held.
    func release() async {
        heartbeat?.cancel()
        heartbeat = nil
        guard active else {
            pending = []
            return
        }
        if let button = heldButton, let at = lastPoint {
            pending.append(InputAction(type: .up, x: at.x, y: at.y, button: button, clicks: 1))
        }
        // Let the in-flight batch finish, then send the rest, release included.
        await flushTask?.value
        await flush()
        pending = []
        active = false
        heldButton = nil
        do {
            try await store.client.releaseControl(runId: runId)
            await store.reloadTranscript(runId)
        } catch {
            store.report(error)
        }
    }

    private func startHeartbeat() {
        heartbeat?.cancel()
        heartbeat = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: ControlPilot.renewal)
                guard let self, !Task.isCancelled, self.active else { return }
                do {
                    _ = try await self.store.client.takeControl(runId: self.runId)
                } catch {
                    // The machine may simply be gone: stop driving and say so.
                    self.active = false
                    self.store.report(error)
                    return
                }
            }
        }
    }

    // MARK: - Driving

    /// Queues actions and returns at once; a click must not wait on the network.
    func send(_ actions: [InputAction]) {
        guard active, !actions.isEmpty else { return }
        for action in actions {
            if let x = action.x, let y = action.y { lastPoint = (x, y) }
            switch action.type {
            case .down: heldButton = action.button ?? "left"
            case .up: heldButton = nil
            default: break
            }
        }
        pending.append(contentsOf: actions)
        flushTask = Task { [weak self] in await self?.flush() }
    }

    /// Runs at most once at a time; the running call re-reads `pending` after
    /// every request, so nothing queued meanwhile is lost.
    private func flush() async {
        guard !flushing else { return }
        flushing = true
        defer { flushing = false }
        while active, !pending.isEmpty {
            let batch = InputBatch.coalesced(pending)
            pending = []
            do {
                screen = try await store.client.input(runId: runId, actions: batch).screen
                store.clearError()
            } catch {
                // The lease or the machine is gone; pressing on posts into nothing.
                pending = []
                active = false
                store.report(error)
                return
            }
        }
    }
}
