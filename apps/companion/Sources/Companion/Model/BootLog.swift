import Foundation

/// The machine coming up, as short terminal lines in the well while it boots and while the
/// live screen connects (ADR 0006, Boot). Only what the daemon has said: the create and
/// boot steps it recorded, the machine's status, its boot time and address, and the
/// screen connecting. The daemon sends no finer boot events (clone, ssh), so there are no
/// lines for them. Pure; `BootLogTests`.
struct BootLine: Equatable, Sendable {
    enum State: Equatable, Sendable {
        case done
        /// Still going: the view ticks and counts from `since`.
        case working(since: Date)
    }

    /// One word, what the step was: create, boot, ready, screen.
    var word: String
    /// The thing it was done to: the image, the machine, its address.
    var detail: String
    var state: State
    /// How long it took, when the daemon said.
    var seconds: TimeInterval?
}

enum BootLog {
    static func lines(facts: RunFacts, detail: RunDetail?, image: String?, steps: [Step]?, connecting: Date?) -> [BootLine] {
        let machine = detail?.machine
        let held = steps ?? []
        let create = held.first { $0.tool == "machine_create" }
        let boot = held.first { $0.tool == "machine_boot" }
        let imageName = firstWord(create?.input?["image"]?.stringValue, machine?.image, detail?.image, image)
        var lines = [BootLine(word: "create", detail: imageName, state: .done,
                              seconds: create.map { Double($0.durationMs) / 1000 })]
        let machineName = firstWord(machine?.name, detail?.machineName)
        switch facts.phase {
        case .booting:
            // From when the machine was made, else when the run began.
            let since = create.map { $0.at.addingTimeInterval(Double($0.durationMs) / 1000) } ?? facts.started
            lines.append(BootLine(word: "boot", detail: machineName, state: .working(since: since)))
        case .live, .idle:
            let seconds = machine?.bootSeconds ?? boot.map { Double($0.durationMs) / 1000 }
            lines.append(BootLine(word: "boot", detail: machineName, state: .done, seconds: seconds))
            lines.append(BootLine(word: "ready", detail: detail?.address ?? "", state: .done))
            if let connecting {
                lines.append(BootLine(word: "screen", detail: "connecting", state: .working(since: connecting)))
            }
        case .ended, .failed:
            return []
        }
        return lines
    }

    private static func firstWord(_ candidates: String?...) -> String {
        candidates.compactMap { $0 }.first { !$0.isEmpty } ?? ""
    }

    /// "0.7s", "28s", "1:04": how long a line's step took, short.
    static func took(_ seconds: TimeInterval) -> String {
        if seconds < 10 { return String(format: "%.1fs", max(0, seconds)) }
        if seconds < 60 { return "\(Int(seconds.rounded()))s" }
        return LoaderMotion.elapsed(seconds)
    }
}
