import Foundation

// Pure rules for how runs, steps and the conversation are presented. No SwiftUI, so
// each rule has a test (`RunPresentationTests`).

/// What a run is called. The task says what it is for; the hash is only a fallback.
enum RunTitle {
    static func text(task: String?, runId: String) -> String {
        let flat = StepSummary.oneLine(task ?? "")
        // A brief that opens with a Markdown heading still reads as a sentence.
        let cleaned = flat.drop { $0 == "#" }.trimmingCharacters(in: .whitespaces)
        if !cleaned.isEmpty { return cleaned }
        let hash = Chrome.runHash(runId)
        return hash.isEmpty ? "Untitled run" : "Run \(hash)"
    }

    /// The line under a run's title: its whole task, or that it has none. With no task the
    /// title is already "Run <hash>", so repeating it under itself said nothing.
    static func subtitle(task: String?, alive: Bool) -> String {
        let flat = StepSummary.oneLine(task ?? "")
        if !flat.isEmpty { return text(task: flat, runId: "") }
        return alive ? "No task yet" : "No task was sent to this run"
    }

    /// The first task message of a transcript, for a run whose summary has none yet.
    static func task(in messages: [Message]) -> String? {
        messages.first { $0.kind == .task }?.text
    }
}

/// One item of a verdict's `evidence`: a step to seek, a screenshot to open, or words.
enum Evidence: Hashable, Sendable {
    case step(Int)
    /// A run-directory file, by name (`GET /api/runs/{id}/artifacts/{name}`).
    case artifact(String)
    case text(String)

    static func parse(_ raw: String) -> Evidence {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        let lower = trimmed.lowercased()
        if lower.hasPrefix("step"), let number = Int(lower.dropFirst(4).trimmingCharacters(in: .whitespaces)) {
            return .step(number)
        }
        let name = (trimmed as NSString).lastPathComponent
        if trimmed.contains("/"), name.contains("."), !name.hasPrefix(".") {
            return .artifact(name)
        }
        return .text(trimmed)
    }

    /// The step a screenshot artifact was taken at, from its `NNN-screenshot.png` name.
    static func step(ofArtifact name: String) -> Int? {
        let digits = name.prefix { $0.isNumber }
        guard !digits.isEmpty, name.dropFirst(digits.count).hasPrefix("-") else { return nil }
        return Int(digits)
    }

    var label: String {
        switch self {
        case .step(let number): "Step \(number)"
        case .artifact(let name): name
        case .text(let words): words
        }
    }

    /// The step this item points at, if any.
    var step: Int? {
        switch self {
        case .step(let number): number
        case .artifact(let name): Evidence.step(ofArtifact: name)
        case .text: nil
        }
    }
}

/// A tool's readable name and symbol. Unknown tools keep their raw name.
enum ToolCatalog {
    struct Entry: Equatable, Sendable {
        var title: String
        var symbol: String
    }

    static func entry(for tool: String) -> Entry {
        switch tool {
        case "machine_create": Entry(title: "Create machine", symbol: "shippingbox")
        case "machine_boot": Entry(title: "Boot", symbol: "power")
        case "machine_wait": Entry(title: "Wait", symbol: "hourglass")
        case "machine_exec": Entry(title: "Command", symbol: "terminal")
        case "machine_sync": Entry(title: "Sync files", symbol: "arrow.triangle.2.circlepath")
        case "machine_pull": Entry(title: "Pull files", symbol: "square.and.arrow.down")
        case "machine_screenshot": Entry(title: "Screenshot", symbol: "camera")
        case "machine_input": Entry(title: "Input", symbol: "cursorarrow.click")
        case "machine_click": Entry(title: "Click", symbol: "cursorarrow.click")
        case "machine_type": Entry(title: "Type", symbol: "keyboard")
        case "machine_key": Entry(title: "Key", symbol: "keyboard")
        case "machine_scroll": Entry(title: "Scroll", symbol: "scroll")
        case "machine_ui": Entry(title: "Read UI", symbol: "list.bullet.indent")
        case "machine_approve_capture": Entry(title: "Approve capture", symbol: "checkmark.shield")
        case "machine_list": Entry(title: "List machines", symbol: "list.bullet")
        case "machine_destroy": Entry(title: "Destroy", symbol: "trash")
        case let name where name.hasPrefix("machine_session"): Entry(title: "Session", symbol: "apple.terminal")
        default: Entry(title: tool.isEmpty ? "Tool" : tool, symbol: "wrench.and.screwdriver")
        }
    }

    /// A progress message's first line is `<tool> <json>`; the tool is the first word,
    /// always snake_case (`machine_screenshot`), which prose never starts with.
    static func tool(ofProgress text: String) -> String? {
        let first = text.split(separator: "\n", maxSplits: 1).first.map(String.init) ?? ""
        let word = first.split(separator: " ", maxSplits: 1).first.map(String.init) ?? ""
        let snake = word.allSatisfy { $0.isLowercase || $0.isNumber || $0 == "_" }
        guard word.contains("_"), snake else { return nil }
        return word
    }
}

/// The conversation as the transcript lays it out: tool calls in a row fold into one
/// group, lifecycle events become lines, and a speaker's name shows once per turn.
enum TranscriptLayout {
    enum Item: Hashable, Sendable, Identifiable {
        case message(Message, showsSender: Bool)
        /// Consecutive verifier `progress` messages.
        case toolCalls([Message])
        /// System events and accepts: things that happened, not speech.
        case event(Message)
        /// The calendar day changed between two messages; `seq` is the first of the new day.
        case day(Date, seq: Int)

        /// The first seq it holds: stable while a group grows at its end.
        var id: Int {
            switch self {
            case .message(let message, _), .event(let message): message.seq
            case .toolCalls(let calls): calls.first?.seq ?? 0
            // Negative, so it never collides with the message it sits above.
            case .day(_, let seq): -seq - 1
            }
        }

        var lastSeq: Int {
            switch self {
            case .message(let message, _), .event(let message): message.seq
            case .toolCalls(let calls): calls.last?.seq ?? 0
            case .day(_, let seq): seq
            }
        }
    }

    /// With `toolCalls` false the verifier's progress is left out, and a speaker's
    /// turns either side of it join up.
    static func items(_ messages: [Message], toolCalls: Bool = true, calendar: Calendar = .current) -> [Item] {
        var out: [Item] = []
        var previousSpeaker: MessageFrom?
        var previousDay: Date?
        for message in messages {
            // A long run crosses midnight; clock times alone would then read backwards.
            let day = calendar.startOfDay(for: message.at)
            if let previousDay, day != previousDay {
                out.append(.day(day, seq: message.seq))
                previousSpeaker = nil
            }
            previousDay = day
            if message.kind == .progress {
                guard toolCalls else { continue }
                if case .toolCalls(var calls) = out.last {
                    calls.append(message)
                    out[out.count - 1] = .toolCalls(calls)
                } else {
                    out.append(.toolCalls([message]))
                }
                // Tool calls are the verifier at work; its next words still name it.
                previousSpeaker = nil
                continue
            }
            if isEvent(message) {
                // The daemon writes "machine destroyed" and "human destroyed the machine"
                // for one destroy; the one that says who stays.
                if case .event(let previous) = out.last, isDestroy(previous), isDestroy(message) {
                    out[out.count - 1] = .event(previous.text.hasPrefix("human") ? previous : message)
                } else {
                    out.append(.event(message))
                }
                previousSpeaker = nil
                continue
            }
            out.append(.message(message, showsSender: previousSpeaker != message.from))
            previousSpeaker = message.from
        }
        return out
    }

    private static func isDestroy(_ message: Message) -> Bool {
        message.from == .system && message.text.contains("destroyed")
    }

    static func isEvent(_ message: Message) -> Bool {
        switch message.kind {
        case .event, .accept: return true
        default: return message.from == .system
        }
    }
}

/// Steps as the app shows them. An older daemon, reattaching to a machine, restarted its
/// numbering, so a legacy log can end "... 179, 180, 1". A step whose number does not go
/// up takes the next number after the highest so far, so order and labels stay sane.
enum StepLog {
    static func normalized(_ steps: [Step]) -> [Step] {
        var highest = 0
        return steps.map { step in
            var step = step
            if step.seq <= highest { step.seq = highest + 1 }
            highest = step.seq
            return step
        }
    }
}

/// What a message says, without bookkeeping a model left in it ("[I reported verdict
/// pass] ..."). Only a short bracketed prefix goes; brackets inside the text stay.
enum TranscriptText {
    static func clean(_ text: String) -> String {
        guard text.hasPrefix("["), let close = text.firstIndex(of: "]"),
              text.distance(from: text.startIndex, to: close) <= 60 else { return text }
        let rest = text[text.index(after: close)...].trimmingCharacters(in: .whitespaces)
        return rest.isEmpty ? text : rest
    }
}

/// How the app is doing at reaching the daemon, as the window shows it. Follows the
/// last full read of the run list, not the event stream: a stream can be between
/// reconnects while the daemon answers fine.
enum ConnectionState: Equatable, Sendable {
    /// No read has finished yet.
    case connecting
    case online
    /// The last read got no answer. `hasData` says whether runs are still on screen.
    case offline(hasData: Bool)
    /// The daemon answered the last read with an HTTP error: it is running and refused,
    /// in these words. Never "not running".
    case refused(String, hasData: Bool)

    /// `reachable` is nil before the first read finishes; `refusal` is the daemon's
    /// words when that read failed with an HTTP status.
    static func derive(reachable: Bool?, refusal: String? = nil, hasData: Bool) -> ConnectionState {
        switch reachable {
        case nil: .connecting
        case true?: .online
        case false?: refusal.map { .refused($0, hasData: hasData) } ?? .offline(hasData: hasData)
        }
    }

    /// What to do about a refusal the daemon's words alone do not explain. The words are
    /// `DaemonError.status`'s description, "greenroom answered <code>: <body>".
    /// - `api.Guard` refuses a Host that is neither loopback nor the `-public-host` name,
    ///   which a LAN or bridge address always sends.
    /// - It answers 401 "unauthorized" to the public name without the right bearer token.
    static func advice(for words: String) -> String? {
        if words.localizedCaseInsensitiveContains("loopback") {
            return "greenroom only answers 127.0.0.1, localhost and the public name it was started with "
                + "(-public-host). Put that name and its token in ~/.greenroom/client.json, "
                + "or use a loopback address, such as a port forward."
        }
        if words.localizedCaseInsensitiveContains("unauthorized") || words.contains("answered 401") {
            return "greenroom refused this app's token. The token in ~/.greenroom/client.json is "
                + "missing or does not match GREENROOM_TOKEN on greenroom's host. "
                + "Run the installer again with the current token."
        }
        return nil
    }
}
