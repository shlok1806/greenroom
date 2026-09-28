import Foundation

/// A fingerprint for a test element: a button in window 7 of process 100.
func mark(_ name: String, path: String = "AXGroup[0]/AXButton[0]", id: String = "", role: String = "AXButton",
           window: Int = 7) -> Fingerprint {
    Fingerprint(pid: 100, started: "1700000000.000001", window: window, path: path, identifier: id, role: role, name: name)
}

func testAnElementKeepsItsRefAcrossWalks() {
    var table = RefTable<String>()
    let save = table.see("save", mark("Save"), at: 1)
    let open = table.see("open", mark("Open", path: "AXGroup[0]/AXButton[1]"), at: 1)
    expectEqual(save, "e1")
    expectEqual(open, "e2")
    // A second walk sees them in another order, and one of them renamed.
    expectEqual(table.see("open", mark("Open...", path: "AXGroup[0]/AXButton[1]"), at: 2), "e2", "the same element")
    expectEqual(table.see("save", mark("Save"), at: 2), "e1", "the same element")
    expectEqual(table.see("new", mark("New", path: "AXGroup[0]/AXButton[2]"), at: 2), "e3", "a new element")
    expectEqual(table.entry("e2")?.fingerprint.name, "Open...", "the fingerprint follows the element")
    expectEqual(table.entry("e2")?.seenAt, 2, "last seen")
    expectEqual(table.ref(of: "save"), "e1")
    expect(table.ref(of: "never seen") == nil, "an element no walk saw has no ref")
    expectEqual(table.count, 3)
}

func testARefIsNeverReused() {
    var table = RefTable<String>(capacity: 2)
    _ = table.see("a", mark("A"), at: 1)
    _ = table.see("b", mark("B"), at: 2)
    _ = table.see("c", mark("C"), at: 3) // evicts a
    expect(table.entry("e1") == nil, "e1 was evicted")
    // The evicted element is seen again: it is a new ref, and e1 stays dead.
    expectEqual(table.see("a", mark("A"), at: 4), "e4")
    expect(table.entry("e1") == nil, "e1 is not given again")
    expectEqual(table.issued, 4)
}

func testARaisedCounterNeverGivesAnOldRefAgain() {
    // A new agent after a reconnect: the daemon saw refs up to e39 on the old one.
    var table = RefTable<String>()
    expectEqual(table.raise(next: 40), 40)
    expectEqual(table.see("show runs", mark("Show runs"), at: 1), "e40", "the first ref is past every old one")
    expect(table.entry("e39") == nil, "e39 names nothing here")
    // Never lowered: a raise below the counter leaves it.
    expectEqual(table.raise(next: 10), 41)
    expectEqual(table.see("inspect", mark("Inspect", path: "AXGroup[0]/AXButton[1]"), at: 2), "e41")
    // Capped, so a ref stays one refNumber reads.
    expectEqual(table.raise(next: Int.max), maxRefNumber)
    let far = table.see("far", mark("Far", path: "AXGroup[0]/AXButton[2]"), at: 3)
    expectEqual(far, "e\(maxRefNumber)")
    expectEqual(refNumber(far), maxRefNumber)
}

func testTheLeastRecentlySeenRefsGoFirst() {
    var table = RefTable<String>(capacity: 3)
    _ = table.see("a", mark("A"), at: 1)
    _ = table.see("b", mark("B"), at: 1)
    _ = table.see("c", mark("C"), at: 1)
    // The same clock reading for all three: the order of sights still decides.
    _ = table.see("a", mark("A"), at: 1)
    table.touch("e2", at: 1)
    _ = table.see("d", mark("D"), at: 1)
    expect(table.entry("e3") == nil, "c was seen least recently")
    expect(table.entry("e1") != nil && table.entry("e2") != nil && table.entry("e4") != nil, "a, b and d stay")
    expect(table.ref(of: "c") == nil, "an evicted element is no longer found")
    expectEqual(table.count, 3)
}

func testATableNeverHoldsMoreThanItsCapacity() {
    var table = RefTable<Int>(capacity: 64)
    for element in 0..<1000 {
        _ = table.see(element, mark("row \(element)"), at: Double(element))
        expect(table.count <= 64, "\(table.count) refs after \(element + 1) elements")
    }
    expect(table.ref(of: 999) != nil, "the newest element is never the one evicted")
    expect(table.ref(of: 0) == nil, "the oldest is gone")
}

func testReadersHaveTablesOfTheirOwn() {
    var tables: [String: RefTable<String>] = [:]
    _ = tables["coder", default: RefTable()].see("bill", mark("Bill"), at: 1)
    _ = tables["coder", default: RefTable()].see("tip", mark("Tip"), at: 1)
    // The verifier sees the same elements in another order: its refs are its own.
    expectEqual(tables["verifier", default: RefTable()].see("tip", mark("Tip"), at: 2), "e1")
    expectEqual(tables["coder"]?.ref(of: "tip"), "e2")
    expect(tables["verifier"]?.entry("e2") == nil, "the coder's e2 is not the verifier's")
}

func testARefIsReboundToTheElementItsFingerprintFound() {
    var table = RefTable<String>()
    _ = table.see("old", mark("Save", id: "save"), at: 1)
    table.rebind("e1", to: "new", mark("Save", id: "save"), at: 5)
    expectEqual(table.entry("e1")?.handle, "new")
    expectEqual(table.ref(of: "new"), "e1", "walks find the new element under the old ref")
    expect(table.ref(of: "old") == nil, "the dead element is forgotten")

    // The new element already had a ref of its own: both name it, and walks keep using that one.
    _ = table.see("other", mark("Open", id: "open"), at: 6)
    _ = table.see("replacement", mark("Open", id: "open"), at: 7)
    table.rebind("e2", to: "replacement", mark("Open", id: "open"), at: 8)
    expectEqual(table.entry("e2")?.handle, "replacement")
    expectEqual(table.ref(of: "replacement"), "e3")
}

func testOnlyRefsParse() {
    expectEqual(refNumber("e17"), 17)
    for bad in ["", "e", "17", "e-1", "e1.5", "E17", "e017", "e17 ", "window", "e99999999999999"] {
        expect(refNumber(bad) == nil, "\(bad) is not a ref")
    }
}
