// Thinking-trace step rows and tool-call chips ported from Beautiful UI ThinkingState and ToolChips (MIT, (c) 2026 Shane Levine).
import Foundation

// PROTOTYPE: readable step summaries (never raw JSON in a row) and the two row shapes that
// read them: the steps trace (spinner -> muted check, settles, stays expandable) and the
// transcript's tool-call chips.

extension ProtoStep {
    /// Short chrome/data verb: mono, lower case, used only as a secondary fact (never the
    /// row's headline text - revision 18c wants plain words first).
    var verb: String {
        switch tool {
        case "machine_exec": "exec"
        case "machine_click": "click"
        case "machine_type": "type"
        case "machine_key": "key"
        case "machine_screenshot": "look"
        default: tool
        }
    }

    var target: String {
        guard let p = click else { return "" }
        let L = TipSplitLayout.self
        let named: [(String, CGPoint)] = [("Bill field", L.billCenter), ("People −", L.minus), ("People +", L.plus)]
            + L.tips.map { ("Tip \($0)%", L.tipCenter($0)) }
        return named.min { hypot($0.1.x - p.x, $0.1.y - p.y) < hypot($1.1.x - p.x, $1.1.y - p.y) }?.0 ?? ""
    }

    /// The raw argument text (a command, typed text, a key), unquoted, for `summary` and
    /// the mono chip inside a plain-language row.
    var rawArg: String {
        switch tool {
        case "machine_exec":
            if let r = input.range(of: "\"cmd\":\"") {
                var s = String(input[r.upperBound...])
                if s.hasSuffix("\"}") { s.removeLast(2) }
                return s.replacingOccurrences(of: "\\\"", with: "\"").replacingOccurrences(of: "\\\\", with: "\\")
            }
            return input
        case "machine_type":
            if let r = input.range(of: "\"text\":\"") { return String(input[r.upperBound...].dropLast(2)) }
            return input
        case "machine_key":
            if input.contains("cmd") { return "⌘A" }
            if input.contains("tab") { return "tab" }
            return input
        default: return input
        }
    }

    /// Kept for the expanded raw view; the row itself now reads `plainText`.
    var summary: String {
        switch tool {
        case "machine_exec": rawArg
        case "machine_click": target
        case "machine_type": "\"" + rawArg + "\""
        case "machine_key": input.contains("cmd") ? "⌘A  select all" : rawArg
        case "machine_screenshot": input.contains("region") ? "screenshot of the totals" : "screenshot"
        default: input
        }
    }

    /// A step read as a sentence, revision 18c: "Clicked Bill field", "Typed 120", "Took
    /// a screenshot", "Ran swift test --filter TipModelTests". The tool name and raw
    /// input stay one expand (⏎) away, never in the row itself.
    var plainText: String {
        switch tool {
        case "machine_click": "Clicked \(target)"
        case "machine_type": "Typed \(rawArg)"
        case "machine_key": input.contains("cmd") ? "Selected all" : "Pressed \(rawArg)"
        case "machine_screenshot": "Took a screenshot"
        case "machine_exec": "Ran \(rawArg)"
        default: rawArg
        }
    }

    var args: String {
        if let c = click { return String(format: "%.2f, %.2f", c.x, c.y) }
        return ""
    }

    /// The step's detail, shown when a row is expanded: input, then output or error.
    func detail(width: Int) -> [GridLine] {
        var out: [GridLine] = []
        let gutter: Ink = .role(.border)
        func add(_ text: String, _ ink: Ink = .fg, _ weight: Weight = .regular) {
            for raw in text.components(separatedBy: "\n") {
                for l in wrap([Span(raw, .tool, weight, ink: ink)], width: max(8, width - 2)) {
                    out.append([Span("│ ", ink: gutter)] + l)
                }
            }
        }
        add("\(tool) \(input)", .dim)
        if let error {
            add("error: " + error, .role(.failure))
        }
        if let stdout { add(stdout) }
        if let exitCode { add("exit \(exitCode) · \(durationLabel)", exitCode == 0 ? .dim : .role(.failure)) }
        else if error == nil { add("ok · \(durationLabel)", .dim) }
        return out
    }
}

extension PrototypeModel {
    enum StepPhase { case running, justDone, done, failed }

    func phase(_ s: ProtoStep) -> StepPhase {
        if stepRunning(s) { return .running }
        if s.failed { return .failed }
        if isScripted, playT - (s.t + s.play) < 0.35, !reduceMotion { return .justDone }
        return .done
    }

    /// Spinner while running, a check that lands bright then settles to muted, a cross on failure.
    func statusSpan(_ s: ProtoStep) -> Span {
        switch phase(s) {
        case .running: Span(FX.spinner(now, frozen: reduceMotion), ink: .role(.live))
        case .justDone: Span("✓", .chrome, .bold, ink: .fg)
        case .done: Span("✓", ink: .dim)
        case .failed: Span("✗", .chrome, .bold, ink: .role(.failure))
        }
    }

    /// A step row in the steps trace, revision 18c: plain words first ("Clicked Bill
    /// field"), at most 2 secondary facts (evidence, duration) - the tool name and raw
    /// input are one expand (⏎) away in `detail(width:)`.
    func stepRow(_ s: ProtoStep, width: Int, selected: Bool) -> GridLine {
        let num = String(repeating: " ", count: max(0, 3 - String(s.id).count)) + String(s.id)
        let failed = s.failed
        let evidence = verdict != nil && Synthetic.evidence.contains(s.id)
        let left: GridLine = [
            Span(" "), statusSpan(s), Span(" "),
            Span(num, ink: .dim), Span("  "),
            Span(s.plainText, .chrome, .medium, ink: failed ? .role(.failure) : .fg),
        ]
        var right: GridLine = []
        if evidence { right += [Span("◆ evidence", ink: .role(.needsYou)), Span("  ")] }
        right.append(Span(s.durationLabel.leftPad(6), ink: .dim))
        right.append(Span(" "))
        let line = spread(left, right, width: width)
        return selected ? line.map { var x = $0; x.back = .selection; return x } : line
    }

    /// A tool-call chip in the transcript: glyph, a plain-language phrase, duration.
    func toolChip(_ s: ProtoStep, width: Int, expanded: Bool) -> [GridLine] {
        let left: GridLine = [
            Span("  "), statusSpan(s), Span(" "),
            Span(s.plainText, .chrome, ink: s.failed ? .role(.failure) : .alpha(.fg, 0.86)),
        ]
        let right: GridLine = [Span(expanded ? "▾ " : "", ink: .dim), Span(s.durationLabel, ink: .dim)]
        var out = [spread(left, right, width: width)]
        if expanded {
            out += s.detail(width: width - 4).map { [Span("    ")] + $0 }
        }
        return out
    }
}

extension String {
    func leftPad(_ n: Int) -> String { count >= n ? self : String(repeating: " ", count: n - count) + self }
}
