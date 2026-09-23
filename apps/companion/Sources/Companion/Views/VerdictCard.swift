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

    @AppStorage("verdictCardExpanded") private var expanded = false
    @State private var acting: Action?
    @State private var reason = ""
    @State private var sending = false
    @State private var confirmingAccept = false
    /// Accepting without having looked asks first.
    @State private var openedEvidence = false
    @FocusState private var reasonFocused: Bool

    private enum Action { case reject, recheck }

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
        HStack(spacing: Space.s) {
            Image(systemName: "seal")
                .foregroundStyle(.tertiary)
            Text("No verdict")
                .font(.headline)
                .foregroundStyle(.secondary)
            Text(facts.isAlive
                ? "The verifier proposes one when it has checked the task."
                : "This run ended without one.")
                .font(.callout)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.tail)
            Spacer(minLength: 0)
        }
        .padding(.horizontal, Space.m)
        .padding(.vertical, Space.s)
        .background(.fill.quinary, in: RoundedRectangle(cornerRadius: Radius.card, style: .continuous))
    }

    // MARK: - A verdict

    private func card(_ verdict: VerdictState) -> some View {
        let messages = store.messages[runId] ?? []
        let review = VerdictReview.of(verdict, messages: messages)
        let checks = VerdictCheck.checks(verdict, messages: messages, steps: store.steps[runId])
        let tint = Palette.outcome(verdict.verdict)
        let words = TranscriptText.clean(text(verdict))
        let detailed = expanded && !compact
        return VStack(alignment: .leading, spacing: Space.s) {
            headline(review, verdict: verdict)
            outcome(verdict, tint: tint)
            if let note = review.note {
                Label(note, systemImage: review.humanReviewed ? "info.circle" : "person.crop.circle.badge.questionmark")
                    .font(.callout)
                    .foregroundStyle(verdict.status == .accepted && !review.humanReviewed ? Palette.attention : .secondary)
            }
            // Short reasons in full; long ones to three lines until opened. Numbers are
            // what a reviewer checks, so two lines was never enough.
            Text(words)
                .font(.callout)
                .lineLimit(detailed || words.count <= 280 ? nil : 3)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
            ForEach(checks, id: \.self) { check in
                Label(check.text, systemImage: "exclamationmark.triangle.fill")
                    .font(.callout.weight(.medium))
                    .foregroundStyle(check == .noEvidence ? Palette.failure : Palette.attention)
            }
            evidence(verdict, claims: VerdictCheck.claimedValues(words), detailed: detailed)
            if detailed || verdict.status == .contested {
                disputes(verdict, messages: messages)
            }
            actions(verdict, review: review)
        }
        .padding(Space.m)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(background(verdict, review: review, tint: tint), in: RoundedRectangle(cornerRadius: Radius.card, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: Radius.card, style: .continuous)
                .strokeBorder(stroke(verdict, review: review, tint: tint), lineWidth: 1)
        )
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Verdict, \(review.state), \(Chrome.outcomeTitle(verdict.verdict))")
        .confirmationDialog(
            "Accept \(Chrome.outcomeTitle(verdict.verdict)) without opening its evidence?",
            isPresented: $confirmingAccept,
            titleVisibility: .visible
        ) {
            Button("Accept \(Chrome.outcomeTitle(verdict.verdict))") { accept(verdict) }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("You have not opened any step or screenshot this verdict cites. Accepting closes the verdict for good.")
        }
    }

    /// State and decider first: that is what decides whether to trust the rest.
    private func headline(_ review: VerdictReview, verdict: VerdictState) -> some View {
        let urgent = !review.humanReviewed && verdict.status != .rejected
        return HStack(alignment: .firstTextBaseline, spacing: 6) {
            Text(review.state.uppercased())
                .font(.caption.weight(.bold))
                .tracking(0.6)
                .foregroundStyle(urgent ? Palette.attention : .secondary)
            Text(review.decision)
                .font(.callout)
                .foregroundStyle(.secondary)
                .lineLimit(2)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: Space.s)
            if !compact {
                Button {
                    withAnimation(.snappy(duration: 0.2)) { expanded.toggle() }
                } label: {
                    Label(expanded ? "Less" : "Evidence", systemImage: expanded ? "chevron.up" : "chevron.down")
                        .labelStyle(.titleAndIcon)
                        .font(.callout)
                }
                .buttonStyle(.borderless)
                .help(expanded ? "Fold the verdict" : "Show each cited step's picture, what it claims there, and the disputes")
            }
        }
    }

    private func outcome(_ verdict: VerdictState, tint: Color) -> some View {
        HStack(spacing: 6) {
            Image(systemName: Chrome.outcomeSymbol(verdict.verdict))
                .foregroundStyle(tint)
            Text(Chrome.outcomeTitle(verdict.verdict))
                .foregroundStyle(tint)
            if verdict.status.isOpen {
                Text("proposed by the verifier")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
        }
        .font(.title3.weight(.semibold))
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
    private func evidence(_ verdict: VerdictState, claims: [String], detailed: Bool) -> some View {
        let items = Self.byStep((verdict.evidence ?? []).map(Evidence.parse))
        if !items.isEmpty {
            if detailed {
                VStack(alignment: .leading, spacing: Space.m) {
                    ForEach(items, id: \.self) { item in
                        EvidenceRow(store: store, runId: runId, item: item, claims: claims,
                                    show: { seek(item, inSteps: false) }, record: { seek(item, inSteps: true) })
                    }
                }
            } else {
                HStack(spacing: Space.xs) {
                    Text("Cites")
                        .font(.callout)
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
        openedEvidence = true
        store.requestSeek(runId: runId, step: step, fromVerdict: true, inSteps: inSteps)
    }

    // MARK: - Disputes

    /// Each dispute and what the verifier said to it, so the count can be checked.
    @ViewBuilder
    private func disputes(_ verdict: VerdictState, messages: [Message]) -> some View {
        let history = DisputeRecord.history(for: verdict, in: messages)
        if !history.isEmpty {
            VStack(alignment: .leading, spacing: Space.s) {
                Text(history.count == 1 ? "1 dispute" : "\(history.count) disputes")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                ForEach(Array(history.enumerated()), id: \.offset) { _, record in
                    VStack(alignment: .leading, spacing: 2) {
                        Text("\(record.dispute.from.displayName) disputed at \(Chrome.shortTime(record.dispute.at))")
                            .font(.caption.weight(.medium))
                        Text(TranscriptText.clean(record.dispute.text))
                            .font(.callout)
                            .lineLimit(2)
                        if let answer = record.answer {
                            Text("Verifier answered with \(answer.kind == .verdict ? "a \(Chrome.outcomeTitle(answer.verdict)) verdict" : "a \(answer.kind.text)"): \(TranscriptText.clean(answer.text))")
                                .font(.callout)
                                .foregroundStyle(.secondary)
                                .lineLimit(2)
                        } else {
                            Text("No answer from the verifier yet")
                                .font(.callout)
                                .foregroundStyle(Palette.attention)
                        }
                    }
                    .padding(.leading, Space.s)
                    .overlay(alignment: .leading) {
                        Rectangle().fill(Palette.hairline).frame(width: 2)
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
        if verdict.status.isOpen || unreviewed {
            VStack(alignment: .leading, spacing: Space.s) {
                if let acting {
                    reasonForm(verdict, action: acting, outcome: outcome)
                } else {
                    Text(explanation(verdict, unreviewed: unreviewed, outcome: outcome))
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                    // Mac order: the primary action last, on the right, apart from the other.
                    HStack(spacing: Space.s) {
                        if unreviewed {
                            Spacer(minLength: 0)
                            Button("Ask for a Re-check...") { acting = .recheck }
                                .help("Send the verifier your reason to check again")
                        } else {
                            Button("Reject...") { acting = .reject }
                                .disabled(sending)
                                .help("Close it as rejected, with your reason")
                            Spacer(minLength: Space.l)
                            Button("Accept \(outcome)") {
                                if openedEvidence || (verdict.evidence ?? []).isEmpty { accept(verdict) } else { confirmingAccept = true }
                            }
                            .buttonStyle(.borderedProminent)
                            .disabled(sending)
                            .help("Agree with this \(outcome.lowercased()) verdict. This closes it.")
                        }
                    }
                }
            }
            .controlSize(.regular)
            .padding(.top, Space.xxs)
        }
    }

    /// What each action does, in the daemon's own terms (session rules, ADR 0006).
    private func explanation(_ verdict: VerdictState, unreviewed: Bool, outcome: String) -> String {
        if unreviewed {
            let rule = "The daemon does not let a person reopen a verdict the coding agent accepted. "
            if !facts.isAlive {
                return rule + "This run has ended, so the verifier answers a re-check from the record: "
                    + "the steps, pictures and conversation it already has."
            }
            return rule
                + "A re-check asks the verifier to look again with your reason; you can accept or reject what it proposes next."
        }
        if verdict.status == .contested {
            return "Accept closes it with this \(outcome.lowercased()) verdict. Reject closes it as rejected. "
                + "Either way the coding agent and the verifier read your decision in the conversation."
        }
        return "Accept closes it with this \(outcome.lowercased()) verdict. Reject sends your reason to the verifier, which looks again; "
            + "after that only a person can close its verdicts. The coding agent sees both in the conversation."
    }

    private func reasonForm(_ verdict: VerdictState, action: Action, outcome: String) -> some View {
        VStack(alignment: .leading, spacing: Space.s) {
            TextField(action == .reject ? "Why is it wrong? Cite a step or screenshot." : "What should the verifier check again?",
                      text: $reason, axis: .vertical)
                .lineLimit(2...6)
                .textFieldStyle(.plain)
                .focused($reasonFocused)
                .padding(Space.s)
                .background(.background, in: RoundedRectangle(cornerRadius: Radius.control))
                .overlay(RoundedRectangle(cornerRadius: Radius.control).strokeBorder(Palette.hairline))
                .onAppear { reasonFocused = true }
            HStack {
                Button("Cancel") { acting = nil }
                    .keyboardShortcut(.cancelAction)
                Spacer()
                Button(action == .reject ? "Reject \(outcome)" : "Send Re-check") { act(verdict, action) }
                    .buttonStyle(.borderedProminent)
                    .disabled(sending || reason.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
    }

    private func accept(_ verdict: VerdictState) {
        guard let seq = verdict.seq else { return }
        sending = true
        Task {
            await store.send(runId: runId, kind: .accept, text: "accepted", replyTo: seq)
            sending = false
        }
    }

    private func act(_ verdict: VerdictState, _ action: Action) {
        let text = reason.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, let seq = verdict.seq else { return }
        sending = true
        Task {
            let sent: Bool
            switch action {
            case .reject:
                sent = await store.send(runId: runId, kind: .dispute, text: text, replyTo: seq)
            case .recheck:
                // A task starts a verifier turn; no reply-to, since the accepted verdict is closed.
                sent = await store.send(runId: runId, kind: .task,
                                        text: "Re-check the \(Chrome.outcomeTitle(verdict.verdict).lowercased()) verdict (message \(seq)) before it is trusted: \(text)")
            }
            if sent {
                reason = ""
                acting = nil
            }
            sending = false
        }
    }

    // MARK: - Style

    /// Only a verdict a person accepted is filled with its outcome; everything waiting on
    /// someone is outlined in the attention colour, solid, not dashed.
    private func background(_ verdict: VerdictState, review: VerdictReview, tint: Color) -> AnyShapeStyle {
        if verdict.status == .accepted, review.humanReviewed { return AnyShapeStyle(tint.opacity(0.12)) }
        return AnyShapeStyle(.fill.quinary)
    }

    private func stroke(_ verdict: VerdictState, review: VerdictReview, tint: Color) -> Color {
        if verdict.status == .accepted, review.humanReviewed { return tint.opacity(0.35) }
        if verdict.status == .rejected { return Palette.hairline }
        return Palette.attention.opacity(0.55)
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

    var body: some View {
        if let step = item.step {
            Button(action: open) {
                HStack(spacing: 3) {
                    Text(item.label)
                        .underline(hovering)
                    Image(systemName: "arrow.up.right")
                        .font(.system(size: 8, weight: .bold))
                }
                .font(.callout.weight(.medium))
                .foregroundStyle(Color.accentColor)
                .padding(.horizontal, 6)
                .padding(.vertical, 2)
                .background(Color.accentColor.opacity(hovering ? 0.14 : 0.08), in: RoundedRectangle(cornerRadius: Radius.chip))
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
            Chip(text: item.label, symbol: "text.quote")
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
                    Text(ToolCatalog.entry(for: step.tool).title + outcomeText(step))
                        .font(.callout.weight(.medium))
                        .foregroundStyle(step.outcome.isFailure ? Palette.failure : .primary)
                } else if item.step != nil {
                    Text("Not in the run's record")
                        .font(.callout)
                        .foregroundStyle(Palette.failure)
                } else {
                    Text(item.label).font(.callout)
                }
                Spacer(minLength: 0)
                if item.step != nil {
                    Button("Step Record", action: record)
                        .buttonStyle(.link)
                        .font(.callout)
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
            if !claims.isEmpty {
                HStack(alignment: .firstTextBaseline, spacing: Space.xs) {
                    Text("It claims")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.secondary)
                    FlowLayout(spacing: Space.xs) {
                        ForEach(claims, id: \.self) { value in
                            Text(value)
                                .font(.callout.monospacedDigit().weight(.semibold))
                                .padding(.horizontal, 6)
                                .padding(.vertical, 1)
                                .background(.fill.tertiary, in: RoundedRectangle(cornerRadius: Radius.chip))
                        }
                    }
                }
                .help("Values the verdict's own words say are on screen: check them against the picture")
            }
            if let step {
                Text(StepExcerpt.text(step))
                    .font(.caption.monospaced())
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

    var body: some View {
        ZStack {
            RoundedRectangle(cornerRadius: Radius.chip + 2, style: .continuous).fill(Palette.well)
            if let image {
                Image(nsImage: image)
                    .resizable()
                    .interpolation(.medium)
                    .scaledToFit()
            } else if loaded {
                Image(systemName: "photo").foregroundStyle(.tertiary)
            } else {
                ProgressView().controlSize(.small)
            }
        }
        .clipShape(RoundedRectangle(cornerRadius: Radius.chip + 2, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Radius.chip + 2, style: .continuous).strokeBorder(Palette.hairline))
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
