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

    private static func line(tool: String, input: JSONValue?, output: JSONValue?, screenshot: String?) -> String {
        switch tool {
        case "machine_ui": return uiLine(input: input, output: output)
        case "machine_approve_capture":
            if let app = input?["app"]?.stringValue, !app.isEmpty { return appName(app) }
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
    private static func uiLine(input: JSONValue?, output: JSONValue?) -> String {
        let asked = input?["app"]?.stringValue.flatMap { $0.isEmpty ? nil : appName($0) }
        let app = output?["app"]?.stringValue.flatMap { $0.isEmpty ? nil : $0 } ?? asked ?? "frontmost app"
        guard case .array(let elements)? = output?["elements"] else { return app }
        let count = elements.count == 1 ? "1 element" : "\(elements.count) elements"
        return "\(app), \(count)"
    }

    /// "/System/Applications/TextEdit.app" reads "TextEdit"; a bundle id or name stays.
    private static func appName(_ app: String) -> String {
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

    private static func number(_ value: JSONValue?) -> Double? {
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
