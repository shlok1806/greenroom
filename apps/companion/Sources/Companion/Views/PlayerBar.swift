import SwiftUI

// The player under the screen's well (ADR 0008, 0011): play and pause, the scrubber with
// a tick where each step began (errored ones in the failure role, the verdict's evidence
// marked), where the playhead is in words under the track, the speed and Follow Live.
// Every control has a registry key (space, ← →, G, f, ⌘L); nothing here is the only way.

/// Where the picture comes from: exactly one of live, connecting, recording, driving.
enum ScreenSourceState: Equatable {
    case live
    case connecting
    /// The recording, at `position` of `total` since the run started.
    case recording(position: TimeInterval, total: TimeInterval)
    case driving
}

/// Marks on the scrubber: steps that errored, the verdict's evidence, and evidence an
/// earlier, replaced verdict cited.
struct TrackMarks {
    var failed: Set<Int>
    var evidence: Set<Int>
    var superseded: Set<Int>
    var verdict: String?
}

/// Play, the track, and under it the step at the playhead in words, the speed and
/// Follow Live. Status lines (the live screen down, control ended) close it.
struct PlayerBar: View {
    @Binding var player: PlayerModel
    let steps: [Step]
    let verdict: VerdictState?
    /// Verdicts the conversation has replaced, whose evidence stays marked as superseded.
    let supersededEvidence: Set<Int>
    @Binding var hoverIndex: Int?
    let driving: Bool
    let machineIsReady: Bool
    let liveFailure: String?
    let endedReason: String?
    let goLive: () -> Void

    @Environment(\.theme) private var theme

    /// The play button's width: the words under the track line up with the track.
    private static let playWidth: CGFloat = 28

    var body: some View {
        VStack(alignment: .leading, spacing: Space.xs) {
            HStack(spacing: Space.s) {
                playButton
                FrameTrack(
                    frames: player.frames,
                    index: player.index,
                    marks: marks,
                    enabled: !driving,
                    hoverIndex: $hoverIndex
                ) { newIndex in
                    player.live = false
                    player.index = newIndex
                }
                .frame(height: 24)
            }

            // In a narrow stage the legend, speed and Follow Live wrap under the position,
            // never pushing the stage wider than its column.
            ViewThatFits(in: .horizontal) {
                HStack(spacing: Space.m) {
                    position
                        .frame(maxWidth: .infinity, alignment: .leading)
                    legend
                    speed
                    liveButton
                }
                VStack(alignment: .leading, spacing: Space.s) {
                    position
                        .frame(maxWidth: .infinity, alignment: .leading)
                    HStack(spacing: Space.m) {
                        legend
                        Spacer(minLength: 0)
                        speed
                        liveButton
                    }
                }
            }
            .padding(.leading, Self.playWidth + Space.s)

            VStack(alignment: .leading, spacing: Space.xs) {
                if let liveFailure {
                    statusLine("Live screen down, showing the recording: \(liveFailure)", tint: theme.dim)
                }
                // Outside the ready check: a stopped machine hides the button but keeps the reason.
                if let endedReason {
                    statusLine("! Control ended: \(endedReason)", tint: theme.color(.attention))
                }
            }
            .padding(.leading, Self.playWidth + Space.s)
            .padding(.top, liveFailure != nil || endedReason != nil ? Space.xs : 0)
        }
        .padding(.horizontal, Space.l)
    }

    private var playButton: some View {
        Button {
            player.playing.toggle()
        } label: {
            Text(player.playing ? "❚❚" : "▶")
                .font(Typeface.monoBold.font(size: TypeScale.small))
                .frame(width: Self.playWidth, height: 24)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .hoverHighlight(radius: Radius.sm)
        .disabled(player.frames.count < 2 || driving)
        .help(player.playing ? "Pause (\(ActionRegistry.label(.play)))" : "Play the recording (\(ActionRegistry.label(.play)))")
        .accessibilityLabel(player.playing ? "Pause" : "Play")
    }

    private var speed: some View {
        SegmentedSwitch(options: [(PlayerModel.Speed.normal, "1×"), (PlayerModel.Speed.fast, "4×")],
                        selection: $player.speed, small: true)
            .disabled(driving || player.frames.count < 2)
            .help("Playback speed (\(ActionRegistry.label(.speed)))")
    }

    @ViewBuilder
    private var liveButton: some View {
        if machineIsReady, !player.live, !driving {
            Button("Follow Live") { goLive() }
                .buttonStyle(.quiet(small: true))
                .fixedSize()
                .help("Show the machine's screen as it happens (\(ActionRegistry.label(.followLive)))")
        }
    }

    private func statusLine(_ text: String, tint: Color) -> some View {
        Text(text)
            .monoStyle(size: TypeScale.monoSmall)
            .foregroundStyle(tint)
            .lineLimit(2)
            .truncationMode(.tail)
            .textSelection(.enabled)
            .help(text)
    }

    /// The step at the pointer while hovering, else at the playhead, in plain words. The
    /// words give way at the end, so the number always stays.
    private var position: some View {
        let index = hoverIndex ?? player.index
        let frame = player.frames.indices.contains(index) ? player.frames[index] : nil
        let step = frame.flatMap { frame in steps.last { $0.seq <= frame.step } }
        return HStack(spacing: Space.s) {
            if let step {
                Text("Step \(step.seq)")
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
                    .fixedSize()
                Text(StepSummary.phrase(for: step, in: steps))
                    .foregroundStyle(step.outcome.isFailure ? theme.color(.failure) : theme.foreground)
                    .lineLimit(1)
                    .truncationMode(.tail)
                    // A small ideal width: the bar's one-line layout is chosen by what
                    // must fit, not by how long a command is.
                    .frame(minWidth: 0, idealWidth: 60, maxWidth: .infinity, alignment: .leading)
                    .layoutPriority(-1)
            } else if player.frames.isEmpty {
                Text("No frames yet").foregroundStyle(.secondary)
            } else {
                Text("Before the first step").foregroundStyle(.secondary)
            }
        }
        .monoStyle(size: TypeScale.small)
        .lineLimit(1)
        .help(frame.map { "\(Chrome.stamp($0.at)) (\(Chrome.zone))\n\($0.file)" } ?? "")
    }

    @ViewBuilder
    private var legend: some View {
        let marks = marks
        if !marks.failed.isEmpty || !marks.evidence.isEmpty || !marks.superseded.isEmpty {
            HStack(spacing: Space.m) {
                if !marks.failed.isEmpty {
                    HStack(spacing: Space.xs) {
                        Rectangle().fill(theme.color(.failure)).frame(width: 2, height: 9)
                        Text("errored")
                    }
                }
                if !marks.evidence.isEmpty {
                    HStack(spacing: Space.xs) {
                        Text("◆").foregroundStyle(theme.outcome(marks.verdict))
                        Text("evidence")
                    }
                }
                if !marks.superseded.isEmpty {
                    HStack(spacing: Space.xs) {
                        Text("◇")
                        Text("earlier")
                    }
                }
            }
            .monoStyle(size: TypeScale.monoSmall)
            .foregroundStyle(.secondary)
            .fixedSize()
            .help("Red ticks: errors. Filled diamonds: cited by the verdict. Hollow: cited by an earlier verdict.")
        }
    }

    private var marks: TrackMarks {
        let failed = Set(steps.filter { $0.outcome.isFailure }.map(\.seq))
        let evidence = Set((verdict?.evidence ?? []).compactMap { Evidence.parse($0).step })
        return TrackMarks(failed: failed, evidence: evidence, superseded: supersededEvidence.subtracting(evidence),
                          verdict: verdict?.verdict)
    }
}

/// Where the picture comes from, in one quiet line over the well: "● live" in the live
/// role, "⠋ connecting", "recording 4:17 of 11:34", "● driving". One source, one truth.
struct SourceLabel: View {
    let state: ScreenSourceState

    @Environment(\.theme) private var theme

    var body: some View {
        HStack(spacing: Space.xs) {
            switch state {
            case .live:
                LiveMark()
                Text("live").foregroundStyle(theme.color(.live))
            case .connecting:
                Spinner(size: TypeScale.monoSmall)
                Text("connecting")
            case .recording(let position, let total):
                Text("recording").foregroundStyle(.secondary)
                Text("\(Chrome.clock(position)) of \(Chrome.clock(total))")
                    .monospacedDigit()
            case .driving:
                Text("●")
                Text("driving")
            }
        }
        .monoStyle(.monoMedium, size: TypeScale.monoSmall)
        .foregroundStyle(state == .driving ? theme.color(.driving) : theme.foreground)
        .lineLimit(1)
        .fixedSize()
        .help(help)
        .accessibilityElement(children: .combine)
    }

    private var help: String {
        switch state {
        case .live: "The machine's screen as it happens"
        case .connecting: "Connecting to the live screen. The recording shows until then."
        case .recording: "A recorded frame. Times count from the run's start."
        case .driving: "Your mouse and keys go to the machine"
        }
    }
}

/// A seek track with a tick wherever a step began (errored steps in the failure role,
/// taller), the verdicts' evidence marked above it, and an olive thumb. Idle stretches
/// are squeezed so steps do not bunch up; marks closer than a few points merge into one,
/// with the count in its tooltip.
struct FrameTrack: View {
    let frames: [Frame]
    let index: Int
    let marks: TrackMarks
    var enabled: Bool = true
    @Binding var hoverIndex: Int?
    let onSeek: (Int) -> Void

    private static let trackHeight: CGFloat = 4
    private static let thumb: CGFloat = 12

    @Environment(\.theme) private var theme

    var body: some View {
        GeometryReader { geometry in
            let width = geometry.size.width
            let timeline = FrameTimeline(frames: frames, width: width)
            let playhead = timeline.x(of: index)
            ZStack(alignment: .leading) {
                Capsule()
                    .fill(theme.hairline)
                    .frame(height: Self.trackHeight)

                Capsule()
                    .fill(enabled ? theme.foreground.opacity(0.45) : theme.dim.opacity(0.6))
                    .frame(width: max(playhead, Self.trackHeight), height: Self.trackHeight)

                Canvas { context, size in
                    let mid = size.height / 2
                    for tick in timeline.stepTicks(for: frames, failed: marks.failed) {
                        let x = min(max(tick.x, 0), size.width - (tick.failed ? 2 : 1))
                        let height: CGFloat = tick.failed ? 10 : 7
                        let rect = CGRect(x: x, y: mid - height / 2, width: tick.failed ? 2 : 1, height: height)
                        context.fill(Path(rect), with: .color(tick.failed ? theme.color(.failure) : theme.dim.opacity(0.7)))
                    }
                }
                .allowsHitTesting(false)
                .accessibilityHidden(true)

                let merged = FrameTimeline.cluster(
                    timeline.marks(for: frames, failed: [], evidence: marks.evidence.union(marks.superseded),
                                   verdict: marks.verdict),
                    minGap: 8
                )
                ForEach(merged, id: \.first.step) { group in
                    let superseded = marks.superseded.contains(group.first.step) && !marks.evidence.contains(group.first.step)
                    Text(superseded ? "◇" : "◆")
                        .font(.system(size: TypeScale.mark, weight: .bold))
                        .foregroundStyle(superseded ? theme.dim : theme.outcome(marks.verdict))
                        .frame(width: 11, height: 11)
                        .contentShape(Rectangle())
                        .help(markHelp(group))
                        .offset(x: min(max(group.first.x - 5.5, 0), width - 11), y: -11)
                }

                if let hoverIndex, enabled {
                    Rectangle()
                        .fill(theme.dim)
                        .frame(width: 1, height: 14)
                        .offset(x: min(max(timeline.x(of: hoverIndex), 0), width - 1))
                        .allowsHitTesting(false)
                }

                // Clamped so the thumb does not hang off either end.
                Circle()
                    .fill(enabled ? theme.brand : theme.dim)
                    .overlay(Circle().strokeBorder(theme.background, lineWidth: 2))
                    .frame(width: Self.thumb, height: Self.thumb)
                    .offset(x: min(max(playhead - Self.thumb / 2, 0), max(width - Self.thumb, 0)))
                    .opacity(frames.isEmpty ? 0 : 1)
                    .allowsHitTesting(false)
            }
            .frame(width: width, height: geometry.size.height, alignment: .leading)
            .contentShape(Rectangle())
            .gesture(
                DragGesture(minimumDistance: 0)
                    .onChanged { value in
                        guard enabled, frames.count > 1 else { return }
                        onSeek(timeline.index(atX: value.location.x))
                    }
            )
            .onContinuousHover { phase in
                switch phase {
                case .active(let point):
                    guard enabled, frames.count > 1 else { return }
                    hoverIndex = timeline.index(atX: point.x)
                case .ended:
                    hoverIndex = nil
                }
            }
            .opacity(frames.count > 1 ? 1 : 0.4)
        }
        .accessibilityElement()
        .accessibilityLabel("Recording position")
        .accessibilityValue(frames.isEmpty ? "no frames" : "frame \(index + 1) of \(frames.count)")
        .accessibilityAdjustableAction { direction in
            guard enabled, !frames.isEmpty else { return }
            switch direction {
            case .increment: onSeek(min(index + 1, frames.count - 1))
            case .decrement: onSeek(max(index - 1, 0))
            @unknown default: break
            }
        }
    }

    private func markHelp(_ group: FrameTimeline.MarkGroup) -> String {
        let steps = group.marks.map { "\($0.step)" }.joined(separator: ", ")
        if group.marks.count > 1 { return "Steps \(steps), cited as evidence" }
        return marks.superseded.contains(group.first.step) && !marks.evidence.contains(group.first.step)
            ? "Step \(steps), cited by an earlier verdict"
            : "Step \(steps), cited by the verdict"
    }
}
