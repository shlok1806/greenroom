import Foundation

/// One line saying what a step did, read from the input keys the daemon's
/// tools write, falling back to compact JSON.
enum StepSummary {
    /// Input keys the daemon records for itself, never shown: `reader` says whose
    /// `machine_ui` read it was (coder or verifier), which aiming needs and a person does not.
    private static let bookkeeping: Set<String> = ["reader"]

    static func line(for step: Step) -> String {
        line(tool: step.tool, input: step.input, output: step.output, screenshot: step.screenshotArtifact)
    }

    /// A verifier tool call as its progress message carries it, `<tool> <json>` then the
    /// result, for a row whose step record is not held yet. Same rules as `line(for:)`.
    static func line(ofProgress text: String) -> String {
        let (head, result) = text.firstIndex(of: "\n").map { (String(text[..<$0]), String(text[text.index(after: $0)...])) }
            ?? (text, "")
        let parts = head.split(separator: " ", maxSplits: 1).map(String.init)
        guard let tool = parts.first else { return "" }
        let arguments = parts.count > 1 ? parts[1] : ""
        let input = json(arguments)
        guard input != nil || arguments.isEmpty else {
            // Not JSON: say what it says, on one line.
            return oneLine(arguments)
        }
        return line(tool: tool, input: input, output: json(result), screenshot: nil)
    }

    static func line(tool: String, input: JSONValue?, output: JSONValue?, screenshot: String?) -> String {
        switch tool {
        case "machine_ui": return uiLine(input: input, output: output)
        case "machine_approve_capture":
            if let app = input?["app"]?.stringValue, !app.isEmpty { return appName(app) }
        case "machine_click", "machine_key", "machine_scroll":
            // The single-action tools record the action's own fields, without a type.
            if case .object(var fields)? = input {
                if let element = number(fields["element"]), element != 0 { return "click element \(Int(element))" }
                fields["type"] = .string(String(tool.dropFirst("machine_".count)))
                return actionLine(.object(fields))
            }
        default: break
        }
        if let actions = input?["actions"], case .array(let list) = actions {
            return inputLine(list)
        }
        for key in ["command", "text", "dest", "path", "image", "name", "url"] {
            if let value = input?[key]?.stringValue, !value.isEmpty {
                return oneLine(value)
            }
        }
        if let screenshot { return screenshot }
        if case .object(var fields)? = input {
            for key in bookkeeping { fields[key] = nil }
            if !fields.isEmpty { return oneLine(JSONValue.object(fields).compact) }
        }
        return ""
    }

    /// "TipSplit, 20 elements" once the tree came back; else the app it asked for.
    static func uiLine(input: JSONValue?, output: JSONValue?) -> String {
        let asked = input?["app"]?.stringValue.flatMap { $0.isEmpty ? nil : appName($0) }
        let app = output?["app"]?.stringValue.flatMap { $0.isEmpty ? nil : $0 } ?? asked ?? "frontmost app"
        guard case .array(let elements)? = output?["elements"] else { return app }
        let count = elements.count == 1 ? "1 element" : "\(elements.count) elements"
        return "\(app), \(count)"
    }

    /// "/System/Applications/TextEdit.app" reads "TextEdit"; a bundle id or name stays.
    static func appName(_ app: String) -> String {
        let name = (app as NSString).lastPathComponent
        let bare = name.hasSuffix(".app") ? String(name.dropLast(4)) : name
        return bare.isEmpty ? oneLine(app) : bare
    }

    private static func json(_ text: String) -> JSONValue? {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard trimmed.hasPrefix("{") || trimmed.hasPrefix("[") else { return nil }
        return try? JSONDecoder.daemon().decode(JSONValue.self, from: Data(trimmed.utf8))
    }

    /// The first action spelled out, the rest counted.
    private static func inputLine(_ actions: [JSONValue]) -> String {
        guard let first = actions.first else { return "no actions" }
        let head = actionLine(first)
        guard actions.count > 1 else { return head }
        return "\(head) +\(actions.count - 1) more"
    }

    private static func actionLine(_ action: JSONValue) -> String {
        let type = action["type"]?.stringValue ?? "action"
        switch type {
        case "type":
            let text = action["text"]?.stringValue ?? ""
            return "type \(quoted(text))"
        case "key":
            let mods = modifiers(action["mods"])
            let key = action["key"]?.stringValue ?? "?"
            return "key \(mods)\(key)"
        case "scroll":
            return "scroll"
        case "sleep":
            return "sleep"
        default:
            guard let at = place(action) else { return type }
            return "\(type) \(at)"
        }
    }

    private static func modifiers(_ value: JSONValue?) -> String {
        guard case .array(let list)? = value else { return "" }
        let names = list.compactMap(\.stringValue)
        return names.isEmpty ? "" : names.joined(separator: "-") + "-"
    }

    /// Percentages, because the app only knows fractions (ADR 0009).
    private static func place(_ action: JSONValue) -> String? {
        guard let x = number(action["x"]), let y = number(action["y"]) else { return nil }
        return String(format: "%.0f%%, %.0f%%", x * 100, y * 100)
    }

    static func number(_ value: JSONValue?) -> Double? {
        switch value {
        case .double(let value): return value
        case .int(let value): return Double(value)
        default: return nil
        }
    }

    private static func quoted(_ text: String) -> String {
        let flat = oneLine(text)
        let clipped = flat.count > 32 ? flat.prefix(32) + "…" : flat[...]
        return "\"\(clipped)\""
    }

    /// Collapses all whitespace runs to single spaces.
    static func oneLine(_ text: String) -> String {
        text.split(whereSeparator: \.isWhitespace).joined(separator: " ")
    }
}

// MARK: - Plain words (ADR 0008)

/// A step as a sentence a person would say: "Clicked Bill field", "Typed 120", "Took a
/// screenshot", "Ran swift test". The raw tool name and JSON are one expand away, never
/// in the row. `line(for:)` stays the terse data form (evidence captions, search).
extension StepSummary {
    /// `steps` is the run's record so far: a click is named by the control under it in
    /// the latest earlier UI read (`machine_ui`), when there is one.
    static func phrase(for step: Step, in steps: [Step] = []) -> String {
        let screen = steps.last { $0.seq < step.seq && $0.tool == "machine_ui" && elements(of: $0) != nil }
        return phrase(tool: step.tool, input: step.input, output: step.output, screen: screen.flatMap(elements(of:)))
    }

    /// A verifier tool call as its progress message carries it, `<tool> <json>` then the
    /// result, for a row whose step record is not held yet.
    static func phrase(ofProgress text: String) -> String {
        let head = text.split(separator: "\n", maxSplits: 1, omittingEmptySubsequences: false).first.map(String.init) ?? text
        let result = text.firstIndex(of: "\n").map { String(text[text.index(after: $0)...]) } ?? ""
        let parts = head.split(separator: " ", maxSplits: 1).map(String.init)
        guard let tool = parts.first, ToolCatalog.tool(ofProgress: text) != nil else { return oneLine(head) }
        let arguments = parts.count > 1 ? parts[1] : ""
        return phrase(tool: tool, input: progressJSON(arguments), output: progressJSON(result), screen: nil)
    }

    private static func phrase(tool: String, input: JSONValue?, output: JSONValue?, screen: [Element]?) -> String {
        switch tool {
        case "machine_create":
            if let image = input?["image"]?.stringValue, !image.isEmpty { return "Created a machine from \(image)" }
            return "Created a machine"
        case "machine_boot": return "Booted the machine"
        case "machine_wait": return "Waited for the machine"
        case "machine_list": return "Listed the machines"
        case "machine_destroy": return "Destroyed the machine"
        case "machine_screenshot": return "Took a screenshot"
        case "machine_sync":
            if let dest = input?["dest"]?.stringValue, !dest.isEmpty { return "Copied \(dest) to the machine" }
            return "Copied files to the machine"
        case "machine_pull":
            if let source = input?["source"]?.stringValue, !source.isEmpty { return "Copied \(source) from the machine" }
            return "Copied files from the machine"
        case "machine_exec":
            if let command = input?["command"]?.stringValue, !command.isEmpty { return "Ran \(oneLine(command))" }
            return "Ran a command"
        case "machine_ui":
            let app = uiLine(input: input, output: output).split(separator: ",").first.map(String.init) ?? "frontmost app"
            return app == "frontmost app" ? "Read the frontmost app" : "Read \(app)"
        case "machine_approve_capture":
            if let app = input?["app"]?.stringValue, !app.isEmpty { return "Allowed \(appName(app)) to be captured" }
            return "Allowed screen capture"
        case "machine_type":
            return "Typed \(clipped(input?["text"]?.stringValue ?? ""))"
        case "machine_click", "machine_key", "machine_scroll":
            if case .object(var fields)? = input {
                if let element = number(fields["element"]), element != 0 { return "Clicked element \(Int(element))" }
                fields["type"] = .string(String(tool.dropFirst("machine_".count)))
                return actionPhrase(.object(fields), screen: screen)
            }
        case let name where name.hasPrefix("machine_session"):
            return "Used a terminal session"
        default:
            break
        }
        if case .array(let actions)? = input?["actions"] {
            guard let first = actions.first else { return "Sent no input" }
            let head = actionPhrase(first, screen: screen)
            return actions.count == 1 ? head : "\(head), then \(actions.count - 1) more"
        }
        let entry = ToolCatalog.entry(for: tool)
        let detail = line(tool: tool, input: input, output: output, screenshot: nil)
        let title = entry.title == tool ? "Used \(tool)" : entry.title
        return detail.isEmpty ? title : "\(title): \(detail)"
    }

    private static func actionPhrase(_ action: JSONValue, screen: [Element]?) -> String {
        switch action["type"]?.stringValue ?? "" {
        case "click":
            let count = number(action["clicks"]).map(Int.init) ?? 1
            let verb = count >= 2 ? "Double-clicked" : "Clicked"
            return "\(verb) \(target(of: action, screen: screen))"
        case "down": return "Pressed the mouse on \(target(of: action, screen: screen))"
        case "up": return "Released the mouse"
        case "move": return "Moved the pointer"
        case "type": return "Typed \(clipped(action["text"]?.stringValue ?? ""))"
        case "key": return "Pressed \(keyName(action["key"]?.stringValue ?? "", mods: action["mods"]))"
        case "scroll":
            let dy = number(action["deltaY"]) ?? number(action["dy"]) ?? 0
            // The daemon's positive deltaY scrolls down (`InputBatch.scroll`).
            return dy < 0 ? "Scrolled up" : dy > 0 ? "Scrolled down" : "Scrolled"
        case "sleep": return "Waited"
        case let other where !other.isEmpty: return other.prefix(1).uppercased() + other.dropFirst()
        default: return "Sent input"
        }
    }

    /// "⌘A", "Tab", "Return", "⇧⌘Z".
    static func keyName(_ key: String, mods: JSONValue?) -> String {
        let order = ["ctrl": "⌃", "control": "⌃", "alt": "⌥", "option": "⌥", "opt": "⌥", "shift": "⇧", "cmd": "⌘", "command": "⌘"]
        var names: [String] = []
        if case .array(let list)? = mods { names = list.compactMap(\.stringValue).map { $0.lowercased() } }
        let symbols: [String] = ["⌃", "⌥", "⇧", "⌘"].filter { symbol in names.contains { order[$0] == symbol } }
        let named: [String: String] = [
            "return": "Return", "enter": "Return", "tab": "Tab", "escape": "Esc", "esc": "Esc", "space": "Space",
            "delete": "Delete", "backspace": "Delete", "up": "↑", "down": "↓", "left": "←", "right": "→",
        ]
        let base: String = named[key.lowercased()] ?? (key.count == 1 ? key.uppercased() : key.prefix(1).uppercased() + String(key.dropFirst()))
        return symbols.joined(separator: "") + base
    }

    private static func clipped(_ text: String) -> String {
        let flat = oneLine(text)
        return flat.count > 32 ? String(flat.prefix(32)) + "…" : flat
    }

    private static func progressJSON(_ text: String) -> JSONValue? {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard trimmed.hasPrefix("{") || trimmed.hasPrefix("[") else { return nil }
        return try? JSONDecoder.daemon().decode(JSONValue.self, from: Data(trimmed.utf8))
    }

    // MARK: Naming what was clicked

    /// One control from a UI read: its centre and size as screen fractions.
    struct Element: Equatable, Sendable {
        var role: String
        var subrole: String?
        var title: String?
        var label: String?
        var value: String?
        var identifier: String?
        var depth: Int
        var x: Double
        var y: Double
        var w: Double
        var h: Double

        func contains(x px: Double, y py: Double) -> Bool {
            abs(px - x) <= w / 2 && abs(py - y) <= h / 2
        }
    }

    static func elements(of step: Step) -> [Element]? {
        guard case .array(let list)? = step.output?["elements"] else { return nil }
        let out = list.compactMap { item -> Element? in
            guard let x = number(item["x"]), let y = number(item["y"]),
                  let w = number(item["w"]), let h = number(item["h"]) else { return nil }
            func text(_ key: String) -> String? {
                item[key]?.stringValue.flatMap { $0.trimmingCharacters(in: .whitespaces).isEmpty ? nil : $0 }
            }
            return Element(role: text("role") ?? "", subrole: text("subrole"), title: text("title"), label: text("label"),
                           value: text("value"), identifier: text("identifier"),
                           depth: number(item["depth"]).map(Int.init) ?? 0, x: x, y: y, w: w, h: h)
        }
        return out.isEmpty ? nil : out
    }

    /// The control under a point in words ("Bill field", "20%", "People +"), else the place.
    private static func target(of action: JSONValue, screen: [Element]?) -> String {
        guard let x = number(action["x"]), let y = number(action["y"]) else { return "the screen" }
        let place = String(format: "at %.0f%%, %.0f%%", x * 100, y * 100)
        guard let screen, let hit = hit(x: x, y: y, in: screen), let name = name(of: hit, in: screen) else { return place }
        return name
    }

    /// The deepest control that holds the point, the smallest if two are as deep.
    static func hit(x: Double, y: Double, in elements: [Element]) -> Element? {
        elements
            .filter { $0.contains(x: x, y: y) && $0.role != "Window" }
            .max { ($0.depth, -$0.w * $0.h) < ($1.depth, -$1.w * $1.h) }
    }

    /// What a person would call a control: its own label or title, else the text beside
    /// it on the left ("Bill" for the field after it), with the kind of control after.
    static func name(of element: Element, in elements: [Element]) -> String? {
        let own = element.label ?? element.title
        let kind: String
        switch (element.role, element.subrole) {
        case (_, "IncrementArrow"?): kind = " +"
        case (_, "DecrementArrow"?): kind = " −"
        case ("TextField", _), ("TextArea", _), ("SearchField", _), ("ComboBox", _): kind = " field"
        case ("StaticText", _): return element.value.map { "\u{201C}\(clipped($0))\u{201D}" }
        default: kind = ""
        }
        if let own, kind.isEmpty || element.role != "TextField" { return own + kind }
        if let beside = caption(beside: element, in: elements) { return beside + kind }
        if let own { return own + kind }
        if let identifier = element.identifier, !identifier.contains(".") { return identifier + kind }
        return nil
    }

    /// The static text to the left of a control on its row: a form's caption. "People: 2"
    /// reads "People".
    private static func caption(beside element: Element, in elements: [Element]) -> String? {
        let row = max(element.h, 0.02)
        let left = element.x - element.w / 2
        let captions = elements.filter { candidate in
            candidate.role == "StaticText" && candidate.value != nil
                && abs(candidate.y - element.y) <= row && candidate.x + candidate.w / 2 <= left + 0.005
        }
        guard let nearest = captions.max(by: { $0.x < $1.x }), let value = nearest.value else { return nil }
        let words = value.split(separator: ":", maxSplits: 1).first.map { $0.trimmingCharacters(in: .whitespaces) } ?? value
        return words.isEmpty ? nil : words
    }
}
