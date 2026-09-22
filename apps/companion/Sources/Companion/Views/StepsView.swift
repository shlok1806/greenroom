import AppKit
import SwiftUI

/// The evidence timeline: every tool call the run recorded.
///
/// It is a table, because that is what it is: one row per step, in fixed
/// columns, so the eye runs down the times, the tools and the durations rather
/// than reading each row from the start. The column that earns the most is the
/// summary: a hundred and thirty rows all reading `machine_input` say nothing,
/// while "click 35%, 40%" and "type \"hi shlok\"" say what happened.
struct StepsView: View {
    @Bindable var store: RunStore
    let runId: String

    private var steps: [Step] { store.steps[runId] ?? [] }

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 0, pinnedViews: .sectionHeaders) {
                Section {
                    // Keyed by the whole record, not by `seq`: a daemon that
                    // was restarted mid-run used to hand out a number twice,
                    // and two rows with one id make SwiftUI draw the first
                    // one's content for both.
                    ForEach(Array(steps.enumerated()), id: \.element) { position, step in
                        StepRow(store: store, runId: runId, step: step, shaded: position.isMultiple(of: 2))
                    }
                } header: {
                    StepsHeader()
                }
            }
        }
        .overlay {
            if steps.isEmpty {
                ContentUnavailableView(
                    "No steps",
                    systemImage: "list.bullet.rectangle",
                    description: Text("A step is recorded for every tool call against the machine.")
                )
            }
        }
    }
}

/// The widths the header and every row share. Two views agreeing on a number
/// by accident is how a table stops lining up.
private enum StepColumn {
    static let chevron: CGFloat = 16
    static let seq: CGFloat = 40
    static let time: CGFloat = 66
    static let tool: CGFloat = 132
    static let duration: CGFloat = 62
    static let gap: CGFloat = 10
}

private struct StepsHeader: View {
    var body: some View {
        HStack(spacing: StepColumn.gap) {
            Spacer().frame(width: StepColumn.chevron)
            Text("#").frame(width: StepColumn.seq, alignment: .trailing)
            Text("Time").frame(width: StepColumn.time, alignment: .leading)
            Text("Tool").frame(width: StepColumn.tool, alignment: .leading)
            Text("What it did").frame(maxWidth: .infinity, alignment: .leading)
            Text("Took").frame(width: StepColumn.duration, alignment: .trailing)
        }
        .font(.system(size: 9, weight: .semibold))
        .tracking(0.6)
        .foregroundStyle(.tertiary)
        .textCase(.uppercase)
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.bar)
        .overlay(alignment: .bottom) { Divider() }
    }
}

private struct StepRow: View {
    @Bindable var store: RunStore
    let runId: String
    let step: Step
    /// Alternating rows, which is what lets an eye stay on one line while it
    /// crosses a wide window.
    let shaded: Bool

    @State private var expanded = false
    @State private var thumbnail: NSImage?
    @State private var hovering = false

    private var failed: Bool { !(step.error ?? "").isEmpty }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            // Two targets, because one click cannot do both: opening the JSON
            // and jumping to the Screen tab used to fire together, so the rows
            // expanded on a tab the person was already being carried away from
            // and the JSON was unreadable. The chevron opens the step; the
            // rest of the row goes and looks at it.
            HStack(spacing: StepColumn.gap) {
                Button {
                    expanded.toggle()
                } label: {
                    Image(systemName: expanded ? "chevron.down" : "chevron.right")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .frame(width: StepColumn.chevron, alignment: .leading)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(expanded ? "Hide this step's input and output" : "Show this step's input and output")

                Button {
                    store.requestSeek(runId: runId, step: step.seq)
                } label: {
                    HStack(spacing: StepColumn.gap) {
                        Text("\(step.seq)")
                            .font(.caption.monospacedDigit())
                            .foregroundStyle(.tertiary)
                            .frame(width: StepColumn.seq, alignment: .trailing)

                        Text(Chrome.timeOfDay(step.at))
                            .font(.caption.monospacedDigit())
                            .foregroundStyle(.secondary)
                            .frame(width: StepColumn.time, alignment: .leading)

                        HStack(spacing: 4) {
                            if failed {
                                Image(systemName: "exclamationmark.triangle.fill")
                                    .font(.caption2)
                                    .foregroundStyle(.red)
                            }
                            Text(step.tool)
                                .font(.system(.caption, design: .monospaced))
                                .lineLimit(1)
                                .truncationMode(.middle)
                        }
                        .frame(width: StepColumn.tool, alignment: .leading)

                        Text(StepSummary.line(for: step))
                            .font(.system(.caption, design: .monospaced))
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                            .truncationMode(.tail)
                            .frame(maxWidth: .infinity, alignment: .leading)

                        Text(Chrome.duration(step.durationMs))
                            .font(.caption.monospacedDigit())
                            .foregroundStyle(.tertiary)
                            .frame(width: StepColumn.duration, alignment: .trailing)
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help("Show the screen at this step")
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 5)

            if failed, let error = step.error {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .textSelection(.enabled)
                    .padding(.horizontal, 12)
                    .padding(.leading, StepColumn.chevron + StepColumn.gap)
                    .padding(.bottom, 6)
            }

            if expanded {
                VStack(alignment: .leading, spacing: 8) {
                    if let thumbnail {
                        Image(nsImage: thumbnail)
                            .resizable()
                            .scaledToFit()
                            .frame(maxWidth: 420, maxHeight: 260)
                            .clipShape(RoundedRectangle(cornerRadius: 4))
                    }
                    if let input = step.input {
                        json("Input", input)
                    }
                    if let output = step.output {
                        json("Output", output)
                    }
                }
                .padding(.horizontal, 12)
                .padding(.leading, StepColumn.chevron + StepColumn.gap)
                .padding(.bottom, 10)
            }
        }
        .background(background)
        .onHover { hovering = $0 }
        // The screenshot is fetched only when the row is opened. Loading a
        // thumbnail for every one of a few hundred rows was a few hundred
        // requests for pictures nobody had asked to see.
        .task(id: expanded) {
            guard expanded, let name = step.screenshotArtifact, thumbnail == nil else { return }
            let loaded = await store.artifactImage(runId: runId, name: name)
            guard !Task.isCancelled else { return }
            thumbnail = loaded
        }
    }

    @ViewBuilder
    private var background: some View {
        if hovering {
            Color.primary.opacity(0.06)
        } else if shaded {
            Color.primary.opacity(0.025)
        } else {
            Color.clear
        }
    }

    private func json(_ label: String, _ value: JSONValue) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(label.uppercased())
                .font(.system(size: 9, weight: .semibold))
                .tracking(0.6)
                .foregroundStyle(.tertiary)
            Text(value.prettyPrinted)
                .font(.system(.caption, design: .monospaced))
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(8)
                .background(.quinary, in: RoundedRectangle(cornerRadius: 4))
        }
    }
}
