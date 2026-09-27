import Foundation

// Wire types. `apps/daemon/internal/api/api.go` is the authority on every shape.
// Decoders live in extensions so the memberwise initialisers survive.

struct Machine: Codable, Hashable, Sendable {
    var runId: String
    var name: String
    var image: String
    var ip: String?
    var status: MachineStatus
    var error: String?
    var bootSeconds: Double?
    var createdAt: Date
    var dir: String
    /// Who holds the mouse and keyboard, if anyone (ADR 0009).
    var control: ControlLease?
    /// Each boot phase as it started and ended, oldest first. Empty from a daemon before
    /// boot phases, and for a machine a restarted daemon reattached.
    var boot: [BootPhase] = []
}

extension Machine {
    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        runId = try c.decode(.runId, or: "")
        name = try c.decode(.name, or: "")
        image = try c.decode(.image, or: "")
        ip = try c.decodeIfPresent(String.self, forKey: .ip)
        status = try c.decode(.status, or: .unknown(""))
        error = try c.decodeIfPresent(String.self, forKey: .error)
        bootSeconds = try c.decodeIfPresent(Double.self, forKey: .bootSeconds)
        createdAt = try c.decode(.createdAt, or: .epoch)
        dir = try c.decode(.dir, or: "")
        control = try c.decodeIfPresent(ControlLease.self, forKey: .control)
        boot = try c.decode(.boot, or: [])
    }
}

/// One stage of a machine coming up (`machine.BootPhase`): sent when it starts, with no
/// `seconds`, and again when it ends.
struct BootPhase: Codable, Hashable, Sendable {
    var phase: BootPhaseName
    var at: Date
    /// How long it took; nil while it runs.
    var seconds: Double?
    /// What it produced or worked on: the image, the VM, the address.
    var detail: String?
    var error: String?

    var running: Bool { seconds == nil }
}

extension BootPhase {
    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        phase = try c.decode(.phase, or: .unknown(""))
        at = try c.decode(.at, or: .epoch)
        seconds = try c.decodeIfPresent(Double.self, forKey: .seconds)
        detail = try c.decodeIfPresent(String.self, forKey: .detail)
        error = try c.decodeIfPresent(String.self, forKey: .error)
    }
}

extension [BootPhase] {
    /// `phase` merged in: it replaces the one of the same name (its start, or a repeat),
    /// else it goes on the end.
    func merging(_ phase: BootPhase) -> [BootPhase] {
        var out = self
        if let index = out.firstIndex(where: { $0.phase == phase.phase }) {
            out[index] = phase
        } else {
            out.append(phase)
        }
        return out
    }
}

/// A screen-control lease (ADR 0009). The daemon expires it on its own.
struct ControlLease: Codable, Hashable, Sendable {
    var holder: String
    var since: Date
    var expires: Date
    var actions: Int
}

extension ControlLease {
    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        holder = try c.decode(.holder, or: "")
        since = try c.decode(.since, or: .epoch)
        expires = try c.decode(.expires, or: .epoch)
        actions = try c.decode(.actions, or: 0)
    }
}

/// The guest display's size, shown to the person only: the app sends fractions.
struct GuestScreen: Codable, Hashable, Sendable {
    var width: Int
    var height: Int

    var label: String { "\(width)x\(height)" }
}

/// One thing to do to the machine's screen. `x` and `y` are fractions of the
/// display (0 to 1); the daemon multiplies them out (ADR 0009).
struct InputAction: Codable, Hashable, Sendable {
    /// The kinds the daemon's guest helper knows.
    enum Kind: String, Codable, Sendable {
        case move, click, down, up, scroll, type, key, sleep
    }

    var type: Kind
    var x: Double?
    var y: Double?
    var button: String?
    var clicks: Int?
    var deltaX: Double?
    var deltaY: Double?
    var text: String?
    var key: String?
    var mods: [String]?
}

/// `POST /api/runs/{id}/control`.
struct ControlResponse: Codable, Hashable, Sendable {
    var control: ControlLease?
    var screen: GuestScreen?
}

/// `POST /api/runs/{id}/input`.
struct InputResult: Codable, Hashable, Sendable {
    var actions: Int
    var screen: GuestScreen
    var seconds: Double?
    var step: Int?
}

/// How a run ended, as the coding agent said with `run_finish` (root ADR 0031): on the run
/// list, the run detail (from its manifest) and the system event that recorded it. Absent on
/// an unfinished run and on every run from before it.
struct RunFinish: Codable, Hashable, Sendable {
    var outcome: FinishOutcome
    /// One or two sentences of what changed.
    var summary: String = ""
    var ref = RunRef()
    var at: Date?
}

extension RunFinish {
    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        outcome = try c.decode(.outcome, or: .unknown(""))
        summary = try c.decode(.summary, or: "")
        // A malformed ref drops the ref, never the finish.
        ref = (try? c.decodeIfPresent(RunRef.self, forKey: .ref)) ?? RunRef()
        at = try? c.decodeIfPresent(Date.self, forKey: .at)
    }
}

/// What the work became. Each field is free text: a branch name, a commit sha, a PR URL or
/// number. Only an http(s) PR is a link.
struct RunRef: Codable, Hashable, Sendable {
    var branch: String?
    var commit: String?
    var pr: String?

    var isEmpty: Bool { [branch, commit, pr].allSatisfy { ($0 ?? "").isEmpty } }

    /// The PR as a link to open in the browser, only when it is an http(s) URL.
    var prURL: URL? {
        guard let pr = pr?.trimmingCharacters(in: .whitespaces), let url = URL(string: pr),
              let scheme = url.scheme?.lowercased(), scheme == "http" || scheme == "https", url.host() != nil
        else { return nil }
        return url
    }
}

extension RunRef {
    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        func text(_ key: CodingKeys) -> String? {
            guard let value = try? c.decodeIfPresent(String.self, forKey: key) else { return nil }
            let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
            return trimmed.isEmpty ? nil : trimmed
        }
        branch = text(.branch)
        commit = text(.commit)
        pr = text(.pr)
    }
}

struct VerdictState: Codable, Hashable, Sendable {
    var seq: Int?
    var verdict: String?
    var summary: String?
    var evidence: [String]?
    var status: VerdictStatus = .none
    var acceptedBy: MessageFrom?
    var disputes = 0
    /// The verdict's checklist (root ADR 0024), as the run list carries it.
    var checks: [AcceptanceCheck] = []
}

extension VerdictState {
    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        seq = try c.decodeIfPresent(Int.self, forKey: .seq)
        verdict = try c.decodeIfPresent(String.self, forKey: .verdict)
        summary = try c.decodeIfPresent(String.self, forKey: .summary)
        evidence = try c.decodeIfPresent([String].self, forKey: .evidence)
        status = try c.decode(.status, or: .none)
        acceptedBy = try c.decodeIfPresent(MessageFrom.self, forKey: .acceptedBy)
        disputes = try c.decode(.disputes, or: 0)
        // One malformed check drops the list, never the verdict.
        checks = (try? c.decodeIfPresent([AcceptanceCheck].self, forKey: .checks)) ?? []
    }
}

struct Message: Codable, Hashable, Sendable, Identifiable {
    var seq: Int
    var at: Date
    var from: MessageFrom
    var kind: MessageKind
    var text: String
    var replyTo: Int?
    var step: Int?
    var verdict: String?
    var evidence: [String]?
    /// Acceptance checks (root ADR 0024): answered on a verdict, declared (id and criterion
    /// only) on the verifier's progress message that plans them. Empty on older messages.
    var checks: [AcceptanceCheck] = []
    /// On the system event that recorded `run_finish` (root ADR 0031).
    var finish: RunFinish?

    var id: Int { seq }
}

extension Message {
    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        seq = try c.decode(.seq, or: 0)
        at = try c.decode(.at, or: .epoch)
        from = try c.decode(.from, or: .unknown(""))
        kind = try c.decode(.kind, or: .unknown(""))
        text = try c.decode(.text, or: "")
        replyTo = try c.decodeIfPresent(Int.self, forKey: .replyTo)
        step = try c.decodeIfPresent(Int.self, forKey: .step)
        verdict = try c.decodeIfPresent(String.self, forKey: .verdict)
        evidence = try c.decodeIfPresent([String].self, forKey: .evidence)
        // One malformed check drops the list, never the message.
        checks = (try? c.decodeIfPresent([AcceptanceCheck].self, forKey: .checks)) ?? []
        finish = try? c.decodeIfPresent(RunFinish.self, forKey: .finish)
    }
}

struct Step: Codable, Hashable, Sendable, Identifiable {
    var seq: Int
    var at: Date
    var tool: String
    var input: JSONValue?
    var output: JSONValue?
    var error: String?
    var durationMs = 0

    var id: Int { seq }

    /// The artifact name of a screenshot step, from `output.path`.
    var screenshotArtifact: String? {
        guard tool == "machine_screenshot", let path = output?["path"]?.stringValue else { return nil }
        let name = (path as NSString).lastPathComponent
        return name.isEmpty ? nil : name
    }
}

extension Step {
    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        seq = try c.decode(.seq, or: 0)
        at = try c.decode(.at, or: .epoch)
        tool = try c.decode(.tool, or: "")
        input = try c.decodeIfPresent(JSONValue.self, forKey: .input)
        output = try c.decodeIfPresent(JSONValue.self, forKey: .output)
        error = try c.decodeIfPresent(String.self, forKey: .error)
        durationMs = try c.decode(.durationMs, or: 0)
    }
}

/// One frame of a run's recording (ADR 0008). `step` is the latest step
/// recorded when it was taken, which is what makes "jump to step N" work.
struct Frame: Codable, Hashable, Sendable, Identifiable {
    var at: Date
    var file: String
    var step: Int
    var bytes = 0

    var id: String { file }
}

extension Frame {
    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        at = try c.decode(.at, or: .epoch)
        file = try c.decode(.file, or: "")
        step = try c.decode(.step, or: 0)
        bytes = try c.decode(.bytes, or: 0)
    }
}

struct RunSummary: Codable, Hashable, Sendable, Identifiable {
    var runId: String
    var createdAt: Date
    var destroyedAt: Date?
    var image: String
    var status: RunStatus
    var ip: String?
    /// Counts, not sequence numbers: step numbering can have gaps.
    var steps: Int
    var verdict: VerdictState?
    var lastActivity: Date
    var messages: Int
    /// Optional so a daemon from before recording (ADR 0008) still decodes.
    var frames: Int?
    /// The first task message, clipped by the daemon. Optional so an older daemon still decodes.
    var task: String?
    /// The newest recorded frame, what the row's thumbnail is drawn from. nil for a run
    /// with none, and from a daemon before it (the row then shows the empty mark).
    var lastFrame: Frame?
    /// How the coding agent ended the run (root ADR 0031); nil while it has not.
    var finish: RunFinish?

    var id: String { runId }

    init(
        runId: String,
        createdAt: Date,
        destroyedAt: Date? = nil,
        image: String = "",
        status: RunStatus = .unknown(""),
        ip: String? = nil,
        steps: Int = 0,
        verdict: VerdictState? = nil,
        lastActivity: Date? = nil,
        messages: Int = 0,
        frames: Int? = nil,
        task: String? = nil,
        lastFrame: Frame? = nil,
        finish: RunFinish? = nil
    ) {
        self.runId = runId
        self.createdAt = createdAt
        self.destroyedAt = destroyedAt
        self.image = image
        self.status = status
        self.ip = ip
        self.steps = steps
        self.verdict = verdict
        self.lastActivity = lastActivity ?? createdAt
        self.messages = messages
        self.frames = frames
        self.task = task
        self.lastFrame = lastFrame
        self.finish = finish
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        runId = try c.decode(.runId, or: "")
        createdAt = try c.decode(.createdAt, or: .epoch)
        destroyedAt = try c.decodeIfPresent(Date.self, forKey: .destroyedAt)
        image = try c.decode(.image, or: "")
        status = try c.decode(.status, or: .unknown(""))
        ip = try c.decodeIfPresent(String.self, forKey: .ip)
        steps = try c.decode(.steps, or: 0)
        verdict = try c.decodeIfPresent(VerdictState.self, forKey: .verdict)
        lastActivity = try c.decode(.lastActivity, or: createdAt)
        messages = try c.decode(.messages, or: 0)
        frames = try c.decodeIfPresent(Int.self, forKey: .frames)
        task = try c.decodeIfPresent(String.self, forKey: .task)
        lastFrame = try c.decodeIfPresent(Frame.self, forKey: .lastFrame)
        finish = try? c.decodeIfPresent(RunFinish.self, forKey: .finish)
    }
}

struct RunDetail: Codable, Hashable, Sendable, Identifiable {
    var runId: String
    var image = ""
    var machineName = ""
    var ip: String?
    var createdAt = Date.epoch
    var destroyedAt: Date?
    var steps = 0
    var machine: Machine?
    var verdict = VerdictState()
    /// The manifest's `finish` (root ADR 0031).
    var finish: RunFinish?

    var id: String { runId }

    var address: String? { machine?.ip ?? ip }

    /// No live machine means finished, even for an old manifest with no
    /// `destroyedAt`: reading that as unknown contradicted the run list.
    var status: RunStatus {
        guard let machine else { return .finished }
        return RunStatus(text: machine.status.text)
    }
}

extension RunDetail {
    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        runId = try c.decode(.runId, or: "")
        image = try c.decode(.image, or: "")
        machineName = try c.decode(.machineName, or: "")
        ip = try c.decodeIfPresent(String.self, forKey: .ip)
        createdAt = try c.decode(.createdAt, or: .epoch)
        destroyedAt = try c.decodeIfPresent(Date.self, forKey: .destroyedAt)
        steps = try c.decode(.steps, or: 0)
        machine = try c.decodeIfPresent(Machine.self, forKey: .machine)
        verdict = try c.decode(.verdict, or: VerdictState())
        finish = try? c.decodeIfPresent(RunFinish.self, forKey: .finish)
    }
}

/// `GET /api/runs/{id}/messages`.
struct MessagePage: Codable, Hashable, Sendable {
    var messages: [Message]
    var last: Int
    var verdict: VerdictState
}

extension MessagePage {
    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        messages = try c.decode(.messages, or: [])
        last = try c.decode(.last, or: messages.last?.seq ?? 0)
        verdict = try c.decode(.verdict, or: VerdictState())
    }
}
