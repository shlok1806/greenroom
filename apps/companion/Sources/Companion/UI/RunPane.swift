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
                        .frame(width: CGFloat(min(max(inspectorWidth, inspectorRange.lowerBound), inspectorRange.upperBound)))
                        .transition(.move(edge: .trailing).combined(with: .opacity))
                }
            }
            .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { bodyWidth = $0 }
            .animation(.easeOut(duration: Motion.settle), value: shell.zoomed)
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
            if shell.canTakeControl(summary) {
                ToolbarButton(icon: .pointer, title: "Take control") {
                    shell.perform(SummaryAction(id: SummaryAction.takeControl, label: "Take control"))
                }
                .help("Take control of the Mac (T)")
                .accessibilityIdentifier("toolbar.takeControl")
            }
            DetailsButton(shell: shell, summary: summary)
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

/// The More menu: the system's menu, with what is not on screen.
private struct RunMoreMenu: View {
    @Bindable var shell: ShellModel
    var summary: Summary

    var body: some View {
        Menu {
            Button("Open the evidence (E)") { shell.evidenceOpen = true }
            Button(shell.zoomed ? "Show checks and Activity (Z)" : "Picture only (Z)") { shell.toggleZoom() }
            if summary.machine.isUp {
                Button("New task for the verifier") { shell.openComposer(.task) }
            }
            Button("Command palette (⌘K)") { shell.paletteOpen = true }
            Divider()
            if shell.canCapture {
                Button("Capture a screenshot (C)") { shell.capture() }
            }
            if shell.canExport {
                Button("Save the recording…") { shell.exportRecording() }
            }
            Button("Copy run ID") { shell.copyRunID() }
            if summary.machine.status == "on" || shell.canDestroy {
                Divider()
            }
            if summary.machine.status == "on" {
                Button("Restart the Mac") { shell.perform(SummaryAction(id: SummaryAction.restart, label: "Restart the Mac")) }
            }
            if shell.canDestroy {
                Button("Destroy the Mac…", role: .destructive) { shell.confirmingDestroy = true }
            }
        } label: {
            IconView(icon: .more).foregroundStyle(Palette.textSecondary)
                .clonePart("Icon/more")
                .frame(width: Metrics.buttonHeight, height: Metrics.buttonHeight)
        }
        .menuStyle(.button)
        .buttonStyle(IconButtonStyle())
        .menuIndicator(.hidden)
        .fixedSize()
        .help("More")
        .accessibilityLabel("More")
    }
}

/// Details, one click away: the popover with the whole task, the times, the models, the Mac,
/// the finish and the run ID.
private struct DetailsButton: View {
    @Bindable var shell: ShellModel
    var summary: Summary

    var body: some View {
        IconButton(icon: .info, name: "Details") { shell.detailsOpen.toggle() }
            .popover(isPresented: $shell.detailsOpen, arrowEdge: .bottom) {
                RunDetailsView(shell: shell, summary: summary)
            }
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
                            (Text("Now ").foregroundStyle(Palette.textSecondary) + Text(model.line).foregroundStyle(Palette.text))
                                .textStyle(.body)
                                .clonePart("Text")
                        } else {
                            Text(model.line).textStyle(.body).foregroundStyle(Palette.textSecondary)
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
