import Foundation
import Observation

/// Holds the screen-control lease for one run and posts what the person does
/// with it (ADR 0009).
///
/// It exists because driving a machine is the one thing the app does that is
/// a *session* rather than a request: the daemon lends the screen for a
/// minute at a time, the lease has to be renewed while the person is still
/// using it, and it has to be given back when they stop, when the tab
/// changes, when the machine dies and when the window closes.
///
/// Everything here runs on the main actor, which is what makes the queue
/// safe: a mouse event appends to `pending` while a request is in flight, and
/// the flush loop picks it up when the request returns. That is also the
/// coalescing: a slow daemon means bigger batches, not a backlog of requests.
@Observable
@MainActor
final class ControlPilot {
    /// True from the moment the daemon grants the lease until it is given
    /// back. The Screen tab reads it to decide whether it is a picture or a
    /// screen.
    private(set) var active = false
    /// What the person is driving, for the badge. Never used for arithmetic:
    /// the app sends fractions.
    private(set) var screen: GuestScreen?
    private(set) var lease: ControlLease?
    private(set) var busy = false

    let runId: String
    private let store: RunStore

    private var pending: [InputAction] = []
    private var flushing = false
    private var flushTask: Task<Void, Never>?
    private var heartbeat: Task<Void, Never>?

    /// The button the guest still has held, and where it was last seen. A
    /// press whose release never arrives leaves the machine dragging for the
    /// rest of the run, so the release is sent before the lease is given up.
    private var heldButton: String?
    private var lastPoint: (x: Double, y: Double)?

    /// How often the lease is renewed. The daemon's own lease is a minute and
    /// every posted action renews it, so this only matters while the person
    /// is looking at the screen without touching it.
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
            lease = answer.control
            screen = answer.screen
            active = answer.control != nil
            store.clearError()
            startHeartbeat()
            // The handover is a message in the run's conversation, so the
            // transcript has to catch up with it.
            await store.reloadTranscript(runId)
        } catch {
            active = false
            store.report(error)
        }
    }

    /// Gives the screen back. Safe to call when the app never had it: the
    /// daemon treats releasing nothing as nothing.
    func release() async {
        heartbeat?.cancel()
        heartbeat = nil
        guard active else {
            pending = []
            return
        }
        // Let go of the mouse button before letting go of the screen.
        if let button = heldButton, let at = lastPoint {
            pending.append(InputAction(type: .up, x: at.x, y: at.y, button: button, clicks: 1))
        }
        // Anything already on its way goes first, then what is left,
        // including that release.
        await flushTask?.value
        await flush()
        pending = []
        active = false
        lease = nil
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
                    let answer = try await self.store.client.takeControl(runId: self.runId)
                    self.lease = answer.control
                } catch {
                    // Losing the lease is not an error to shout about: the
                    // machine may simply be gone. Stop driving and say so.
                    self.active = false
                    self.store.report(error)
                    return
                }
            }
        }
    }

    // MARK: - Driving

    /// Queues actions and starts a flush. Returns at once: a click must not
    /// wait for the network before the next one is accepted.
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

    /// Sends everything queued, one request at a time. It is re-entrant-safe
    /// by refusing to run twice: the call already in flight will pick up
    /// whatever arrived while it was waiting, because it re-reads `pending`
    /// after every request.
    private func flush() async {
        guard !flushing else { return }
        flushing = true
        defer { flushing = false }
        while active, !pending.isEmpty {
            let batch = InputBatch.coalesced(pending)
            pending = []
            do {
                let result = try await store.client.input(runId: runId, actions: batch)
                screen = result.screen
                store.clearError()
            } catch {
                // A refused batch means the lease is gone or the machine is:
                // either way the pointer is not this app's any more, and
                // pressing on would post events into nothing.
                pending = []
                active = false
                store.report(error)
                return
            }
        }
    }
}
