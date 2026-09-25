import AppKit
import SwiftUI

/// Every tool call the run recorded. A row opens to show the screen at that step, what
/// went in and what came out. The step a person was sent to is highlighted and open.
struct StepsView: View {
    let store: RunStore
    let runId: String

    @State private var expanded: Set<Step> = []
    @AppStorage("stepsErrorsOnly") private var errorsOnly = false
    /// While live, keep the newest step in view.
    @State private var following = true
    @FocusState private var focused: Bool

    private var allSteps: [Step] { store.steps[runId] ?? [] }
    private var steps: [Step] { errorsOnly ? allSteps.filter { $0.outcome.isFailure } : allSteps }
    private var focusedStep: Int? {
        guard let request = store.focusedStep, request.runId == runId else { return nil }
        return request.step
    }

    private var facts: RunFacts { store.facts(runId) }

    @AppStorage("showsConversation") private var showsConversation = true

    private var fromVerdict: Bool { store.focusedStep?.fromVerdict == true && store.focusedStep?.runId == runId }

    var body: some View {
        VStack(spacing: 0) {
            if fromVerdict, let focusedStep {
                EvidenceBar(step: focusedStep, back: {
                    showsConversation = true
                    store.clearFocus()
                }, record: nil)
                .padding(.bottom, Space.s)
                Hairline()
            }
            table
        }
    }

    private var table: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0, pinnedViews: .sectionHeaders) {
                    Section {
                        // Keyed by the whole record: a restarted daemon has reused seqs.
                        ForEach(steps, id: \.self) { step in
                            StepRow(
                                store: store,
                                runId: runId,
                                step: step,
                                highlighted: step.seq == focusedStep,
                                expanded: Binding(
                                    get: { expanded.contains(step) },
                                    set: { open in
                                        if open { expanded.insert(step) } else { expanded.remove(step) }
                                    }
                                )
                            )
                        }
                    } header: {
                        StepsHeader(
                            count: allSteps.count,
                            failures: facts.failures.count,
                            errorsOnly: $errorsOnly,
                            following: facts.isAlive ? $following : nil
                        )
                    }
                }
            }
            .overlayScrollers()
            .overlay {
                if allSteps.isEmpty {
                    QuietEmpty(title: "No steps yet", message: "A step is recorded for every tool call against the machine.")
                } else if steps.isEmpty {
                    QuietEmpty(title: "No failed steps", message: "Every tool call in this run succeeded.")
                }
            }
            .onAppear {
                // The stage, not the composer, takes the keyboard when Steps opens.
                focused = true
                if let focusedStep {
                    reveal(focusedStep, proxy: proxy)
                } else {
                    openFirstFailure()
                    // Following a live run opens at the newest step, not at step 1 (#59):
                    // the count may never change while the stage is open.
                    if following, facts.isAlive, let last = steps.last {
                        Task {
                            try? await Task.sleep(for: .milliseconds(60))
                            proxy.scrollTo(last, anchor: .bottom)
                        }
                    }
                }
            }
            .onChange(of: store.seekRequest) {
                guard let focusedStep else { return }
                reveal(focusedStep, proxy: proxy)
            }
            .onChange(of: allSteps.count) {
                guard following, facts.isAlive, let last = steps.last else { return }
                // The row's identity in the lazy stack is the `Step`, not its seq.
                withAnimation { proxy.scrollTo(last, anchor: .bottom) }
            }
        }
        .focusable()
        .focusEffectDisabled()
        .focused($focused)
    }

    private func reveal(_ number: Int, proxy: ScrollViewProxy) {
        guard let step = allSteps.first(where: { $0.seq == number }) else { return }
        if errorsOnly, !step.outcome.isFailure { errorsOnly = false }
        expanded.insert(step)
        following = false
        // A lazy stack knows a row it has not built only by its `ForEach` identity (the
        // `Step`), so the seq id on the row's summary line cannot be found until the row
        // exists: bring the row in first, then place its summary line.
        proxy.scrollTo(step, anchor: .top)
        Task {
            // After the row has laid out, so the anchor is right.
            try? await Task.sleep(for: .milliseconds(60))
            // The row's summary line, not the whole opened row: its top must stay in view.
            withAnimation { proxy.scrollTo(number, anchor: UnitPoint(x: 0.5, y: 0.25)) }
        }
    }

    /// A failed run opens on its first failure, the way a CI log does.
    private func openFirstFailure() {
        guard expanded.isEmpty, let first = allSteps.first(where: { $0.outcome.isFailure }) else { return }
        expanded.insert(first)
    }
}

/// Widths shared by the header and every row.
private enum StepColumn {
    static let glyph: CGFloat = 14
    static let seq: CGFloat = 32
    static let duration: CGFloat = 64
    static let gap: CGFloat = Space.s
    static let horizontal: CGFloat = Space.l
    static let detailInset: CGFloat = horizontal + glyph + gap + seq + gap
}

private struct StepsHeader: View {
    let count: Int
    let failures: Int
    @Binding var errorsOnly: Bool
    let following: Binding<Bool>?

    @Environment(\.theme) private var theme

    var body: some View {
        HStack(spacing: Space.l) {
            SectionLabel(title: Chrome.plural(count, "step"))
            Spacer(minLength: Space.s)
            if let following {
                Toggle("Follow newest", isOn: following)
                    .help("Keep the newest step in view while the run is live")
            }
            Toggle(failures > 0 ? "Only the \(failures) that errored" : "Only errors", isOn: $errorsOnly)
                .disabled(failures == 0 && !errorsOnly)
                .help("Show only the steps whose tool call failed or whose command exited non-zero")
        }
        .padding(.horizontal, StepColumn.horizontal)
        .padding(.vertical, Space.s)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(theme.background)
        .overlay(alignment: .bottom) { Hairline() }
    }
}

/// One step in plain words (ADR 0008): a glyph for how it went, its number, what it did
/// as a sentence, and at most two facts (a failure badge, how long it took). The tool,
/// its time and its raw input and output are one click away.
private struct StepRow: View {
    let store: RunStore
    let runId: String
    let step: Step
    let highlighted: Bool
    @Binding var expanded: Bool

    @State private var hovering = false
    @Environment(\.theme) private var theme

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Button {
                withAnimation(.snappy(duration: 0.18)) { expanded.toggle() }
            } label: {
                summaryLine
            }
            .buttonStyle(.plain)
            .id(step.seq)
            .help(expanded ? "Hide this step" : "Show this step's screen, input and output")
            .accessibilityHint(expanded ? "Collapses the step" : "Expands the step")

            if expanded {
                StepDetail(store: store, runId: runId, step: step)
                    .padding(.leading, StepColumn.detailInset)
                    .padding(.trailing, StepColumn.horizontal)
                    .padding(.bottom, Space.l)
                    .transition(.opacity)
            }
        }
        // Background and separator both span the full row, so they line up.
        .background(background)
        .overlay(alignment: .leading) {
            if highlighted {
                Rectangle().fill(theme.brand).frame(width: 3)
            }
        }
        .overlay(alignment: .bottom) { Hairline() }
        .onHover { hovering = $0 }
    }

    private var failed: Bool { step.outcome.isFailure }

    private var summaryLine: some View {
        HStack(spacing: StepColumn.gap) {
            Text(failed ? "✗" : "✓")
                .foregroundStyle(failed ? theme.color(.failure) : theme.dim)
                .frame(width: StepColumn.glyph)
                .accessibilityLabel(failed ? "Errored" : "Done")

            Text("\(step.seq)")
                .monospacedDigit()
                .foregroundStyle(.secondary)
                .frame(width: StepColumn.seq, alignment: .trailing)

            HStack(spacing: Space.s) {
                if step.isRisky {
                    Text("!")
                        .fontWeight(.bold)
                        .foregroundStyle(theme.color(.attention))
                        .help("This command can destroy data or change the machine for good")
                        .accessibilityLabel("Risky command")
                }
                Text(StepSummary.phrase(for: step, in: store.steps[runId] ?? []))
                    .foregroundStyle(failed ? theme.color(.failure) : theme.foreground)
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .help(detailHelp)
                if let badge {
                    Text(badge)
                        .font(Typeface.monoMedium.font(size: TypeScale.monoSmall))
                        .foregroundStyle(theme.color(.failure))
                        .fixedSize()
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)

            Text(Chrome.duration(step.durationMs))
                .monospacedDigit()
                .foregroundStyle(.secondary)
                .frame(width: StepColumn.duration, alignment: .trailing)
                .help("\(Chrome.stamp(step.at)) (\(Chrome.zone))")
        }
        .monoStyle()
        .padding(.horizontal, StepColumn.horizontal)
        .padding(.vertical, Space.s)
        .contentShape(Rectangle())
    }

    private var badge: String? {
        switch step.outcome {
        case .ok: nil
        case .exit(let code): "exit \(code)"
        case .error: "error"
        }
    }

    private var detailHelp: String {
        let raw = "\(step.tool): \(StepSummary.line(for: step))"
        switch step.outcome {
        case .error(let error): return "\(raw)\n\nThe tool call failed: \(error)"
        default: return raw
        }
    }

    @ViewBuilder
    private var background: some View {
        if highlighted {
            theme.brand.opacity(0.10)
        } else if hovering || expanded {
            theme.highlight
        } else {
            Color.clear
        }
    }
}

private struct StepDetail: View {
    let store: RunStore
    let runId: String
    let step: Step

    @Environment(\.theme) private var theme

    var body: some View {
        VStack(alignment: .leading, spacing: Space.m) {
            outcomeBanner
            ViewThatFits(in: .horizontal) {
                HStack(alignment: .top, spacing: Space.l) {
                    // An ideal width, or a long line of JSON would never let the columns fit.
                    dataColumn.frame(minWidth: 240, idealWidth: 320)
                    pictureColumn.frame(width: 240)
                }
                VStack(alignment: .leading, spacing: Space.m) {
                    dataColumn
                    pictureColumn.frame(maxWidth: 320)
                }
            }
        }
    }

    private var pictureColumn: some View {
        VStack(alignment: .leading, spacing: Space.s) {
            StepThumbnail(store: store, runId: runId, step: step.seq)
                .aspectRatio(4 / 3, contentMode: .fit)
            Button("Show on Screen") {
                store.requestSeek(runId: runId, step: step.seq)
            }
            .buttonStyle(.quiet(small: true))
            .help("Open the recording at this step")
        }
    }

    private var dataColumn: some View {
        VStack(alignment: .leading, spacing: Space.m) {
            // The raw record, for whoever needs it: the tool's own name, then its JSON.
            Text("\(step.tool)  ·  \(Chrome.stamp(step.at))  ·  took \(Chrome.duration(step.durationMs))")
                .monoStyle(size: TypeScale.monoSmall)
                .foregroundStyle(.secondary)
                .textSelection(.enabled)
            if let input = step.input {
                block("Input", input.prettyPrinted)
            }
            if let output = step.output {
                // After a tool error the output is what came back before it failed, and
                // its zero values (exitCode 0) are not a result.
                block(step.error != nil ? "Partial output" : "Output", output.prettyPrinted)
                    .help(step.error != nil ? "What came back before the tool call failed. Its values, exitCode 0 included, are not a result." : "")
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    @ViewBuilder
    private var outcomeBanner: some View {
        switch step.outcome {
        case .ok:
            EmptyView()
        case .exit(let code):
            Text("✗ The command ran and exited \(code).")
                .readingStyle(.readingMedium, size: TypeScale.readingSmall)
                .foregroundStyle(theme.color(.failure))
        case .error(let error):
            VStack(alignment: .leading, spacing: Space.xs) {
                Text("✗ The tool call failed. The command's result is unknown.")
                    .readingStyle(.readingMedium, size: TypeScale.readingSmall)
                    .foregroundStyle(theme.color(.failure))
                Text(error)
                    .monoStyle()
                    .foregroundStyle(theme.color(.failure, on: .surface))
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(Space.s)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .panel(radius: Radius.sm, edge: theme.color(.failure))
            }
        }
    }

    /// JSON keeps its lines and scrolls sideways; long blocks scroll inside a fixed height.
    private func block(_ title: String, _ text: String) -> some View {
        VStack(alignment: .leading, spacing: Space.xs) {
            SectionLabel(title: title)
            let long = text.reduce(0) { $1 == "\n" ? $0 + 1 : $0 } > 14
            ScrollView(long ? [.horizontal, .vertical] : .horizontal) {
                Text(text)
                    .monoStyle(size: TypeScale.small)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: true, vertical: true)
                    .padding(Space.s)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .frame(height: long ? 240 : nil)
            .frame(maxWidth: .infinity, alignment: .leading)
            .panel(radius: Radius.sm)
        }
    }
}
