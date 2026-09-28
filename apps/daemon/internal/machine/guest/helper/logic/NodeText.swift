// An element's words as a node carries them (daemon ADR 0006, "Ops and results"): its name, the
// 240 character limit with its marks, the secure field whose value is never sent, and the roles
// and states in the wire's spelling.

import Foundation

/// A role or subrole as the wire spells it: without AX's prefix.
func wireRole(_ role: String) -> String {
    role.hasPrefix("AX") && role.count > 2 ? String(role.dropFirst(2)) : role
}

/// Text that says something: nil for a string that is empty or only white space.
func meaningful(_ text: String?) -> String? {
    guard let text, text.contains(where: { !$0.isWhitespace }) else { return nil }
    return text
}

/// An element's name: its title, else its description, else its placeholder, without the white
/// space around it.
func nodeName(title: String?, description: String?, placeholder: String?) -> String {
    (meaningful(title) ?? meaningful(description) ?? meaningful(placeholder) ?? "")
        .trimmingCharacters(in: .whitespacesAndNewlines)
}

/// Whether an element is a secure text field, by role or subrole.
func isSecure(role: String, subrole: String) -> Bool {
    role == "AXSecureTextField" || subrole == "AXSecureTextField"
}

/// The roles whose value of 0 or 1 is the `selected` state and not a value.
let toggleRoleNames: Set<String> = ["AXRadioButton", "AXCheckBox", "AXSwitch", "AXToggle"]

/// The text fields of a node, whole.
struct NodeText: Equatable {
    var name = ""
    var value = ""
    var desc = ""
    var help = ""
}

/// The text fields of a node as sent.
struct SentText: Equatable {
    var text = NodeText()
    /// The fields that were cut at the limit, in the wire's order.
    var cut: [String] = []
    /// The full length of the longest cut field, or of a secret's value; nil when nothing was
    /// cut or withheld.
    var chars: Int?
}

/// Applies the limit to a node's text. `full` lifts it (the ref is in the request's
/// `fullText`); `secret` withholds the value whatever `full` says, keeping only its length.
func sentText(_ whole: NodeText, secret: Bool, full: Bool, limit: Int = textLimit) -> SentText {
    var sent = SentText(text: whole)
    var longest = 0
    func bound(_ field: String, _ path: WritableKeyPath<NodeText, String>) {
        let (text, wasCut) = cut(whole[keyPath: path], limit: limit)
        guard wasCut else { return }
        sent.text[keyPath: path] = text
        sent.cut.append(field)
        longest = max(longest, whole[keyPath: path].count)
    }
    if secret {
        sent.text.value = ""
    }
    if !full {
        bound("name", \.name)
        if !secret { bound("value", \.value) }
        bound("desc", \.desc)
        bound("help", \.help)
    }
    if secret {
        // The length is all a secret says, and it says it even when it is zero.
        sent.chars = whole.value.count
    } else if longest > 0 {
        sent.chars = longest
    }
    return sent
}

// MARK: - What a walk lists

enum WalkMode: String {
    /// Controls, text, described images, windows, sheets, scroll areas and named groups.
    case interactive
    /// Every element with a frame.
    case all
    /// Only elements with text.
    case text
}

/// Roles that are only layout: walked through, and listed in interactive mode only when they
/// carry text of their own. The same set as the old tree's (Tree.swift's `containerRoles`, which
/// goes with `machine_ui` in wave 4), minus the scroll area, which is always listed.
let layoutRoles: Set<String> = [
    "AXGroup", "AXSplitGroup", "AXLayoutArea", "AXLayoutItem", "AXUnknown",
    "AXSplitter", "AXMatte", "AXGrowArea", "AXRow", "AXColumn", "AXCell",
]

/// Roles that frame other elements, listed in every mode so a tree keeps its shape.
let frameRoles: Set<String> = ["AXWindow", "AXSheet", "AXDrawer", "AXPopover", "AXScrollArea"]

/// Roles that hold other elements. Their centers belong to their children, so a hit test there
/// says nothing about them, and they are not hit-tested.
let holderRoles: Set<String> = layoutRoles.union(frameRoles).union([
    "AXApplication", "AXList", "AXTable", "AXOutline", "AXBrowser", "AXToolbar", "AXTabGroup",
    "AXRadioGroup", "AXWebArea", "AXMenuBar", "AXMenu", "AXGrid", "AXScrollBar",
])

/// Whether a walk in `mode` lists an element. `named` is whether it has a name, `valued` a
/// value, `described` a description, `identified` an identifier worth sending. A scroll bar is
/// listed only in `all`: its scroll area's `scroll` already says where it is and which ways it
/// moves, and its value changing on every scroll would fill an action's effect with noise.
func isListed(mode: WalkMode, role: String, named: Bool, valued: Bool, described: Bool, identified: Bool) -> Bool {
    if mode != .all, role == "AXScrollBar" { return false }
    switch mode {
    case .all:
        return true
    case .text:
        return role == "AXWindow" || named || valued || described
    case .interactive:
        if frameRoles.contains(role) { return true }
        if role == "AXImage" { return named || described }
        if layoutRoles.contains(role) { return named || valued || described || identified }
        return true
    }
}

/// Whether a listed element with a visible rect is hit-tested for what covers it.
func isHitTested(role: String) -> Bool {
    !holderRoles.contains(role)
}
