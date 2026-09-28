// The ref table of one reader (daemon ADR 0006 point 3): `e1`, `e2`... in order of first sight,
// never reused, each with its element and fingerprint. Generic over the element so the host
// tests run it with plain values; the guest uses AXUIElement wrapped to hash with CFHash and
// compare with CFEqual (Refs.swift).

import Foundation

/// The most refs a reader's table holds. The least recently seen go first.
let refTableCapacity = 20000

struct RefEntry<Handle: Hashable> {
    let ref: String
    var handle: Handle
    var fingerprint: Fingerprint
    /// When the element was last seen in a walk or used, in seconds of the agent's clock.
    var seenAt: Double
    /// The order of sights. Every sight has its own tick, so eviction order is never a tie even
    /// when one walk sees a thousand elements in the same millisecond.
    fileprivate var tick: UInt64
}

/// The number of a ref written `e<n>`, or nil for anything else.
func refNumber(_ ref: String) -> Int? {
    guard ref.hasPrefix("e"), ref.count > 1, ref.count <= 12 else { return nil }
    let digits = ref.dropFirst()
    guard digits.allSatisfy({ $0.isASCII && $0.isNumber }), digits.first != "0" else { return nil }
    return Int(digits)
}

struct RefTable<Handle: Hashable> {
    let capacity: Int
    private var next = 1
    private var tick: UInt64 = 0
    private var entries: [Int: RefEntry<Handle>] = [:]
    /// The ref an element is found under in a walk: by hash, then equality.
    private var byHandle: [Handle: Int] = [:]

    init(capacity: Int = refTableCapacity) {
        self.capacity = max(1, capacity)
    }

    var count: Int { entries.count }

    /// The highest ref number given so far: refs above it were never this table's.
    var issued: Int { next - 1 }

    /// The element's ref: the one it has, with its fingerprint brought up to date (a name
    /// changes while the element lives), or the next new one.
    mutating func see(_ handle: Handle, _ fingerprint: Fingerprint, at now: Double) -> String {
        tick += 1
        if let number = byHandle[handle], var entry = entries[number] {
            entry.fingerprint = fingerprint
            entry.seenAt = now
            entry.tick = tick
            // The equal element may be another instance; the newer one is the one to message.
            entry.handle = handle
            entries[number] = entry
            return entry.ref
        }
        let number = next
        next += 1
        let ref = "e\(number)"
        entries[number] = RefEntry(ref: ref, handle: handle, fingerprint: fingerprint, seenAt: now, tick: tick)
        byHandle[handle] = number
        evict()
        return ref
    }

    /// The ref the element already has, without touching it.
    func ref(of handle: Handle) -> String? {
        guard let number = byHandle[handle] else { return nil }
        return entries[number]?.ref
    }

    func entry(_ ref: String) -> RefEntry<Handle>? {
        guard let number = refNumber(ref) else { return nil }
        return entries[number]
    }

    /// Marks a ref used now, so a ref a caller keeps acting on is not evicted under it.
    mutating func touch(_ ref: String, at now: Double) {
        guard let number = refNumber(ref), var entry = entries[number] else { return }
        tick += 1
        entry.seenAt = now
        entry.tick = tick
        entries[number] = entry
    }

    /// Points a ref at the element its fingerprint was matched to, after its own element died.
    /// The new element may already have a ref of its own from a later walk; both then name it,
    /// and walks go on finding it under the one it had.
    mutating func rebind(_ ref: String, to handle: Handle, _ fingerprint: Fingerprint, at now: Double) {
        guard let number = refNumber(ref), var entry = entries[number] else { return }
        tick += 1
        if byHandle[entry.handle] == number { byHandle[entry.handle] = nil }
        entry.handle = handle
        entry.fingerprint = fingerprint
        entry.seenAt = now
        entry.tick = tick
        entries[number] = entry
        if byHandle[handle] == nil { byHandle[handle] = number }
    }

    /// Drops the least recently seen entries once the table is over its capacity. It drops a
    /// sixteenth at a time, so a long walk over a full table sorts it once in a while and not
    /// once per element.
    private mutating func evict() {
        guard entries.count > capacity else { return }
        let batch = max(entries.count - capacity, capacity / 16)
        let oldest = entries.values.sorted { $0.tick < $1.tick }.prefix(min(batch, entries.count - 1))
        for entry in oldest {
            guard let number = refNumber(entry.ref) else { continue }
            entries[number] = nil
            if byHandle[entry.handle] == number { byHandle[entry.handle] = nil }
        }
    }
}
