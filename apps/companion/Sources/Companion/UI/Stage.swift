import AppKit
import SwiftUI

/// The stage (Figma Mockups 02 to 04, 07): the picture that proves the selected check, with its
/// mark; the sentence under it ("Expected $50.00, saw $10.00"); the recording's timeline
/// (redesign 7, which replaced the filmstrip and the key frames row). While the run is open
/// with no outcome, the picture is the live screen. The picture shrinks to the room it has
/// (Activity open beside it), never away.
struct StageView: View {
    @Bindable var shell: ShellModel
    var summary: Summary
    var windowClass: WindowClass
    @Environment(\.frozenNow) private var frozenNow

    var body: some View {
        let frames = shell.store.frames[summary.runId] ?? []
        let content = StageContent.of(summary, check: shell.selectedCheck, pickedFrame: shell.selectedFrame,
                                      liveWanted: shell.showsLive, framesHeld: frames)
        // The timeline shows once there is a record to scrub; not while the Mac starts.
        let strip = summary.state != .starting && (!frames.isEmpty || !(shell.store.steps[summary.runId] ?? []).isEmpty)
        let captionHeight = captionHeight(content)
        GeometryReader { geo in
            // The design's stage: 24 above, 16 below, 32 at the sides (16 once the stage is
            // narrow, as with Activity open); the evidence (the 4:3 picture, 12, a 28 pt
            // caption), 20, the 32 pt timeline across the stage, centred in what is left. The
            // picture takes the full width unless the height ends first.
            let narrow = geo.size.width < 560
            let padding = narrow ? (horizontal: Gap.x16, top: Gap.x16) : windowClass.stagePadding
            let chrome: CGFloat = 12 + captionHeight + (strip ? 20 + TimelineBar.height : 0)
            let tall = geo.size.height - padding.top - Gap.x16 - chrome
            let room = min(geo.size.width - padding.horizontal * 2, tall * 4 / 3)
            let width = max(120, room)
            VStack(alignment: .center, spacing: 20) {
                VStack(alignment: .leading, spacing: Gap.x12) {
                    picture(content)
                        .frame(width: width, height: width * 3 / 4)
                        .overlay {
                            if summary.state == .restarting {
                                RestartProgress(phases: shell.store.details[summary.runId]?.machine?.boot ?? [])
                            }
                        }
                        .onTapGesture { if case .picture = content { shell.evidenceOpen = true } }
                        .cloneScope("Screen")
                    caption(content)
                        .frame(width: width, height: captionHeight)
                        .cloneScope("Caption")
                }
                .cloneScope("Evidence")
                if strip {
                    TimelineBar(shell: shell, summary: summary)
                        .frame(width: max(width, geo.size.width - padding.horizontal * 2))
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .padding(.top, padding.top)
            .padding(.bottom, Gap.x16)
        }
        .background(Palette.bgStage)
    }

    /// The click a scrubbed-to frame's step made, as a small box on the picture (the old
    /// window's click marks).
    private var clickMark: SummaryBox? {
        guard shell.selectedFrame != nil else { return nil }
        let t = shell.timeline()
        let steps = shell.store.steps[summary.runId] ?? []
        guard let seq = t.step(at: shell.currentSeconds(t)), let step = steps.first(where: { $0.seq == seq }),
              let target = ClickMarks.target(of: step, in: steps) else { return nil }
        let size = 0.03
        return SummaryBox(x: target.fraction.x - size / 2, y: target.fraction.y - size * 2 / 3, w: size, h: size * 4 / 3)
    }

    @ViewBuilder
    private func picture(_ content: StageContent) -> some View {
        switch content {
        case .live:
            LivePicture(shell: shell, runId: summary.runId)
        case .picture(let picture, let mark, let color, let dimmed):
            let click = mark == nil ? clickMark : nil
            StorePicture(store: shell.store, runId: summary.runId, picture: picture) { frame in
                EvidenceFrame(content: frame, mark: mark ?? click, markColor: click == nil ? color : .accent, dimmed: dimmed,
                              openRecording: { shell.evidenceOpen = true })
            }
            .accessibilityElement(children: .contain)
            .accessibilityLabel(pictureLabel)
        case .waiting(let words):
            placeholder(words, glyph: .starting)
        case .none(let words):
            placeholder(words, glyph: nil)
        }
    }

    private var pictureLabel: String {
        let caption = EvidenceCaption.of(shell.selectedCheck).words
        return caption.isEmpty ? "The Mac's screen" : "The Mac's screen. \(caption)"
    }

    private func placeholder(_ words: String, glyph: GlyphKind?) -> some View {
        RoundedRectangle(cornerRadius: Corner.row)
            .fill(Palette.bgSelected)
            .aspectRatio(4 / 3, contentMode: .fit)
            .overlay {
                if let glyph {
                    StatusGlyph(kind: glyph, color: .accent, size: 24)
                        .accessibilityLabel(words)
                } else {
                    Text(words).textStyle(.body).foregroundStyle(Palette.textSecondary)
                }
            }
    }

    @ViewBuilder
    private func caption(_ content: StageContent) -> some View {
        HStack(spacing: Gap.x8) {
            captionText(content)
                .textStyle(.title).fontWeight(.regular)
                .lineLimit(1)
                .clonePart("Text")
                .frame(maxWidth: .infinity, alignment: .leading)
            if case .live = content {
                EmptyView()
            } else if hasRecording {
                // Compact has room for the icon alone (Figma 03 compact).
                // The player plays the recording itself; this opens it large with the video.
                IconButton(icon: .video, name: "Open the evidence large, with the video (E)") { shell.evidenceOpen = true }
                    .cloneScope("Icon button")
            }
        }
    }

    /// Whether the caption offers the recording: not while the Mac starts, restarts or does not
    /// answer, when there is none to watch yet (Figma 06, 07a, 07b).
    private var hasRecording: Bool {
        summary.lastFrame != nil && ![.starting, .restarting, .notAnswering].contains(summary.state)
    }

    /// The caption's line: 28 with the Recording button, 22 with the Live badge, else one line of
    /// Title (20), as Figma 03, 02 and 07 draw it.
    private func captionHeight(_ content: StageContent) -> CGFloat {
        if case .live = content { return 22 }
        return hasRecording ? Metrics.buttonHeight : 20
    }

    private func captionText(_ content: StageContent) -> Text {
        switch content {
        case .live:
            // The header's Now line already says what the verifier does (say it once).
            return Text("")
        case .picture(_, _, _, let dimmed) where dimmed:
            // Restarting says so over the picture; a stuck screen says how old its last picture is.
            if summary.state == .restarting { return Text("Last picture before the restart").foregroundStyle(Palette.textSecondary) }
            guard summary.state == .notAnswering else { return Text("") }
            return Text("Last picture, \(Clock.elapsed(summary.inStatus(now: frozenNow ?? Date()))) ago").foregroundStyle(Palette.textSecondary)
        case .picture:
            if shell.selectedFrame != nil {
                // What happened at the playhead, in words.
                let t = shell.timeline()
                let steps = shell.store.steps[summary.runId] ?? []
                guard let seq = t.step(at: shell.currentSeconds(t)), let step = steps.first(where: { $0.seq == seq }) else {
                    return Text("Before the first step").foregroundStyle(Palette.textSecondary)
                }
                let phrase = StepSummary.phrase(for: step, in: steps)
                if step.error != nil {
                    return Text("Step \(seq) ").foregroundStyle(Palette.textSecondary) + Text(phrase).foregroundStyle(Palette.text)
                        + Text(", failed").foregroundStyle(Palette.fail)
                }
                return Text("Step \(seq) ").foregroundStyle(Palette.textSecondary) + Text(phrase).foregroundStyle(Palette.text)
            }
            switch EvidenceCaption.of(shell.selectedCheck) {
            case .disagreement(let expected, let saw):
                return Text("Expected ").foregroundStyle(Palette.textSecondary) + Text(AgentMarkdown.inline(expected)).fontWeight(.semibold).foregroundStyle(Palette.text)
                    + Text(", saw ").foregroundStyle(Palette.textSecondary) + Text(AgentMarkdown.inline(saw)).fontWeight(.semibold).foregroundStyle(Palette.fail)
            case .agreement(let saw):
                return Text("Saw ").foregroundStyle(Palette.textSecondary) + Text(AgentMarkdown.inline(saw)).fontWeight(.semibold).foregroundStyle(Palette.text)
                    + Text(", as expected").foregroundStyle(Palette.textSecondary)
            case .sentence(let words):
                return Text(AgentMarkdown.inline(words)).foregroundStyle(Palette.textSecondary)
            case .lastStep(let words):
                return Text(words).foregroundStyle(Palette.textSecondary)
            case .none:
                return Text("The newest picture").foregroundStyle(Palette.textSecondary)
            }
        case .waiting, .none:
            return Text("")
        }
    }
}

/// The live screen and, while the person drives, the surface that sends their input (ADR 0009).
struct LivePicture: View {
    @Bindable var shell: ShellModel
    var runId: String
    @State private var live: LiveScreen?

    var body: some View {
        ZStack {
            Palette.bgSelected
            if let live {
                LiveScreenView(layer: live.output.layer, pixelSize: live.pixelSize ?? .zero)
                if let size = live.pixelSize {
                    InputSurface(imageSize: size, active: shell.driving) { actions in
                        shell.store.existingPilot(runId)?.send(actions)
                    }
                }
                if live.phase != .playing {
                    VStack(spacing: Gap.x8) {
                        StatusGlyph(kind: .checking, color: .accent, size: 20)
                        Text(liveWords(live.phase)).textStyle(.body).foregroundStyle(Palette.textSecondary)
                    }
                }
            }
        }
        .aspectRatio(live?.pixelSize.map { $0.width / max(1, $0.height) } ?? 4 / 3, contentMode: .fit)
        .clipShape(RoundedRectangle(cornerRadius: Corner.row))
        .overlay(RoundedRectangle(cornerRadius: Corner.row)
            .strokeBorder(shell.driving ? Palette.accent : Palette.border, lineWidth: shell.driving ? 2 : 1))
        .onAppear {
            let screen = shell.store.liveScreen(for: runId)
            screen.start()
            live = screen
        }
        .onDisappear {
            live?.stop()
            live = nil
        }
        .accessibilityLabel(shell.driving ? "The Mac's screen. You have control." : "The Mac's screen, live")
    }

    private func liveWords(_ phase: LiveScreen.Phase) -> String {
        switch phase {
        case .connecting: "Connecting to the screen"
        case .playing: ""
        case .failed(let why): "The screen is not coming through: \(why)"
        }
    }
}

/// The Mac's restart, over the dimmed last picture (Figma Mockups 07b): no buttons.
struct RestartProgress: View {
    var phases: [BootPhase]
    @Environment(\.frozenNow) private var frozenNow

    var body: some View {
        let start = phases.first { $0.phase.text == "start" }
        let rows: [(String, GlyphKind, String)] = [
            ("Stopped the Mac", .passed, phases.first { $0.phase.text == "stop" }?.seconds.map { Clock.elapsed(Int($0.rounded())) } ?? ""),
            ("Starting macOS", start.map { !$0.running } == true ? .passed : .checking, start.map(time) ?? ""),
            ("Reconnecting the screen", phases.contains { $0.phase.text == "ssh" } ? .checking : .pending, ""),
        ]
        // Figma 07b: 20 in, rows 18 tall and 12 apart, the glyph 10 from a 200 pt label, the
        // phase's time 10 after it in a 24 pt column.
        VStack(alignment: .leading, spacing: Gap.x12) {
            ForEach(Array(rows.enumerated()), id: \.offset) { index, row in
                let (text, glyph, time) = row
                HStack(spacing: 10) {
                    StatusGlyph(kind: glyph, color: glyph == .passed ? .pass : (glyph == .checking ? .accent : .tertiary))
                        .clonePart("Glyph")
                    Text(text).textStyle(.body).foregroundStyle(glyph == .pending ? Palette.textSecondary : Palette.text)
                        .frame(width: 200, alignment: .leading)
                        .clonePart(time.isEmpty ? "Text" : "Text[0]")
                    Text(time).textStyle(.caption).foregroundStyle(Palette.textSecondary)
                        .frame(width: 24, alignment: .trailing)
                        .clonePart(time.isEmpty ? "Time" : "Text[1]")
                }
                .frame(height: 18)
                .cloneScope("Frame[\(index)]")
            }
        }
        .padding(20)
        .background(RoundedRectangle(cornerRadius: Corner.sheet).fill(Palette.bgRaised))
        .shadow(color: .black.opacity(Elevation.raisedOpacity), radius: Elevation.raisedRadius / 2, y: Elevation.raisedY)
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Restarting the Mac")
        .cloneScope("Restart progress")
    }

    /// A phase's time: how long it took, or how long it has run.
    private func time(_ phase: BootPhase) -> String {
        Clock.elapsed(Int((phase.seconds ?? (frozenNow ?? Date()).timeIntervalSince(phase.at)).rounded()))
    }
}

/// A screenshot artifact or a recorded frame of a run, loaded through the store and kept.
struct StorePicture<Content: View>: View {
    let store: RunStore
    let runId: String
    let picture: SummaryPicture
    @ViewBuilder var content: (FrameContent) -> Content
    @State private var image: NSImage?
    @State private var failed = false

    var body: some View {
        let held = image ?? PictureCache.shared.image(runId: runId, file: picture.file)
        content(held.map(FrameContent.image) ?? (failed ? .missing : .loading))
            .task(id: "\(runId)/\(picture.file)") {
                failed = false
                if let held = PictureCache.shared.image(runId: runId, file: picture.file) {
                    image = held
                    return
                }
                image = nil
                let loaded = picture.isScreenshot
                    ? await store.artifactImage(runId: runId, name: picture.file)
                    : await store.frameImage(runId: runId, file: picture.file)
                if let loaded { PictureCache.shared.store(loaded, runId: runId, file: picture.file) }
                image = loaded
                failed = loaded == nil
            }
    }
}

/// The pictures the stage and the filmstrip showed lately, bounded (the frames stay small:
/// JPEG headers parse on read, pixels decode on first draw).
@MainActor
final class PictureCache {
    static let shared = PictureCache()
    private var images: [String: NSImage] = [:]
    private var order: [String] = []
    private let capacity = 48

    func image(runId: String, file: String) -> NSImage? { images["\(runId)/\(file)"] }

    func store(_ image: NSImage, runId: String, file: String) {
        let key = "\(runId)/\(file)"
        if images[key] == nil { order.append(key) }
        images[key] = image
        while order.count > capacity { images[order.removeFirst()] = nil }
    }
}
