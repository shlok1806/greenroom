import Foundation

// A run in the words a person reads first, as the daemon derives it (root ADR 0036,
// `apps/daemon/internal/summary`): `GET /api/summary` (the board), `GET /api/runs/{id}/summary`
// and the event stream's `summary` event. The Companion renders it as it comes (companion ADR
// 0019): the status word, the group, the tally, the sentence under the status, what the run is
// doing now and the one primary action are the daemon's, never worked out here.
//
// Every field decodes leniently: a daemon that leaves one out, or sends a value this app does
// not know, still gives a summary (an unknown state keeps its word; an unknown action id is
// shown by its label and does nothing it cannot do).

/// A status word's stable id (the ADR's vocabulary).
enum SummaryState: Hashable, Sendable {
    case starting, checking, paused, notAnswering, restarting, passed, failed, inconclusive, stopped
    case unknown(String)

    init(_ raw: String) {
        switch raw {
        case "starting": self = .starting
        case "checking": self = .checking
        case "paused": self = .paused
        case "not-answering": self = .notAnswering
        case "restarting": self = .restarting
        case "passed": self = .passed
        case "failed": self = .failed
        case "inconclusive": self = .inconclusive
        case "stopped": self = .stopped
        default: self = .unknown(raw)
        }
    }

    /// Whether the run has an outcome the checks show.
    var isOutcome: Bool { self == .passed || self == .failed || self == .inconclusive }

    /// Whether the Mac is working on the run: the header's time counts up.
    var isWorking: Bool { self == .starting || self == .checking || self == .restarting }
}

/// The one colour a status may carry.
enum SummaryTone: String, Sendable {
    case pass, fail, live, wait, quiet
}

/// Where a run sits in the list.
enum SummaryGroup: String, CaseIterable, Sendable {
    case needsYou = "needs-you"
    case running
    case done

    var title: String {
        switch self {
        case .needsYou: "Needs you"
        case .running: "Running"
        case .done: "Done"
        }
    }
}

/// One thing a person can do, as a control says it.
struct SummaryAction: Hashable, Sendable, Decodable {
    /// accept, reject, continue, answer, restart, keep-waiting, take-control, give-back, recheck.
    var id: String
    var label: String

    static let accept = "accept"
    static let reject = "reject"
    static let `continue` = "continue"
    static let answer = "answer"
    static let restart = "restart"
    static let keepWaiting = "keep-waiting"
    static let takeControl = "take-control"
    static let giveBack = "give-back"
    static let recheck = "recheck"

    init(id: String, label: String) {
        self.id = id
        self.label = label
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(.id, or: "")
        label = try c.decode(.label, or: "")
    }

    private enum CodingKeys: String, CodingKey { case id, label }
}

/// An image of the screen and where to fetch it.
struct SummaryPicture: Hashable, Sendable, Decodable {
    /// "screenshot" (a PNG artifact) or "frame" (a recorded JPEG).
    var kind: String
    var file: String
    var url: String
    var at: Date?
    var step: Int?

    init(kind: String, file: String, url: String = "", at: Date? = nil, step: Int? = nil) {
        (self.kind, self.file, self.url, self.at, self.step) = (kind, file, url, at, step)
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        kind = try c.decode(.kind, or: "")
        file = try c.decode(.file, or: "")
        url = try c.decode(.url, or: "")
        at = try? c.decodeIfPresent(Date.self, forKey: .at)
        step = try c.decodeIfPresent(Int.self, forKey: .step)
    }

    private enum CodingKeys: String, CodingKey { case kind, file, url, at, step }

    var isScreenshot: Bool { kind == "screenshot" }
}

/// A rectangle as fractions of the screen: left, top, width, height.
struct SummaryBox: Hashable, Sendable, Decodable {
    var x: Double
    var y: Double
    var w: Double
    var h: Double
}

/// One check as its row and its proof read it.
struct SummaryCheck: Hashable, Sendable, Decodable, Identifiable {
    enum State: String, Sendable { case pass, fail, pending }

    var id: String
    var text: String
    var state: State
    var expected: String?
    var saw: String?
    var observed: String?
    var step: Int?
    var picture: SummaryPicture?
    var mark: SummaryBox?

    init(id: String, text: String, state: State, expected: String? = nil, saw: String? = nil,
         observed: String? = nil, step: Int? = nil, picture: SummaryPicture? = nil, mark: SummaryBox? = nil) {
        (self.id, self.text, self.state, self.expected, self.saw) = (id, text, state, expected, saw)
        (self.observed, self.step, self.picture, self.mark) = (observed, step, picture, mark)
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(.id, or: "")
        text = try c.decode(.text, or: "")
        state = State(rawValue: try c.decode(.state, or: "pending")) ?? .pending
        expected = (try c.decodeIfPresent(String.self, forKey: .expected)).nonEmpty
        saw = (try c.decodeIfPresent(String.self, forKey: .saw)).nonEmpty
        observed = (try c.decodeIfPresent(String.self, forKey: .observed)).nonEmpty
        step = try c.decodeIfPresent(Int.self, forKey: .step)
        picture = try? c.decodeIfPresent(SummaryPicture.self, forKey: .picture)
        mark = try? c.decodeIfPresent(SummaryBox.self, forKey: .mark)
    }

    private enum CodingKeys: String, CodingKey { case id, text, state, expected, saw, observed, step, picture, mark }
}

/// The tally of the checks that apply now.
struct SummaryChecks: Hashable, Sendable, Decodable {
    var total = 0
    var passed = 0
    var failed = 0
    var pending = 0
    /// "2 of 4 checks failed"; empty with no checks.
    var text = ""
    var items: [SummaryCheck] = []

    init(total: Int = 0, passed: Int = 0, failed: Int = 0, pending: Int = 0, text: String = "", items: [SummaryCheck] = []) {
        (self.total, self.passed, self.failed, self.pending, self.text, self.items) = (total, passed, failed, pending, text, items)
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        total = try c.decode(.total, or: 0)
        passed = try c.decode(.passed, or: 0)
        failed = try c.decode(.failed, or: 0)
        pending = try c.decode(.pending, or: 0)
        text = try c.decode(.text, or: "")
        items = (try? c.decodeIfPresent([SummaryCheck].self, forKey: .items)) ?? []
    }

    private enum CodingKeys: String, CodingKey { case total, passed, failed, pending, text, items }
}

/// The first failing check and its proof.
struct SummaryFailing: Hashable, Sendable, Decodable {
    var text: String
    var expected: String?
    var saw: String?
    var observed: String?
    var step: Int?
    var picture: SummaryPicture?
    var mark: SummaryBox?

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        text = try c.decode(.text, or: "")
        expected = (try c.decodeIfPresent(String.self, forKey: .expected)).nonEmpty
        saw = (try c.decodeIfPresent(String.self, forKey: .saw)).nonEmpty
        observed = (try c.decodeIfPresent(String.self, forKey: .observed)).nonEmpty
        step = try c.decodeIfPresent(Int.self, forKey: .step)
        picture = try? c.decodeIfPresent(SummaryPicture.self, forKey: .picture)
        mark = try? c.decodeIfPresent(SummaryBox.self, forKey: .mark)
    }

    private enum CodingKeys: String, CodingKey { case text, expected, saw, observed, step, picture, mark }
}

/// The run's Mac in words.
struct SummaryMachine: Hashable, Sendable, Decodable {
    /// "starting", "on", "restarting", "not running" or "off".
    var status = ""
    /// A problem a person should act on, in words.
    var warning: String?

    init(status: String = "", warning: String? = nil) {
        self.status = status
        self.warning = warning
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        status = try c.decode(.status, or: "")
        warning = (try c.decodeIfPresent(String.self, forKey: .warning)).nonEmpty
    }

    private enum CodingKeys: String, CodingKey { case status, warning }

    /// Whether a Mac is up for the run (on, starting or restarting).
    var isUp: Bool { status == "on" || status == "starting" || status == "restarting" }
}

/// One run's summary.
struct Summary: Hashable, Sendable, Decodable, Identifiable {
    var runId: String
    var name: String
    var source: String?
    var state: SummaryState
    var status: String
    var tone: SummaryTone
    var group: SummaryGroup
    var detail: String?
    var now: String?
    var since: Date
    var startedAt: Date
    var endedAt: Date?
    var elapsedSeconds: Int
    var checks: SummaryChecks
    var failing: SummaryFailing?
    var primaryAction: SummaryAction?
    var secondaryActions: [SummaryAction]
    var machine: SummaryMachine
    var outcome: String?
    var lastFrame: SummaryPicture?
    var updatedAt: Date

    var id: String { runId }

    init(runId: String, name: String, state: SummaryState = .checking, status: String = "Checking",
         tone: SummaryTone = .live, group: SummaryGroup = .running, since: Date = .epoch, startedAt: Date = .epoch) {
        self.runId = runId
        self.name = name
        self.state = state
        self.status = status
        self.tone = tone
        self.group = group
        self.since = since
        self.startedAt = startedAt
        elapsedSeconds = 0
        checks = SummaryChecks()
        secondaryActions = []
        machine = SummaryMachine()
        updatedAt = since
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        runId = try c.decode(.runId, or: "")
        name = try c.decode(.name, or: "")
        source = (try c.decodeIfPresent(String.self, forKey: .source)).nonEmpty
        state = SummaryState(try c.decode(.state, or: ""))
        status = try c.decode(.status, or: "")
        tone = SummaryTone(rawValue: try c.decode(.tone, or: "quiet")) ?? .quiet
        group = SummaryGroup(rawValue: try c.decode(.group, or: "done")) ?? .done
        detail = (try c.decodeIfPresent(String.self, forKey: .detail)).nonEmpty
        now = (try c.decodeIfPresent(String.self, forKey: .now)).nonEmpty
        since = (try? c.decodeIfPresent(Date.self, forKey: .since)) ?? .epoch
        startedAt = (try? c.decodeIfPresent(Date.self, forKey: .startedAt)) ?? .epoch
        endedAt = try? c.decodeIfPresent(Date.self, forKey: .endedAt)
        elapsedSeconds = try c.decode(.elapsedSeconds, or: 0)
        checks = (try? c.decodeIfPresent(SummaryChecks.self, forKey: .checks)) ?? SummaryChecks()
        failing = try? c.decodeIfPresent(SummaryFailing.self, forKey: .failing)
        primaryAction = (try? c.decodeIfPresent(SummaryAction.self, forKey: .primaryAction)).flatMap { $0.id.isEmpty ? nil : $0 }
        secondaryActions = ((try? c.decodeIfPresent([SummaryAction].self, forKey: .secondaryActions)) ?? []).filter { !$0.id.isEmpty }
        machine = (try? c.decodeIfPresent(SummaryMachine.self, forKey: .machine)) ?? SummaryMachine()
        outcome = (try c.decodeIfPresent(String.self, forKey: .outcome)).nonEmpty
        lastFrame = try? c.decodeIfPresent(SummaryPicture.self, forKey: .lastFrame)
        updatedAt = (try? c.decodeIfPresent(Date.self, forKey: .updatedAt)) ?? since
    }

    private enum CodingKeys: String, CodingKey {
        case runId, name, source, state, status, tone, group, detail, now, since, startedAt, endedAt
        case elapsedSeconds, checks, failing, primaryAction, secondaryActions, machine, outcome, lastFrame, updatedAt
    }
}

/// How many Macs the host may still start.
struct SummaryMacs: Hashable, Sendable, Decodable {
    var free = 0
    var total = 0
    /// "2 of 3 Macs free"; empty when the daemon has no limit.
    var text = ""

    init(free: Int = 0, total: Int = 0, text: String = "") {
        (self.free, self.total, self.text) = (free, total, text)
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        free = try c.decode(.free, or: 0)
        total = try c.decode(.total, or: 0)
        text = try c.decode(.text, or: "")
    }

    private enum CodingKeys: String, CodingKey { case free, total, text }
}

/// Every run's summary in its group.
struct SummaryBoard: Hashable, Sendable, Decodable {
    struct Group: Hashable, Sendable, Decodable {
        var id: SummaryGroup
        var title: String
        var count: Int
        var runs: [Summary]

        init(id: SummaryGroup, runs: [Summary]) {
            self.id = id
            title = id.title
            count = runs.count
            self.runs = runs
        }

        init(from decoder: any Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            id = SummaryGroup(rawValue: try c.decode(.id, or: "done")) ?? .done
            title = try c.decode(.title, or: id.title)
            runs = (try? c.decodeIfPresent([Summary].self, forKey: .runs)) ?? []
            count = try c.decode(.count, or: runs.count)
        }

        private enum CodingKeys: String, CodingKey { case id, title, count, runs }
    }

    var groups: [Group]
    var macs: SummaryMacs
    var updatedAt: Date?

    init(groups: [Group], macs: SummaryMacs = SummaryMacs()) {
        self.groups = groups
        self.macs = macs
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        groups = (try? c.decodeIfPresent([Group].self, forKey: .groups)) ?? []
        macs = (try? c.decodeIfPresent(SummaryMacs.self, forKey: .macs)) ?? SummaryMacs()
        updatedAt = try? c.decodeIfPresent(Date.self, forKey: .updatedAt)
    }

    private enum CodingKeys: String, CodingKey { case groups, macs, updatedAt }

    /// Every run, in list order.
    var runs: [Summary] { groups.flatMap(\.runs) }
}

/// The event stream's `summary` event: one run's new summary and the host's Macs.
struct SummaryEvent: Hashable, Sendable, Decodable {
    var runId: String
    var summary: Summary
    var macs: SummaryMacs?
}

extension Optional where Wrapped == String {
    /// Nil for an absent, empty or blank string.
    var nonEmpty: String? {
        guard let self, !self.trimmingCharacters(in: .whitespaces).isEmpty else { return nil }
        return self
    }
}
