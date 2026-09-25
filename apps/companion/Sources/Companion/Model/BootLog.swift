import Foundation

/// The machine coming up, as short terminal lines in the well while it boots and while the
/// live screen connects (ADR 0006, Boot). Only what the daemon has said: one line per boot
/// phase as it arrives (`Machine.boot`: clone, start, agent, ip, key, settings, helper,
/// desktop checks, ssh), the running one ticking, then ready and the screen connecting. A
/// daemon before boot phases sends none; then the lines are the create and boot steps it
/// recorded, the machine's status, its boot time and address. Pure; `BootLogTests`.
struct BootLine: Equatable, Sendable {
    enum State: Equatable, Sendable {
        case done
        /// Still going: the view ticks and counts from `since`.
        case working(since: Date)
    }

    /// One word, what the step was: clone, agent, ip, ssh, ready, screen.
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
        if let phases = machine?.boot, !phases.isEmpty {
            return lines(phases: phases, facts: facts, machineName: firstWord(machine?.name, detail?.machineName),
                         bootSeconds: machine?.bootSeconds, connecting: connecting)
        }
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

    /// One line per phase the daemon sent, in its order.
    private static func lines(phases: [BootPhase], facts: RunFacts, machineName: String, bootSeconds: Double?,
                              connecting: Date?) -> [BootLine] {
        var lines = phases.map(line)
        switch facts.phase {
        case .booting:
            // Between two phases nothing runs; the boot as a whole still does.
            if let last = phases.last, !last.running {
                let since = last.at.addingTimeInterval(last.seconds ?? 0)
                lines.append(BootLine(word: "boot", detail: machineName, state: .working(since: since)))
            }
        case .live, .idle:
            lines.append(BootLine(word: "ready", detail: machineName, state: .done, seconds: bootSeconds))
            if let connecting {
                lines.append(BootLine(word: "screen", detail: "connecting", state: .working(since: connecting)))
            }
        case .ended, .failed:
            return []
        }
        return lines
    }

    /// "clone greenroom-lean-a", "ip 192.168.64.5", "ssh ready": the phase in a word and
    /// what it did, or what it is doing while it runs.
    static func line(_ phase: BootPhase) -> BootLine {
        let running = phase.running
        let said = phase.detail ?? ""
        let word: String
        let detail: String
        switch phase.phase {
        case .clone: (word, detail) = ("clone", said)
        case .start: (word, detail) = ("start", said)
        case .agent: (word, detail) = ("agent", running ? "waiting" : "ready")
        case .ip: (word, detail) = ("ip", running ? "waiting" : said)
        case .key: (word, detail) = ("key", running ? "installing" : "installed")
        case .settings: (word, detail) = ("settings", running ? "applying" : "applied")
        case .helper: (word, detail) = ("helper", running ? "checking" : "ready")
        case .checks: (word, detail) = ("desktop", running ? "checking" : "checked")
        case .ssh: (word, detail) = ("ssh", running ? "waiting" : "ready")
        case .unknown(let raw): (word, detail) = (raw, said)
        }
        return BootLine(word: word, detail: phase.error == nil ? detail : "failed",
                        state: running ? .working(since: phase.at) : .done, seconds: phase.seconds)
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
