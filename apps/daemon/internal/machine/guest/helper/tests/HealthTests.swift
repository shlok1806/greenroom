import Foundation

func testAStallIsReportedOnceAndItsRecovery() {
    var tracker = StallTracker()
    let walk = tracker.begin("ax", at: 0)
    expectEqual(tracker.tick(at: 5), [], "under the threshold")
    expectEqual(tracker.tick(at: 10.5), [.stalled(in: "ax", seconds: 10)], "over it")
    expectEqual(tracker.tick(at: 12), [], "reported once")
    tracker.end(walk)
    expectEqual(tracker.tick(at: 13), [.recovered(in: "ax")], "recovered when it ends")
    expectEqual(tracker.tick(at: 14), [], "and once")
}

func testKindsStallApart() {
    var tracker = StallTracker()
    let capture = tracker.begin("capture", at: 0)
    _ = tracker.begin("ax", at: 8)
    expectEqual(tracker.tick(at: 11), [.stalled(in: "capture", seconds: 11)])
    expectEqual(tracker.tick(at: 19), [.stalled(in: "ax", seconds: 11)])
    tracker.end(capture)
    expectEqual(tracker.tick(at: 20), [.recovered(in: "capture")], "ax is still stalled")
}

func testAStallLastsUntilItsLongestWorkEnds() {
    var tracker = StallTracker()
    let long = tracker.begin("ax", at: 0)
    expectEqual(tracker.tick(at: 11), [.stalled(in: "ax", seconds: 11)])
    let short = tracker.begin("ax", at: 11)
    tracker.end(short)
    expectEqual(tracker.tick(at: 12), [], "the long walk still runs")
    tracker.end(long)
    expectEqual(tracker.tick(at: 13), [.recovered(in: "ax")])
}

func testATailBufferKeepsTheEnd() {
    var tail = TailBuffer(limit: 8)
    tail.append(Data("hello ".utf8))
    expectEqual(tail.text, "hello ", "under the limit")
    tail.append(Data("world!".utf8))
    expectEqual(tail.text, "o world!", "the last 8 bytes")
    expectEqual(tail.dropped, 4, "dropped")
    tail.append(Data(String(repeating: "x", count: 20).utf8))
    expectEqual(tail.text, "xxxxxxxx", "one write over the limit")
    expectEqual(tail.data.startIndex, 0, "indices start at zero")
}
