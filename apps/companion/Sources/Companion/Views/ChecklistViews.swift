import SwiftUI

// A verdict's checklist in its card, and the verifier's declared plan in the transcript
// (root ADR 0024). What each says comes from `Model/Checklist.swift`; these only draw it.

/// The checks a verdict answers, under its reasons: the scope in one line, then each check
/// as its mark, its criterion, what was observed and the steps that show it.
struct VerdictChecklist: View {
    let store: RunStore
    let runId: String
    let checklist: Checklist
    /// Seeks the run to a cited step, as the card's other evidence does.
    let open: (Int) -> Void

    var body: some View {
        if let scope = checklist.scope {
            VStack(alignment: .leading, spacing: Space.s) {
                Text(scope)
                    .readingStyle(.readingMedium, size: TypeScale.readingSmall)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityAddTraits(.isHeader)
                VStack(alignment: .leading, spacing: Space.s) {
                    ForEach(Array(checklist.ordered.enumerated()), id: \.offset) { _, check in
                        CheckRow(store: store, runId: runId, check: check, open: open)
                    }
                }
            }
            .padding(.top, Space.xs)
            .accessibilityElement(children: .contain)
            .accessibilityLabel("Checks")
        }
    }
}

/// One check: the mark in its own column, so wrapped criteria line up under each other.
private struct CheckRow: View {
    let store: RunStore
    let runId: String
    let check: AcceptanceCheck
    let open: (Int) -> Void

    @Environment(\.theme) private var theme

    var body: some View {
        let mark = check.mark
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            Text(mark.glyph)
                .monoStyle(.monoBold, size: TypeScale.readingSmall)
                .foregroundStyle(theme.color(mark.role, on: .surface))
                .frame(width: Space.m, alignment: .leading)
                .accessibilityLabel(mark.word)
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
                if !check.evidence.isEmpty {
                    FlowLayout(spacing: Space.xs) {
                        ForEach(check.evidence, id: \.self) { step in
                            EvidenceLink(store: store, runId: runId, item: .step(step)) { open(step) }
                        }
                    }
                    .padding(.top, 2)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .accessibilityElement(children: .contain)
    }

    /// The criterion, and for a check that is not a pass, its state in words after it: a
    /// fail is more than a red mark, and a check not made must read as outside the scope.
    private func criterion(_ mark: CheckMark) -> Text {
        let words = Text(check.criterion.isEmpty ? check.id : check.criterion)
            .font(Typeface.readingRegular.font(size: TypeScale.readingSmall))
            .foregroundStyle(check.status == .unchecked ? theme.dim(on: .surface) : theme.foreground)
        guard check.status != .pass else { return words }
        let state = Text("  " + mark.word.lowercased())
            .font(Typeface.monoMedium.font(size: TypeScale.monoSmall))
            .foregroundStyle(check.status == .fail ? theme.color(.failure, on: .surface) : theme.dim(on: .surface))
        return words + state
    }
}

/// The verifier's declared checks, in the transcript where it declared them: "Plan: 4
/// checks" and each criterion, the rest behind one line when there are many.
struct CheckPlanBlock: View {
    let message: Message

    @State private var open = false
    @Environment(\.theme) private var theme

    var body: some View {
        if let plan = CheckPlan.of(message, open: open) {
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
