import Foundation

private func outcome(_ match: Rematch<String>) -> String {
    switch match {
    case let .unique(candidate): return "unique \(candidate)"
    case .none: return "none"
    case let .ambiguous(count): return "ambiguous \(count)"
    }
}

private func among(_ candidates: [(String, Fingerprint)]) -> [(candidate: String, fingerprint: Fingerprint)] {
    candidates.map { (candidate: $0.0, fingerprint: $0.1) }
}

func testAnIdentifierFindsItsElementWhereverItMoved() {
    let wanted = mark("Save", path: "AXGroup[0]/AXButton[0]", id: "save-button")
    let now = among([
        ("cancel", mark("Cancel", path: "AXGroup[0]/AXButton[0]", id: "cancel-button")),
        // The same control after the window was rebuilt: another place, another title.
        ("save", mark("Save changes", path: "AXGroup[1]/AXGroup[0]/AXButton[3]", id: "save-button")),
    ])
    expectEqual(outcome(rematch(wanted, among: now)), "unique save")
}

func testAnIdentifierOnAnotherRoleIsNotTheElement() {
    let wanted = mark("Save", id: "save")
    let now = among([("label", mark("Save", id: "save", role: "AXStaticText"))])
    expectEqual(outcome(rematch(wanted, among: now)), "none")
}

func testRowsThatShareAnIdentifierAreToldApartByPlaceAndName() {
    let wanted = mark("Run 12", path: "AXList[0]/AXButton[1]", id: "row")
    let rows = among([
        ("first", mark("Run 11", path: "AXList[0]/AXButton[0]", id: "row")),
        ("second", mark("Run 12", path: "AXList[0]/AXButton[1]", id: "row")),
        ("third", mark("Run 13", path: "AXList[0]/AXButton[2]", id: "row")),
    ])
    expectEqual(outcome(rematch(wanted, among: rows)), "unique second")

    // The list reloaded with other rows: none of them is Run 12 where it was, so none is taken.
    let reloaded = among([
        ("first", mark("Run 20", path: "AXList[0]/AXButton[0]", id: "row")),
        ("second", mark("Run 21", path: "AXList[0]/AXButton[1]", id: "row")),
    ])
    expectEqual(outcome(rematch(wanted, among: reloaded)), "ambiguous 2")
}

func testWithoutAnIdentifierPathRoleAndNameMustAllMatch() {
    let wanted = mark("20%", path: "AXGroup[0]/AXRadioGroup[0]/AXRadioButton[1]", role: "AXRadioButton")
    let window = among([
        ("eighteen", mark("18%", path: "AXGroup[0]/AXRadioGroup[0]/AXRadioButton[0]", role: "AXRadioButton")),
        ("twenty", mark("20%", path: "AXGroup[0]/AXRadioGroup[0]/AXRadioButton[1]", role: "AXRadioButton")),
    ])
    expectEqual(outcome(rematch(wanted, among: window)), "unique twenty")

    let renamed = among([("twenty", mark("25%", path: wanted.path, role: "AXRadioButton"))])
    expectEqual(outcome(rematch(wanted, among: renamed)), "none", "another name in its place")
    let moved = among([("twenty", mark("20%", path: "AXGroup[1]/AXRadioButton[1]", role: "AXRadioButton"))])
    expectEqual(outcome(rematch(wanted, among: moved)), "none", "its name in another place")
    let identified = among([("twenty", mark("20%", path: wanted.path, id: "tip-20", role: "AXRadioButton"))])
    expectEqual(outcome(rematch(wanted, among: identified)), "none", "an element with an identifier is not one without")
    expectEqual(outcome(rematch(wanted, among: [])), "none", "an empty window")
}

func testTwoElementsThatFitAreNoMatch() {
    // An element with no window is looked for in the whole app, where two windows can hold the
    // same path.
    let wanted = mark("OK", path: "AXButton[0]", window: 0)
    let app = among([
        ("first window", mark("OK", path: "AXButton[0]", window: 11)),
        ("second window", mark("OK", path: "AXButton[0]", window: 12)),
    ])
    expectEqual(outcome(rematch(wanted, among: app)), "ambiguous 2")
}

func testAppKitsGeneratedIdentifiersAreDropped() {
    expectEqual(stableIdentifier("_NS:12"), "")
    expectEqual(stableIdentifier("bill-field"), "bill-field")
    expectEqual(stableIdentifier(nil), "")
    expectEqual(stableIdentifier(""), "")
}

func testARolePathGrowsByRoleAndIndex() {
    expectEqual(pathAppending("", role: "AXGroup", index: 0), "AXGroup[0]")
    expectEqual(pathAppending("AXGroup[0]", role: "AXButton", index: 12), "AXGroup[0]/AXButton[12]")
}
