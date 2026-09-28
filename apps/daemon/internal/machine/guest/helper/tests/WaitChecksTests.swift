import Foundation

func testWaitTimeoutsDefaultAndAreCapped() {
    expectEqual(waitTimeoutMs(nil, default: defaultWaitTimeoutMs), 10_000)
    expectEqual(waitTimeoutMs(nil, default: defaultExpectTimeoutMs), 2000)
    expectEqual(waitTimeoutMs(60_000, default: defaultWaitTimeoutMs), 40_000, "under the 45 s of a request")
    expectEqual(waitTimeoutMs(300, default: defaultWaitTimeoutMs), 300)
}

func testExpectPollsBackOffToEverySecond() {
    expectEqual((0..<7).map(expectPollWait(after:)), [100, 250, 500, 1000, 1000, 1000, 1000])
}

private func matcher(_ op: String, _ expected: String) -> TextMatcher? {
    try? TextMatcher(op: op, expected: expected)
}

func testTextComparisons() {
    expect(matcher("equals", "$24.00")?.matches("$24.00") == true, "equals the whole text")
    expect(matcher("equals", "$24.00")?.matches("Total: $24.00") == false, "equals is not contains")
    expect(matcher("contains", "24")?.matches("Total: $24.00") == true)
    expect(matcher("contains", "total")?.matches("Total: $24.00") == false, "case counts")
    expect(matcher("matches", "^Total: \\$[0-9]+\\.00$")?.matches("Total: $24.00") == true)
    expect(matcher("matches", "^[0-9]+$")?.matches("12a") == false)
    do {
        _ = try TextMatcher(op: "matches", expected: "(unclosed")
        expect(false, "a bad pattern is refused")
    } catch let error as PatternError {
        expect(error.message.contains("not a regular expression"), error.message)
    } catch {
        expect(false, "\(error)")
    }
    do {
        _ = try TextMatcher(op: "startsWith", expected: "a")
        expect(false, "an unknown op is refused")
    } catch let error as PatternError {
        expect(error.message.contains("equals, contains or matches"), error.message)
    } catch {
        expect(false, "\(error)")
    }
}

func testCountComparisons() {
    expectEqual(countMatches(op: "equals", expected: 3, observed: 3), true)
    expectEqual(countMatches(op: "atLeast", expected: 3, observed: 5), true)
    expectEqual(countMatches(op: "atLeast", expected: 3, observed: 2), false)
    expectEqual(countMatches(op: "atMost", expected: 0, observed: 0), true)
    expect(countMatches(op: "contains", expected: 1, observed: 1) == nil, "not a count op")
}

func testWaitStates() {
    let gone = WaitObservation()
    let shown = WaitObservation(exists: true, shows: true, enabled: true, focused: false, name: "Continue", value: "")
    var disabled = shown
    disabled.enabled = false
    var offscreen = shown
    offscreen.shows = false
    expect(waitSatisfied(.appears, now: shown, first: gone, matcher: nil))
    expect(!waitSatisfied(.appears, now: offscreen, first: gone, matcher: nil), "a row below the fold has not appeared")
    expect(waitSatisfied(.disappears, now: gone, first: shown, matcher: nil))
    expect(waitSatisfied(.disappears, now: offscreen, first: shown, matcher: nil), "scrolled out of sight")
    expect(!waitSatisfied(.disappears, now: shown, first: shown, matcher: nil))
    expect(waitSatisfied(.enabled, now: shown, first: disabled, matcher: nil), "Prepare then Continue")
    expect(!waitSatisfied(.enabled, now: disabled, first: disabled, matcher: nil))
    expect(!waitSatisfied(.enabled, now: gone, first: gone, matcher: nil), "what is not there is not enabled")
    expect(waitSatisfied(.disabled, now: disabled, first: shown, matcher: nil))
    var focused = shown
    focused.focused = true
    expect(waitSatisfied(.focused, now: focused, first: shown, matcher: nil))
    expect(!waitSatisfied(.focused, now: shown, first: shown, matcher: nil))
}

func testAWaitForChangeComparesWithTheFirstPoll() {
    let first = WaitObservation(exists: true, shows: true, enabled: true, focused: false, name: "Result", value: "…")
    var later = first
    expect(!waitSatisfied(.changes, now: later, first: first, matcher: nil), "the same")
    later.value = "42"
    expect(waitSatisfied(.changes, now: later, first: first, matcher: nil), "the delayed result landed")
    var app = WaitObservation(exists: true, shows: true)
    var signature = Signature()
    signature.add("a")
    app.tree = signature
    var moved = app
    signature.add("b")
    moved.tree = signature
    expect(waitSatisfied(.changes, now: moved, first: app, matcher: nil), "an app's tree changed")
}

func testAWaitForAValueUsesItsMatcher() {
    let result = WaitObservation(exists: true, shows: true, enabled: true, focused: false, name: "Result", value: "Total: 42")
    expect(waitSatisfied(.value, now: result, first: result, matcher: matcher("contains", "42")))
    expect(!waitSatisfied(.value, now: result, first: result, matcher: matcher("equals", "42")))
    expect(!waitSatisfied(.value, now: result, first: result, matcher: nil), "no matcher, never")
    expect(!waitSatisfied(.value, now: WaitObservation(), first: result, matcher: matcher("contains", "")), "nothing to read")
}

func testExpectedValuesMustFitTheirProperty() {
    expectEqual(try? expectedFor(.value, op: "equals", expected: .text("42")), .text("42"))
    expectEqual(try? expectedFor(.enabled, op: "equals", expected: nil), .flag(true), "a flag is true unless said")
    expectEqual(try? expectedFor(.count, op: "atLeast", expected: .count(2)), .count(2))
    expect((try? expectedFor(.count, op: "contains", expected: .count(2))) == nil, "contains is not a count op")
    expect((try? expectedFor(.value, op: "equals", expected: .flag(true))) == nil, "a value is text")
    expect((try? expectedFor(.visible, op: "equals", expected: .text("yes"))) == nil, "a flag is a bool")
    expect((try? expectedFor(.count, op: "equals", expected: .count(-1))) == nil, "no negative count")
    expect((try? expectedFor(.selected, op: "atLeast", expected: .flag(true))) == nil, "a flag only equals")
}

func testExpectationsPassOnWhatWasObserved() {
    let field = ExpectObservation(exists: true, visible: true, enabled: true, selected: false, name: "Amount", value: "120", count: 1)
    expect(expectPasses(.value, op: "equals", expected: .text("120"), matcher: matcher("equals", "120"), field))
    expect(!expectPasses(.value, op: "equals", expected: .text("12"), matcher: matcher("equals", "12"), field))
    expect(expectPasses(.name, op: "contains", expected: .text("Amo"), matcher: matcher("contains", "Amo"), field))
    expect(expectPasses(.exists, op: "equals", expected: .flag(true), matcher: nil, field))
    expect(expectPasses(.exists, op: "equals", expected: .flag(false), matcher: nil, ExpectObservation()), "expecting absence")
    expect(expectPasses(.visible, op: "equals", expected: .flag(true), matcher: nil, field))
    var covered = field
    covered.visible = false
    expect(!expectPasses(.visible, op: "equals", expected: .flag(true), matcher: nil, covered), "covered by the Inspector")
    expect(expectPasses(.enabled, op: "equals", expected: .flag(true), matcher: nil, field))
    expect(expectPasses(.selected, op: "equals", expected: .flag(false), matcher: nil, field))
    expect(!expectPasses(.enabled, op: "equals", expected: .flag(false), matcher: nil, ExpectObservation()), "absent is neither")
    var rows = ExpectObservation()
    rows.count = 4
    expect(expectPasses(.count, op: "atLeast", expected: .count(3), matcher: nil, rows))
    expect(!expectPasses(.count, op: "atMost", expected: .count(3), matcher: nil, rows))
    expect(expectPasses(.count, op: "equals", expected: .count(4), matcher: nil, rows))
}

func testASecretIsNeverObserved() {
    let password = ExpectObservation(exists: true, visible: true, enabled: true, name: "Password", value: "hunter2", count: 1, secret: true)
    expect(!expectPasses(.value, op: "equals", expected: .text("hunter2"), matcher: matcher("equals", "hunter2"), password),
           "not even the right guess passes")
    expectEqual(observedValue(.value, password) as? String, "<secret, 7 chars>")
    expectEqual(observedValue(.name, password) as? String, "Password")
    expect(expectPasses(.exists, op: "equals", expected: .flag(true), matcher: nil, password))
    expect(expectPasses(.enabled, op: "equals", expected: .flag(true), matcher: nil, password))
}

func testObservedIsExactAndNullWhenAbsent() {
    let field = ExpectObservation(exists: true, visible: true, enabled: false, selected: true, name: "Tip", value: "18%", count: 1)
    expectEqual(observedValue(.value, field) as? String, "18%")
    expectEqual(observedValue(.enabled, field) as? Bool, false)
    expectEqual(observedValue(.selected, field) as? Bool, true)
    expectEqual(observedValue(.count, field) as? Int, 1)
    expect(observedValue(.value, ExpectObservation()) is NSNull, "not found")
    expectEqual(observedValue(.exists, ExpectObservation()) as? Bool, false)
    expectEqual(observedValue(.visible, ExpectObservation()) as? Bool, false)
    expect(JSONSerialization.isValidJSONObject(["observed": observedValue(.name, ExpectObservation())]), "null is JSON")
}
