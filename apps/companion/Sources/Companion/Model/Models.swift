import Foundation

// MARK: - Open enums

/// A string-backed enum that keeps a value it does not recognise instead of
/// failing to decode. The daemon may grow a message kind or a machine status
/// before the app knows about it, and that must never crash a window.
protocol OpenEnum: Codable, Hashable, Sendable, CustomStringConvertible {
    init(text: String)
    var text: String { get }
}

extension OpenEnum {
    init(from decoder: any Decoder) throws {
        self.init(text: try decoder.singleValueContainer().decode(String.self))
    }

    func encode(to encoder: any Encoder) throws {
        var container = encoder.singleValueContainer()
        try container.encode(text)
    }

    var description: String { text }
}

/// The lifecycle of a run as the run list reports it. `finished` is a run whose
/// machine is gone; the other three come from the machine package.
enum RunStatus: OpenEnum {
    case booting
    case ready
    case failed
    case finished
    case unknown(String)

    init(text: String) {
        switch text {
        case "booting": self = .booting
        case "ready": self = .ready
        case "failed": self = .failed
        case "finished": self = .finished
        default: self = .unknown(text)
        }
    }

    var text: String {
        switch self {
        case .booting: return "booting"
        case .ready: return "ready"
        case .failed: return "failed"
        case .finished: return "finished"
        case .unknown(let raw): return raw
        }
    }
}

/// A machine's own status. A run can be `finished` while no machine exists at all.
enum MachineStatus: OpenEnum {
    case booting
    case ready
    case failed
    case unknown(String)

    init(text: String) {
        switch text {
        case "booting": self = .booting
        case "ready": self = .ready
        case "failed": self = .failed
        default: self = .unknown(text)
        }
    }

    var text: String {
        switch self {
        case .booting: return "booting"
        case .ready: return "ready"
        case .failed: return "failed"
        case .unknown(let raw): return raw
        }
    }
}

/// Who said something. ADR 0006 names four participants.
enum MessageFrom: OpenEnum {
    case coder
    case human
    case verifier
    case system
    case unknown(String)

    init(text: String) {
        switch text {
        case "coder": self = .coder
        case "human": self = .human
        case "verifier": self = .verifier
        case "system": self = .system
        default: self = .unknown(text)
        }
    }

    var text: String {
        switch self {
        case .coder: return "coder"
        case .human: return "human"
        case .verifier: return "verifier"
        case .system: return "system"
        case .unknown(let raw): return raw
        }
    }
}

/// What a message is for. The table in ADR 0006 is the reference.
enum MessageKind: OpenEnum {
    case task
    case note
    case reply
    case question
    case answer
    case progress
    case verdict
    case accept
    case dispute
    case event
    case unknown(String)

    init(text: String) {
        switch text {
        case "task": self = .task
        case "note": self = .note
        case "reply": self = .reply
        case "question": self = .question
        case "answer": self = .answer
        case "progress": self = .progress
        case "verdict": self = .verdict
        case "accept": self = .accept
        case "dispute": self = .dispute
        case "event": self = .event
        default: self = .unknown(text)
        }
    }

    var text: String {
        switch self {
        case .task: return "task"
        case .note: return "note"
        case .reply: return "reply"
        case .question: return "question"
        case .answer: return "answer"
        case .progress: return "progress"
        case .verdict: return "verdict"
        case .accept: return "accept"
        case .dispute: return "dispute"
        case .event: return "event"
        case .unknown(let raw): return raw
        }
    }
}

/// Where the latest verdict stands.
enum VerdictStatus: OpenEnum {
    case none
    case proposed
    case accepted
    case contested
    case rejected
    case unknown(String)

    init(text: String) {
        switch text {
        case "none": self = .none
        case "proposed": self = .proposed
        case "accepted": self = .accepted
        case "contested": self = .contested
        case "rejected": self = .rejected
        default: self = .unknown(text)
        }
    }

    var text: String {
        switch self {
        case .none: return "none"
        case .proposed: return "proposed"
        case .accepted: return "accepted"
        case .contested: return "contested"
        case .rejected: return "rejected"
        case .unknown(let raw): return raw
        }
    }

    /// A human may close a verdict that is still open to argument.
    var isOpen: Bool {
        switch self {
        case .proposed, .contested: return true
        default: return false
        }
    }
}

/// What kind of lifecycle event the daemon published.
enum LifecycleKind: OpenEnum {
    case created
    case ready
    case failed
    case destroyed
    case unknown(String)

    init(text: String) {
        switch text {
        case "created": self = .created
        case "ready": self = .ready
        case "failed": self = .failed
        case "destroyed": self = .destroyed
        default: self = .unknown(text)
        }
    }

    var text: String {
        switch self {
        case .created: return "created"
        case .ready: return "ready"
        case .failed: return "failed"
        case .destroyed: return "destroyed"
        case .unknown(let raw): return raw
        }
    }
}

// MARK: - Dates

/// RFC3339 in, RFC3339 out. Go writes fractional seconds only when it has
/// them, so both spellings have to parse.
enum DaemonDate {
    static let withFractionalSeconds = Date.ISO8601FormatStyle(includingFractionalSeconds: true)
    static let plain = Date.ISO8601FormatStyle()

    static func parse(_ text: String) -> Date? {
        if let date = try? withFractionalSeconds.parse(text) { return date }
        return try? plain.parse(text)
    }

    static func format(_ date: Date) -> String {
        date.formatted(withFractionalSeconds)
    }
}

extension JSONDecoder {
    /// The one decoder the app uses for daemon JSON.
    static func daemon() -> JSONDecoder {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .custom { decoder in
            let container = try decoder.singleValueContainer()
            let text = try container.decode(String.self)
            guard let date = DaemonDate.parse(text) else {
                throw DecodingError.dataCorruptedError(
                    in: container,
                    debugDescription: "not an RFC3339 time: \(text)"
                )
            }
            return date
        }
        return decoder
    }
}

extension JSONEncoder {
    static func daemon() -> JSONEncoder {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .custom { date, encoder in
            var container = encoder.singleValueContainer()
            try container.encode(DaemonDate.format(date))
        }
        return encoder
    }
}

// MARK: - JSON values

/// A step's input and output are whatever the tool passed. The app shows them
/// rather than interpreting them, so it keeps the tree as it arrived.
indirect enum JSONValue: Codable, Hashable, Sendable {
    case null
    case bool(Bool)
    case int(Int)
    case double(Double)
    case string(String)
    case array([JSONValue])
    case object([String: JSONValue])

    init(from decoder: any Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() {
            self = .null
        } else if let value = try? container.decode(Bool.self) {
            self = .bool(value)
        } else if let value = try? container.decode(Int.self) {
            self = .int(value)
        } else if let value = try? container.decode(Double.self) {
            self = .double(value)
        } else if let value = try? container.decode(String.self) {
            self = .string(value)
        } else if let value = try? container.decode([JSONValue].self) {
            self = .array(value)
        } else if let value = try? container.decode([String: JSONValue].self) {
            self = .object(value)
        } else {
            throw DecodingError.dataCorruptedError(in: container, debugDescription: "not JSON")
        }
    }

    func encode(to encoder: any Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .null: try container.encodeNil()
        case .bool(let value): try container.encode(value)
        case .int(let value): try container.encode(value)
        case .double(let value): try container.encode(value)
        case .string(let value): try container.encode(value)
        case .array(let value): try container.encode(value)
        case .object(let value): try container.encode(value)
        }
    }

    /// The value at a top-level key, if this is an object.
    subscript(key: String) -> JSONValue? {
        if case .object(let fields) = self { return fields[key] }
        return nil
    }

    var stringValue: String? {
        if case .string(let value) = self { return value }
        return nil
    }

    /// Indented JSON for the steps view. Falls back to a plain description if
    /// re-encoding ever fails.
    var prettyPrinted: String {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]
        guard let data = try? encoder.encode(self), let text = String(data: data, encoding: .utf8) else {
            return String(describing: self)
        }
        return text
    }
}

// MARK: - Payloads

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
    var vncUrl: String?

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        runId = try c.decodeIfPresent(String.self, forKey: .runId) ?? ""
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        image = try c.decodeIfPresent(String.self, forKey: .image) ?? ""
        ip = try c.decodeIfPresent(String.self, forKey: .ip)
        status = try c.decodeIfPresent(MachineStatus.self, forKey: .status) ?? .unknown("")
        error = try c.decodeIfPresent(String.self, forKey: .error)
        bootSeconds = try c.decodeIfPresent(Double.self, forKey: .bootSeconds)
        createdAt = try c.decodeIfPresent(Date.self, forKey: .createdAt) ?? Date(timeIntervalSince1970: 0)
        dir = try c.decodeIfPresent(String.self, forKey: .dir) ?? ""
        vncUrl = try c.decodeIfPresent(String.self, forKey: .vncUrl)
    }
}

struct VerdictState: Codable, Hashable, Sendable {
    var seq: Int?
    var verdict: String?
    var summary: String?
    var evidence: [String]?
    var status: VerdictStatus
    var acceptedBy: MessageFrom?
    var disputes: Int

    init(
        seq: Int? = nil,
        verdict: String? = nil,
        summary: String? = nil,
        evidence: [String]? = nil,
        status: VerdictStatus = .none,
        acceptedBy: MessageFrom? = nil,
        disputes: Int = 0
    ) {
        self.seq = seq
        self.verdict = verdict
        self.summary = summary
        self.evidence = evidence
        self.status = status
        self.acceptedBy = acceptedBy
        self.disputes = disputes
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        seq = try c.decodeIfPresent(Int.self, forKey: .seq)
        verdict = try c.decodeIfPresent(String.self, forKey: .verdict)
        summary = try c.decodeIfPresent(String.self, forKey: .summary)
        evidence = try c.decodeIfPresent([String].self, forKey: .evidence)
        status = try c.decodeIfPresent(VerdictStatus.self, forKey: .status) ?? .none
        acceptedBy = try c.decodeIfPresent(MessageFrom.self, forKey: .acceptedBy)
        disputes = try c.decodeIfPresent(Int.self, forKey: .disputes) ?? 0
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

    var id: Int { seq }

    init(
        seq: Int,
        at: Date,
        from: MessageFrom,
        kind: MessageKind,
        text: String,
        replyTo: Int? = nil,
        step: Int? = nil,
        verdict: String? = nil,
        evidence: [String]? = nil
    ) {
        self.seq = seq
        self.at = at
        self.from = from
        self.kind = kind
        self.text = text
        self.replyTo = replyTo
        self.step = step
        self.verdict = verdict
        self.evidence = evidence
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        seq = try c.decodeIfPresent(Int.self, forKey: .seq) ?? 0
        at = try c.decodeIfPresent(Date.self, forKey: .at) ?? Date(timeIntervalSince1970: 0)
        from = try c.decodeIfPresent(MessageFrom.self, forKey: .from) ?? .unknown("")
        kind = try c.decodeIfPresent(MessageKind.self, forKey: .kind) ?? .unknown("")
        text = try c.decodeIfPresent(String.self, forKey: .text) ?? ""
        replyTo = try c.decodeIfPresent(Int.self, forKey: .replyTo)
        step = try c.decodeIfPresent(Int.self, forKey: .step)
        verdict = try c.decodeIfPresent(String.self, forKey: .verdict)
        evidence = try c.decodeIfPresent([String].self, forKey: .evidence)
    }
}

struct Step: Codable, Hashable, Sendable, Identifiable {
    var seq: Int
    var at: Date
    var tool: String
    var input: JSONValue?
    var output: JSONValue?
    var error: String?
    var durationMs: Int

    var id: Int { seq }

    init(
        seq: Int,
        at: Date,
        tool: String,
        input: JSONValue? = nil,
        output: JSONValue? = nil,
        error: String? = nil,
        durationMs: Int = 0
    ) {
        self.seq = seq
        self.at = at
        self.tool = tool
        self.input = input
        self.output = output
        self.error = error
        self.durationMs = durationMs
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        seq = try c.decodeIfPresent(Int.self, forKey: .seq) ?? 0
        at = try c.decodeIfPresent(Date.self, forKey: .at) ?? Date(timeIntervalSince1970: 0)
        tool = try c.decodeIfPresent(String.self, forKey: .tool) ?? ""
        input = try c.decodeIfPresent(JSONValue.self, forKey: .input)
        output = try c.decodeIfPresent(JSONValue.self, forKey: .output)
        error = try c.decodeIfPresent(String.self, forKey: .error)
        durationMs = try c.decodeIfPresent(Int.self, forKey: .durationMs) ?? 0
    }

    /// The artifact name of a screenshot step, taken from `output.path`.
    var screenshotArtifact: String? {
        guard tool == "machine_screenshot", let path = output?["path"]?.stringValue else { return nil }
        let name = (path as NSString).lastPathComponent
        return name.isEmpty ? nil : name
    }
}

/// One frame of a run's recording (ADR 0008): a screenshot the daemon takes
/// on its own, every `-frame-interval`, from `ready` to `destroyed`. `step`
/// is the latest step number recorded when the frame was taken, which is
/// what makes "jump to step 14" possible.
struct Frame: Codable, Hashable, Sendable, Identifiable {
    var at: Date
    var file: String
    var step: Int
    var bytes: Int

    var id: String { file }

    init(at: Date, file: String, step: Int, bytes: Int = 0) {
        self.at = at
        self.file = file
        self.step = step
        self.bytes = bytes
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        at = try c.decodeIfPresent(Date.self, forKey: .at) ?? Date(timeIntervalSince1970: 0)
        file = try c.decodeIfPresent(String.self, forKey: .file) ?? ""
        step = try c.decodeIfPresent(Int.self, forKey: .step) ?? 0
        bytes = try c.decodeIfPresent(Int.self, forKey: .bytes) ?? 0
    }
}

struct RunSummary: Codable, Hashable, Sendable, Identifiable {
    var runId: String
    var createdAt: Date
    var destroyedAt: Date?
    var image: String
    var status: RunStatus
    var ip: String?
    var vncUrl: String?
    var steps: Int
    var verdict: VerdictState?
    var lastActivity: Date
    var messages: Int
    /// How many frames the run's recording holds so far (ADR 0008). Optional
    /// so a daemon built before recording existed still decodes.
    var frames: Int?

    var id: String { runId }

    init(
        runId: String,
        createdAt: Date,
        destroyedAt: Date? = nil,
        image: String = "",
        status: RunStatus = .unknown(""),
        ip: String? = nil,
        vncUrl: String? = nil,
        steps: Int = 0,
        verdict: VerdictState? = nil,
        lastActivity: Date? = nil,
        messages: Int = 0,
        frames: Int? = nil
    ) {
        self.runId = runId
        self.createdAt = createdAt
        self.destroyedAt = destroyedAt
        self.image = image
        self.status = status
        self.ip = ip
        self.vncUrl = vncUrl
        self.steps = steps
        self.verdict = verdict
        self.lastActivity = lastActivity ?? createdAt
        self.messages = messages
        self.frames = frames
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        runId = try c.decodeIfPresent(String.self, forKey: .runId) ?? ""
        createdAt = try c.decodeIfPresent(Date.self, forKey: .createdAt) ?? Date(timeIntervalSince1970: 0)
        destroyedAt = try c.decodeIfPresent(Date.self, forKey: .destroyedAt)
        image = try c.decodeIfPresent(String.self, forKey: .image) ?? ""
        status = try c.decodeIfPresent(RunStatus.self, forKey: .status) ?? .unknown("")
        ip = try c.decodeIfPresent(String.self, forKey: .ip)
        vncUrl = try c.decodeIfPresent(String.self, forKey: .vncUrl)
        steps = try c.decodeIfPresent(Int.self, forKey: .steps) ?? 0
        verdict = try c.decodeIfPresent(VerdictState.self, forKey: .verdict)
        lastActivity = try c.decodeIfPresent(Date.self, forKey: .lastActivity) ?? createdAt
        messages = try c.decodeIfPresent(Int.self, forKey: .messages) ?? 0
        frames = try c.decodeIfPresent(Int.self, forKey: .frames)
    }

    /// The first segment of the run id, which is enough to tell runs apart.
    var shortId: String { RunSummary.shorten(runId) }

    static func shorten(_ runId: String) -> String {
        guard runId.count > 12 else { return runId }
        var short = String(runId.prefix(12))
        while let last = short.last, last == "-" || last == "_" { short.removeLast() }
        return short
    }
}

struct RunDetail: Codable, Hashable, Sendable, Identifiable {
    var runId: String
    var image: String
    var machineName: String
    var ip: String?
    var createdAt: Date
    var destroyedAt: Date?
    var steps: Int
    var machine: Machine?
    var verdict: VerdictState

    var id: String { runId }

    init(
        runId: String,
        image: String = "",
        machineName: String = "",
        ip: String? = nil,
        createdAt: Date = Date(timeIntervalSince1970: 0),
        destroyedAt: Date? = nil,
        steps: Int = 0,
        machine: Machine? = nil,
        verdict: VerdictState = VerdictState()
    ) {
        self.runId = runId
        self.image = image
        self.machineName = machineName
        self.ip = ip
        self.createdAt = createdAt
        self.destroyedAt = destroyedAt
        self.steps = steps
        self.machine = machine
        self.verdict = verdict
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        runId = try c.decodeIfPresent(String.self, forKey: .runId) ?? ""
        image = try c.decodeIfPresent(String.self, forKey: .image) ?? ""
        machineName = try c.decodeIfPresent(String.self, forKey: .machineName) ?? ""
        ip = try c.decodeIfPresent(String.self, forKey: .ip)
        createdAt = try c.decodeIfPresent(Date.self, forKey: .createdAt) ?? Date(timeIntervalSince1970: 0)
        destroyedAt = try c.decodeIfPresent(Date.self, forKey: .destroyedAt)
        steps = try c.decodeIfPresent(Int.self, forKey: .steps) ?? 0
        machine = try c.decodeIfPresent(Machine.self, forKey: .machine)
        verdict = try c.decodeIfPresent(VerdictState.self, forKey: .verdict) ?? VerdictState()
    }

    /// The address a run is reachable at, from the live machine first.
    var address: String? { machine?.ip ?? ip }

    var status: RunStatus {
        guard let machine else { return destroyedAt == nil ? .unknown("") : .finished }
        switch machine.status {
        case .booting: return .booting
        case .ready: return .ready
        case .failed: return .failed
        case .unknown(let raw): return .unknown(raw)
        }
    }
}

struct LifecycleEvent: Codable, Hashable, Sendable {
    var kind: LifecycleKind
    var runId: String
    var machine: Machine?

    init(kind: LifecycleKind, runId: String, machine: Machine? = nil) {
        self.kind = kind
        self.runId = runId
        self.machine = machine
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        kind = try c.decodeIfPresent(LifecycleKind.self, forKey: .kind) ?? .unknown("")
        runId = try c.decodeIfPresent(String.self, forKey: .runId) ?? ""
        machine = try c.decodeIfPresent(Machine.self, forKey: .machine)
    }
}

/// `GET /api/runs/{id}/messages`.
struct MessagePage: Codable, Hashable, Sendable {
    var messages: [Message]
    var last: Int
    var verdict: VerdictState

    init(messages: [Message] = [], last: Int = 0, verdict: VerdictState = VerdictState()) {
        self.messages = messages
        self.last = last
        self.verdict = verdict
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        messages = try c.decodeIfPresent([Message].self, forKey: .messages) ?? []
        last = try c.decodeIfPresent(Int.self, forKey: .last) ?? messages.last?.seq ?? 0
        verdict = try c.decodeIfPresent(VerdictState.self, forKey: .verdict) ?? VerdictState()
    }
}

/// The body `POST /api/runs/{id}/messages` returns.
struct SentMessage: Codable, Hashable, Sendable {
    var seq: Int
    var at: Date
}

/// The body `POST /api/runs/{id}/screenshot` returns.
struct ScreenshotResult: Codable, Hashable, Sendable {
    var step: Int
    var path: String
    var bytes: Int

    var artifactName: String { (path as NSString).lastPathComponent }
}

/// One frame off `/api/events`.
enum ServerEvent: Hashable, Sendable {
    case run(LifecycleEvent)
    /// The daemon sends the step's number; `step` is filled in only when a
    /// stream carries the whole record.
    case step(runId: String, seq: Int, step: Step?)
    case message(runId: String, message: Message)
    /// A new frame was captured for the run's recording (ADR 0008).
    case frame(runId: String, frame: Frame)

    var runId: String {
        switch self {
        case .run(let event): return event.runId
        case .step(let runId, _, _): return runId
        case .message(let runId, _): return runId
        case .frame(let runId, _): return runId
        }
    }
}

/// The envelope a step event arrives in. `step` is a number on the wire today;
/// a whole Step is accepted too so the app does not care which it gets.
struct StepEvent: Codable, Hashable, Sendable {
    var runId: String
    var seq: Int
    var step: Step?

    init(runId: String, seq: Int, step: Step? = nil) {
        self.runId = runId
        self.seq = seq
        self.step = step
    }

    private enum CodingKeys: String, CodingKey {
        case runId, step
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        runId = try c.decodeIfPresent(String.self, forKey: .runId) ?? ""
        if let number = try? c.decode(Int.self, forKey: .step) {
            seq = number
            step = nil
        } else {
            let record = try c.decode(Step.self, forKey: .step)
            seq = record.seq
            step = record
        }
    }

    func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(runId, forKey: .runId)
        if let step {
            try c.encode(step, forKey: .step)
        } else {
            try c.encode(seq, forKey: .step)
        }
    }
}

struct MessageEvent: Codable, Hashable, Sendable {
    var runId: String
    var message: Message
}

/// The envelope a `frame` event arrives in on `/api/events`: `{ runId, at,
/// file, step }`, with no `bytes` (that only comes back from `GET .../frames`).
struct FrameEvent: Codable, Hashable, Sendable {
    var runId: String
    var frame: Frame

    init(runId: String, frame: Frame) {
        self.runId = runId
        self.frame = frame
    }

    private enum CodingKeys: String, CodingKey {
        case runId, at, file, step
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        runId = try c.decodeIfPresent(String.self, forKey: .runId) ?? ""
        let at = try c.decodeIfPresent(Date.self, forKey: .at) ?? Date(timeIntervalSince1970: 0)
        let file = try c.decodeIfPresent(String.self, forKey: .file) ?? ""
        let step = try c.decodeIfPresent(Int.self, forKey: .step) ?? 0
        frame = Frame(at: at, file: file, step: step)
    }

    func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(runId, forKey: .runId)
        try c.encode(frame.at, forKey: .at)
        try c.encode(frame.file, forKey: .file)
        try c.encode(frame.step, forKey: .step)
    }
}
