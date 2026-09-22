/// A string enum that decodes a value it does not recognise into `unknown`
/// instead of throwing: the daemon may add a kind before the app knows it.
protocol OpenEnum: Codable, Hashable, Sendable, CustomStringConvertible {
    static var known: [Self] { get }
    static func unknown(_ text: String) -> Self
    var text: String { get }
}

extension OpenEnum {
    init(text: String) {
        self = Self.known.first { $0.text == text } ?? .unknown(text)
    }

    init(from decoder: any Decoder) throws {
        self.init(text: try decoder.singleValueContainer().decode(String.self))
    }

    func encode(to encoder: any Encoder) throws {
        var container = encoder.singleValueContainer()
        try container.encode(text)
    }

    var description: String { text }
}

/// `finished` means the machine is gone; the rest mirror `MachineStatus`.
enum RunStatus: OpenEnum {
    case booting, ready, failed, finished
    case unknown(String)

    static let known: [Self] = [.booting, .ready, .failed, .finished]

    var text: String {
        switch self {
        case .booting: "booting"
        case .ready: "ready"
        case .failed: "failed"
        case .finished: "finished"
        case .unknown(let raw): raw
        }
    }
}

enum MachineStatus: OpenEnum {
    case booting, ready, failed
    case unknown(String)

    static let known: [Self] = [.booting, .ready, .failed]

    var text: String {
        switch self {
        case .booting: "booting"
        case .ready: "ready"
        case .failed: "failed"
        case .unknown(let raw): raw
        }
    }
}

/// The participants of ADR 0006.
enum MessageFrom: OpenEnum {
    case coder, human, verifier, system
    case unknown(String)

    static let known: [Self] = [.coder, .human, .verifier, .system]

    var text: String {
        switch self {
        case .coder: "coder"
        case .human: "human"
        case .verifier: "verifier"
        case .system: "system"
        case .unknown(let raw): raw
        }
    }
}

/// The message kinds of ADR 0006.
enum MessageKind: OpenEnum {
    case task, note, reply, question, answer, progress, verdict, accept, dispute, event
    case unknown(String)

    static let known: [Self] = [
        .task, .note, .reply, .question, .answer, .progress, .verdict, .accept, .dispute, .event,
    ]

    var text: String {
        switch self {
        case .task: "task"
        case .note: "note"
        case .reply: "reply"
        case .question: "question"
        case .answer: "answer"
        case .progress: "progress"
        case .verdict: "verdict"
        case .accept: "accept"
        case .dispute: "dispute"
        case .event: "event"
        case .unknown(let raw): raw
        }
    }
}

enum VerdictStatus: OpenEnum {
    case none, proposed, accepted, contested, rejected
    case unknown(String)

    static let known: [Self] = [.none, .proposed, .accepted, .contested, .rejected]

    var text: String {
        switch self {
        case .none: "none"
        case .proposed: "proposed"
        case .accepted: "accepted"
        case .contested: "contested"
        case .rejected: "rejected"
        case .unknown(let raw): raw
        }
    }

    /// A human may still accept or dispute it.
    var isOpen: Bool { self == .proposed || self == .contested }
}

enum LifecycleKind: OpenEnum {
    /// `control`: a screen lease was taken or given back (ADR 0009).
    case created, ready, failed, destroyed, control
    case unknown(String)

    static let known: [Self] = [.created, .ready, .failed, .destroyed, .control]

    var text: String {
        switch self {
        case .created: "created"
        case .ready: "ready"
        case .failed: "failed"
        case .destroyed: "destroyed"
        case .control: "control"
        case .unknown(let raw): raw
        }
    }
}
