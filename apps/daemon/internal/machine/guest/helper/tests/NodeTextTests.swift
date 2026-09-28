import Foundation

func testANameIsTheTitleThenTheDescriptionThenThePlaceholder() {
    expectEqual(nodeName(title: "Open run", description: "Opens the run", placeholder: nil), "Open run")
    expectEqual(nodeName(title: nil, description: "Close", placeholder: "unused"), "Close")
    expectEqual(nodeName(title: "", description: "  \n", placeholder: "Search runs"), "Search runs", "empty and blank say nothing")
    expectEqual(nodeName(title: "  Bill \n", description: nil, placeholder: nil), "Bill", "trimmed")
    expectEqual(nodeName(title: nil, description: nil, placeholder: nil), "")
}

func testRolesLoseTheirPrefixOnTheWire() {
    expectEqual(wireRole("AXButton"), "Button")
    expectEqual(wireRole("AXStandardWindow"), "StandardWindow")
    expectEqual(wireRole("Custom"), "Custom")
    expectEqual(wireRole("AX"), "AX")
    expectEqual(wireRole(""), "")
}

func testALongStringIsCutAndMarked() {
    let long = String(repeating: "word ", count: 100) // 500 characters
    let sent = sentText(NodeText(name: "Details", value: long), secret: false, full: false)
    expectEqual(sent.text.value.count, textLimit, "the value is cut at the limit")
    expectEqual(sent.text.name, "Details", "a short field is whole")
    expectEqual(sent.cut, ["value"])
    expectEqual(sent.chars, 500, "the full length")

    // Several long fields: each is named, and chars is the longest.
    let all = sentText(NodeText(name: long, value: long + "more", desc: "short", help: long), secret: false, full: false)
    expectEqual(all.cut, ["name", "value", "help"])
    expectEqual(all.chars, 504)

    let short = sentText(NodeText(name: "Bill", value: "84.00"), secret: false, full: false)
    expectEqual(short, SentText(text: NodeText(name: "Bill", value: "84.00")), "nothing cut, nothing marked")
    let exact = sentText(NodeText(value: String(repeating: "a", count: textLimit)), secret: false, full: false)
    expect(exact.cut.isEmpty && exact.chars == nil, "exactly the limit is whole")
}

func testFullTextLiftsTheLimit() {
    let long = String(repeating: "a", count: 2000)
    let sent = sentText(NodeText(value: long), secret: false, full: true)
    expectEqual(sent.text.value.count, 2000)
    expect(sent.cut.isEmpty && sent.chars == nil, "nothing is marked")
}

func testASecureFieldNeverSendsItsValue() {
    expect(isSecure(role: "AXSecureTextField", subrole: ""), "by role")
    expect(isSecure(role: "AXTextField", subrole: "AXSecureTextField"), "by subrole")
    expect(!isSecure(role: "AXTextField", subrole: "AXSearchField"), "a search field is not secret")

    let sent = sentText(NodeText(name: "Password", value: "hunter2"), secret: true, full: false)
    expectEqual(sent.text.value, "", "the value is withheld")
    expectEqual(sent.text.name, "Password")
    expectEqual(sent.chars, 7, "only its length is sent")
    expect(sent.cut.isEmpty, "withheld is not cut")

    // fullText asks for everything and still gets no secret.
    let full = sentText(NodeText(value: String(repeating: "x", count: 300)), secret: true, full: true)
    expectEqual(full.text.value, "")
    expectEqual(full.chars, 300)
    // An empty secret says that it is empty.
    expectEqual(sentText(NodeText(name: "PIN"), secret: true, full: false).chars, 0)
}

func testInteractiveModeListsControlsTextAndNamedContainers() {
    func listed(_ role: String, named: Bool = false, valued: Bool = false, described: Bool = false, identified: Bool = false,
                mode: WalkMode = .interactive) -> Bool {
        isListed(mode: mode, role: role, named: named, valued: valued, described: described, identified: identified)
    }
    expect(listed("AXButton"), "a control, even with no name")
    expect(listed("AXStaticText", valued: true), "text")
    expect(listed("AXWindow") && listed("AXSheet") && listed("AXPopover"), "windows and sheets")
    expect(listed("AXScrollArea"), "a scroll area is always listed: it is where its rows are")
    expect(!listed("AXGroup"), "bare layout is walked through")
    expect(listed("AXGroup", named: true), "a named group")
    expect(listed("AXGroup", identified: true), "a group an app gave an identifier")
    expect(!listed("AXImage"), "an image that says nothing")
    expect(listed("AXImage", described: true), "an image with a description")
    expect(!listed("AXImage", identified: true), "an identifier describes nothing")

    expect(listed("AXGroup", mode: .all) && listed("AXImage", mode: .all), "all lists everything")
    expect(!listed("AXButton", mode: .text), "text leaves out a control with no words")
    expect(listed("AXButton", named: true, mode: .text) && listed("AXStaticText", valued: true, mode: .text), "text lists words")
    expect(listed("AXWindow", mode: .text), "and the window they are in")
}

func testContainersAreNotHitTested() {
    for role in ["AXWindow", "AXScrollArea", "AXGroup", "AXList", "AXTable", "AXToolbar", "AXWebArea"] {
        expect(!isHitTested(role: role), "\(role) holds other elements")
    }
    for role in ["AXButton", "AXStaticText", "AXTextField", "AXImage", "AXCheckBox", "AXLink", "AXSlider"] {
        expect(isHitTested(role: role), "\(role) is hit-tested")
    }
}
