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

    @AppStorage("verdictCardExpanded") private var verdictExpanded = false
    @AppStorage("showsConversation") private var showsConversation = true

    private var fromVerdict: Bool { store.focusedStep?.fromVerdict == true && store.focusedStep?.runId == runId }

    var body: some View {
        VStack(spacing: 0) {
            if fromVerdict, let focusedStep {
                EvidenceBar(step: focusedStep, back: {
                    verdictExpanded = true
                    showsConversation = true
                    store.clearFocus()
                }, record: nil)
                .padding(.bottom, Space.s)
                Divider()
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
                    ContentUnavailableView(
                        "No steps yet",
                        systemImage: "list.bullet.rectangle",
                        description: Text("A step is recorded for every tool call against the machine.")
                    )
                } else if steps.isEmpty {
                    ContentUnavailableView(
                        "No failed steps",
                        systemImage: "checkmark.circle",
                        description: Text("Every tool call in this run succeeded.")
                    )
                }
            }
            .onAppear {
                // The stage, not the composer, takes the keyboard when Steps opens.
                focused = true
                if let focusedStep {
                    reveal(focusedStep, proxy: proxy)
                } else {
                    openFirstFailure()
                }
            }
            .onChange(of: store.seekRequest) {
                guard let focusedStep else { return }
                reveal(focusedStep, proxy: proxy)
            }
            .onChange(of: allSteps.count) {
                guard following, facts.isAlive, let last = steps.last else { return }
                withAnimation { proxy.scrollTo(last.seq, anchor: .bottom) }
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

/// Column widths shared by the header and every row.
private enum StepColumn {
    static let seq: CGFloat = 36
    static let icon: CGFloat = 18
    static let title: CGFloat = 112
    static let time: CGFloat = 60
    static let duration: CGFloat = 60
    static let gap: CGFloat = 10
    static let horizontal: CGFloat = Space.l
    static let detailInset: CGFloat = horizontal + seq + gap
}

private struct StepsHeader: View {
    let count: Int
    let failures: Int
    @Binding var errorsOnly: Bool
    let following: Binding<Bool>?

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: Space.m) {
                Text(Chrome.plural(count, "step"))
                    .font(.headline)
                Spacer(minLength: Space.s)
                if let following {
                    Toggle("Follow newest", isOn: following)
                        .toggleStyle(.checkbox)
                        .help("Keep the newest step in view while the run is live")
                }
                Toggle(failures > 0 ? "Only the \(failures) that errored" : "Only errors", isOn: $errorsOnly)
                    .toggleStyle(.checkbox)
                    .disabled(failures == 0 && !errorsOnly)
                    .help("Show only the steps whose tool call failed or whose command exited non-zero")
            }
            .font(.callout)
            .controlSize(.small)
            .padding(.horizontal, StepColumn.horizontal)
            .padding(.vertical, Space.s)

            HStack(spacing: StepColumn.gap) {
                Text("Step").frame(width: StepColumn.seq, alignment: .trailing)
                Text("Tool").frame(width: StepColumn.icon + StepColumn.gap + StepColumn.title, alignment: .leading)
                Text("Detail").frame(maxWidth: .infinity, alignment: .leading)
                Text("Time").frame(width: StepColumn.time, alignment: .trailing)
                    .help("Your local time (\(Chrome.zone))")
                Text("Took").frame(width: StepColumn.duration, alignment: .trailing)
            }
            .font(.caption.weight(.medium))
            .foregroundStyle(.secondary)
            .padding(.horizontal, StepColumn.horizontal)
            .padding(.bottom, 6)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .windowBackgroundColor))
        .overlay(alignment: .bottom) { Divider() }
    }
}

private struct StepRow: View {
    let store: RunStore
    let runId: String
    let step: Step
    let highlighted: Bool
    @Binding var expanded: Bool

    @State private var hovering = false

    private var entry: ToolCatalog.Entry { ToolCatalog.entry(for: step.tool) }

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
                    .padding(.bottom, Space.m)
                    .transition(.opacity)
            }
        }
        // Background and separator both span the full row, so they line up.
        .background(background)
        .overlay(alignment: .leading) {
            if highlighted {
                Rectangle().fill(Color.accentColor).frame(width: 3)
            }
        }
        .overlay(alignment: .bottom) {
            Rectangle().fill(Palette.hairline.opacity(0.6)).frame(height: 1)
        }
        .onHover { hovering = $0 }
    }

    private var failureColour: AnyShapeStyle {
        step.outcome.isFailure ? AnyShapeStyle(Palette.failure) : AnyShapeStyle(.primary)
    }

    private var summaryLine: some View {
        HStack(spacing: StepColumn.gap) {
            Text("\(step.seq)")
                .font(.callout.monospacedDigit())
                .foregroundStyle(.secondary)
                .frame(width: StepColumn.seq, alignment: .trailing)

            HStack(spacing: StepColumn.gap) {
                Image(systemName: step.outcome.isFailure ? "exclamationmark.triangle.fill" : entry.symbol)
                    .font(.callout)
                    .foregroundStyle(step.outcome.isFailure ? AnyShapeStyle(Palette.failure) : AnyShapeStyle(.secondary))
                    .frame(width: StepColumn.icon)
                Text(entry.title)
                    .font(.body)
                    .foregroundStyle(failureColour)
                    .lineLimit(1)
                    .frame(width: StepColumn.title, alignment: .leading)
                    .help(step.tool)
            }

            HStack(spacing: 6) {
                if step.isRisky {
                    Image(systemName: "exclamationmark.octagon.fill")
                        .font(.caption)
                        .foregroundStyle(Palette.attention)
                        .help("This command can destroy data or change the machine for good")
                }
                Text(StepSummary.line(for: step))
                    .font(.callout.monospaced())
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .help(detailHelp)
                if let badge {
                    Text(badge)
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(Palette.failure)
                        .padding(.horizontal, 5)
                        .padding(.vertical, 1)
                        .background(Palette.failure.opacity(0.12), in: RoundedRectangle(cornerRadius: Radius.chip))
                        .fixedSize()
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)

            Text(Chrome.shortTime(step.at))
                .font(.callout.monospacedDigit())
                .foregroundStyle(.secondary)
                .frame(width: StepColumn.time, alignment: .trailing)
                .help(Chrome.stamp(step.at))

            Text(Chrome.duration(step.durationMs))
                .font(.callout.monospacedDigit())
                .foregroundStyle(.secondary)
                .frame(width: StepColumn.duration, alignment: .trailing)
        }
        .padding(.horizontal, StepColumn.horizontal)
        .padding(.vertical, 7)
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
        switch step.outcome {
        case .error(let error): "\(StepSummary.line(for: step))\n\nThe tool call failed: \(error)"
        default: StepSummary.line(for: step)
        }
    }

    @ViewBuilder
    private var background: some View {
        if highlighted {
            Color.accentColor.opacity(0.10)
        } else if expanded {
            Color.primary.opacity(0.02)
        } else if hovering {
            Color.primary.opacity(0.05)
        } else {
            Color.clear
        }
    }
}

private struct StepDetail: View {
    let store: RunStore
    let runId: String
    let step: Step

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
            Button {
                store.requestSeek(runId: runId, step: step.seq)
            } label: {
                Label("Show on Screen", systemImage: "play.rectangle")
            }
            .controlSize(.small)
            .help("Open the recording at this step")
        }
    }

    private var dataColumn: some View {
        VStack(alignment: .leading, spacing: Space.m) {
            if let input = step.input {
                block("Input", input.prettyPrinted)
            }
            if let output = step.output {
                // After a tool error the output is what came back before it failed, and
                // its zero values (exitCode 0) are not a result.
                block(step.error != nil ? "Partial output" : "Output", output.prettyPrinted)
                    .help(step.error != nil ? "What came back before the tool call failed. Its values, exitCode 0 included, are not a result." : "")
            }
            Text("\(step.tool)  ·  \(Chrome.stamp(step.at))  ·  took \(Chrome.duration(step.durationMs))")
                .font(.caption.monospaced())
                .foregroundStyle(.secondary)
                .textSelection(.enabled)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    @ViewBuilder
    private var outcomeBanner: some View {
        switch step.outcome {
        case .ok:
            EmptyView()
        case .exit(let code):
            Label("The command ran and exited \(code).", systemImage: "xmark.octagon.fill")
                .font(.callout.weight(.medium))
                .foregroundStyle(Palette.failure)
        case .error(let error):
            VStack(alignment: .leading, spacing: Space.xs) {
                Label("The tool call failed. The command's result is unknown.", systemImage: "exclamationmark.triangle.fill")
                    .font(.callout.weight(.medium))
                    .foregroundStyle(Palette.failure)
                Text(error)
                    .font(.callout.monospaced())
                    .foregroundStyle(Palette.failure)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(Space.s)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Palette.failure.opacity(0.08), in: RoundedRectangle(cornerRadius: Radius.control))
            }
        }
    }

    /// JSON keeps its lines and scrolls sideways; long blocks scroll inside a fixed height.
    private func block(_ title: String, _ text: String) -> some View {
        VStack(alignment: .leading, spacing: Space.xs) {
            Text(title)
                .font(.caption.weight(.medium))
                .foregroundStyle(.secondary)
            let long = text.reduce(0) { $1 == "\n" ? $0 + 1 : $0 } > 14
            ScrollView(long ? [.horizontal, .vertical] : .horizontal) {
                Text(text)
                    .font(.callout.monospaced())
                    .textSelection(.enabled)
                    .fixedSize(horizontal: true, vertical: true)
                    .padding(Space.s)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .frame(height: long ? 240 : nil)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(.fill.quinary, in: RoundedRectangle(cornerRadius: Radius.control))
            .overlay(RoundedRectangle(cornerRadius: Radius.control).strokeBorder(Palette.hairline.opacity(0.6)))
        }
    }
}
