import SwiftUI

/// The run pane (Figma Mockups 02 to 04, 07): the toolbar, the header that answers "is it
/// working, did it pass, what do I do now", then the checks beside the stage.
struct RunPane: View {
    @Bindable var shell: ShellModel
    var summary: Summary
    var windowClass: WindowClass

    var body: some View {
        VStack(spacing: 0) {
            RunToolbar(shell: shell, summary: summary)
            StatusHeader(shell: shell, summary: summary)
            if let warning = shell.warning(for: summary) {
                WarningBanner(text: warning, restart: summary.state == .notAnswering ? nil : {
                    shell.perform(SummaryAction(id: SummaryAction.restart, label: "Restart the Mac"))
                }, dismiss: shell.dismissWarning)
            }
            HStack(spacing: 0) {
                HStack(spacing: 0) {
                    ChecksColumn(shell: shell, summary: summary)
                    // The 1 pt border is inside the column's width, as the design draws it.
                    Rectangle().fill(Palette.border).frame(width: 1)
                }
                .frame(width: windowClass.checks)
                .cloneScope("Checks")
                if shell.activityOpen {
                    ActivityPanel(shell: shell, summary: summary)
                } else {
                    StageView(shell: shell, summary: summary, windowClass: windowClass).cloneScope("Stage")
                }
            }
            .cloneScope("Body")
        }
        .background(Palette.bg)
        .cloneScope("Run")
    }
}

/// The toolbar: the run's name, who started it and when, and the panels.
struct RunToolbar: View {
    @Bindable var shell: ShellModel
    var summary: Summary
    @Environment(\.frozenNow) private var frozenNow

    var body: some View {
        HStack(spacing: Gap.x8) {
            Text(summary.name).textStyle(.bodyEmphasis).foregroundStyle(Palette.text).lineLimit(1)
                .fixedSize().clonePart("Run name")
            Text(origin).textStyle(.body).foregroundStyle(Palette.textSecondary).lineLimit(1)
                .layoutPriority(-1).clonePart("Run meta")
            Spacer(minLength: Gap.x8)
            ToolbarButton(icon: .activity, title: "Activity", on: shell.activityOpen) { shell.toggleActivity() }
                .help("Activity (A)")
                .cloneScope("Toolbar button[0]")
            // Only while something will answer: a Mac that is gone takes no messages.
            if summary.machine.isUp {
                ToolbarButton(icon: .message, title: "Message", on: shell.composer == .message) { shell.openComposer(.message) }
                    .help("Message the verifier (M)")
                    .cloneScope("Toolbar button[1]")
            }
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
            Button("Open the evidence") { shell.evidenceOpen = true }
            Button("Command palette") { shell.paletteOpen = true }
            if summary.machine.status == "on" {
                Divider()
                Button("Restart the Mac") { shell.perform(SummaryAction(id: SummaryAction.restart, label: "Restart the Mac")) }
            }
            Divider()
            Button("Copy run ID") {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(summary.runId, forType: .string)
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
                    heading("Checks")
                    Text(emptyText).textStyle(.body).foregroundStyle(Palette.textSecondary)
                        .padding(.horizontal, Gap.x12)
                        .padding(.vertical, 10)
                        .fixedSize(horizontal: false, vertical: true)
                } else {
                    heading("Checks")
                    let many = summary.checks.items.count > 1
                    ForEach(Array(summary.checks.items.enumerated()), id: \.element.id) { index, check in
                        CheckRowView(check: check, selected: check.id == shell.selectedCheckID, metaOverride: meta(for: check),
                                     showsPassValue: summary.state != .failed)
                            .onTapGesture { shell.select(check: check.id) }
                            .accessibilityAction { shell.select(check: check.id) }
                            .cloneScope(many ? "Check row[\(index)]" : "Check row")
                    }
                }
            }
            .padding(Gap.x16)
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
    private func meta(for check: SummaryCheck) -> String? {
        guard check.state == .pending else { return nil }
        switch summary.state {
        case .notAnswering: return check.id == shell.selectedCheckID ? "waiting" : nil
        case .paused: return check.id == shell.selectedCheckID ? "paused" : nil
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
