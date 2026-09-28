import Foundation

/// Activity (Figma wireframe 10): the verifier's work as task rows grouped under the check
/// they served, each row named by what the verifier said it was doing and holding its tool
/// calls as chips. Pure, with a test in `RunPaneTests`.
enum ActivityLayout {
    struct Section: Equatable, Sendable, Identifiable {
        var id: String { title }
        var title: String
        var rows: [TaskRowModel]
    }

    /// A row per verifier progress message (its steps are the tool calls made after it, until
    /// the next), grouped: "Setup" until the first row that touches a check's evidence, then
    /// under each check in the order the checks read. The last row of a run the verifier is
    /// still working on is running; a row with an error or proving a failed check has failed.
    static func sections(messages: [Message], steps: [Step], checks: [SummaryCheck], working: Bool) -> [Section] {
        let progress = messages.filter { $0.from == .verifier && ($0.kind == .progress || $0.kind == .reply) && !$0.text.isEmpty }
        var rows: [(TaskRowModel, Set<Int>)] = []
        let sortedSteps = steps.sorted { $0.seq < $1.seq }

        func chip(_ step: Step) -> ToolChipModel {
            let state: ToolChipModel.State = step.error.map { .error(short($0)) } ?? .done
            return ToolChipModel(id: step.seq, icon: icon(step.tool), label: chipLabel(step, in: sortedSteps),
                                 meta: String(format: "%.1fs", Double(step.durationMs) / 1000), state: state)
        }

        if progress.isEmpty {
            // No words from the verifier: one row per step.
            for step in sortedSteps {
                rows.append((TaskRowModel(id: "s\(step.seq)", title: StepSummary.phrase(for: step, in: sortedSteps),
                                          glyph: step.error == nil ? .passed : .failed, color: step.error == nil ? .pass : .fail,
                                          meta: String(format: "%.1fs", Double(step.durationMs) / 1000), chips: [], note: nil,
                                          opensItself: false, steps: [step.seq]), [step.seq]))
            }
        } else {
            for (index, message) in progress.enumerated() {
                let end = index + 1 < progress.count ? progress[index + 1].at : Date.distantFuture
                let mine = sortedSteps.filter { $0.at >= message.at && $0.at < end }
                let failedStep = mine.contains { $0.error != nil }
                let span = (mine.last?.at ?? message.at).timeIntervalSince(message.at) + Double(mine.last?.durationMs ?? 0) / 1000
                let isLast = index == progress.count - 1
                rows.append((TaskRowModel(id: "m\(message.seq)", title: title(message.text), glyph: failedStep ? .failed : .passed,
                                          color: failedStep ? .fail : .pass, meta: Clock.elapsed(Int(span.rounded())),
                                          chips: mine.map(chip), note: nil, opensItself: failedStep, steps: mine.map(\.seq)),
                             Set(mine.map(\.seq))))
                if isLast, working {
                    rows[rows.count - 1].0.glyph = .checking
                    rows[rows.count - 1].0.color = .accent
                    rows[rows.count - 1].0.meta = "now"
                }
            }
        }

        // Group by the check each row's steps prove.
        var sections: [Section] = []
        var seenCheck = false
        for (row, seqs) in rows {
            var row = row
            let check = checks.first { c in c.step.map { seqs.contains($0) } ?? false }
            if let check, check.state == .fail, row.glyph != .checking {
                row.glyph = .failed
                row.color = .fail
                row.opensItself = true
            }
            let title = check?.text ?? (seenCheck ? sections.last?.title ?? "Steps" : "Setup")
            if check != nil { seenCheck = true }
            if sections.last?.title == title {
                sections[sections.count - 1].rows.append(row)
            } else {
                sections.append(Section(title: title, rows: [row]))
            }
        }
        return sections
    }

    /// The row a step belongs to: the one holding it, else the last row that began before it.
    static func row(holding step: Int?, in sections: [Section]) -> String? {
        guard let step else { return nil }
        let rows = sections.flatMap(\.rows)
        if let exact = rows.first(where: { $0.steps.contains(step) }) { return exact.id }
        return rows.last { ($0.steps.first ?? .max) <= step }?.id
    }

    /// A verifier's progress line as a row title: its first sentence, short.
    static func title(_ text: String) -> String {
        // An older verifier echoed each tool call as its progress: say it in words.
        if ToolCatalog.tool(ofProgress: text) != nil { return StepSummary.phrase(ofProgress: text) }
        let first = text.split(whereSeparator: { $0 == "\n" }).first.map(String.init) ?? text
        let sentence = first.split(separator: ".", maxSplits: 1).first.map(String.init) ?? first
        let words = sentence.split(separator: " ")
        return words.count > 9 ? words.prefix(9).joined(separator: " ") + "…" : sentence
    }

    static func short(_ error: String) -> String {
        let lower = error.lowercased()
        if lower.contains("timed out") || lower.contains("deadline") { return "timed out" }
        if lower.contains("not answering") { return "no answer" }
        return "failed"
    }

    static func icon(_ tool: String) -> Icon {
        switch tool {
        case "machine_screenshot": .camera
        case "machine_ui": .eye
        case "machine_click", "machine_input", "machine_scroll": .pointer
        case "machine_type", "machine_key": .keyboard
        case "machine_exec", "machine_exec_wait": .logs
        default: .think
        }
    }

    /// A tool call in two or three words: "Click 25%", "Screenshot", "Read Each pays".
    static func chipLabel(_ step: Step, in steps: [Step]) -> String {
        let phrase = StepSummary.phrase(for: step, in: steps)
        let words = phrase.split(separator: " ")
        return words.count > 4 ? words.prefix(4).joined(separator: " ") + "…" : phrase
    }
}
