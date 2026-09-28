import SwiftUI

/// The raw record of the step at the playhead (the old Steps pane's detail, redesign 7): the
/// tool's own name, when and how long, why it failed, and its input and output JSON. A fold at
/// the foot of Activity, closed by default; it follows the player, so a chip or a log line
/// clicked (which seeks) opens its record here.
struct StepRecordFold: View {
    var step: Step
    var total: Int
    @AppStorage("activityRecordOpen", store: AppDefaults.shared) private var open = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Button { open.toggle() } label: {
                HStack(spacing: Gap.x8) {
                    IconView(icon: .chevronRight, size: 12)
                        .foregroundStyle(Palette.textSecondary)
                        .rotationEffect(.degrees(open ? 90 : 0))
                        .animation(reduceMotion ? nil : Curve.tailwindDefault.animation(Motion.settle), value: open)
                    Text("Step \(step.seq) of \(total), the raw call").textStyle(.captionEmphasis).foregroundStyle(Palette.textSecondary)
                    Spacer(minLength: Gap.x8)
                    if step.isRisky {
                        StatusGlyph(kind: .warning, color: .wait, size: 12)
                            .help("This command can destroy data or change the Mac for good")
                    }
                    if !open {
                        Text(step.tool).font(.system(size: 11, design: .monospaced)).foregroundStyle(Palette.textSecondary).lineLimit(1)
                    }
                }
                .padding(.horizontal, Gap.x12)
                .frame(height: 30)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help(open ? "Hide the step's input and output" : "Show the step's tool, input and output as recorded")
            .accessibilityIdentifier("activity.record")
            .accessibilityValue(open ? "expanded" : "collapsed")

            if open {
                ScrollView {
                    StepRecordView(step: step).padding(Gap.x12).padding(.trailing, Gap.x8)
                }
                .visibleScroller()
                .frame(maxHeight: 260)
                .transition(.opacity)
            }
        }
        .overlay(alignment: .top) { Rectangle().fill(Palette.border).frame(height: 1) }
        .background(Palette.bg)
    }
}

/// A step's record: the outcome, the tool, stamp and duration, then Input and Output.
struct StepRecordView: View {
    var step: Step

    var body: some View {
        VStack(alignment: .leading, spacing: Gap.x8) {
            switch step.outcome {
            case .ok:
                EmptyView()
            case .exit(let code):
                Text("The command exited \(code).").textStyle(.bodyEmphasis).foregroundStyle(Palette.fail)
            case .error(let error):
                Text("The tool call failed. Its result is unknown.").textStyle(.bodyEmphasis).foregroundStyle(Palette.fail)
                Text(error)
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundStyle(Palette.fail)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(Gap.x8)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(RoundedRectangle(cornerRadius: Corner.control).fill(Palette.failSubtle))
            }
            Text("\(step.tool), \(step.at.formatted(date: .omitted, time: .standard)), took \(String(format: "%.1f s", Double(step.durationMs) / 1000))")
                .font(.system(size: 11, design: .monospaced))
                .foregroundStyle(Palette.textSecondary)
                .textSelection(.enabled)
            if let input = step.input {
                block("Input", input.prettyPrinted)
            }
            if let output = step.output {
                // After a tool error the output is what came back before it failed, and its
                // zero values (exitCode 0 too) are not a result.
                block(step.error != nil ? "Partial output" : "Output", output.prettyPrinted)
                    .help(step.error != nil ? "What came back before the call failed. These values, exitCode 0 too, are not a result." : "")
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// JSON keeps its lines and scrolls sideways, with Copy.
    private func block(_ title: String, _ text: String) -> some View {
        VStack(alignment: .leading, spacing: Gap.x4) {
            Text(title).textStyle(.captionEmphasis).foregroundStyle(Palette.textSecondary)
            AgentCodeBlock(text: text, language: "json")
        }
    }
}
