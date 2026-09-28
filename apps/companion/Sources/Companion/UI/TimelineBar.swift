import SwiftUI

/// The player's transport bar under the picture (redesign 7), modelled on a video player's:
/// the scrub bar across the whole width (a mark per step, red where a step errored or a check
/// failed, a numbered point where a check was proven, an amber flag where you had control,
/// dotted where nothing happened, amber where the screen sent no picture; hover shows the
/// frame and one line; drag to scrub), then the controls: Live (or Go live once you scrub
/// back), frame back, play or pause, frame forward, the clock ("1:42 / 4:18"), the failures
/// to jump through, 1x/2x/4x and picture only. Every figure's words are in its tooltip.
struct TimelineBar: View {
    @Bindable var shell: ShellModel
    var summary: Summary
    @Environment(\.frozenNow) private var frozenNow

    var body: some View {
        Group {
            if ShellModel.isLive(summary), frozenNow == nil {
                TimelineView(.periodic(from: .now, by: 1)) { context in content(now: context.date) }
            } else {
                content(now: frozenNow ?? Date())
            }
        }
        .frame(height: TimelineBar.height)
        .cloneScope("Timeline")
    }

    static let height: CGFloat = 64

    private func content(now: Date) -> some View {
        let t = shell.timeline(now: now)
        let current = shell.currentSeconds(t)
        let failures = t.failures.count
        let steps = shell.store.steps[summary.runId] ?? []
        return VStack(spacing: 4) {
            TimelineTrack(shell: shell, summary: summary, timeline: t, current: current, steps: steps)
                .cloneScope("Track")
            HStack(spacing: Gap.x4) {
                if t.live {
                    LivePin(following: shell.showsLive) { shell.goLive() }
                        .cloneScope("Live")
                        .padding(.trailing, Gap.x4)
                }
                IconButton(icon: .skipBack, name: "Previous frame (Left)") { shell.moveFrame(by: -1) }
                    .disabled(t.frames.isEmpty)
                    .cloneScope("Icon button[0]")
                IconButton(icon: shell.playing ? .pause : .play, name: shell.playing ? "Pause (Space)" : "Play the recording (Space)") {
                    shell.togglePlay()
                }
                .disabled(t.frames.isEmpty)
                .accessibilityIdentifier("timeline.play")
                .cloneScope("Icon button[1]")
                IconButton(icon: .skipForward, name: "Next frame (Right)") { shell.moveFrame(by: 1) }
                    .disabled(t.frames.isEmpty)
                    .cloneScope("Icon button[2]")
                Text(TimelineWords.clock(current, of: t.duration))
                    .textStyle(.caption)
                    .monospacedDigit()
                    .foregroundStyle(Palette.textSecondary)
                    .fixedSize()
                    .padding(.leading, Gap.x4)
                    .help(clockHelp(t, current: current, steps: steps.count))
                    .accessibilityLabel("\(Clock.elapsed(Int(current))) of \(Clock.elapsed(Int(t.duration)))")
                    .accessibilityIdentifier("timeline.clock")
                    .clonePart("Clock")
                Spacer(minLength: Gap.x8)
                if failures > 0 {
                    HStack(spacing: 0) {
                        Button { shell.jumpToFailure(forward: false) } label: {
                            IconView(icon: .chevronRight, size: 12).rotationEffect(.degrees(180)).frame(width: 22, height: 22)
                        }
                        .buttonStyle(.plain)
                        .help("Previous failure (Shift-N)")
                        .accessibilityLabel("Previous failure")
                        Rectangle().fill(Palette.fail.opacity(0.3)).frame(width: 1, height: 12)
                            .accessibilityHidden(true)
                        Button { shell.jumpToFailure() } label: {
                            IconView(icon: .chevronRight, size: 12).frame(width: 22, height: 22)
                        }
                        .buttonStyle(.plain)
                        .help("Next failure (N). \(failures) \(failures == 1 ? "failure" : "failures") on the recording")
                        .accessibilityLabel("Next failure")
                        .accessibilityIdentifier("timeline.nextFailure")
                    }
                    .foregroundStyle(Palette.fail)
                    .padding(.horizontal, 2)
                    .frame(height: 24)
                    .background(RoundedRectangle(cornerRadius: Corner.control).fill(Palette.failSubtle))
                    .clonePart("Failures")
                }
                DropdownButton(center: shell.dropdowns, id: "speed", width: 150, items: {
                    [1.0, 2, 4].map { value in
                        DropdownItem(id: "speed-\(Int(value))", title: "\(Int(value))× speed", checked: shell.speed == value) { shell.setSpeed(value) }
                    }
                }) {
                    Text("\(Int(shell.speed))×").textStyle(.captionEmphasis).monospacedDigit()
                        .foregroundStyle(shell.speed == 1 ? Palette.textSecondary : Palette.accent)
                        .frame(width: 30, height: 22)
                        .background(RoundedRectangle(cornerRadius: Corner.control).strokeBorder(Palette.border, lineWidth: 1))
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help("Playing at \(Int(shell.speed))×. Change speed (F)")
                .accessibilityLabel("Speed \(Int(shell.speed))×")
                .accessibilityIdentifier("timeline.speed")
                .clonePart("Speed")
                IconButton(icon: .expand, name: shell.zoomed ? "Show the inspector (Z)" : "Picture only (Z)") { shell.toggleZoom() }
                    .cloneScope("Icon button[3]")
            }
            .frame(height: 28)
            .cloneScope("Controls")
        }
    }

    private func clockHelp(_ t: RecordingTimeline, current: TimeInterval, steps: Int) -> String {
        var parts = ["\(Clock.elapsed(Int(current))) into a \(Clock.elapsed(Int(t.duration))) run"]
        parts.append("\(steps) \(steps == 1 ? "step" : "steps")")
        parts.append("\(t.frames.count) frames recorded")
        let idle = t.spans.filter { $0.kind == .idle }.reduce(0) { $0 + $1.length }
        if idle >= 60 { parts.append("\(Clock.elapsed(Int(idle))) with nothing happening, drawn short") }
        return parts.joined(separator: ", ")
    }
}

/// Live, at the start of a live run's controls: a red dot and "Live" while the picture follows
/// the screen; "Go live" once you scrub back, and a click (or L) returns to the live edge.
struct LivePin: View {
    var following: Bool
    var action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 6) {
                Circle().fill(following ? Palette.fail : Palette.textTertiary).frame(width: 8, height: 8)
                Text(following ? "Live" : "Go live").textStyle(.captionEmphasis)
                    .foregroundStyle(following ? Palette.fail : Palette.text)
            }
            .padding(.horizontal, 10)
            .frame(height: 24)
            .background(RoundedRectangle(cornerRadius: Corner.control).fill(following ? Palette.failSubtle : Palette.bgRaised))
            .overlay(RoundedRectangle(cornerRadius: Corner.control).strokeBorder(following ? .clear : Palette.border, lineWidth: 1))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(following ? "Following the live screen" : "Back to the live screen (L)")
        .accessibilityLabel(following ? "Live" : "Go live")
        .accessibilityIdentifier("timeline.live")
    }
}

/// The bar itself: drawn in one `Canvas` (a run can hold thousands of steps), with the hover
/// card and the drag on top.
struct TimelineTrack: View {
    @Bindable var shell: ShellModel
    var summary: Summary
    var timeline: RecordingTimeline
    var current: TimeInterval
    var steps: [Step]

    @State private var hover: CGFloat?
    @State private var dragging = false
    @Environment(\.redactsGuestScreen) private var redacted

    /// The rail's centre from the top: room above for the numbers and flags.
    static let railY: CGFloat = 20
    static let height: CGFloat = 32

    var body: some View {
        GeometryReader { geo in
            let width = geo.size.width
            let t = timeline
            let marks = t.visibleMarks(width: Double(width))
            ZStack(alignment: .topLeading) {
                Canvas { context, size in
                    draw(t, marks: marks, in: &context, size: size)
                }
                .accessibilityHidden(true)
                if let hover {
                    hoverCard(at: hover, width: width)
                }
            }
            .contentShape(Rectangle())
            .onContinuousHover { phase in
                switch phase {
                case .active(let point): hover = min(max(0, point.x), width)
                case .ended: hover = nil
                }
            }
            .gesture(DragGesture(minimumDistance: 0)
                .onChanged { value in
                    if !dragging {
                        dragging = true
                        shell.pause()
                    }
                    hover = min(max(0, value.location.x), width)
                    shell.seek(to: t.seconds(atFraction: Double(value.location.x / max(width, 1))))
                }
                .onEnded { value in
                    dragging = false
                    // A click on a mark lands on it exactly (a failed check shows its proof).
                    if abs(value.translation.width) < 3,
                       let mark = t.mark(near: Double(value.location.x / max(width, 1)), tolerance: Double(6 / max(width, 1))) {
                        if mark.kind == .failure || mark.kind == .keyFrame, let number = mark.check,
                           shell.checks.indices.contains(number - 1),
                           (shell.checks[number - 1].picture?.step ?? shell.checks[number - 1].step) == mark.step {
                            shell.select(check: shell.checks[number - 1].id)
                        } else {
                            shell.seek(toStep: mark.step)
                        }
                    }
                })
        }
        .frame(height: TimelineTrack.height)
        .accessibilityElement()
        .accessibilityLabel("Recording")
        .accessibilityValue(accessibilityValue)
        .accessibilityIdentifier("timeline.track")
        .accessibilityAdjustableAction { direction in
            shell.moveFrame(by: direction == .increment ? 1 : -1)
        }
        .accessibilityAction(named: "Next failure") { shell.jumpToFailure() }
    }

    private var accessibilityValue: String {
        var words = "\(Clock.elapsed(Int(current))) of \(Clock.elapsed(Int(timeline.duration)))"
        if let step = timeline.step(at: current) { words += ", step \(step)" }
        return words
    }

    // MARK: - Drawing

    private func draw(_ t: RecordingTimeline, marks: [RecordingTimeline.Mark], in context: inout GraphicsContext, size: CGSize) {
        let width = size.width
        let y = Self.railY
        let rail: CGFloat = 4
        func x(_ seconds: TimeInterval) -> CGFloat { CGFloat(t.fraction(of: seconds)) * width }

        // The rail, then what has played.
        context.fill(Path(roundedRect: CGRect(x: 0, y: y - rail / 2, width: width, height: rail), cornerRadius: rail / 2),
                     with: .color(Palette.border))
        let playedTo = x(current)
        if playedTo > 0 {
            context.fill(Path(roundedRect: CGRect(x: 0, y: y - rail / 2, width: playedTo, height: rail), cornerRadius: rail / 2),
                         with: .color(Palette.accent.opacity(0.55)))
        }
        // Idle: the rail gives way to dots. No picture: amber.
        for span in t.spans {
            let x0 = x(span.from), x1 = x(span.to)
            guard x1 - x0 >= 1 else { continue }
            switch span.kind {
            case .idle:
                context.fill(Path(CGRect(x: x0, y: y - rail / 2 - 1, width: x1 - x0, height: rail + 2)), with: .color(Palette.bgStage))
                var dot = x0 + 2
                while dot < x1 - 1 {
                    context.fill(Path(ellipseIn: CGRect(x: dot - 1, y: y - 1, width: 2, height: 2)), with: .color(Palette.textTertiary))
                    dot += 5
                }
            case .noPicture:
                context.fill(Path(CGRect(x: x0, y: y - rail / 2, width: x1 - x0, height: rail)), with: .color(Palette.wait.opacity(0.6)))
            }
        }
        // Plain steps first, so the marks worth finding draw over them.
        for mark in marks where mark.kind == .step {
            let mx = x(mark.at)
            context.fill(Path(CGRect(x: mx - 0.5, y: y - 5, width: 1, height: 10)), with: .color(Palette.textTertiary))
        }
        for mark in marks where mark.kind == .human {
            let mx = x(mark.at)
            var flag = Path()
            flag.move(to: CGPoint(x: mx - 4, y: y - 12))
            flag.addLine(to: CGPoint(x: mx + 4, y: y - 12))
            flag.addLine(to: CGPoint(x: mx, y: y - 6))
            flag.closeSubpath()
            context.fill(flag, with: .color(Palette.wait))
            context.fill(Path(CGRect(x: mx - 0.5, y: y - 6, width: 1, height: 11)), with: .color(Palette.wait))
        }
        for mark in marks where mark.kind == .keyFrame || mark.kind == .failure {
            let mx = x(mark.at)
            let color = mark.kind == .failure ? Palette.fail : Palette.textSecondary
            if mark.check != nil {
                var diamond = Path()
                diamond.move(to: CGPoint(x: mx, y: y - 5))
                diamond.addLine(to: CGPoint(x: mx + 5, y: y))
                diamond.addLine(to: CGPoint(x: mx, y: y + 5))
                diamond.addLine(to: CGPoint(x: mx - 5, y: y))
                diamond.closeSubpath()
                context.fill(diamond, with: .color(color))
                context.stroke(diamond, with: .color(Palette.bgStage), lineWidth: 1)
            } else {
                context.fill(Path(roundedRect: CGRect(x: mx - 1, y: y - 7, width: 2, height: 14), cornerRadius: 1), with: .color(color))
            }
        }
        // The playhead.
        let px = min(max(playedTo, 1), width - 1)
        context.fill(Path(roundedRect: CGRect(x: px - 1, y: y - 10, width: 2, height: 20), cornerRadius: 1), with: .color(Palette.text))
        context.fill(Path(ellipseIn: CGRect(x: px - 5, y: y - 5, width: 10, height: 10)), with: .color(Palette.text))
        context.stroke(Path(ellipseIn: CGRect(x: px - 5, y: y - 5, width: 10, height: 10)), with: .color(Palette.bgStage), lineWidth: 1.5)
        // The checks' numbers last, on the stage's ground, so the playhead never runs through them.
        for mark in marks where (mark.kind == .keyFrame || mark.kind == .failure) && mark.check != nil {
            let numbers = mark.checks.isEmpty ? [mark.check ?? 0] : mark.checks
            let color = mark.kind == .failure ? Palette.fail : Palette.textSecondary
            let label = context.resolve(Text(numbers.map(String.init).joined(separator: ","))
                .font(.system(size: 9, weight: .semibold)).foregroundStyle(color))
            let size = label.measure(in: CGSize(width: 200, height: 20))
            let center = CGPoint(x: x(mark.at), y: y - 13)
            let ground = CGRect(x: center.x - size.width / 2 - 2, y: center.y - size.height / 2, width: size.width + 4, height: size.height)
            context.fill(Path(roundedRect: ground, cornerRadius: 3), with: .color(Palette.bgStage))
            context.draw(label, at: center, anchor: .center)
        }
    }

    // MARK: - Hover

    private func hoverCard(at x: CGFloat, width: CGFloat) -> some View {
        let t = timeline
        let fraction = Double(x / max(width, 1))
        let seconds = t.seconds(atFraction: fraction)
        let mark = t.mark(near: fraction, tolerance: Double(6 / max(width, 1)))
        let at = mark?.at ?? seconds
        let line: String
        if let mark {
            line = TimelineWords.label(mark, steps: steps, checks: shell.checks)
        } else if let span = t.span(at: seconds) {
            line = TimelineWords.label(span)
        } else if let step = t.step(at: seconds), let held = steps.first(where: { $0.seq == step }) {
            line = "Step \(step) · \(StepSummary.phrase(for: held, in: steps))"
        } else {
            line = "Before the first step"
        }
        let frame = t.frame(at: at)
        let cardWidth: CGFloat = 224
        let left = min(max(0, x - cardWidth / 2), max(0, width - cardWidth))
        return VStack(alignment: .leading, spacing: 6) {
            if let frame {
                StorePicture(store: shell.store, runId: summary.runId, picture: SummaryPicture(kind: "frame", file: frame.file)) { content in
                    ZStack {
                        Palette.bgSelected
                        if case .image(let image) = content, !redacted {
                            Image(nsImage: image).resizable().interpolation(.medium).aspectRatio(contentMode: .fill)
                        }
                    }
                    .frame(width: cardWidth - 16, height: (cardWidth - 16) * 3 / 4)
                    .clipShape(RoundedRectangle(cornerRadius: Corner.control))
                }
            }
            HStack(spacing: 6) {
                Text(Clock.elapsed(Int(at))).textStyle(.captionEmphasis).monospacedDigit().foregroundStyle(Palette.text)
                Text(line).textStyle(.caption).foregroundStyle(mark?.kind == .failure ? Palette.fail : Palette.textSecondary)
                    .lineLimit(1)
                    .truncationMode(.tail)
            }
        }
        .padding(Gap.x8)
        .frame(width: cardWidth, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: Corner.row).fill(Palette.bgRaised))
        .overlay(RoundedRectangle(cornerRadius: Corner.row).strokeBorder(Palette.border, lineWidth: 1))
        .shadow(color: .black.opacity(Elevation.raisedOpacity), radius: Elevation.raisedRadius / 2, y: Elevation.raisedY)
        .fixedSize()
        .alignmentGuide(.top) { d in d.height + 4 }
        .offset(x: left)
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }
}
