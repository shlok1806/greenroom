import SwiftUI

/// The run pane (Figma Mockups 02 to 04, 07; rethought around a player in redesign 7): the
/// toolbar with the basic controls, the header that answers "is it working, did it pass, what
/// do I do now", then the player (the picture, its caption and the transport bar) beside the
/// inspector column (Checks, Activity, Message). Opening Activity never hides the player: the
/// picture shrinks to the room left. Z folds the inspector away.
struct RunPane: View {
    @Bindable var shell: ShellModel
    var summary: Summary
    var windowClass: WindowClass
    @AppStorage("inspectorWidth", store: AppDefaults.shared) private var inspectorWidth = InspectorDivider.defaultWidth
    @State private var bodyWidth: CGFloat = 0

    /// The inspector's widths: at least 280, and never so wide that the player has under 360.
    private var inspectorRange: ClosedRange<Double> {
        let most = max(280, Double(bodyWidth) - 360)
        return 280...min(720, most)
    }

    var body: some View {
        VStack(spacing: 0) {
            RunToolbar(shell: shell, summary: summary, windowClass: windowClass)
            StatusHeader(shell: shell, summary: summary)
            if shell.confirmingAccept {
                AcceptConfirmBanner(busy: shell.busy == SummaryAction.accept, look: {
                    shell.cancelAccept()
                    if let first = summary.checks.items.first(where: { $0.step != nil }) { shell.select(check: first.id) }
                }, accept: shell.acceptAnyway)
            }
            if shell.confirmingDestroy {
                DestroyConfirmBanner(busy: shell.busy == "destroy", keep: { shell.confirmingDestroy = false }, destroy: shell.destroy)
            }
            if let error = shell.store.lastError {
                ErrorBanner(text: error) { shell.store.clearError() }
            }
            if let warning = shell.warning(for: summary) {
                WarningBanner(text: warning, restart: summary.state == .notAnswering ? nil : {
                    shell.perform(SummaryAction(id: SummaryAction.restart, label: "Restart the Mac"))
                }, dismiss: shell.dismissWarning)
            }
            HStack(spacing: 0) {
                StageView(shell: shell, summary: summary, windowClass: windowClass).cloneScope("Stage")
                if !shell.zoomed {
                    InspectorDivider(width: $inspectorWidth, range: inspectorRange)
                    InspectorColumn(shell: shell, summary: summary)
                        .cloneScope("Inspector")
                        .frame(width: CGFloat(min(max(inspectorWidth, inspectorRange.lowerBound), inspectorRange.upperBound)))
                        .transition(.move(edge: .trailing).combined(with: .opacity))
                }
            }
            .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { bodyWidth = $0 }
            .animation(Motion.easeOut(Motion.settle), value: shell.zoomed)
            .cloneScope("Body")
        }
        .background(Palette.bg)
        .overlay(alignment: .bottom) { UndoToast(shell: shell) }
        .cloneScope("Run")
    }
}

/// The toolbar: the run's name, who started it and when, and the panels. A compact window
/// shows the name alone (Figma 03 compact).
struct RunToolbar: View {
    @Bindable var shell: ShellModel
    var summary: Summary
    var windowClass: WindowClass = .regular
    @Environment(\.frozenNow) private var frozenNow

    var body: some View {
        HStack(spacing: Gap.x8) {
            // Who started it and when is in the tooltip and Details: the words go to the player.
            Text(summary.name).textStyle(.bodyEmphasis).foregroundStyle(Palette.text).lineLimit(1)
                .help("\(summary.name), \(origin)")
                .clonePart("Run name")
            Spacer(minLength: Gap.x8)
            if shell.busy == "capture" || shell.busy == "export" {
                // What the More menu started, until it is done.
                HStack(spacing: 6) {
                    StatusGlyph(kind: .checking, color: .accent, size: 12)
                    Text(shell.busy == "capture" ? "Capturing" : "Saving the recording").textStyle(.caption).foregroundStyle(Palette.textSecondary)
                }
                .accessibilityElement(children: .combine)
                .transition(.opacity)
            }
            if shell.canTakeControl(summary) {
                ToolbarButton(icon: .pointer, title: "Take control") {
                    shell.perform(SummaryAction(id: SummaryAction.takeControl, label: "Take control"))
                }
                .help("Take control of the Mac (T)")
                .accessibilityIdentifier("toolbar.takeControl")
                .cloneScope("Take control")
            }
            DetailsButton(shell: shell, summary: summary).cloneScope("Details")
            ToolbarButton(icon: .activity, title: "Activity", on: shell.activityOpen, showsTitle: false) { shell.toggleActivity() }
                .help("Activity (A)")
                .cloneScope("Toolbar button[0]")
            ToolbarButton(icon: .message, title: "Message", on: shell.inspectorTab == .message && !shell.zoomed, showsTitle: false) {
                if shell.inspectorTab == .message && !shell.zoomed { shell.show(.checks) } else { shell.openComposer(.message) }
            }
            .help(summary.machine.isUp ? "The conversation; message the verifier (M)" : "The conversation (M). The Mac is gone, so nothing answers")
            .cloneScope("Toolbar button[1]")
            RunMoreMenu(shell: shell, summary: summary).cloneScope("Icon button")
        }
        .padding(.leading, Gap.x24)
        .padding(.trailing, Gap.x12)
        .frame(height: Metrics.toolbarHeight)
        .overlay(alignment: .bottom) { Rectangle().fill(Palette.border).frame(height: 1) }
        .cloneScope("Toolbar")
    }

    /// Who started the run and when; once it is done, only when (who started it matters while
    /// you can still act on it).
    private var origin: String {
        let when = Clock.ago(summary.startedAt, now: frozenNow ?? Date())
        guard summary.group != .done else { return when }
        return summary.source.map { "from \($0), \(when)" } ?? when
    }
}

/// The More menu, in the app's own dropdown: what is not on screen.
private struct RunMoreMenu: View {
    @Bindable var shell: ShellModel
    var summary: Summary

    var body: some View {
        DropdownButton(center: shell.dropdowns, id: "more", width: 260, items: items) {
            IconView(icon: .more).foregroundStyle(Palette.textSecondary)
                .clonePart("Icon/more")
        }
        .buttonStyle(IconButtonStyle())
        .fixedSize()
        .help("More")
        .accessibilityLabel("More")
        .accessibilityIdentifier("toolbar.more")
    }

    private func items() -> [DropdownItem] {
        var out: [DropdownItem] = [
            DropdownItem(id: "details", title: "Run details", icon: .info) { shell.detailsOpen = true },
            DropdownItem(id: "evidence", title: "Open the evidence", icon: .video, keys: "E") { shell.evidenceOpen = true },
            DropdownItem(id: "zoom", title: shell.zoomed ? "Show the inspector" : "Picture only", icon: .expand, keys: "Z") { shell.toggleZoom() },
        ]
        if summary.machine.isUp {
            out.append(DropdownItem(id: "task", title: "New task for the verifier", icon: .message) { shell.openComposer(.task) })
        }
        out.append(DropdownItem(id: "palette", title: "Command palette", icon: .search, keys: "⌘K") { shell.paletteOpen = true })
        var record: [DropdownItem] = []
        if shell.canCapture {
            record.append(DropdownItem(id: "capture", title: "Capture a screenshot", icon: .camera, keys: "C") { shell.capture() })
        }
        if shell.canExport {
            record.append(DropdownItem(id: "export", title: "Save the recording…", icon: .download) { shell.exportRecording() })
        }
        record.append(DropdownItem(id: "copy", title: "Copy run ID", icon: .copy) { shell.copyRunID() })
        record[0].separated = true
        out += record
        var mac: [DropdownItem] = []
        if summary.machine.status == "on" {
            mac.append(DropdownItem(id: "restart", title: "Restart the Mac", icon: .restart) {
                shell.perform(SummaryAction(id: SummaryAction.restart, label: "Restart the Mac"))
            })
        }
        if shell.canDestroy {
            mac.append(DropdownItem(id: "destroy", title: "Destroy the Mac…", icon: .trash, destructive: true) { shell.confirmingDestroy = true })
        }
        if !mac.isEmpty { mac[0].separated = true }
        return out + mac
    }
}

/// Details, one click away: the popover with the whole task, the times, the models, the Mac,
/// the finish and the run ID.
private struct DetailsButton: View {
    @Bindable var shell: ShellModel
    var summary: Summary

    var body: some View {
        IconButton(icon: .info, name: "Details") { shell.detailsOpen.toggle() }
            .accessibilityIdentifier("toolbar.details")
    }
}

/// The header: status glyph and word, the tally or time, one line under it, and the actions.
struct StatusHeader: View {
    @Bindable var shell: ShellModel
    var summary: Summary
    @Environment(\.frozenNow) private var frozenNow
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        let ticking = summary.state.isWorking || summary.state == .paused || summary.state == .notAnswering
        Group {
            if ticking && frozenNow == nil {
                TimelineView(.periodic(from: .now, by: 1)) { context in content(now: context.date) }
            } else {
                content(now: frozenNow ?? Date())
            }
        }
        .padding(.horizontal, Gap.x24)
        .padding(.vertical, Gap.x16)
        .frame(minHeight: Metrics.headerHeight)
        // The design's header is 80 of content and padding over its 1 pt border (81 in all).
        .padding(.bottom, 1)
        .overlay(alignment: .bottom) { Rectangle().fill(Palette.border).frame(height: 1) }
        .cloneScope("Header")
        // The verdict landing, said to VoiceOver as the old window said it ("Verdict: Failed, 2 of 4").
        .onChange(of: RunStateKey(runId: summary.runId, state: summary.state)) { old, new in
            guard old.runId == new.runId, !old.state.isOutcome, new.state.isOutcome else { return }
            let model = HeaderModel(summary, now: Date())
            AccessibilityNotification.Announcement("Verdict: \(model.status)\(model.tally.isEmpty ? "" : ", \(model.tally)")").post()
        }
    }

    private func content(now: Date) -> some View {
        let model = HeaderModel(summary, now: now)
        let (primary, secondary) = shell.actions(for: summary)
        return HStack(alignment: .center, spacing: Gap.x8) {
            VStack(alignment: .leading, spacing: 2) {
                HStack(alignment: .center, spacing: 10) {
                    StatusGlyph(kind: model.glyph, color: model.glyphColor, size: Metrics.headerGlyph)
                        .clonePart("Glyph")
                    Text(model.status).textStyle(.display).foregroundStyle(Palette.text)
                        .contentTransition(.opacity)
                        .clonePart("Status word")
                    if !model.tally.isEmpty {
                        Text(model.tally).textStyle(.title).foregroundStyle(Palette.textSecondary)
                            .contentTransition(.numericText())
                            .clonePart("Tally")
                    }
                }
                .cloneScope("Outcome")
                .accessibilityElement(children: .combine)
                .accessibilityAddTraits(.isHeader)
                .animation(Motion.change(Motion.land, reduce: reduceMotion), value: model.status)
                if !model.line.isEmpty {
                    // In line with the status word: 30 in (the 20 pt glyph and its 10 pt gap).
                    HStack(spacing: 6) {
                        if model.isNow {
                            Circle().fill(Palette.accent).frame(width: 6, height: 6).clonePart("Live pulse")
                            (Text("Now ").foregroundStyle(Palette.textSecondary) + Text(AgentMarkdown.inline(model.line)).foregroundStyle(Palette.text))
                                .textStyle(.body)
                                .clonePart("Text")
                        } else {
                            Text(AgentMarkdown.inline(model.line)).textStyle(.body).foregroundStyle(Palette.textSecondary)
                                .clonePart("Now text")
                        }
                    }
                    .lineLimit(1)
                    .padding(.leading, 30)
                    .cloneScope("Now")
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .cloneScope("Status")
            let buttons = secondary.count + (primary == nil ? 0 : 1)
            ForEach(Array(secondary.enumerated()), id: \.element.id) { index, action in
                Button(action.label) { shell.perform(action) }
                    .buttonStyle(ActionButtonStyle(kind: .secondary, loading: shell.busy == action.id))
                    .cloneScope(buttons > 1 ? "Button[\(index)]" : "Button")
            }
            if let primary {
                Button(primary.label) { shell.perform(primary) }
                    .buttonStyle(ActionButtonStyle(kind: .primary, loading: shell.busy == primary.id))
                    .help(Keys.hint(for: primary))
                    .cloneScope(buttons > 1 ? "Button[\(buttons - 1)]" : "Button")
            }
        }
    }
}

/// One amber line under the header, dismissible (Figma wireframe 08): the run still works.
struct WarningBanner: View {
    var text: String
    var restart: (() -> Void)?
    var dismiss: () -> Void

    var body: some View {
        HStack(spacing: Gap.x8) {
            StatusGlyph(kind: .warning, color: .wait)
            Text(text).textStyle(.body).foregroundStyle(Palette.text).lineLimit(1)
            Spacer(minLength: Gap.x8)
            if let restart {
                Button("Restart the Mac", action: restart).buttonStyle(ActionButtonStyle(kind: .plain))
            }
            IconButton(icon: .close, name: "Dismiss", action: dismiss)
        }
        .padding(.leading, Gap.x24)
        .padding(.trailing, Gap.x12)
        .frame(height: 40)
        .background(Palette.waitSubtle)
        .overlay(alignment: .bottom) { Rectangle().fill(Palette.border).frame(height: 1) }
    }
}

/// The checks, the first failure selected by itself; or, while the Mac starts, the steps of
/// getting it ready.
struct ChecksColumn: View {
    @Bindable var shell: ShellModel
    var summary: Summary

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 2) {
                if summary.state == .starting {
                    heading("Getting ready")
                    let phases = shell.store.details[summary.runId]?.machine?.boot ?? []
                    ForEach(BootRow.rows(phases)) { row in
                        BootRowView(row: row, current: row.glyph == .checking)
                    }
                } else if summary.checks.items.isEmpty, !citedSteps.isEmpty {
                    // A verdict without checks: the steps it cites, each one click from the player.
                    heading("Cited steps")
                    CitedStepsView(cited: citedSteps, steps: shell.store.steps[summary.runId] ?? [],
                                   shown: shell.currentStep(shell.timeline()), seek: { shell.seek(toStep: $0) })
                } else if summary.checks.items.isEmpty {
                    Text(emptyText).textStyle(.body).foregroundStyle(Palette.textSecondary)
                        .padding(.horizontal, Gap.x12)
                        .padding(.vertical, 10)
                        .fixedSize(horizontal: false, vertical: true)
                } else {
                    let many = summary.checks.items.count > 1
                    ForEach(Array(summary.checks.items.enumerated()), id: \.element.id) { index, check in
                        CheckRowView(check: check, selected: check.id == shell.selectedCheckID, metaOverride: meta(for: check),
                                     showsPassValue: summary.state == .passed,
                                     checking: summary.state == .checking && check.id == CheckSelection.current(summary.checks.items))
                            .onTapGesture { shell.select(check: check.id) }
                            .accessibilityAction { shell.select(check: check.id) }
                            .cloneScope(many ? "Check row[\(index)]" : "Check row")
                        if check.id == shell.selectedCheckID, let record = shell.acceptanceCheck(check.id) {
                            CheckProofView(check: record, steps: shell.store.steps[summary.runId] ?? [],
                                           shown: shell.currentStep(shell.timeline()), seek: { shell.seek(toStep: $0) })
                        }
                    }
                }
            }
            .padding(Gap.x16)
        }
        .visibleScroller { metrics in
            let count = summary.checks.items.count
            guard metrics.content > metrics.viewport * 1.5, count > 0 else { return nil }
            return "\(count) checks"
        }
        .background(Palette.bg)
    }

    private var citedSteps: [Int] { shell.store.citedSteps(summary.runId) }

    private var emptyText: String {
        switch summary.state {
        case .checking: "The verifier lists its checks before it acts."
        case .stopped: "This run ended before the verifier checked anything."
        default: "No checks for this run."
        }
    }

    /// While the Mac is stuck or restarting, a check not yet answered says it waits.
    /// The check the verifier is on (the first not yet answered) says what is happening to it:
    /// "waiting" while the screen does not answer or the Mac restarts, "paused" (Figma 07a and
    /// 07b); while live it is drawn checking (Figma 02).
    private func meta(for check: SummaryCheck) -> String? {
        guard check.state == .pending, check.id == CheckSelection.current(summary.checks.items) else { return nil }
        switch summary.state {
        case .notAnswering, .restarting: return "waiting"
        case .paused: return "paused"
        default: return nil
        }
    }

    /// Caption Emphasis, 12 in, 4 above and 6 below (24 tall).
    private func heading(_ title: String) -> some View {
        Text(title).textStyle(.captionEmphasis).foregroundStyle(Palette.textSecondary)
            .clonePart("Text")
            .padding(.leading, Gap.x12)
            .padding(.top, Gap.x4)
            .padding(.bottom, 6)
            .frame(maxWidth: .infinity, alignment: .leading)
            .cloneScope("Heading")
            .accessibilityAddTraits(.isHeader)
    }
}

/// A step of getting the Mac ready: a check row's shape with the boot step's words.
private struct BootRowView: View {
    var row: BootRow
    var current: Bool

    var body: some View {
        HStack(spacing: 10) {
            StatusGlyph(kind: row.glyph, color: row.glyph == .passed ? .pass : (row.glyph == .checking ? .accent : .tertiary))
            Text(row.text).textStyle(current ? .bodyEmphasis : .body)
                .foregroundStyle(row.glyph == .pending ? Palette.textSecondary : Palette.text)
                .frame(maxWidth: .infinity, alignment: .leading)
            Text(row.meta).textStyle(.body).foregroundStyle(Palette.textSecondary)
        }
        .padding(.horizontal, Gap.x12)
        .padding(.vertical, 10)
        .frame(minHeight: Metrics.checkRowMinHeight)
        .background(RoundedRectangle(cornerRadius: Corner.row).fill(current ? Palette.bgSelected : .clear))
        .accessibilityElement(children: .combine)
    }
}

/// Accepting with none of the cited proof looked at, asked inline under the header (the old
/// verdict card asked the same): look at it, or accept anyway.
struct AcceptConfirmBanner: View {
    var busy: Bool
    var look: () -> Void
    var accept: () -> Void

    var body: some View {
        HStack(spacing: Gap.x8) {
            StatusGlyph(kind: .warning, color: .wait)
            Text("You have not looked at any proof yet. Accepting is final once the undo ends.")
                .textStyle(.body).foregroundStyle(Palette.text).lineLimit(1)
            Spacer(minLength: Gap.x8)
            Button("Show the proof", action: look).buttonStyle(ActionButtonStyle(kind: .secondary))
            Button("Accept anyway", action: accept).buttonStyle(ActionButtonStyle(kind: .primary, loading: busy))
                .accessibilityIdentifier("accept.anyway")
        }
        .padding(.leading, Gap.x24)
        .padding(.trailing, Gap.x12)
        .frame(height: 44)
        .background(Palette.waitSubtle)
        .overlay(alignment: .bottom) { Rectangle().fill(Palette.border).frame(height: 1) }
    }
}

/// A run and its state: what changes when a verdict lands on the run that shows.
struct RunStateKey: Equatable {
    var runId: String
    var state: SummaryState
}

/// Under the selected check, what its record says beyond the row: its kind ("visual",
/// "timing, within 2 s"), every step it cites (each seeks the player), and the text its UI
/// reads say a person cannot see (the old checklist's detail).
struct CheckProofView: View {
    var check: AcceptanceCheck
    var steps: [Step]
    /// The step the player shows.
    var shown: Int?
    var seek: (Int) -> Void

    var body: some View {
        let warnings = check.unseenWarnings(in: steps)
        let cited = check.evidence
        if check.kindTag != nil || cited.count > 1 || !warnings.isEmpty {
            VStack(alignment: .leading, spacing: 6) {
                if check.kindTag != nil || cited.count > 1 {
                    ChipFlowLayout(spacing: 6) {
                        if let tag = check.kindTag {
                            Text(tag).textStyle(.caption).foregroundStyle(Palette.textSecondary)
                                .frame(height: 22)
                                .help("The kind of check: a visual one is judged from the picture, a timing one by when it happened")
                        }
                        if cited.count > 1 {
                            ForEach(cited, id: \.self) { seq in
                                StepLink(seq: seq, known: steps.contains { $0.seq == seq }, current: seq == shown) { seek(seq) }
                                    .help(steps.first { $0.seq == seq }.map { "Step \(seq): \(StepSummary.phrase(for: $0, in: steps)). Show it on the player" }
                                          ?? "Step \(seq) is not in the record")
                            }
                        }
                    }
                }
                ForEach(warnings, id: \.self) { warning in
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        StatusGlyph(kind: .warning, color: .wait, size: 12)
                        Text(warning).textStyle(.caption).foregroundStyle(Palette.text)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    .help("The verifier's UI read says this text is not visible, so the picture cannot prove it")
                }
            }
            .padding(.leading, 38)
            .padding(.trailing, Gap.x12)
            .padding(.bottom, 6)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}

/// A step a verdict cites, as a small chip that seeks the player.
struct StepLink: View {
    var seq: Int
    var known: Bool
    var current: Bool
    var action: () -> Void

    var body: some View {
        Button(action: action) {
            Text("Step \(seq)")
                .textStyle(.caption)
                .monospacedDigit()
                .foregroundStyle(known ? Palette.text : Palette.textSecondary)
                .strikethrough(!known)
                .padding(.horizontal, 6)
                .frame(height: 22)
                .background(RoundedRectangle(cornerRadius: Corner.control).fill(current ? Palette.bgSelected : Palette.bgHover))
                .overlay(RoundedRectangle(cornerRadius: Corner.control).strokeBorder(current ? Palette.text : Palette.border, lineWidth: 1))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(!known)
        .accessibilityLabel(known ? "Step \(seq)" : "Step \(seq), not in the record")
    }
}

/// A verdict's cited steps when it has no checks: each in words, one click from the player.
struct CitedStepsView: View {
    var cited: [Int]
    var steps: [Step]
    var shown: Int?
    var seek: (Int) -> Void

    var body: some View {
        ForEach(cited, id: \.self) { seq in
            let step = steps.first { $0.seq == seq }
            Button { seek(seq) } label: {
                HStack(spacing: 10) {
                    StatusGlyph(kind: step?.failed == true ? .failed : .passed,
                                color: step == nil ? .tertiary : (step?.failed == true ? .fail : .pass))
                    Text(step.map { StepSummary.phrase(for: $0, in: steps) } ?? "Not in the record")
                        .textStyle(.body)
                        .foregroundStyle(step == nil ? Palette.textSecondary : Palette.text)
                        .lineLimit(2)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Text("\(seq)").textStyle(.caption).monospacedDigit().foregroundStyle(Palette.textSecondary)
                }
                .padding(.horizontal, Gap.x12)
                .padding(.vertical, 10)
                .background(RoundedRectangle(cornerRadius: Corner.row).fill(seq == shown ? Palette.bgSelected : .clear))
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .disabled(step == nil)
            .help(step == nil ? "The verdict cites step \(seq), which is not in the record" : StepHelp.text(seq, in: steps))
        }
    }
}
