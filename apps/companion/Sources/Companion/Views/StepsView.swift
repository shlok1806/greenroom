import AppKit
import SwiftUI

/// The evidence timeline: every tool call the run recorded.
struct StepsView: View {
    @Bindable var store: RunStore
    let runId: String

    private var steps: [Step] { store.steps[runId] ?? [] }

    var body: some View {
        List {
            // Keyed by the whole record, not by `seq`: a daemon that was
            // restarted mid-run used to hand out a number twice, and two rows
            // with one id make SwiftUI draw the first one's content for both.
            ForEach(steps, id: \.self) { step in
                StepRow(store: store, runId: runId, step: step)
            }
        }
        .listStyle(.inset)
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

private struct StepRow: View {
    @Bindable var store: RunStore
    let runId: String
    let step: Step

    @State private var expanded = false
    @State private var thumbnail: NSImage?

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            // Two targets, because one click cannot do both: opening the JSON
            // and jumping to the Screen tab used to fire together, so the rows
            // expanded on a tab the person was already being carried away from
            // and the JSON was unreadable. The chevron opens the step; the
            // rest of the row goes and looks at it.
            HStack(spacing: 8) {
                Button {
                    expanded.toggle()
                } label: {
                    Image(systemName: expanded ? "chevron.down" : "chevron.right")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .frame(width: 14, alignment: .leading)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(expanded ? "Hide this step's input and output" : "Show this step's input and output")

                Button {
                    store.requestSeek(runId: runId, step: step.seq)
                } label: {
                    HStack(spacing: 8) {
                        Text(String(format: "%03d", step.seq))
                            .font(.caption.monospaced())
                            .foregroundStyle(.secondary)
                        Text(step.tool)
                            .font(.body.monospaced())
                        Spacer()
                        Text(Chrome.duration(step.durationMs))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help("Show the screen at this step")
            }

            if let error = step.error, !error.isEmpty {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .padding(.leading, 22)
            }

            if let thumbnail {
                Image(nsImage: thumbnail)
                    .resizable()
                    .scaledToFit()
                    .frame(maxHeight: 140)
                    .padding(.leading, 22)
            }

            if expanded {
                VStack(alignment: .leading, spacing: 6) {
                    if let input = step.input {
                        json("Input", input)
                    }
                    if let output = step.output {
                        json("Output", output)
                    }
                }
                .padding(.leading, 22)
            }
        }
        .padding(.vertical, 2)
        .task(id: step.screenshotArtifact) {
            guard let name = step.screenshotArtifact, thumbnail == nil else { return }
            if let data = await store.artifact(runId: runId, name: name) {
                thumbnail = NSImage(data: data)
            }
        }
    }

    private func json(_ label: String, _ value: JSONValue) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(label.uppercased())
                .font(.caption2)
                .foregroundStyle(.tertiary)
            Text(value.prettyPrinted)
                .font(.caption.monospaced())
                .textSelection(.enabled)
        }
    }
}
