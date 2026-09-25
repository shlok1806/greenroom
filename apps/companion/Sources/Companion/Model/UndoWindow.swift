import Foundation

/// An accept or dispute held for its undo window (companion ADR 0005): the choice shows
/// at once, but nothing reaches the daemon until the window ends, because a message the
/// daemon has recorded cannot be taken back. Pure: the clock is passed in.
struct UndoWindow<Payload: Equatable & Sendable>: Equatable, Sendable {
    /// ADR 0005's five seconds, from `tokens.json` `motion.undoMs`.
    static var length: TimeInterval { TimeInterval(DesignData.shared.tokens.motion.undoMs) / 1000 }

    struct Pending: Equatable, Sendable {
        var payload: Payload
        var deadline: Date
    }

    private(set) var pending: Pending?

    /// Holds `payload` until `now` plus the window. A choice already held is not undoable
    /// once another is made: it is handed back to be sent now.
    @discardableResult
    mutating func start(_ payload: Payload, now: Date) -> Payload? {
        let displaced = pending?.payload
        pending = Pending(payload: payload, deadline: now.addingTimeInterval(Self.length))
        return displaced
    }

    /// Takes the held choice back. Nil when nothing is held (it was already sent).
    mutating func undo() -> Payload? {
        defer { pending = nil }
        return pending?.payload
    }

    /// The held choice once its window has ended, to be sent; nil while it is still
    /// undoable or when nothing is held.
    mutating func takeDue(now: Date) -> Payload? {
        guard let pending, now >= pending.deadline else { return nil }
        self.pending = nil
        return pending.payload
    }

    /// Time left to undo; nil when nothing is held or the window has ended.
    func remaining(now: Date) -> TimeInterval? {
        guard let pending else { return nil }
        let left = pending.deadline.timeIntervalSince(now)
        return left > 0 ? left : nil
    }

    /// Whole seconds left, rounded up, as the hint bar counts: 5, 4, 3, 2, 1.
    func seconds(now: Date) -> Int? {
        remaining(now: now).map { Int($0.rounded(.up)) }
    }
}

/// A verdict choice waiting out its undo.
struct PendingVerdictChoice: Equatable, Sendable {
    enum Kind: Equatable, Sendable {
        case accept
        /// A dispute (the card's Reject), with the person's reason.
        case dispute(reason: String)
    }

    var runId: String
    var verdictSeq: Int
    var kind: Kind

    /// The hint bar's word while it waits.
    var word: String {
        switch kind {
        case .accept: "accepting"
        case .dispute: "disputing"
        }
    }
}
