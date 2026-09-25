import AppKit
import SwiftUI

/// Every tool call the run recorded, read as a thinking trace (`StepTrace`, after Beautiful
/// UI's ThinkingState): a row per step in plain words, a muted check once it is done, `✗`
/// and the word when it errored, and while the verifier works a last row that ticks. A
/// row opens (`⏎`) to show the screen at that step, what went in and what came out. The
/// step a person was sent to is highlighted and open; the one at the playhead is marked.
/// New rows settle in; under Reduce Motion they appear at once.
struct StepsView: View {
    let store: RunStore
    let runId: String
    /// Has the keyboard: its label takes the brand.
    var focused = false
    /// Folds the list back to its one-row track (medium and narrow windows), when it can.
    var collapse: (() -> Void)?
    /// Opened from the verdict, the way back; off beside the screen, whose bar has it.
    var showsEvidenceBar = true
    /// The step at the screen's playhead, beside the list (a wide window): its row is
    /// marked and kept in view, so the list and the picture show the same moment.
    var playhead: Int?
    /// Takes SwiftUI focus when it appears, so no text field takes the keyboard; off
    /// beside the screen, which does that already (two claims cancel out).
    var claimsFocus = true

    @State private var expanded: Set<Step> = []
    @AppStorage("stepsErrorsOnly", store: AppDefaults.shared) private var errorsOnly = false
    /// While live, keep the newest step in view.
    @State private var following = true
    /// The keyboard's row: `j` and `k` move it, `⏎` opens it.
    @State private var cursor: Step?
    /// The person moved through the list themselves: it stops following the playhead.
    @State private var browsing = false
    @FocusState private var hasFocus: Bool
    @Environment(\.keyboard) private var keyboard
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var allSteps: [Step] { store.steps[runId] ?? [] }
    private var steps: [Step] { errorsOnly ? allSteps.filter { $0.outcome.isFailure } : allSteps }
    private var focusedStep: Int? {
        guard let request = store.focusedStep, request.runId == runId else { return nil }
        return request.step
    }

    private var facts: RunFacts { store.facts(runId) }

    @AppStorage("showsConversation", store: AppDefaults.shared) private var showsConversation = true

    private var fromVerdict: Bool { store.focusedStep?.fromVerdict == true && store.focusedStep?.runId == runId }

    var body: some View {
        VStack(spacing: 0) {
            if showsEvidenceBar, fromVerdict, let focusedStep {
                EvidenceBar(step: focusedStep, back: backToVerdict, record: nil)
                .padding(.bottom, Space.s)
                Hairline()
            }
            table
        }
    }

    private func backToVerdict() {
        showsConversation = true
        store.clearFocus()
    }

    /// The cursor shows only while the steps have the keyboard: it mirrors real focus.
    private var cursorShown: Bool { keyboard?.pane == .stage && keyboard?.stage == .steps }

    private var offered: Set<ActionID> {
        var ids: Set<ActionID> = []
        if fromVerdict { ids.insert(.backToVerdict) }
        if !steps.isEmpty { ids.formUnion([.moveDown, .moveUp, .latest]) }
        if cursor != nil { ids.insert(.open) }
        return ids
    }

    private func perform(_ id: ActionID, proxy: ScrollViewProxy) {
        switch id {
        case .moveDown, .moveUp:
            guard !steps.isEmpty else { return }
            let delta = id == .moveDown ? 1 : -1
            let at = cursor.flatMap { steps.firstIndex(of: $0) }
            let next = at.map { min(max($0 + delta, 0), steps.count - 1) } ?? (delta > 0 ? 0 : steps.count - 1)
            cursor = steps[next]
            following = false
            browsing = true
            proxy.scrollTo(steps[next])
        case .open:
            guard let cursor else { return }
            withAnimation(.snappy(duration: 0.18)) {
                if expanded.contains(cursor) { expanded.remove(cursor) } else { expanded.insert(cursor) }
            }
        case .latest:
            guard let last = steps.last else { return }
            cursor = last
            following = facts.isAlive
            withAnimation { proxy.scrollTo(last, anchor: .bottom) }
        case .backToVerdict:
            backToVerdict()
        default:
            break
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
                                cursor: cursorShown && step == cursor,
                                atPlayhead: step.seq == playhead,
                                expanded: Binding(
                                    get: { expanded.contains(step) },
                                    set: { open in
                                        if open { expanded.insert(step) } else { expanded.remove(step) }
                                    }
                                )
                            )
                            .transition(.asymmetric(insertion: .opacity.combined(with: .offset(y: Space.s)), removal: .opacity))
                        }
                        if let working = StepTrace.working(facts) {
                            RunningRow(working: working)
                                .id(RunningRow.id)
                                .transition(.opacity)
                        }
                    } header: {
                        StepsHeader(
                            count: allSteps.count,
                            failures: facts.failures.count,
                            errorsOnly: $errorsOnly,
                            following: facts.isAlive ? $following : nil,
                            focused: focused,
                            collapse: collapse
                        )
                    }
                }
                // New steps settle in (ADR 0006 `settle`); instant under Reduce Motion.
                .animation(PaneMotion.settle(reduceMotion: reduceMotion), value: allSteps.count)
                .animation(PaneMotion.settle(reduceMotion: reduceMotion), value: StepTrace.working(facts) != nil)
            }
            .overlayScrollers()
            .overlay {
                if allSteps.isEmpty, StepTrace.working(facts) == nil {
                    QuietEmpty(title: "No steps yet", message: facts.isAlive
                        ? "Each tool call on the machine adds a step."
                        : "This run made no tool calls on its machine.")
                } else if steps.isEmpty {
                    QuietEmpty(title: "No failed steps", message: "Every tool call succeeded.")
                }
            }
            .onAppear {
                // The stage, not the composer, takes the keyboard when Steps opens.
                if claimsFocus { hasFocus = true }
                if let focusedStep {
                    reveal(focusedStep, proxy: proxy)
                } else if playhead != nil, !facts.isAlive {
                    // Opened beside the picture: at the step it shows.
                    showPlayhead(proxy)
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
            // The list follows the picture until the person moves through it themselves.
            .onChange(of: playhead) { showPlayhead(proxy) }
            .onChange(of: allSteps.count) {
                guard following, facts.isAlive, let last = steps.last else { return }
                // The row's identity in the lazy stack is the `Step`, not its seq.
                withAnimation { proxy.scrollTo(last, anchor: .bottom) }
                // The ticking row under it, once the lazy stack has built it.
                guard StepTrace.working(facts) != nil else { return }
                Task {
                    try? await Task.sleep(for: .milliseconds(60))
                    withAnimation { proxy.scrollTo(RunningRow.id, anchor: .bottom) }
                }
            }
            .offersActions(.steps, offered, refresh: runId) { perform($0, proxy: proxy) }
        }
        .focusable()
        .focusEffectDisabled()
        .focused($hasFocus)
    }

    /// Keeps the playhead's step in view, the keyboard's row on it, until the person moves
    /// through the list themselves or a step was asked for.
    private func showPlayhead(_ proxy: ScrollViewProxy) {
        guard !browsing, focusedStep == nil, let playhead,
              let step = steps.first(where: { $0.seq == playhead }) else { return }
        cursor = step
        // As `reveal`: bring the row in, then place it once it has laid out.
        proxy.scrollTo(step, anchor: .center)
        Task {
            try? await Task.sleep(for: .milliseconds(120))
            proxy.scrollTo(step, anchor: .center)
        }
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
    static let seq: CGFloat = 32
    static let glyph: CGFloat = 14
    static let duration: CGFloat = 64
    static let gap: CGFloat = Space.s
    static let horizontal: CGFloat = Space.l
    static let detailInset: CGFloat = horizontal + seq + gap + glyph + gap
}

private struct StepsHeader: View {
    let count: Int
    let failures: Int
    @Binding var errorsOnly: Bool
    let following: Binding<Bool>?
    var focused = false
    var collapse: (() -> Void)?

    @Environment(\.theme) private var theme

    var body: some View {
        HStack(spacing: Space.l) {
            SectionLabel(title: Chrome.plural(count, "step"), ink: focused ? theme.brandInk(on: .background) : nil)
            Spacer(minLength: Space.s)
            if let following {
                Toggle("Follow newest", isOn: following)
                    .help("Keep the newest step in view")
            }
            Toggle(failures > 0 ? "Only the \(failures) that errored" : "Only errors", isOn: $errorsOnly)
                .disabled(failures == 0 && !errorsOnly)
                .help("Show only failed tool calls and non-zero exits")
            if let collapse {
                Button("Fold ⌃", action: collapse)
                    .buttonStyle(.textLink)
                    .help("Fold the steps to one row (\(ActionRegistry.label(.goScreen)))")
            }
        }
        .padding(.horizontal, StepColumn.horizontal)
        .padding(.vertical, Space.s)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(theme.background)
        .overlay(alignment: .bottom) { Hairline() }
    }
}

/// One step in plain words (ADR 0008): its number, a glyph for how it went (a muted
/// check, or `✗` in the failure role), what it did as a sentence, and at most two facts
/// (the failure's word, how long it took). The tool, its time and its raw input and
/// output are one `⏎` away.
private struct StepRow: View {
    let store: RunStore
    let runId: String
    let step: Step
    let highlighted: Bool
    /// The keyboard's row: the brand cursor at its leading edge.
    var cursor = false
    /// The step the screen is showing: its number in the foreground, a quiet edge.
    var atPlayhead = false
    @Binding var expanded: Bool

    @State private var hovering = false
    @Environment(\.theme) private var theme

    private var row: StepTrace.Row {
        StepTrace.row(for: step, in: store.steps[runId] ?? [], playhead: atPlayhead ? step.seq : nil)
    }

    var body: some View {
        let row = row
        VStack(alignment: .leading, spacing: 0) {
            Button {
                withAnimation(.snappy(duration: 0.18)) { expanded.toggle() }
            } label: {
                summaryLine(row)
            }
            .buttonStyle(.plain)
            .id(step.seq)
            .help(expanded ? "Hide this step" : "Show this step's screen, input and output")
            .accessibilityLabel("Step \(row.seq), \(StepTrace.spoken(row.state)): \(row.words)")
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
            if highlighted || cursor {
                Rectangle().fill(theme.brand).frame(width: 3)
            } else if atPlayhead {
                Rectangle().fill(theme.foreground.opacity(0.55)).frame(width: 2)
            }
        }
        .overlay(alignment: .bottom) { Hairline() }
        .onHover { hovering = $0 }
    }

    private func summaryLine(_ row: StepTrace.Row) -> some View {
        HStack(spacing: StepColumn.gap) {
            Text("\(row.seq)")
                .monospacedDigit()
                .fontWeight(atPlayhead ? .bold : nil)
                .foregroundStyle(atPlayhead ? AnyShapeStyle(theme.foreground) : AnyShapeStyle(.secondary))
                .frame(width: StepColumn.seq, alignment: .trailing)

            Text(StepTrace.glyph(row.state) ?? "")
                .foregroundStyle(row.failed ? theme.color(.failure) : theme.dim)
                .frame(width: StepColumn.glyph)

            HStack(spacing: Space.s) {
                if row.risky {
                    Text("!")
                        .fontWeight(.bold)
                        .foregroundStyle(theme.color(.attention))
                        .help("This command can destroy data or change the machine for good")
                        .accessibilityLabel("Risky command")
                }
                Text(row.words)
                    .foregroundStyle(row.failed ? theme.color(.failure) : theme.foreground)
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .help(detailHelp)
                if case .failed(let word) = row.state {
                    Text(word)
                        .font(Typeface.monoMedium.font(size: TypeScale.monoSmall))
                        .foregroundStyle(theme.color(.failure))
                        .fixedSize()
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)

            Text(row.duration)
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
        } else if hovering || expanded || cursor || atPlayhead {
            theme.highlight
        } else {
            Color.clear
        }
    }
}

/// The trace's last row while the verifier works on its next call: the tick where the
/// glyph goes, who is working in words, and how long since the last step or message.
private struct RunningRow: View {
    let working: StepTrace.Working

    static let id = "running"

    @Environment(\.theme) private var theme

    var body: some View {
        HStack(spacing: StepColumn.gap) {
            Color.clear.frame(width: StepColumn.seq, height: 1)
            Spinner()
                .foregroundStyle(theme.foreground)
                .frame(width: StepColumn.glyph)
            Text(working.words)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .frame(maxWidth: .infinity, alignment: .leading)
            TimelineView(.periodic(from: working.since, by: 1)) { context in
                Text(LoaderMotion.elapsed(context.date.timeIntervalSince(working.since)))
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
            }
            .frame(width: StepColumn.duration, alignment: .trailing)
        }
        .monoStyle()
        .padding(.horizontal, StepColumn.horizontal)
        .padding(.vertical, Space.s)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("\(working.words), for \(LoaderMotion.spokenElapsed(Date().timeIntervalSince(working.since)))")
        .accessibilityAddTraits(.updatesFrequently)
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
                    .help(step.error != nil ? "What came back before the call failed. These values, exitCode 0 too, are not a result." : "")
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
            Text("✗ The command exited \(code).")
                .readingStyle(.readingMedium, size: TypeScale.readingSmall)
                .foregroundStyle(theme.color(.failure))
        case .error(let error):
            VStack(alignment: .leading, spacing: Space.xs) {
                Text("✗ The tool call failed. Its result is unknown.")
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

// MARK: - The one-row track

/// The steps folded to one row under the screen (medium and narrow windows, ADR 0004
/// decision 8): a cell per step in the trace's states (done dim, errored in the failure
/// role, the tick in a last cell while the verifier works), the step at the screen's
/// playhead drawn full height in the foreground, and that step in words beside it. A
/// click on a cell shows its step on the screen; hovering names it; "All Steps" opens
/// the list (`g s`).
struct StepsTrack: View {
    let store: RunStore
    let runId: String
    /// The step at the screen's playhead (`PlayheadStepKey`).
    let playhead: Int?
    let expand: () -> Void

    @State private var hovered: Int?
    @Environment(\.theme) private var theme

    /// The widest a cell grows, so one step is not a bar across the stage.
    private static let maxPitch: Double = 10

    private var steps: [Step] { store.steps[runId] ?? [] }

    var body: some View {
        let steps = steps
        let working = StepTrace.working(store.facts(runId))
        HStack(spacing: Space.m) {
            SectionLabel(title: Chrome.plural(steps.count, "step"))
                .fixedSize()
            if !steps.isEmpty || working != nil {
                HStack(spacing: Space.xs) {
                    cells(steps)
                        .frame(height: 16)
                        .frame(minWidth: 80)
                    // The running cell: the tick after the last step (ADR 0006 `tick`).
                    if working != nil {
                        Spinner(size: TypeScale.small)
                            .foregroundStyle(theme.foreground)
                            .fixedSize()
                    }
                }
                // The words first: the cells take whatever is left.
                label(steps, working: working)
                    .frame(minWidth: 120, idealWidth: 240, maxWidth: 260, alignment: .leading)
                    .layoutPriority(1)
            } else {
                Spacer(minLength: 0)
            }
            Button("All Steps ⌄", action: expand)
                .buttonStyle(.textLink)
                .fixedSize()
                .help("Show all steps (\(ActionRegistry.label(.goSteps)))")
        }
        .padding(.horizontal, Space.l)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .overlay(alignment: .top) { Hairline() }
    }

    /// The hovered step, else the one at the playhead: its number and what it did. With
    /// neither, while the verifier works, who is working.
    @ViewBuilder
    private func label(_ steps: [Step], working: StepTrace.Working?) -> some View {
        let seq = hoveredSeq(steps) ?? playhead
        if let seq, let step = steps.first(where: { $0.seq == seq }) {
            let row = StepTrace.row(for: step, in: steps, playhead: playhead)
            HStack(spacing: Space.s) {
                Text("\(row.seq)")
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
                    .fixedSize()
                Text(row.words)
                    .foregroundStyle(row.failed ? theme.color(.failure) : theme.foreground)
                    .lineLimit(1)
                    .truncationMode(.tail)
                if case .failed(let word) = row.state {
                    Text(word)
                        .foregroundStyle(theme.color(.failure))
                        .fixedSize()
                }
            }
            .monoStyle(size: TypeScale.small)
        } else if let working {
            Text(working.words)
                .monoStyle(size: TypeScale.small)
                .foregroundStyle(.secondary)
                .lineLimit(1)
        } else {
            Color.clear.frame(height: 1)
        }
    }

    private func cells(_ steps: [Step]) -> some View {
        GeometryReader { geometry in
            let width = geometry.size.width
            let height = geometry.size.height
            let current = hoveredSeq(steps) ?? playhead
            let cells = StepTrace.cells(steps, current: current, working: false)
            // A few steps keep cells of a step's size, from the left; many share the width.
            let pitch = min(width / Double(max(cells.count, 1)), Self.maxPitch)
            // A gap between cells while there is room for one.
            let gap: Double = pitch >= 4 ? 1 : 0
            ZStack(alignment: .leading) {
                Canvas { context, _ in
                    for (index, cell) in cells.enumerated() {
                        let cellHeight = cell.current ? height : height * 0.55
                        let rect = CGRect(x: Double(index) * pitch, y: (height - cellHeight) / 2,
                                          width: max(pitch - gap, 1), height: cellHeight)
                        let ink: Color
                        switch cell.state {
                        case _ where cell.current: ink = theme.foreground
                        case .failed: ink = theme.color(.failure)
                        case .done: ink = theme.dim.opacity(0.55)
                        case .running: continue
                        }
                        context.fill(Path(roundedRect: rect, cornerRadius: min(1, rect.width / 2)), with: .color(ink))
                    }
                }
            }
            .contentShape(Rectangle())
            .onContinuousHover { phase in
                switch phase {
                case .active(let point): hovered = index(at: point.x, pitch: pitch, count: steps.count)
                case .ended: hovered = nil
                }
            }
            .gesture(
                SpatialTapGesture().onEnded { value in
                    guard let at = index(at: value.location.x, pitch: pitch, count: steps.count) else { return }
                    store.requestSeek(runId: runId, step: steps[at].seq)
                }
            )
        }
        .accessibilityElement()
        .accessibilityLabel("Steps")
        .accessibilityValue(playhead.map { "at step \($0) of \(steps.count)" } ?? "\(steps.count) steps")
        .help("One cell per step. Red ones errored. Click one to show it.")
    }

    private func hoveredSeq(_ steps: [Step]) -> Int? {
        hovered.flatMap { steps.indices.contains($0) ? steps[$0].seq : nil }
    }

    /// The step under `x`; past the last step (the running cell) is none.
    private func index(at x: Double, pitch: Double, count: Int) -> Int? {
        guard count > 0, pitch > 0 else { return nil }
        let at = Int(x / pitch)
        return at < count ? max(at, 0) : nil
    }
}
