import AppKit
import SwiftUI

// What the old window could do that the redesign had lost (redesign 7, the parity table in
// PR "Companion redesign 7"): the run's details, destroying the Mac, the undo after an accept
// or reject, the daemon's errors, and the handle that sizes the Activity column.

/// The run's details (the old header's Details): the whole task, when it ran, who verified
/// it, the Mac, how it finished and the run ID. One click away, behind the toolbar's Details.
struct RunDetailsView: View {
    let shell: ShellModel
    let summary: Summary

    var body: some View {
        let store = shell.store
        let detail = store.details[summary.runId]
        let task = (store.messages[summary.runId] ?? []).first { $0.kind == .task }?.text
        let finish = detail?.finish
        let facts = RunDetailFacts(detail: detail, listed: store.run(summary.runId),
                                   messages: store.messages[summary.runId], frames: store.frames[summary.runId])
        ScrollView {
            VStack(alignment: .leading, spacing: Gap.x12) {
                Text("Details").textStyle(.title).foregroundStyle(Palette.text)
                Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: Gap.x16, verticalSpacing: 6) {
                    row("Started", summary.startedAt.formatted(date: .abbreviated, time: .shortened))
                    if let ended = summary.endedAt { row("Ended", ended.formatted(date: .abbreviated, time: .shortened)) }
                    row("Duration", Clock.elapsed(summary.elapsed(now: Date())))
                    row("Steps", "\((store.steps[summary.runId] ?? []).count)")
                    row("Frames", facts.frames.formatted())
                    row("Messages", facts.messages.formatted())
                    row("Verifier", facts.models?.verifier ?? "Not recorded for this run")
                    if let describer = facts.models?.describer { row("Describer", describer) }
                    if let image = facts.image { row("Image", image) }
                    if let name = detail?.machineName, !name.isEmpty { row("Mac", name) }
                    if let ip = detail?.address { row("Address", ip) }
                    if let boot = facts.boot { row("Boot", boot) }
                    if let ended = summary.machine.ended { row("Mac ended", ended) }
                    if let finish {
                        row("Finished", finish.outcome.text + (finish.summary.isEmpty ? "" : ". \(finish.summary)"))
                        if let branch = finish.ref.branch, !branch.isEmpty { row("Branch", branch) }
                        if let commit = finish.ref.commit, !commit.isEmpty { row("Commit", commit) }
                        if let pr = finish.ref.pr, !pr.isEmpty {
                            GridRow {
                                label("PR")
                                if let url = finish.ref.prURL {
                                    Link(pr, destination: url).textStyle(.body)
                                } else {
                                    Text(pr).textStyle(.body).foregroundStyle(Palette.text).textSelection(.enabled)
                                }
                            }
                        }
                    }
                    GridRow {
                        label("Run ID")
                        HStack(spacing: Gap.x8) {
                            Text(summary.runId).textStyle(.body).foregroundStyle(Palette.text).textSelection(.enabled)
                            IconButton(icon: .copy, name: "Copy run ID") { shell.copyRunID() }
                        }
                    }
                }
                if let task, !task.isEmpty {
                    Text("Task").textStyle(.captionEmphasis).foregroundStyle(Palette.textSecondary).padding(.top, Gap.x4)
                    AgentMarkdown(text: task)
                }
            }
            .padding(Gap.x16)
            .frame(width: 420, alignment: .leading)
        }
        .visibleScroller()
        .frame(maxHeight: 520)
        .fixedSize(horizontal: true, vertical: false)
    }

    private func label(_ text: String) -> some View {
        Text(text).textStyle(.caption).foregroundStyle(Palette.textSecondary)
    }

    private func row(_ name: String, _ value: String) -> some View {
        GridRow {
            label(name)
            Text(value).textStyle(.body).foregroundStyle(Palette.text).textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
        }
    }
}

/// What Details says beyond the summary, as the old window's RunInfo worked it out: the run's
/// own detail first, else what the run list carries (an older detail, or none read yet).
struct RunDetailFacts: Equatable {
    var image: String?
    var models: VerifierModels?
    /// How long the Mac took to boot: "12.4 s".
    var boot: String?
    var frames: Int
    var messages: Int

    init(detail: RunDetail?, listed: RunSummary?, messages held: [Message]?, frames heldFrames: [Frame]?) {
        image = [detail?.image, listed?.image].compactMap { $0 }.first { !$0.isEmpty }
        models = detail?.models ?? listed?.models
        boot = detail?.machine?.bootSeconds.map { String(format: "%.1f s", $0) }
        frames = heldFrames?.count ?? listed?.frames ?? 0
        messages = held?.count ?? listed?.messages ?? 0
    }
}

/// Destroying the Mac, asked inline under the header (the old top bar asked the same way).
struct DestroyConfirmBanner: View {
    var busy: Bool
    var keep: () -> Void
    var destroy: () -> Void

    var body: some View {
        HStack(spacing: Gap.x8) {
            IconView(icon: .trash, size: 14).foregroundStyle(Palette.fail)
            Text("Destroy this Mac? The recording and the record stay.").textStyle(.body).foregroundStyle(Palette.text).lineLimit(1)
            Spacer(minLength: Gap.x8)
            Button("Keep it", action: keep).buttonStyle(ActionButtonStyle(kind: .secondary))
            Button("Destroy", action: destroy).buttonStyle(ActionButtonStyle(kind: .primary, loading: busy))
                .accessibilityIdentifier("destroy.confirm")
        }
        .padding(.leading, Gap.x24)
        .padding(.trailing, Gap.x12)
        .frame(height: 44)
        .background(Palette.failSubtle)
        .overlay(alignment: .bottom) { Rectangle().fill(Palette.border).frame(height: 1) }
    }
}

/// What Greenroom refused or failed, in its words, until dismissed.
struct ErrorBanner: View {
    var text: String
    var dismiss: () -> Void

    var body: some View {
        HStack(spacing: Gap.x8) {
            StatusGlyph(kind: .failed, color: .fail)
            Text(text).textStyle(.body).foregroundStyle(Palette.text).lineLimit(1).help(text)
            Spacer(minLength: Gap.x8)
            IconButton(icon: .close, name: "Dismiss", action: dismiss)
        }
        .padding(.leading, Gap.x24)
        .padding(.trailing, Gap.x12)
        .frame(height: 40)
        .background(Palette.failSubtle)
        .overlay(alignment: .bottom) { Rectangle().fill(Palette.border).frame(height: 1) }
    }
}

/// The accept or rejection waiting out its undo: what, on which run, and Undo (U) with the
/// seconds left.
struct UndoToast: View {
    let shell: ShellModel

    var body: some View {
        // Ticks only while a choice is held and the window shows (companion ADR 0021).
        if shell.store.verdictUndo.pending != nil {
            if OnScreen.shared.visible {
                TimelineView(.periodic(from: .now, by: 0.25)) { context in toast(now: context.date) }
            } else {
                toast(now: Date())
            }
        }
    }

    @ViewBuilder
    private func toast(now: Date) -> some View {
        if let held = shell.heldChoice(now: now) {
            HStack(spacing: Gap.x12) {
                StatusGlyph(kind: held.choice.kind == .accept ? .passed : .failed, color: held.choice.kind == .accept ? .pass : .fail)
                Text(held.choice.kind == .accept ? "Accepted \(held.name)" : "Rejected \(held.name)")
                    .textStyle(.body).foregroundStyle(Palette.text).lineLimit(1)
                Text("\(held.seconds)").textStyle(.captionEmphasis).monospacedDigit().foregroundStyle(Palette.textSecondary)
                    .help("Goes out in \(held.seconds) s")
                Button("Undo") { shell.undoVerdictChoice() }
                    .buttonStyle(ActionButtonStyle(kind: .secondary))
                    .help("Undo (U)")
                    .accessibilityIdentifier("undo")
            }
            .padding(.leading, Gap.x16)
            .padding(.trailing, Gap.x8)
            .frame(height: 44)
            .background(RoundedRectangle(cornerRadius: Corner.sheet).fill(Palette.bgRaised))
            .overlay(RoundedRectangle(cornerRadius: Corner.sheet).strokeBorder(Palette.border, lineWidth: 1))
            .shadow(color: .black.opacity(Elevation.raisedOpacity), radius: Elevation.raisedRadius / 2, y: Elevation.raisedY)
            .padding(.bottom, Gap.x16)
            .transition(.opacity)
        }
    }
}

/// The handle between the stage and Activity: drag to size the column (remembered), a
/// double click puts it back to its default width.
struct InspectorDivider: View {
    @Binding var width: Double
    var range: ClosedRange<Double>
    @State private var start: Double?
    @State private var hovering = false

    static let defaultWidth: Double = 400

    var body: some View {
        Rectangle().fill(hovering || start != nil ? Palette.accent : Palette.border)
            .frame(width: hovering || start != nil ? 2 : 1)
            .frame(width: 9)
            .contentShape(Rectangle())
            // The system's pointer style, never an NSCursor push: a hover end that never comes
            // (the app sent to the back mid-hover) left a pushed cursor on the stack.
            .pointerStyle(.columnResize)
            .onHover { hovering = $0 }
            .gesture(DragGesture(minimumDistance: 1, coordinateSpace: .global)
                .onChanged { value in
                    if start == nil { start = width }
                    // Dragging left widens the column.
                    width = min(max((start ?? width) - value.translation.width, range.lowerBound), range.upperBound)
                }
                .onEnded { _ in start = nil })
            .onTapGesture(count: 2) { width = Self.defaultWidth }
            .padding(.horizontal, -4)
            .zIndex(1)
            .help("Drag to size Activity")
            .accessibilityElement()
            .accessibilityLabel("Activity width")
            .accessibilityValue("\(Int(width)) points")
            .accessibilityAdjustableAction { direction in
                width = min(max(width + (direction == .increment ? -20 : 20), range.lowerBound), range.upperBound)
            }
    }
}
