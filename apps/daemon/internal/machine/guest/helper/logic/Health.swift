// The agent's bookkeeping for its own health (daemon ADR 0005 points 8 and 9): which walks and
// captures run too long, and the tail of a script's output. Pure, with the clock passed in, so
// the host tests run it without waiting.

import Foundation

/// How long a walk or capture runs before the agent says it stalled.
let stallSeconds: Double = 10

/// How long the agent waits for any frame at all before it ends itself: a dead host exec does
/// not always close its stdin.
let idleExitSeconds: Double = 15

enum StallEvent: Equatable {
    case stalled(in: String, seconds: Int)
    case recovered(in: String)
}

/// The work in flight by kind (`ax`, `capture`), and which kinds have been reported stalled.
/// A kind is stalled while any of its work has run over the threshold; it is reported once when
/// that starts and once when it ends.
struct StallTracker {
    private var running: [Int: (kind: String, since: Double)] = [:]
    private var next = 0
    private var reported: Set<String> = []

    mutating func begin(_ kind: String, at now: Double) -> Int {
        next += 1
        running[next] = (kind, now)
        return next
    }

    mutating func end(_ token: Int) {
        running[token] = nil
    }

    /// The events due at `now`, stalls first, each kind in name order.
    mutating func tick(at now: Double, threshold: Double = stallSeconds) -> [StallEvent] {
        var oldest: [String: Double] = [:]
        for (_, work) in running {
            oldest[work.kind] = min(oldest[work.kind] ?? work.since, work.since)
        }
        var events: [StallEvent] = []
        for (kind, since) in oldest.sorted(by: { $0.key < $1.key }) where now - since > threshold && !reported.contains(kind) {
            reported.insert(kind)
            events.append(.stalled(in: kind, seconds: Int(now - since)))
        }
        for kind in reported.sorted() {
            if let since = oldest[kind], now - since > threshold { continue }
            reported.remove(kind)
            events.append(.recovered(in: kind))
        }
        return events
    }
}

/// The last `limit` bytes written to it, so a script that prints forever costs a fixed amount
/// and the end of its output, where errors are, survives.
struct TailBuffer {
    let limit: Int
    private(set) var data = Data()
    /// How many bytes were dropped from the front.
    private(set) var dropped = 0

    init(limit: Int) {
        self.limit = max(0, limit)
    }

    mutating func append(_ bytes: Data) {
        data.append(bytes)
        let over = data.count - limit
        if over > 0 {
            // A fresh Data, not removeFirst: that can leave a slice whose indices no longer
            // start at zero.
            data = Data(data.suffix(limit))
            dropped += over
        }
    }

    var text: String { String(decoding: data, as: UTF8.self) }
}
