import SwiftUI

/// Activity and the composer (Figma wireframe 10), one click or one key (A, M) away and never
/// on the default view: the verifier's work as task rows grouped by check, raw logs a click
/// further, and the message field at the bottom.
struct ActivityPanel: View {
    @Bindable var shell: ShellModel
    var summary: Summary
    @State private var raw = false
    @State private var open: [String: Bool] = [:]

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: Gap.x8) {
                Text("Activity").textStyle(.title).foregroundStyle(Palette.text).accessibilityAddTraits(.isHeader)
                Spacer()
                Button { raw.toggle() } label: { Label("Raw logs", systemImage: "apple.terminal") }
                    .buttonStyle(ToolbarButtonStyle(on: raw))
                IconButton(systemImage: "xmark", name: "Close Activity") { shell.toggleActivity() }
            }
            .padding(.leading, Gap.x24)
            .padding(.trailing, Gap.x12)
            .frame(height: 48)
            .overlay(alignment: .bottom) { Rectangle().fill(Palette.border).frame(height: 1) }

            ScrollViewReader { proxy in
                ScrollView {
                    if raw { rawLogs } else { timeline }
                }
                .defaultScrollAnchor(.bottom)
                .onChange(of: steps.count) { _, _ in proxy.scrollTo("end", anchor: .bottom) }
            }

            if let mode = shell.composer ?? (summary.group == .done ? nil : ComposerMode.message) {
                ComposerView(text: $shell.composerDraft, placeholder: mode.placeholder, sending: shell.sending,
                             disabledReason: disabledReason(mode), focusRequest: shell.composerFocus, send: {
                                 if shell.composer == nil { shell.composer = mode }
                                 shell.sendComposer()
                             })
                    .padding(Gap.x16)
                    .overlay(alignment: .top) { Rectangle().fill(Palette.border).frame(height: 1) }
            }
        }
        .background(Palette.bg)
    }

    private var steps: [Step] { shell.store.steps[summary.runId] ?? [] }

    private var timeline: some View {
        let sections = ActivityLayout.sections(messages: shell.store.messages[summary.runId] ?? [], steps: steps,
                                               checks: summary.checks.items, working: summary.state == .checking)
        return LazyVStack(alignment: .leading, spacing: 0) {
            if sections.isEmpty {
                Text("Nothing yet. The verifier's steps show here as it works.")
                    .textStyle(.body).foregroundStyle(Palette.textSecondary).padding(Gap.x24)
            }
            ForEach(sections) { section in
                Text(section.title).textStyle(.captionEmphasis).foregroundStyle(Palette.textSecondary)
                    .padding(.horizontal, Gap.x24)
                    .padding(.top, Gap.x16)
                    .padding(.bottom, Gap.x4)
                    .accessibilityAddTraits(.isHeader)
                ForEach(section.rows) { row in
                    TaskRowView(row: row, expanded: Binding(
                        get: { open[row.id] ?? row.opensItself },
                        set: { open[row.id] = $0 }))
                        .padding(.horizontal, Gap.x16)
                }
            }
            if summary.state == .checking {
                ThinkingView(live: true).padding(.horizontal, Gap.x24).padding(.vertical, Gap.x8)
            }
            Color.clear.frame(height: 1).id("end")
        }
        .padding(.bottom, Gap.x16)
    }

    private var rawLogs: some View {
        LazyVStack(alignment: .leading, spacing: 2) {
            ForEach(steps) { step in
                Text("\(step.seq)  \(step.tool)  \(StepSummary.line(for: step))\(step.error.map { "  error: \($0)" } ?? "")")
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundStyle(step.error == nil ? Palette.text : Palette.fail)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            Color.clear.frame(height: 1).id("end")
        }
        .padding(Gap.x16)
    }

    private func disabledReason(_ mode: ComposerMode) -> String? {
        guard mode == .message || mode == .answer else { return nil }
        if summary.state == .paused, mode == .message, summary.primaryAction?.id == SummaryAction.continue {
            return nil
        }
        return summary.machine.isUp ? nil : "The verifier stopped with the Mac. Nothing will answer."
    }
}
