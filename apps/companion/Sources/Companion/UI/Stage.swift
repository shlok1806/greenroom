import AppKit
import SwiftUI

/// The stage (Figma Mockups 02 to 04, 07): the picture that proves the selected check, with its
/// mark; the sentence under it ("Expected $50.00, saw $10.00"); the filmstrip. While the run is
/// open with no outcome, the picture is the live screen.
struct StageView: View {
    @Bindable var shell: ShellModel
    var summary: Summary
    var windowClass: WindowClass
    @Environment(\.frozenNow) private var frozenNow

    var body: some View {
        let frames = shell.store.frames[summary.runId] ?? []
        let content = StageContent.of(summary, check: shell.selectedCheck, pickedFrame: shell.selectedFrame,
                                      liveWanted: shell.showsLive, framesHeld: frames)
        let padding = windowClass.stagePadding
        // A passed run shows the frames its checks were proven on (Figma 04); any other, the
        // filmstrip to scrub (Figma 03).
        let keyFrames = summary.state == .passed ? KeyFrames.items(summary.checks.items, steps: shell.store.steps[summary.runId] ?? []) : []
        // No filmstrip while the Mac starts, restarts or does not answer (Figma 06, 07a, 07b).
        let strip = ![.starting, .notAnswering, .restarting].contains(summary.state) && keyFrames.isEmpty
        let captionHeight = captionHeight(content)
        GeometryReader { geo in
            // The design's stage: 24 above, 16 below, 32 at the sides;
            // the evidence (the 4:3 picture, 12, a 28 pt caption), 24, the 58 pt filmstrip or
            // the 144 pt key frames, centred in what is left. The picture takes the full width
            // unless the height ends first, and no more than the key frames' row under it.
            let chrome: CGFloat = 12 + captionHeight + (keyFrames.isEmpty ? (strip ? 24 + 58 : 0) : 24 + KeyFrames.height + 20)
            let tall = geo.size.height - padding.top - Gap.x16 - chrome
            let room = min(geo.size.width - padding.horizontal * 2, tall * 4 / 3)
            let width = max(200, keyFrames.isEmpty ? room : min(room, KeyFrames.rowWidth))
            VStack(alignment: .leading, spacing: Gap.x24) {
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
                    FilmstripView(shell: shell, summary: summary, frames: frames, width: width)
                        .cloneScope("Filmstrip")
                }
                if !keyFrames.isEmpty {
                    KeyFramesView(shell: shell, summary: summary, frames: keyFrames, width: width)
                        .cloneScope("Key frames")
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .padding(.top, padding.top)
            .padding(.bottom, Gap.x16)
        }
        .background(Palette.bgStage)
    }

    @ViewBuilder
    private func picture(_ content: StageContent) -> some View {
        switch content {
        case .live:
            LivePicture(shell: shell, runId: summary.runId)
        case .picture(let picture, let mark, let color, let dimmed):
            StorePicture(store: shell.store, runId: summary.runId, picture: picture) { frame in
                EvidenceFrame(content: frame, mark: mark, markColor: color, dimmed: dimmed,
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
                HStack(spacing: 6) {
                    Circle().fill(Palette.accent).frame(width: 6, height: 6)
                    Text("Live").textStyle(.captionEmphasis).foregroundStyle(Palette.accent)
                }
            } else if hasRecording {
                // Compact has room for the icon alone (Figma 03 compact).
                if windowClass == .compact {
                    IconButton(icon: .video, name: "Recording") { shell.evidenceOpen = true }
                        .help("Open the evidence and the recording (E)")
                        .cloneScope("Icon button")
                } else {
                    ToolbarButton(icon: .video, title: "Recording") { shell.evidenceOpen = true }
                        .help("Open the evidence and the recording (E)")
                        .cloneScope("Toolbar button")
                }
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
            if shell.selectedFrame != nil { return Text("A frame you picked").foregroundStyle(Palette.textSecondary) }
            switch EvidenceCaption.of(shell.selectedCheck) {
            case .disagreement(let expected, let saw):
                return Text("Expected ").foregroundStyle(Palette.textSecondary) + Text(expected).fontWeight(.semibold).foregroundStyle(Palette.text)
                    + Text(", saw ").foregroundStyle(Palette.textSecondary) + Text(saw).fontWeight(.semibold).foregroundStyle(Palette.fail)
            case .agreement(let saw):
                return Text("Saw ").foregroundStyle(Palette.textSecondary) + Text(saw).fontWeight(.semibold).foregroundStyle(Palette.text)
                    + Text(", as expected").foregroundStyle(Palette.textSecondary)
            case .sentence(let words):
                return Text(words).foregroundStyle(Palette.textSecondary)
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

/// The filmstrip: key frames, a red bar under a frame a failed check cites; the selected one
/// framed. Left and Right step through them.
struct FilmstripView: View {
    @Bindable var shell: ShellModel
    var summary: Summary
    var frames: [Frame]
    var width: CGFloat

    var body: some View {
        // As many 68 pt thumbs as fit at least 2 apart, spread across the picture's width (the
        // design's space-between): eight under a 568 pt picture, six under a compact 432.
        let count = max(1, Int((width + 2) / (Metrics.thumbWidth + 2)))
        let items = Filmstrip.items(frames, checks: summary.checks.items, count: count)
        if items.isEmpty {
            EmptyView()
        } else {
            let thumb = min(Metrics.thumbWidth, (width - CGFloat(count - 1) * 2) / CGFloat(count))
            HStack(spacing: 0) {
                ForEach(Array(items.enumerated()), id: \.element.id) { index, item in
                    if index > 0 { Spacer(minLength: 2) }
                    StorePicture(store: shell.store, runId: summary.runId, picture: SummaryPicture(kind: "frame", file: item.file)) { frame in
                        FilmstripThumb(image: frame.image, selected: isSelected(item, last: index == items.count - 1), mark: item.mark, width: thumb)
                    }
                    .cloneScope(items.count > 1 ? "Filmstrip thumb[\(index)]" : "Filmstrip thumb")
                    .onTapGesture { shell.select(frame: item.file) }
                    .accessibilityElement()
                    .accessibilityLabel(item.mark == .failed ? "Frame of a failed check" : "Frame \(index + 1)")
                    .accessibilityAddTraits(.isButton)
                }
            }
            .frame(width: width)
        }
    }

    private func isSelected(_ item: FilmstripItem, last: Bool) -> Bool {
        if let picked = shell.selectedFrame { return picked == item.file }
        if shell.showsLive { return last }
        if let step = shell.selectedCheck?.picture?.step ?? shell.selectedCheck?.step {
            return Filmstrip.frame(atOrAfter: step, in: frames)?.file == item.file
        }
        return last
    }
}

/// A passed run's key frames (Figma 04): three pictures its checks were proven on, 8 apart,
/// each captioned 6 below; the one proving the selected check ringed as the filmstrip's is.
struct KeyFramesView: View {
    @Bindable var shell: ShellModel
    var summary: Summary
    var frames: [KeyFrame]
    var width: CGFloat
    @Environment(\.redactsGuestScreen) private var redacted
    @Environment(\.displayScale) private var scale

    var body: some View {
        // Every frame the same width on the device's pixel grid, each starting where the
        // design's third starts, rounded: the frames land within half a pixel of the design's
        // 165.33 pt, whatever is left over going to the gaps.
        let each = (width - CGFloat(KeyFrames.count - 1) * KeyFrames.gap) / CGFloat(KeyFrames.count)
        let wide = (each * scale).rounded() / scale
        ZStack(alignment: .topLeading) {
            ForEach(Array(frames.enumerated()), id: \.element.id) { index, frame in
                let start = (CGFloat(index) * (each + KeyFrames.gap) * scale).rounded() / scale
                let selected = shell.selectedCheck?.id == frame.checkID
                    || (shell.selectedCheck?.picture?.step).map { $0 == frame.step } == true
                VStack(alignment: .leading, spacing: 6) {
                    StorePicture(store: shell.store, runId: summary.runId, picture: frame.picture) { content in
                        ZStack {
                            Palette.bgSelected
                            if case .image(let image) = content, !redacted {
                                Image(nsImage: image).resizable().interpolation(.medium).aspectRatio(contentMode: .fill)
                            }
                        }
                        .frame(width: wide, height: (each * 3 / 4 * scale).rounded() / scale)
                        .clipShape(RoundedRectangle(cornerRadius: Corner.control))
                        .overlay {
                            // Figma: 1 pt border inside; selected, 2 pt outside in the text colour.
                            if selected {
                                RoundedRectangle(cornerRadius: Corner.control + 2).strokeBorder(Palette.text, lineWidth: 2).padding(-2)
                            } else {
                                RoundedRectangle(cornerRadius: Corner.control).strokeBorder(Palette.border, lineWidth: 1)
                            }
                        }
                    }
                    .clonePart("Rectangle")
                    Text(frame.caption).textStyle(.caption).foregroundStyle(selected ? Palette.text : Palette.textSecondary)
                        .lineLimit(1)
                        .clonePart("Text")
                }
                .frame(width: wide, alignment: .leading)
                .contentShape(Rectangle())
                .onTapGesture { shell.select(check: frame.checkID) }
                .accessibilityElement(children: .combine)
                .accessibilityLabel("Key frame: \(frame.caption)")
                .accessibilityAddTraits(.isButton)
                .cloneScope(frames.count > 1 ? "Key frame[\(index)]" : "Key frame")
                .padding(.leading, start)
            }
        }
        .frame(width: width, alignment: .leading)
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
