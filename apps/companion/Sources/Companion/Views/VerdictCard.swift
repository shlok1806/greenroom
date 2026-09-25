import SwiftUI

/// The run's current verdict, pinned above the conversation. Its headline is the state
/// and who decided (`VerdictReview`, the same words as the sidebar), then the outcome,
/// the reasons in full enough to read the numbers, what the record says against it, the
/// evidence, and the actions a person has (companion ADR 0002, 0003).
struct VerdictCard: View {
    let store: RunStore
    let runId: String
    let facts: RunFacts
    /// Beside the stage rather than atop the conversation: slimmer.
    var compact = false
    /// The tallest the card may be (`RunLayout.verdictCardMaximum`). Its headline and
    /// actions stay; the reasons and evidence between them scroll.
    var maxHeight: Double?

    @State private var sending = false
    /// The natural height of the card's body, so its scroll view is no taller than it.
    @State private var bodyHeight: CGFloat?
    /// The headline's and the actions' heights: what of `maxHeight` is left for the body.
    @State private var headlineHeight: CGFloat = 0
    @State private var actionsHeight: CGFloat = 0
    @FocusState private var reasonFocused: Bool
    /// The verdict-lands moment this card saw end, so it draws the plain card after it.
    @State private var landed: VerdictMoment?
    @Environment(\.theme) private var theme
    @Environment(\.momentFreeze) private var freeze

    /// Kept by the store: accepting rebuilds this card, and no draft or confirmation may
    /// live in a view that goes away under it. No sheet, alert or dialog in this flow.
    private var draft: VerdictDraft { store.verdictDraft(runId) }

    /// Evidence opened, per run and verdict: never carried to another run or a relaunch.
    private var expanded: Bool { draft.expanded }

    private var reason: Binding<String> {
        Binding(get: { store.verdictDraft(runId).reason },
                set: { text in store.updateVerdictDraft(runId) { $0.reason = text } })
    }

    /// Whose drafts the card holds. A new run or a new verdict must start them empty, so
    /// a reason typed for one verdict cannot be posted against another.
    struct Identity: Hashable {
        let runId: String
        let verdictSeq: Int?
    }

    static func identity(runId: String, verdict: VerdictState?) -> Identity {
        Identity(runId: runId, verdictSeq: verdict?.seq)
    }

    var body: some View {
        if let verdict = facts.verdict {
            card(verdict)
        } else if facts.messageCount > 0, !facts.isAlive || facts.stepCount > 1 {
            none
        }
    }

    // MARK: - No verdict

    private var none: some View {
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            Text("◇ No verdict")
                .monoStyle(.monoMedium, size: TypeScale.monoSmall)
            Text(facts.isAlive
                ? "The verifier proposes one after it checks the task."
                : "This run ended without one.")
                .readingStyle(size: TypeScale.readingSmall)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.tail)
            Spacer(minLength: 0)
        }
        .padding(.horizontal, Space.m)
        .padding(.vertical, Space.s)
        .panel()
    }

    // MARK: - A verdict

    private func card(_ verdict: VerdictState) -> some View {
        let messages = store.messages[runId] ?? []
        let review = VerdictReview.of(verdict, messages: messages, verifierListens: facts.verifierListens)
        let checks = VerdictCheck.checks(verdict, messages: messages, steps: store.steps[runId])
        // Proposed: a dim edge, the outcome in the foreground. Only a verdict a person
        // accepted takes its outcome's colour (ADR 0003); an agent-accepted or rejected one
        // keeps its word, not its colour (`VerdictAppearance`).
        let appearance = VerdictAppearance.of(verdict, review: review)
        let tint = appearance.outcomeInColour ? theme.outcome(verdict.verdict, on: .surface) : theme.foreground
        let words = TranscriptText.clean(text(verdict))
        let reason = MarkdownText.blocks(words, steps: store.stepNumbers(runId))
        let detailed = expanded && !compact
        let moment = landing(verdict)
        let edgeColor = edge(appearance.edge, verdict: verdict)
        return VStack(alignment: .leading, spacing: Space.s) {
            headline(review, verdict: verdict)
                .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { headlineHeight = $0 }
                .layoutPriority(1)
            // Between the headline (with Less) and the actions, so both stay in reach
            // however much evidence is open: the body scrolls once the card is capped.
            ScrollView(.vertical) {
                VStack(alignment: .leading, spacing: Space.s) {
                    outcome(verdict, appearance: appearance, tint: tint, moment: moment)
                    if let note = review.note {
                        Text((review.humanReviewed ? "" : "! ") + note)
                            .readingStyle(size: TypeScale.readingSmall)
                            .foregroundStyle(verdict.status == .accepted && !review.humanReviewed
                                ? theme.color(.attention, on: .surface) : theme.dim(on: .surface))
                    }
                    // Short reasons in full; long ones to three lines until opened. Numbers are
                    // what a reviewer checks, so two lines was never enough. In full, the steps
                    // the reason names are chips that open them, like the evidence below.
                    if detailed || words.count <= 280 {
                        MarkdownView(blocks: reason) { step in seek(.step(step), inSteps: false) }
                    } else {
                        Text(MarkdownText.plain(reason))
                            .readingStyle()
                            .lineLimit(3)
                            .textSelection(.enabled)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    ForEach(checks, id: \.self) { check in
                        Text("! " + check.text)
                            .readingStyle(.readingMedium, size: TypeScale.readingSmall)
                            .foregroundStyle(theme.color(check == .noEvidence ? .failure : .attention, on: .surface))
                    }
                    evidence(verdict, words: words, detailed: detailed)
                    if detailed || verdict.status == .contested {
                        disputes(verdict, messages: messages)
                    }
                    notes(verdict, review: review)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { bodyHeight = $0 }
            }
            .scrollBounceBehavior(.basedOnSize)
            .overlayScrollers()
            // Never taller than what it holds, nor than the card's cap leaves it. Only the
            // body is capped: a capped card frame would stretch to its cap.
            .frame(maxHeight: bodyLimit)
            // Capped: the last visible line fades into the card, so a cut line reads as
            // "more below" and never as text run into what follows.
            .overlay(alignment: .bottom) {
                if let bodyLimit, let bodyHeight, bodyHeight > bodyLimit + 1 {
                    LinearGradient(colors: [theme.surface.opacity(0), theme.surface], startPoint: .top, endPoint: .bottom)
                        .frame(height: Space.l)
                        .allowsHitTesting(false)
                }
            }
            actions(verdict, review: review)
                .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { actionsHeight = $0 }
                // Room for the whole explanation first; the body scrolls in what is left.
                // Not `fixedSize`: laid out narrow for a moment, that text would grow the window.
                .layoutPriority(1)
        }
        .padding(Space.m)
        .frame(maxWidth: .infinity, alignment: .leading)
        // Landing: the edge draws itself (`VerdictBorderDrawLayer`) instead of standing.
        .panel(edge: moment == nil ? edgeColor : .clear)
        .overlay {
            if let moment { VerdictBorderDrawLayer(moment: moment, color: edgeColor) }
        }
        .task(id: moment?.start) {
            guard let moment, freeze == nil else { return }
            let left = VerdictLanding.duration(DesignData.shared.tokens.motion) - Date().timeIntervalSince(moment.start)
            if left > 0 { try? await Task.sleep(for: .seconds(left)) }
            if !Task.isCancelled { landed = moment }
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Verdict, \(review.state), \(Chrome.outcomeTitle(verdict.verdict))")
    }

    /// The body's height: all of it, or what the cap leaves after the headline, the
    /// actions, the padding and the gaps between them.
    private var bodyLimit: CGFloat? {
        let chrome = headlineHeight + actionsHeight + 2 * Space.m + 2 * Space.s
        return RunLayout.verdictBody(natural: bodyHeight.map(Double.init), card: maxHeight, chrome: Double(chrome))
            .map { CGFloat($0) }
    }

    /// State and decider first: that is what decides whether to trust the rest.
    private func headline(_ review: VerdictReview, verdict: VerdictState) -> some View {
        let urgent = !review.humanReviewed && verdict.status != .rejected
        let state = Text((urgent ? "! " : "") + review.state.uppercased())
            .font(Typeface.monoBold.font(size: TypeScale.label))
            .tracking(0.8)
            .foregroundStyle(urgent ? theme.color(.attention, on: .surface) : theme.dim(on: .surface))
            .fixedSize()
        let decision = Text(review.decision)
            .readingStyle(size: TypeScale.small)
            .foregroundStyle(.secondary)
        // On one line while the decision fits whole beside the state; in a narrow column it
        // goes under it, whole, rather than clipped to "you or the coding a...".
        return ViewThatFits(in: .horizontal) {
            HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                state
                decision.lineLimit(1).fixedSize()
                Spacer(minLength: Space.s)
                evidenceToggle
            }
            VStack(alignment: .leading, spacing: Space.xs) {
                HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                    state
                    Spacer(minLength: Space.s)
                    evidenceToggle
                }
                if !review.decision.isEmpty {
                    decision.fixedSize(horizontal: false, vertical: true)
                }
            }
        }
    }

    @ViewBuilder
    private var evidenceToggle: some View {
        if !compact {
            Button(expanded ? "Less ▴" : "Evidence ▾") {
                withAnimation(.snappy(duration: 0.2)) { store.updateVerdictDraft(runId) { $0.expanded.toggle() } }
            }
            .buttonStyle(.textLink)
            .fixedSize()
            .help(expanded ? "Fold the verdict" : "Show the cited steps and any disputes")
        }
    }

    private func edge(_ edge: VerdictAppearance.Edge, verdict: VerdictState) -> Color {
        switch edge {
        case .hairline: theme.hairline
        case .dim: theme.dim
        case .outcome: theme.outcome(verdict.verdict)
        }
    }

    /// The verdict-lands moment for this card's verdict while it plays: nil once it has
    /// ended, under Reduce Motion, and for a card opened on a verdict that was already there.
    private func landing(_ verdict: VerdictState) -> VerdictMoment? {
        guard let moment = store.verdictMoment, moment.runId == runId, moment.seq == verdict.seq,
              moment.plays, moment != landed else { return nil }
        // A card built after the moment ended (a pane shown again) shows the plain card.
        if freeze == nil, Date().timeIntervalSince(moment.start) >= VerdictLanding.duration(DesignData.shared.tokens.motion) {
            return nil
        }
        return moment
    }

    /// The outcome in the mono face, in capitals: the real text, and while a verdict lands,
    /// its decode drawn over it (spec, Signature moments). VoiceOver reads the real text.
    private func outcome(_ verdict: VerdictState, appearance: VerdictAppearance, tint: Color, moment: VerdictMoment?) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Space.m) {
            Text(appearance.outcome)
                .font(Typeface.monoBold.font(size: TypeScale.title))
                .tracking(1.5)
                .foregroundStyle(tint)
                .fixedSize()
                .overlay(alignment: .leading) {
                    if let moment {
                        VerdictDecodeLayer(text: appearance.outcome, moment: moment,
                                           settled: theme.outcome(verdict.verdict, on: .surface),
                                           scrambling: theme.dim(on: .surface), ground: theme.surface)
                    }
                }
                .accessibilityLabel(Chrome.outcomeTitle(verdict.verdict))
            if verdict.status.isOpen {
                Text("proposed by the verifier")
                    .readingStyle(size: TypeScale.readingSmall)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private func text(_ verdict: VerdictState) -> String {
        if let seq = verdict.seq, let message = store.messages[runId]?.first(where: { $0.seq == seq }),
           !message.text.isEmpty {
            return message.text
        }
        return verdict.summary ?? ""
    }

    // MARK: - Evidence

    @ViewBuilder
    private func evidence(_ verdict: VerdictState, words: String, detailed: Bool) -> some View {
        let items = Self.byStep((verdict.evidence ?? []).map(Evidence.parse))
        if !items.isEmpty {
            if detailed {
                // Per step when the words say which step shows what; else once, for the
                // whole verdict, never repeated under every picture.
                let perStep = VerdictCheck.namesSteps(words)
                VStack(alignment: .leading, spacing: Space.m) {
                    if !perStep {
                        ClaimsRow(claims: VerdictCheck.claimedValues(words), label: "It claims")
                    }
                    ForEach(items, id: \.self) { item in
                        EvidenceRow(store: store, runId: runId, item: item,
                                    claims: perStep ? item.step.map { VerdictCheck.claimedValues(words, atStep: $0) } ?? [] : [],
                                    show: { seek(item, inSteps: false) }, record: { seek(item, inSteps: true) })
                    }
                }
            } else {
                HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                    Text("Cites")
                        .readingStyle(size: TypeScale.readingSmall)
                        .foregroundStyle(.secondary)
                    FlowLayout(spacing: Space.xs) {
                        ForEach(items, id: \.self) { item in
                            EvidenceLink(store: store, runId: runId, item: item) { seek(item, inSteps: false) }
                        }
                    }
                }
            }
        }
    }

    /// A step and its screenshot are one piece of evidence; show it once.
    static func byStep(_ items: [Evidence]) -> [Evidence] {
        var seen = Set<Int>()
        var out: [Evidence] = []
        for item in items {
            if let step = item.step {
                guard seen.insert(step).inserted else { continue }
                out.append(.step(step))
                continue
            }
            out.append(item)
        }
        return out
    }

    private func seek(_ item: Evidence, inSteps: Bool) {
        guard let step = item.step else { return }
        store.updateVerdictDraft(runId) { $0.openedEvidence = true }
        store.requestSeek(runId: runId, step: step, fromVerdict: true, inSteps: inSteps)
    }

    // MARK: - Disputes

    /// Each dispute and what the verifier said to it, so the count can be checked.
    @ViewBuilder
    private func disputes(_ verdict: VerdictState, messages: [Message]) -> some View {
        let history = DisputeRecord.history(for: verdict, in: messages)
        if !history.isEmpty {
            VStack(alignment: .leading, spacing: Space.s) {
                SectionLabel(title: history.count == 1 ? "1 dispute" : "\(history.count) disputes")
                ForEach(Array(history.enumerated()), id: \.offset) { _, record in
                    VStack(alignment: .leading, spacing: 2) {
                        Text("\(record.dispute.from.displayName) disputed at \(Chrome.shortTime(record.dispute.at))")
                            .monoStyle(.monoMedium, size: TypeScale.monoSmall)
                        Text(TranscriptText.clean(record.dispute.text))
                            .readingStyle(size: TypeScale.readingSmall)
                            .lineLimit(2)
                        if let answer = record.answer {
                            Text("Verifier answered with \(answer.kind == .verdict ? "a \(Chrome.outcomeTitle(answer.verdict)) verdict" : "a \(answer.kind.text)"): \(TranscriptText.clean(answer.text))")
                                .readingStyle(size: TypeScale.readingSmall)
                                .foregroundStyle(.secondary)
                                .lineLimit(2)
                        } else {
                            Text("! No answer from the verifier yet")
                                .readingStyle(size: TypeScale.readingSmall)
                                .foregroundStyle(theme.color(.attention, on: .surface))
                        }
                    }
                    .padding(.leading, Space.s)
                    .overlay(alignment: .leading) {
                        Rectangle().fill(theme.hairline).frame(width: 2)
                    }
                }
            }
        }
    }

    // MARK: - Actions

    @ViewBuilder
    private func actions(_ verdict: VerdictState, review: VerdictReview) -> some View {
        let outcome = Chrome.outcomeTitle(verdict.verdict)
        let unreviewed = verdict.status == .accepted && !review.humanReviewed
        let draft = draft
        let newer = VerdictReview.newerTask(than: verdict.seq, in: store.messages[runId] ?? [])
        if let held = store.heldVerdictChoice(runId) {
            HeldChoice(choice: held, outcome: outcome, window: store.verdictUndo) { store.undoVerdictChoice() }
                .padding(.top, Space.xs)
        } else if verdict.status.isOpen || unreviewed {
            VStack(alignment: .leading, spacing: Space.s) {
                if let action = draft.action {
                    reasonForm(action: action, outcome: outcome)
                } else if draft.confirmingAccept, verdict.status.isOpen {
                    acceptConfirmation(outcome: outcome)
                } else {
                    // Mac order: the primary action last, on the right, apart from the other.
                    HStack(spacing: Space.s) {
                        if unreviewed {
                            Spacer(minLength: 0)
                            Button("Ask for a Re-check...") { store.updateVerdictDraft(runId) { $0.action = .recheck } }
                                .disabled(!facts.verifierListens || newer != nil)
                                .help(newer != nil
                                    ? "A newer task is open. Its verdict will replace this one."
                                    : facts.verifierListens
                                    ? "Ask the verifier to look again, with your reason"
                                    : "The verifier stopped with the machine")
                        } else {
                            Button("Reject...") { store.updateVerdictDraft(runId) { $0.action = .reject } }
                                .disabled(sending)
                                .help("Dispute it with your reason (\(ActionRegistry.label(.dispute))). "
                                    + (verdict.status == .proposed ? "After that, only you can close its verdicts. " : "")
                                    + "You have \(Self.undoSeconds) s to undo.")
                            Spacer(minLength: Space.l)
                            Button("Accept \(outcome)") { run { await store.requestAccept(runId: runId) } }
                            .buttonStyle(.primary)
                            .disabled(sending)
                            .help("Close it as \(outcome) (\(ActionRegistry.label(.accept))). You have \(Self.undoSeconds) s to undo.")
                        }
                    }
                }
            }
            .padding(.top, Space.xs)
        } else if let result = VerdictAppearance.result(verdict, review: review) {
            // The Approval Card's result, where the actions were once the choice is made.
            Text(result)
                .readingStyle(.readingMedium, size: TypeScale.readingSmall)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.top, Space.s)
                .overlay(alignment: .top) { Hairline() }
        }
    }

    /// What the actions below mean, read before them: inside the scrolling body, so a short
    /// column scrolls these lines rather than squeezing them over the reasons.
    @ViewBuilder
    private func notes(_ verdict: VerdictState, review: VerdictReview) -> some View {
        let unreviewed = verdict.status == .accepted && !review.humanReviewed
        let newer = VerdictReview.newerTask(than: verdict.seq, in: store.messages[runId] ?? [])
        if let newer {
            Text("! " + VerdictReview.staleNote(newerTask: newer, verifierListens: facts.verifierListens))
                .readingStyle(size: TypeScale.readingSmall)
                .foregroundStyle(theme.color(.attention, on: .surface))
                .fixedSize(horizontal: false, vertical: true)
        }
        let draft = draft
        if store.heldVerdictChoice(runId) == nil, verdict.status.isOpen || unreviewed,
           draft.action == nil, !(draft.confirmingAccept && verdict.status.isOpen) {
            Text(VerdictReview.explanation(verdict, unreviewed: unreviewed,
                                           verifierListens: facts.verifierListens, alive: facts.isAlive))
                .readingStyle(size: TypeScale.readingSmall)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    /// Asked in the card, not in a dialog: accepting redraws the card, and a sheet whose
    /// presenter goes away leaves the window unable to take a click.
    private func acceptConfirmation(outcome: String) -> some View {
        VStack(alignment: .leading, spacing: Space.s) {
            Text("! You have not opened any cited step. Accepting is final.")
                .readingStyle(size: TypeScale.readingSmall)
                .foregroundStyle(theme.color(.attention, on: .surface))
                .fixedSize(horizontal: false, vertical: true)
            HStack {
                Button("Cancel") { store.updateVerdictDraft(runId) { $0.confirmingAccept = false } }
                Spacer()
                Button("Accept \(outcome) anyway") { run { await store.holdAccept(runId: runId) } }
                    .buttonStyle(.primary)
                    .disabled(sending)
            }
        }
    }

    private func reasonForm(action: VerdictDraft.Action, outcome: String) -> some View {
        VStack(alignment: .leading, spacing: Space.s) {
            TextField(action == .reject ? "Why is it wrong? Cite a step." : "What should the verifier check again?",
                      text: reason, axis: .vertical)
                .lineLimit(2...6)
                .textFieldStyle(.plain)
                .font(Typeface.readingRegular.font(size: TypeScale.readingSmall))
                .focused($reasonFocused)
                .padding(Space.s)
                .fieldFrame(focused: reasonFocused, radius: Radius.sm)
                .onAppear { reasonFocused = true }
                // esc in the reason is the form's Cancel.
                .typingField(focused: reasonFocused, sends: false) { store.updateVerdictDraft(runId) { $0.action = nil } }
            HStack {
                Button("Cancel") { store.updateVerdictDraft(runId) { $0.action = nil } }
                    .help("Keep the verdict open (\(ActionRegistry.label(.leave)))")
                Spacer()
                Button(action == .reject ? "Reject \(outcome)" : "Send Re-check") {
                    run { await store.submitVerdictAction(runId: runId) }
                }
                .buttonStyle(.primary)
                .disabled(sending || draft.reason.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
    }

    static var undoSeconds: Int { Int(UndoWindow<PendingVerdictChoice>.length.rounded()) }

    private func run(_ work: @escaping @MainActor () async -> Void) {
        sending = true
        Task {
            await work()
            sending = false
        }
    }
}

/// An accept or dispute shown as made while it waits out its undo: nothing has reached
/// the daemon yet, and Undo takes it back (ADR 0005).
private struct HeldChoice: View {
    let choice: PendingVerdictChoice
    let outcome: String
    let window: UndoWindow<PendingVerdictChoice>
    let undo: () -> Void

    @Environment(\.theme) private var theme

    var body: some View {
        TimelineView(.periodic(from: .now, by: 0.25)) { tick in
            let seconds = window.seconds(now: tick.date) ?? 0
            HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(title)
                        .readingStyle(.readingSemiBold, size: TypeScale.readingSmall)
                        .foregroundStyle(theme.color(.attention, on: .surface))
                    Text(seconds > 0 ? "Sends in \(seconds) s" : "Sending")
                        .readingStyle(size: TypeScale.small)
                        .foregroundStyle(.secondary)
                        .monospacedDigit()
                }
                Spacer(minLength: Space.s)
                Button("Undo", action: undo)
                    .help("Take it back (\(ActionRegistry.label(.undo)))")
            }
        }
        .accessibilityElement(children: .combine)
    }

    private var title: String {
        switch choice.kind {
        case .accept: "Accepting \(outcome)"
        case .dispute: "Disputing \(outcome)"
        }
    }
}

// MARK: - Evidence views

/// A cited step as a link: accent, arrow, a picture of the step on hover.
struct EvidenceLink: View {
    let store: RunStore
    let runId: String
    let item: Evidence
    let open: () -> Void

    @State private var hovering = false
    @State private var preview = false
    @Environment(\.theme) private var theme

    var body: some View {
        if let step = item.step {
            Button(action: open) {
                Text("\(item.label) ↗")
                    .underline(hovering)
                    .font(Typeface.monoMedium.font(size: TypeScale.monoSmall))
                    .foregroundStyle(theme.foreground)
                    .padding(.horizontal, Space.s)
                    .padding(.vertical, 2)
                    .background(hovering ? theme.highlight : theme.background,
                                in: RoundedRectangle(cornerRadius: Radius.sm, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: Radius.sm, style: .continuous).strokeBorder(theme.hairline))
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .onHover { inside in
                hovering = inside
                Task {
                    try? await Task.sleep(for: .milliseconds(450))
                    preview = hovering
                }
            }
            .popover(isPresented: $preview, arrowEdge: .bottom) {
                StepThumbnail(store: store, runId: runId, step: step)
                    .frame(width: 320, height: 240)
                    .padding(Space.s)
            }
            .help("Show step \(step) on the screen")
        } else {
            Chip(text: item.label)
                .textSelection(.enabled)
        }
    }
}

/// A cited step in full: its picture at a size the numbers can be read, what the verdict
/// claims to see there beside it, what the tool returned, and the way into its record.
private struct EvidenceRow: View {
    let store: RunStore
    let runId: String
    let item: Evidence
    let claims: [String]
    let show: () -> Void
    let record: () -> Void

    @Environment(\.theme) private var theme

    private var step: Step? {
        guard let number = item.step else { return nil }
        return store.steps[runId]?.first { $0.seq == number }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Space.s) {
            HStack(spacing: Space.s) {
                if let number = item.step {
                    EvidenceLink(store: store, runId: runId, item: .step(number), open: show)
                }
                if let step {
                    Text(StepSummary.phrase(for: step, in: store.steps[runId] ?? []) + outcomeText(step))
                        .monoStyle(.monoMedium, size: TypeScale.small)
                        .foregroundStyle(step.outcome.isFailure ? theme.color(.failure, on: .surface) : theme.foreground)
                        .lineLimit(1)
                } else if item.step != nil {
                    Text("✗ Not in the record")
                        .monoStyle(size: TypeScale.small)
                        .foregroundStyle(theme.color(.failure, on: .surface))
                } else {
                    Text(item.label).readingStyle(size: TypeScale.readingSmall)
                }
                Spacer(minLength: 0)
                if item.step != nil {
                    Button("Step Record", action: record)
                        .buttonStyle(.textLink)
                        .help("Open this step's input and output in Steps")
                }
            }
            if let number = item.step {
                Button(action: show) {
                    // Big enough to read the values the verdict claims.
                    StepThumbnail(store: store, runId: runId, step: number)
                        .frame(maxWidth: .infinity)
                        .frame(height: 240)
                }
                .buttonStyle(.plain)
                .help("Show step \(number) on the screen, full size")
            }
            ClaimsRow(claims: claims, label: "It claims here")
            if let step {
                Text(StepExcerpt.text(step))
                    .monoStyle(size: TypeScale.monoSmall)
                    .foregroundStyle(.secondary)
                    .lineLimit(3)
                    .textSelection(.enabled)
            }
        }
    }

    private func outcomeText(_ step: Step) -> String {
        switch step.outcome {
        case .ok: ""
        case .exit(let code): ", exited \(code)"
        case .error: ", failed"
        }
    }
}

/// Values the verdict's words say are on screen, as chips; nothing when there are none.
private struct ClaimsRow: View {
    let claims: [String]
    let label: String

    var body: some View {
        if !claims.isEmpty {
            HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                SectionLabel(title: label)
                FlowLayout(spacing: Space.xs) {
                    ForEach(claims, id: \.self) { value in
                        Chip(text: value)
                    }
                }
            }
            .help("Values the verdict says are on screen. Check them against the picture.")
        }
    }
}

/// What a step returned, in a few lines: stdout for a command, the file for a screenshot.
enum StepExcerpt {
    static func text(_ step: Step) -> String {
        if case .error(let error) = step.outcome { return error }
        if let stdout = step.output?["stdout"]?.stringValue, !stdout.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return stdout.trimmingCharacters(in: .whitespacesAndNewlines)
        }
        let summary = StepSummary.line(for: step)
        return summary.isEmpty ? step.tool : summary
    }
}

/// The picture of a step: its own screenshot, else the recording's frame there.
struct StepThumbnail: View {
    let store: RunStore
    let runId: String
    let step: Int

    @State private var image: NSImage?
    @State private var loaded = false
    @Environment(\.theme) private var theme

    var body: some View {
        ZStack {
            RoundedRectangle(cornerRadius: Radius.md, style: .continuous).fill(theme.well)
            if let image {
                Image(nsImage: image)
                    .resizable()
                    .interpolation(.medium)
                    .scaledToFit()
            } else {
                // The well is dark in every theme, so its words take the dark theme's dim.
                Group {
                    if loaded { Text("no picture") } else { Spinner(size: TypeScale.monoSmall) }
                }
                .monoStyle(size: TypeScale.monoSmall)
                .foregroundStyle(theme.wellDim)
            }
        }
        .clipShape(RoundedRectangle(cornerRadius: Radius.md, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Radius.md, style: .continuous).strokeBorder(theme.hairline))
        // Again once the steps or frames arrive: a thumbnail drawn first has neither.
        .task(id: "\(step)-\(store.steps[runId]?.count ?? -1)-\(store.frames[runId]?.count ?? -1)") {
            guard image == nil else { return }
            image = await load()
            loaded = store.steps[runId] != nil
        }
    }

    private func load() async -> NSImage? {
        if let record = store.steps[runId]?.first(where: { $0.seq == step }), let name = record.screenshotArtifact {
            return await store.artifactImage(runId: runId, name: name)
        }
        let frames = store.frames[runId] ?? []
        guard let index = FrameTimeline.index(ofStep: step, in: frames) else { return nil }
        return await store.frameImage(runId: runId, file: frames[index].file)
    }
}

/// Lays children out left to right, wrapping to a new line when the width runs out.
struct FlowLayout: Layout {
    var spacing: CGFloat = 4

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let rows = arrange(width: proposal.width ?? .infinity, subviews: subviews)
        let height = rows.last.map { $0.y + $0.height } ?? 0
        let width = rows.map(\.width).max() ?? 0
        return CGSize(width: proposal.width ?? width, height: height)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let rows = arrange(width: bounds.width, subviews: subviews)
        for row in rows {
            var x = bounds.minX
            for index in row.indices {
                let size = subviews[index].sizeThatFits(.unspecified)
                let fitted = CGSize(width: min(size.width, bounds.width), height: size.height)
                subviews[index].place(at: CGPoint(x: x, y: bounds.minY + row.y), proposal: ProposedViewSize(fitted))
                x += fitted.width + spacing
            }
        }
    }

    private struct Row {
        var indices: [Int] = []
        var y: CGFloat = 0
        var width: CGFloat = 0
        var height: CGFloat = 0
    }

    private func arrange(width: CGFloat, subviews: Subviews) -> [Row] {
        var rows: [Row] = []
        var current = Row()
        for index in subviews.indices {
            let size = subviews[index].sizeThatFits(.unspecified)
            let needed = current.indices.isEmpty ? size.width : current.width + spacing + size.width
            if needed > width, !current.indices.isEmpty {
                rows.append(current)
                current = Row(y: current.y + current.height + spacing)
            }
            current.width = current.indices.isEmpty ? min(size.width, width) : current.width + spacing + size.width
            current.height = max(current.height, size.height)
            current.indices.append(index)
        }
        if !current.indices.isEmpty { rows.append(current) }
        return rows
    }
}
