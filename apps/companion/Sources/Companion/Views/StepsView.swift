import AppKit
import SwiftUI

/// The evidence timeline: every tool call the run recorded.
struct StepsView: View {
    @Bindable var store: RunStore
    let runId: String

    private var steps: [Step] { store.steps[runId] ?? [] }

    var body: some View {
        List {
            ForEach(steps) { step in
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
            Button {
                expanded.toggle()
            } label: {
                HStack(spacing: 8) {
                    Image(systemName: expanded ? "chevron.down" : "chevron.right")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
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
            }
            .buttonStyle(.plain)

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
