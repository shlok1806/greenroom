import SwiftUI

// A verdict's checklist in its card, and the verifier's declared plan in the transcript
// (root ADR 0024). What each says comes from `Model/Checklist.swift`; these only draw it.

/// The counts beside the outcome (companion ADR 0011): each with its mark in its role,
/// failed first, so the verdict's scope reads at a glance. Nothing for a verdict without checks.
struct CheckTally: View {
    let checklist: Checklist

    @Environment(\.theme) private var theme

    var body: some View {
        let items = checklist.tally
        if !items.isEmpty {
            HStack(alignment: .firstTextBaseline, spacing: Space.m) {
                ForEach(items, id: \.status) { item in
                    let mark = CheckMark.of(item.status)
                    HStack(alignment: .firstTextBaseline, spacing: Space.xs) {
                        Text(mark.glyph)
                            .foregroundStyle(theme.color(mark.role, on: .surface))
                        Text(item.text)
                            .foregroundStyle(item.status == .fail ? theme.foreground : theme.dim(on: .surface))
                    }
                    .monoStyle(item.status == .fail ? .monoBold : .monoMedium, size: TypeScale.monoSmall)
                    .fixedSize()
                }
            }
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(checklist.summary ?? "")
        }
    }
}

/// The checks a verdict answers, as a ledger (companion ADR 0011): each is its mark, its
/// criterion and kind, what was observed, what of its evidence a person cannot see, and
/// the evidence named by what it is. A row selects its check and shows its evidence.
struct VerdictChecklist: View {
    let store: RunStore
    let runId: String
    let checklist: Checklist

    var body: some View {
        if !checklist.checks.isEmpty {
            let steps = store.steps[runId] ?? []
            let selected = store.verdictDraft(runId).selectedCheck
            VStack(alignment: .leading, spacing: 0) {
                ForEach(checklist.ordered, id: \.id) { check in
                    CheckRow(check: check, steps: steps, selected: check.id == selected,
                             select: { store.selectCheck(runId: runId, id: check.id) },
                             open: { step in store.selectCheck(runId: runId, id: check.id, step: step) },
                             preview: { step in StepThumbnail(store: store, runId: runId, step: step) })
                }
            }
            .accessibilityElement(children: .contain)
            .accessibilityLabel("Checks, \(checklist.summary ?? "")")
        }
    }
}

/// One check: the mark in its own column, so wrapped criteria line up under each other.
private struct CheckRow<Preview: View>: View {
    let check: AcceptanceCheck
    let steps: [Step]
    let selected: Bool
    let select: () -> Void
    let open: (Int) -> Void
    @ViewBuilder let preview: (Int) -> Preview

    @State private var hovering = false
    @Environment(\.theme) private var theme

    var body: some View {
        let mark = check.mark
        let evidence = check.evidence.map { EvidenceStep.of($0, in: steps) }
        let warnings = check.unseenWarnings(in: steps)
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            Text(mark.glyph)
                .monoStyle(.monoBold, size: TypeScale.readingSmall)
                .foregroundStyle(theme.color(mark.role, on: .surface))
                .frame(width: Space.m, alignment: .leading)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: Space.xs) {
                criterion(mark)
                    .fixedSize(horizontal: false, vertical: true)
                if let observed = check.observed {
                    Text(observed)
                        .readingStyle(size: TypeScale.small)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                        .textSelection(.enabled)
                }
                ForEach(warnings, id: \.self) { warning in
                    Text("! " + warning)
                        .readingStyle(.readingMedium, size: TypeScale.small)
                        .foregroundStyle(theme.color(.attention, on: .surface))
                        .fixedSize(horizontal: false, vertical: true)
                }
                if !evidence.isEmpty || !check.actions.isEmpty {
                    FlowLayout(spacing: Space.m, lineSpacing: Space.xs) {
                        ForEach(evidence, id: \.step) { item in
                            EvidenceStepLink(item: item, open: { open(item.step) }, preview: { preview(item.step) })
                        }
                        if !check.actions.isEmpty {
                            Text(Self.after(check.actions))
                                .monoStyle(size: TypeScale.monoSmall)
                                .foregroundStyle(theme.dim(on: .surface))
                                .help("The inputs this check depends on")
                        }
                    }
                    .padding(.top, 2)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.vertical, Space.s)
        .padding(.horizontal, Space.s)
        .background(selected || hovering ? theme.highlight : .clear,
                    in: RoundedRectangle(cornerRadius: Radius.sm, style: .continuous))
        // The selected check: a brand edge, as the selected run has (ADR 0008).
        .overlay(alignment: .leading) {
            if selected {
                Rectangle().fill(theme.brand).frame(width: 2).padding(.vertical, Space.xs)
                    .accessibilityHidden(true)
            }
        }
        .contentShape(Rectangle())
        .onTapGesture(perform: select)
        .onHover { hovering = $0 }
        .help(check.evidence.isEmpty ? "No evidence cited" : "Show this check's evidence on the screen")
        .accessibilityElement(children: .combine)
        .accessibilityLabel(Self.spoken(check, evidence: evidence, warnings: warnings))
        .accessibilityAddTraits(selected ? [.isButton, .isSelected] : .isButton)
        .accessibilityAction(named: "Show evidence", select)
    }

    /// The criterion, and for a check that is not a pass, its state in words after it: a
    /// fail is more than a red mark, and a check not made must read as outside the scope.
    /// A kind beyond a plain value follows in the mono face.
    private func criterion(_ mark: CheckMark) -> Text {
        var words = Text(check.criterion.isEmpty ? check.id : check.criterion)
            .font(Typeface.readingRegular.font(size: TypeScale.readingSmall))
            .foregroundStyle(check.status == .unchecked ? theme.dim(on: .surface) : theme.foreground)
        if check.status != .pass {
            words = words + Text("  " + mark.word.lowercased())
                .font(Typeface.monoMedium.font(size: TypeScale.monoSmall))
                .foregroundStyle(check.status == .fail ? theme.color(.failure, on: .surface) : theme.dim(on: .surface))
        }
        if let kind = check.kindTag {
            words = words + Text("  " + kind)
                .font(Typeface.monoRegular.font(size: TypeScale.monoSmall))
                .foregroundStyle(theme.dim(on: .surface))
        }
        return words
    }

    /// "after step 10", "after steps 6, 7 and 8".
    static func after(_ actions: [Int]) -> String {
        guard actions.count > 1 else { return "after step \(actions[0])" }
        let head = actions.dropLast().map(String.init).joined(separator: ", ")
        return "after steps \(head) and \(actions.last!)"
    }

    /// One sentence for VoiceOver: state, criterion, kind, what was seen, the evidence.
    static func spoken(_ check: AcceptanceCheck, evidence: [EvidenceStep], warnings: [String]) -> String {
        var parts = [check.mark.word, check.criterion]
        if let kind = check.kindTag { parts.append(kind) }
        if let observed = check.observed { parts.append("Observed: \(observed)") }
        parts += warnings
        if !evidence.isEmpty { parts.append("Evidence: " + evidence.map(\.label).joined(separator: ", ")) }
        return parts.joined(separator: ". ")
    }
}

/// A cited step named by what it is ("Screenshot 7", "UI read 6"): a link in the mono
/// face, no box (companion ADR 0011), with a picture of the step on hover.
struct EvidenceStepLink<Preview: View>: View {
    let item: EvidenceStep
    let open: () -> Void
    @ViewBuilder let preview: () -> Preview

    @State private var hovering = false
    @State private var showsPreview = false
    @Environment(\.theme) private var theme

    var body: some View {
        Button(action: open) {
            HStack(spacing: Space.xs) {
                Text(item.label)
                    .underline(hovering)
                Text(item.held ? "↗" : "not in the record")
                    .foregroundStyle(item.held ? theme.dim(on: .surface) : theme.color(.failure, on: .surface))
            }
            .monoStyle(.monoMedium, size: TypeScale.monoSmall)
            .foregroundStyle(item.held ? theme.foreground : theme.color(.failure, on: .surface))
            .padding(.vertical, 2)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { inside in
            hovering = inside
            Task {
                try? await Task.sleep(for: .milliseconds(450))
                showsPreview = hovering && item.held
            }
        }
        .popover(isPresented: $showsPreview, arrowEdge: .bottom) {
            preview()
                .frame(width: 320, height: 240)
                .padding(Space.s)
        }
        .help("Show \(item.label.lowercased()) on the screen")
    }
}

/// The verifier's declared checks, in the transcript where it declared them: "Plan: 4
/// checks" and each criterion, the rest behind one line when there are many.
struct CheckPlanBlock: View {
    let message: Message
    /// The current verdict answers it: one line until opened, its rows are in the card.
    var answered = false

    @State private var open = false
    @Environment(\.theme) private var theme

    var body: some View {
        if answered, !open {
            Button {
                withAnimation(.snappy(duration: 0.2)) { open = true }
            } label: {
                HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                    Text("▸").accessibilityHidden(true)
                    Text("Plan: \(message.checks.count) \(message.checks.count == 1 ? "check" : "checks"), answered in the verdict above")
                    Spacer(minLength: Space.s)
                    Text(Chrome.shortTime(message.at)).monospacedDigit()
                }
                .monoStyle(size: TypeScale.monoSmall)
                .foregroundStyle(.secondary)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .padding(.horizontal, Space.m)
            .padding(.vertical, Space.s)
            .help("Show the checks the verifier declared")
        } else if let plan = CheckPlan.of(message, open: open) {
            VStack(alignment: .leading, spacing: Space.s) {
                HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                    SectionLabel(title: plan.title)
                    Text("by the verifier")
                        .readingStyle(size: TypeScale.small)
                        .foregroundStyle(.secondary)
                    Spacer(minLength: Space.s)
                    Text(Chrome.shortTime(message.at))
                        .monoStyle(size: TypeScale.monoSmall)
                        .monospacedDigit()
                        .foregroundStyle(.secondary)
                        .help(Chrome.stamp(message.at))
                }
                .accessibilityElement(children: .combine)
                VStack(alignment: .leading, spacing: Space.xs) {
                    ForEach(Array(plan.criteria.enumerated()), id: \.offset) { index, criterion in
                        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                            Text("\(index + 1)")
                                .monoStyle(size: TypeScale.monoSmall)
                                .monospacedDigit()
                                .foregroundStyle(theme.dim(on: .surface))
                                .frame(minWidth: Space.m, alignment: .trailing)
                                .accessibilityHidden(true)
                            Text(criterion)
                                .readingStyle(size: TypeScale.readingSmall)
                                .fixedSize(horizontal: false, vertical: true)
                                .textSelection(.enabled)
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                    }
                }
                if plan.hidden > 0 || open {
                    Button {
                        withAnimation(.snappy(duration: 0.2)) { open.toggle() }
                    } label: {
                        HStack(spacing: Space.s) {
                            Text(open ? "▾" : "▸")
                                .accessibilityHidden(true)
                            Text(CheckPlan.fold(hidden: plan.hidden, open: open))
                        }
                        .monoStyle(size: TypeScale.monoSmall)
                        .foregroundStyle(.secondary)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .help(open ? "Show the first \(CheckPlan.shown) checks" : "Show every check")
                }
            }
            .padding(Space.m)
            .frame(maxWidth: .infinity, alignment: .leading)
            .panel(radius: Radius.md)
            .accessibilityElement(children: .contain)
            .accessibilityLabel("The verifier's plan, \(message.checks.count) checks")
        }
    }
}
