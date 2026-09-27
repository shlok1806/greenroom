import Foundation
import Observation

/// The Greenroom section's state (root ADR 0033): the app's build, the daemon's, what
/// `update.sh --check` last said, and the update running now or last. It checks when the app
/// opens, when the section opens and every few hours, and never updates on its own.
@Observable
@MainActor
final class Updates {
    private(set) var app: AppBuild
    private(set) var daemon: DaemonVersion?
    /// Why the daemon's build is unknown: not answering, or older than `/api/version`.
    private(set) var daemonError: String?
    private(set) var check: BuildsSummary.Check = .notYet
    /// The update running now, or the last one this launch.
    private(set) var run: UpdateRun?
    /// Runs whose verifier is mid-turn, named, while the section asks whether to update anyway.
    var confirming: [String]?

    /// Between checks while the app runs.
    static let interval: Duration = .seconds(3 * 60 * 60)

    @ObservationIgnored private let client: DaemonClient
    @ObservationIgnored private let script: any UpdateScript
    @ObservationIgnored private let log: URL
    @ObservationIgnored private var periodic: Task<Void, Never>?
    @ObservationIgnored private var checking: Task<Void, Never>?

    init(client: DaemonClient, app: AppBuild = .current, script: any UpdateScript = ScriptRunner(),
         log: URL = ScriptRunner.defaultLog) {
        self.client = client
        self.app = app
        self.script = script
        self.log = log
    }

    /// The daemon is on this Mac, so the checkout it names is one this app can run.
    var daemonIsLocal: Bool { client.isLocal }

    /// Where `update.sh` runs: the checkout the daemon was installed from, else the one the
    /// app was bundled from. None when the daemon runs on another Mac: updating here would
    /// rebuild a daemon nobody is watching.
    var checkout: String? {
        guard daemonIsLocal else { return nil }
        return daemon?.installedCheckout ?? app.checkout
    }

    var summary: BuildsSummary {
        BuildsSummary.of(app: app, daemon: daemon, daemonIsLocal: daemonIsLocal, checkout: checkout, check: check, run: run)
    }

    // MARK: - Checking

    /// Checks now and every `interval` after. A snapshot never runs anything.
    func start() {
        guard periodic == nil, !SnapshotMode.isActive else { return }
        periodic = Task { [weak self] in
            while !Task.isCancelled {
                await self?.refresh()
                try? await Task.sleep(for: Self.interval)
            }
        }
    }

    func stop() {
        periodic?.cancel()
        periodic = nil
    }

    /// Reads the daemon's build, then asks `update.sh --check`. One at a time; a second call
    /// while one runs waits for it.
    func refresh() async {
        if let checking {
            await checking.value
            return
        }
        let task = Task { await self.readDaemon(); await self.runCheck() }
        checking = task
        await task.value
        checking = nil
    }

    func readDaemon() async {
        do {
            daemon = try await client.version()
            daemonError = nil
        } catch DaemonError.status(code: 404, _) {
            daemon = nil
            daemonError = "This greenroom is older than build reporting. Update it to see its build."
        } catch {
            daemon = nil
            daemonError = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
        }
    }

    private func runCheck() async {
        guard let checkout, run?.running != true else { return }
        check = .checking
        do {
            let output = try await script.check(checkout: checkout)
            if let result = UpdateCheck.parse(output) {
                check = .checked(result, at: Date())
            } else {
                check = .failed("update.sh said something unexpected")
            }
        } catch {
            check = .failed((error as? LocalizedError)?.errorDescription ?? error.localizedDescription)
        }
    }

    // MARK: - Updating

    /// Update, unless a verifier is mid-turn: then the section names those runs and asks
    /// first, since restarting greenroom cuts such a turn off (#163). `busyRuns` reads which.
    func requestUpdate(busyRuns: () async -> [String]) async {
        guard summary.canUpdate else { return }
        let busy = await busyRuns()
        if busy.isEmpty {
            await update()
        } else {
            confirming = busy
        }
    }

    /// The person saw the warning and chose to update anyway.
    func updateAnyway() async {
        confirming = nil
        await update()
    }

    func cancelUpdate() {
        confirming = nil
    }

    /// Runs `update.sh`, taking its lines as they come. On success the Companion's install
    /// quits this app and opens the new one, so the end is usually not seen here; a refusal
    /// or a failure leaves both builds as they were and the section shows why.
    func update() async {
        guard let checkout, run?.running != true, !SnapshotMode.isActive else { return }
        confirming = nil
        run = UpdateRun()
        for await event in script.update(checkout: checkout, log: log) {
            switch event {
            case .line(let line): run?.take(line)
            case .exit(let status): run?.finish(status: status)
            }
        }
        if run?.running == true { run?.finish(status: 1) }
        // What changed is worth reading again, whatever the outcome.
        await readDaemon()
        await runCheck()
    }

    /// Clears a finished run from the section.
    func dismissRun() {
        guard run?.running != true else { return }
        run = nil
    }

    #if DEBUG
    /// The snapshot harness sets the section's state by hand; nothing runs.
    func stage(app: AppBuild, daemon: DaemonVersion?, check: BuildsSummary.Check, run: UpdateRun? = nil,
               confirming: [String]? = nil) {
        self.app = app
        self.daemon = daemon
        self.daemonError = nil
        self.check = check
        self.run = run
        self.confirming = confirming
    }
    #endif
}

extension RunStore {
    /// The runs whose verifier is mid-turn now, by title: live runs whose last turn-starting
    /// message has no answer yet (`awaitingVerifier`). A transcript the window does not hold
    /// is read for this; one that cannot be read counts as not busy.
    func runsWithVerifierTurn() async -> [String] {
        var busy: [String] = []
        for run in runs where facts(run.runId).isAlive {
            var held = messages[run.runId]
            if held == nil { held = try? await client.messages(run.runId) }
            guard let held, RunStore.awaitingVerifier(held) else { continue }
            busy.append(RunTitle.short(task: run.task, runId: run.runId))
        }
        return busy
    }
}
