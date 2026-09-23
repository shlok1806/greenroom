import AppKit
import Combine
import SwiftUI

/// What the Run menu can do to the screen on show.
struct ScreenCommands {
    var goLive: (() -> Void)?
}

extension FocusedValues {
    @Entry var screenCommands: ScreenCommands?
}

/// A player over the frames the daemon captured (ADR 0008), the live stream while
/// following a ready machine (ADR 0011), and while the lease is held, the machine's
/// mouse and keys (ADR 0009). Nothing is drawn over the picture except the driving
/// badge; position lives under the track.
struct ScreenView: View {
    let store: RunStore
    let runId: String

    @State private var player = PlayerModel()
    @State private var image: NSImage?
    /// So a seek request is honoured once, not on every return to the stage.
    @State private var appliedSeekNonce = 0
    @State private var lastTick: Date?
    @State private var pilot: ControlPilot?
    @State private var live: LiveScreen?
    @State private var onScreen = false
    /// The frame under the pointer on the track, previewed under the track.
    @State private var hoverIndex: Int?
    /// Where the player was before evidence was opened, for "Back to verdict".
    @State private var returnPoint: (index: Int, live: Bool)?
    @State private var evidenceStep: Int?
    @AppStorage("verdictCardExpanded") private var verdictExpanded = false
    @AppStorage("showsConversation") private var showsConversation = true
    @FocusState private var focused: Bool

    private var facts: RunFacts { store.facts(runId) }
    private var driving: Bool { pilot?.active == true }

    /// Following live on a ready machine streams the screen (ADR 0011); the
    /// past, and any moment the stream is down, come from the recording.
    private var wantsLive: Bool { onScreen && player.live && machineIsReady }
    private var showsLive: Bool { wantsLive && live?.phase == .playing && live?.pixelSize != nil }

    private let tick = Timer.publish(every: 0.1, on: .main, in: .common).autoconnect()

    /// One answer for "is there a machine to watch or drive": the run's facts.
    private var machineIsReady: Bool { facts.machineReady }

    private var frameCount: Int { store.frames[runId]?.count ?? 0 }

    private var hasPlayer: Bool { !player.frames.isEmpty || machineIsReady || pilot?.endedReason != nil }

    var body: some View {
        stack
        .focusable(!driving)
        // Focus is only for the arrow and space keys; a ring round the whole stage is noise.
        .focusEffectDisabled()
        .focused($focused)
        // While driving, keys belong to the guest.
        .onKeyPress(.leftArrow) {
            guard !driving else { return .ignored }
            step(by: -1)
            return .handled
        }
        .onKeyPress(.rightArrow) {
            guard !driving else { return .ignored }
            step(by: 1)
            return .handled
        }
        .onKeyPress(.space) {
            guard !driving else { return .ignored }
            player.playing.toggle()
            return .handled
        }
        .focusedSceneValue(\.screenCommands, ScreenCommands(
            goLive: machineIsReady && !player.live ? { goLive() } : nil
        ))
        .onAppear {
            focused = true
            onScreen = true
            pilot = store.pilot(for: runId)
            syncFrames()
            // A seek from the Steps or the conversation is raised before this view exists.
            applyPendingSeek()
            lastTick = nil
            syncLive()
        }
        // Leaving the stage or the run gives the screen back.
        .onDisappear {
            onScreen = false
            lastTick = nil
            let leaving = pilot
            Task { await leaving?.release() }
            syncLive()
        }
        .onChange(of: runId) { old, new in
            let leaving = store.pilot(for: old)
            Task { await leaving.release() }
            pilot = store.pilot(for: new)
            // This view is not rebuilt per run, so reset by hand.
            player = PlayerModel()
            image = nil
            hoverIndex = nil
            returnPoint = nil
            evidenceStep = nil
            appliedSeekNonce = 0
            lastTick = nil
            syncFrames()
            applyPendingSeek()
            syncLive()
        }
        .onChange(of: wantsLive) {
            syncLive()
        }
        .onChange(of: machineIsReady) { _, ready in
            guard !ready, let pilot else { return }
            Task { await pilot.release() }
        }
        // However the lease was taken (toolbar, menu, here), the past cannot be clicked.
        .onChange(of: driving) { _, now in
            guard now else { return }
            goLive()
            player.playing = false
            evidenceStep = nil
        }
        .onChange(of: store.verdict(runId)?.seq) {
            // Opened on the old verdict's evidence and not moved since: follow the new one.
            guard evidenceStep != nil, returnPoint == nil, !facts.isAlive, let cited = firstCitedStep else { return }
            player.seek(toStep: cited)
            evidenceStep = cited
        }
        .onChange(of: frameCount) {
            syncFrames()
        }
        .onChange(of: store.seekRequest) {
            applyPendingSeek()
        }
        .onReceive(tick) { now in
            // A state write here redraws the view, so a paused player writes nothing.
            guard player.playing else {
                if lastTick != nil { lastTick = nil }
                return
            }
            // Real elapsed time: the timer fires late under load.
            let elapsed = lastTick.map { now.timeIntervalSince($0) } ?? 0
            lastTick = now
            player.advance(by: min(elapsed, 1))
        }
        .task(id: player.current?.file) {
            await loadCurrentImage()
        }
    }

    private var backAction: (() -> Void)? {
        guard returnPoint != nil else { return nil }
        return { backToVerdict() }
    }

    private var stack: some View {
        VStack(spacing: 0) {
            if driving {
                DrivingBar(screen: pilot?.screen)
                    .padding(.horizontal, Space.l)
                    .padding(.top, Space.m)
                    .transition(.opacity.combined(with: .move(edge: .top)))
            } else if let evidenceStep, !facts.isAlive, facts.verdict != nil {
                EvidenceBar(step: evidenceStep, back: backAction, record: {
                    store.requestSeek(runId: runId, step: evidenceStep, fromVerdict: true, inSteps: true)
                })
            }
            well
                .padding([.horizontal, .top], Space.l)
                .padding(.bottom, hasPlayer ? Space.m : Space.l)
                .layoutPriority(1)
            // A booting machine or an empty run has nothing to play or take.
            if hasPlayer {
                PlayerBar(
                    player: $player,
                    steps: store.steps[runId] ?? [],
                    verdict: store.verdict(runId),
                    supersededEvidence: supersededEvidence,
                    origin: facts.started,
                    total: facts.duration(now: Date()),
                    hoverIndex: $hoverIndex,
                    driving: driving,
                    machineIsReady: machineIsReady,
                    source: sourceState,
                    liveFailure: liveFailure,
                    endedReason: driving ? nil : pilot?.endedReason,
                    goLive: goLive
                )
                RecentSteps(
                    store: store,
                    runId: runId,
                    steps: store.steps[runId] ?? [],
                    upTo: player.live ? nil : player.current?.step
                )
                .padding(.top, Space.m)
            }
            Spacer(minLength: Space.m)
        }
    }

    // MARK: - Picture

    /// What `InputSurface` maps against: the picture actually on screen.
    private var pictureSize: CGSize? {
        showsLive ? live?.pixelSize : image?.size
    }

    /// The well hugs the picture's shape, so there is no letterbox to fill; before a
    /// picture arrives it is a 4:3 placeholder.
    private var well: some View {
        let shape = pictureSize ?? CGSize(width: 4, height: 3)
        return ZStack {
            Palette.well
            if let pictureSize {
                if let image, !showsLive {
                    Image(nsImage: image)
                        .resizable()
                        .interpolation(.medium)
                        .scaledToFit()
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
                // Mounted while connecting too, so the first frame shows the moment it decodes.
                if wantsLive, let live {
                    LiveScreenView(layer: live.output.layer, pixelSize: live.pixelSize ?? .zero)
                        .opacity(showsLive ? 1 : 0)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
                InputSurface(imageSize: pictureSize, active: driving) { actions in
                    pilot?.send(actions)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                emptyWell
            }
        }
        .aspectRatio(shape, contentMode: .fit)
        .clipShape(RoundedRectangle(cornerRadius: Radius.well, style: .continuous))
        .overlay {
            RoundedRectangle(cornerRadius: Radius.well, style: .continuous)
                .strokeBorder(driving ? AnyShapeStyle(Palette.driving) : AnyShapeStyle(Palette.hairline),
                              lineWidth: driving ? 3 : 1)
                .allowsHitTesting(false)
        }
        .animation(.snappy(duration: 0.2), value: driving)
        .frame(maxWidth: .infinity)
    }

    @ViewBuilder
    private var emptyWell: some View {
        if case .booting = facts.phase {
            VStack(spacing: Space.m) {
                ProgressView().controlSize(.regular)
                Text("The machine is starting")
                    .font(.headline)
                Text("Its screen appears here once it is ready.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
        } else if !player.frames.isEmpty {
            ProgressView().controlSize(.small)
        } else {
            ContentUnavailableView {
                Label("No recording", systemImage: "film")
            } description: {
                Text(machineIsReady
                    ? "Recording starts when the machine is ready."
                    : "This run ended without any frames captured.")
            }
        }
    }

    /// Where the picture comes from: exactly one of live, connecting, recording, driving.
    private var sourceState: ScreenSourceState {
        if driving { return .driving }
        if showsLive { return .live }
        if wantsLive, live?.phase == .connecting || live?.phase == .playing { return .connecting }
        return .recording(position: FrameTimeline.offset(player.frames, at: player.index, from: facts.started),
                          total: facts.duration(now: Date()))
    }

    /// Why the live picture is not showing, when it was wanted and failed.
    private var liveFailure: String? {
        guard wantsLive, case .failed(let reason)? = live?.phase else { return nil }
        return reason
    }

    /// Steps an earlier, replaced verdict cited, still marked on the track.
    private var supersededEvidence: Set<Int> {
        let current = store.verdict(runId)?.seq
        let earlier = (store.messages[runId] ?? []).filter { $0.kind == .verdict && $0.seq != current }
        return Set(earlier.flatMap { ($0.evidence ?? []).compactMap { Evidence.parse($0).step } })
    }

    private func toggleControl() {
        guard let pilot else { return }
        Task {
            if pilot.active {
                await pilot.release()
            } else {
                await pilot.take()
            }
        }
    }

    private var firstCitedStep: Int? {
        (store.verdict(runId)?.evidence ?? []).lazy.compactMap { Evidence.parse($0).step }.first
    }

    private func goLive() {
        player.live = true
        if !player.frames.isEmpty { player.index = player.frames.count - 1 }
    }

    private func backToVerdict() {
        if let returnPoint {
            player.index = min(returnPoint.index, max(0, player.frames.count - 1))
            player.live = returnPoint.live
        }
        returnPoint = nil
        evidenceStep = nil
        verdictExpanded = true
        showsConversation = true
        store.clearFocus()
    }

    // MARK: - Loading

    /// Streams only while wanted; scrubbing, leaving, a run change or a
    /// stopped machine all end it.
    private func syncLive() {
        guard wantsLive else {
            live?.stop()
            return
        }
        if live?.runId != runId {
            live?.stop()
            live = store.liveScreen(for: runId)
        }
        live?.start()
    }

    private func step(by delta: Int) {
        guard !player.frames.isEmpty else { return }
        player.live = false
        player.index = min(max(player.index + delta, 0), player.frames.count - 1)
    }

    /// One new frame is appended so `live` follows it; anything else (first
    /// load, reconnect) replaces the list, keeping the frame on screen if it can.
    private func syncFrames() {
        let latest = store.frames[runId] ?? []
        guard latest != player.frames else { return }
        if latest.count == player.frames.count + 1, latest.dropLast().elementsEqual(player.frames), let new = latest.last {
            player.append(new)
            return
        }
        let previousFile = player.current?.file
        player.frames = latest
        if previousFile == nil, !facts.isAlive, let cited = firstCitedStep, !latest.isEmpty {
            // A finished run is judged by what its verdict cites: open there.
            player.seek(toStep: cited)
            evidenceStep = cited
        } else if player.live || previousFile == nil {
            player.index = max(0, latest.count - 1)
        } else if let previousFile, let found = latest.firstIndex(where: { $0.file == previousFile }) {
            player.index = found
        } else {
            player.index = min(player.index, max(0, latest.count - 1))
        }
    }

    private func applyPendingSeek() {
        guard let request = store.seekRequest,
              request.runId == runId,
              request.nonce != appliedSeekNonce,
              !driving else { return }
        appliedSeekNonce = request.nonce
        if request.fromVerdict {
            if returnPoint == nil { returnPoint = (player.index, player.live) }
            evidenceStep = request.step
        } else {
            evidenceStep = nil
            returnPoint = nil
        }
        player.seek(toStep: request.step)
    }

    private func loadCurrentImage() async {
        guard let frame = player.current else {
            image = nil
            return
        }
        let loaded = await store.frameImage(runId: runId, file: frame.file)
        // A cancelled `.task(id:)` still resumes here; never paint a stale frame.
        guard !Task.isCancelled, player.shows(frame.file) else { return }
        image = loaded
    }
}

/// Opened from the verdict: says which step this is and offers the way back.
struct EvidenceBar: View {
    let step: Int
    var back: (() -> Void)?
    var record: (() -> Void)?

    var body: some View {
        HStack(spacing: Space.s) {
            if let back {
                Button(action: back) {
                    Label("Back to Verdict", systemImage: "chevron.backward")
                }
                .controlSize(.small)
                .keyboardShortcut(.escape, modifiers: [])
                .help("Return to where you were (Esc)")
            }
            Image(systemName: "diamond.fill")
                .font(.system(size: 8, weight: .bold))
                .foregroundStyle(.secondary)
            Text("Step \(step), cited by the verdict")
                .font(.callout)
                .foregroundStyle(.secondary)
            Spacer(minLength: 0)
            if let record {
                Button("Step Record", action: record)
                    .buttonStyle(.link)
                    .font(.callout)
                    .help("Open this step's input and output in Steps")
            }
        }
        .padding(.horizontal, Space.l)
        .padding(.top, Space.s)
    }
}

/// Above the well while driving: what is happening, and the way out. Never over the
/// picture, where it would cover the guest's own menu bar.
private struct DrivingBar: View {
    let screen: GuestScreen?

    var body: some View {
        HStack(spacing: Space.s) {
            Image(systemName: "cursorarrow.click.2")
            VStack(alignment: .leading, spacing: 0) {
                Text("You have control")
                    .font(.callout.weight(.semibold))
                Text("Your keys and clicks go to the machine\(screen.map { " (\($0.label))" } ?? "")")
                    .font(.caption)
                    .opacity(0.85)
            }
            Spacer(minLength: Space.s)
            Text("Give Back is in the toolbar")
                .font(.caption.weight(.medium))
                .opacity(0.9)
        }
        .foregroundStyle(.white)
        .padding(.leading, Space.m)
        .padding(.trailing, Space.s)
        .padding(.vertical, 6)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Palette.driving, in: RoundedRectangle(cornerRadius: Radius.card, style: .continuous))
        .accessibilityElement(children: .contain)
        .accessibilityLabel("You have control of the machine")
    }
}

// MARK: - Player bar

/// Where the picture comes from, as one chip: never "live" and "connecting" at once.
enum ScreenSourceState: Equatable {
    case live
    case connecting
    /// The recording, at `position` of `total` since the run started.
    case recording(position: TimeInterval, total: TimeInterval)
    case driving
}

/// Play, speed, the track, the one state chip, and where the playhead is.
private struct PlayerBar: View {
    @Binding var player: PlayerModel
    let steps: [Step]
    let verdict: VerdictState?
    /// Verdicts the conversation has replaced, whose evidence stays marked as superseded.
    let supersededEvidence: Set<Int>
    /// The run's start: every time here is measured from it, as in the header.
    let origin: Date
    let total: TimeInterval
    @Binding var hoverIndex: Int?
    let driving: Bool
    let machineIsReady: Bool
    let source: ScreenSourceState
    let liveFailure: String?
    let endedReason: String?
    let goLive: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: Space.s) {
            HStack(spacing: Space.m) {
                Button {
                    player.playing.toggle()
                } label: {
                    Image(systemName: player.playing ? "pause.fill" : "play.fill")
                        .font(.system(size: 13, weight: .semibold))
                        .frame(width: 28, height: 24)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .hoverHighlight(radius: 6)
                .disabled(player.frames.count < 2 || driving)
                .help(player.playing ? "Pause (Space)" : "Play the recording (Space)")
                .accessibilityLabel(player.playing ? "Pause" : "Play")

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

                SourceChip(state: hoverState ?? source)
            }

            HStack(spacing: Space.m) {
                Picker("Speed", selection: $player.speed) {
                    Text("1×").tag(PlayerModel.Speed.normal)
                    Text("4×").tag(PlayerModel.Speed.fast)
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .controlSize(.small)
                .fixedSize()
                .disabled(driving)
                .help("Playback speed")

                position
                    .frame(maxWidth: .infinity, alignment: .leading)

                legend

                if machineIsReady, !player.live, !driving {
                    Button {
                        goLive()
                    } label: {
                        Label("Go Live", systemImage: "forward.end.fill")
                    }
                    .controlSize(.small)
                    .help("Jump to the machine's screen now (\(Keys.live))")
                }
            }

            if let liveFailure {
                statusLine("The live screen is unavailable, so this is the recording: \(liveFailure)", symbol: "video.slash", tint: .secondary)
            }
            // Outside the ready check: a stopped machine hides the button but keeps the reason.
            if let endedReason {
                statusLine("Control ended: \(endedReason)", symbol: "exclamationmark.triangle.fill", tint: Palette.attention)
            }
        }
        .padding(.horizontal, Space.l)
    }

    /// While hovering the track the chip previews the time under the pointer.
    private var hoverState: ScreenSourceState? {
        guard let hoverIndex else { return nil }
        return .recording(position: FrameTimeline.offset(player.frames, at: hoverIndex, from: origin), total: total)
    }

    private func statusLine(_ text: String, symbol: String, tint: Color) -> some View {
        Label(text, systemImage: symbol)
            .font(.caption)
            .foregroundStyle(tint)
            .lineLimit(2)
            .truncationMode(.tail)
            .textSelection(.enabled)
            .help(text)
    }

    /// The step at the pointer while hovering, else at the playhead. The detail gives
    /// way from the middle, so the start and end of a command stay readable.
    private var position: some View {
        let index = hoverIndex ?? player.index
        let frame = player.frames.indices.contains(index) ? player.frames[index] : nil
        let step = frame.flatMap { frame in steps.last { $0.seq <= frame.step } }
        return HStack(spacing: 5) {
            if let step {
                let entry = ToolCatalog.entry(for: step.tool)
                Text("Step \(step.seq)")
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
                    .fixedSize()
                Image(systemName: entry.symbol)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Text(entry.title)
                    .foregroundStyle(.secondary)
                    .fixedSize()
                Text(StepSummary.line(for: step))
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .frame(minWidth: 0)
                    .layoutPriority(-1)
            } else if player.frames.isEmpty {
                Text("No frames yet").foregroundStyle(.tertiary)
            }
        }
        .font(.callout)
        .lineLimit(1)
        .help(frame.map { "\(Chrome.stamp($0.at)) (\(Chrome.zone))\n\($0.file)" } ?? "")
    }

    @ViewBuilder
    private var legend: some View {
        let marks = marks
        if !marks.failed.isEmpty || !marks.evidence.isEmpty {
            HStack(spacing: Space.s) {
                if !marks.failed.isEmpty {
                    HStack(spacing: 3) {
                        Circle().fill(Palette.failure).frame(width: 6, height: 6)
                        Text("errored")
                    }
                }
                if !marks.evidence.isEmpty {
                    HStack(spacing: 3) {
                        Image(systemName: "diamond.fill")
                            .font(.system(size: 7, weight: .bold))
                            .foregroundStyle(Palette.outcome(marks.verdict))
                        Text("evidence")
                    }
                }
                if !marks.superseded.isEmpty {
                    HStack(spacing: 3) {
                        Image(systemName: "diamond")
                            .font(.system(size: 7, weight: .bold))
                            .foregroundStyle(.secondary)
                        Text("earlier")
                    }
                }
            }
            .font(.caption)
            .foregroundStyle(.secondary)
            .fixedSize()
            .help("Marks on the track: red dots are steps that errored, filled diamonds the steps the verdict cites, hollow ones steps a superseded verdict cited")
        }
    }

    private var marks: TrackMarks {
        let failed = Set(steps.filter { $0.outcome.isFailure }.map(\.seq))
        let evidence = Set((verdict?.evidence ?? []).compactMap { Evidence.parse($0).step })
        return TrackMarks(failed: failed, evidence: evidence, superseded: supersededEvidence.subtracting(evidence),
                          verdict: verdict?.verdict)
    }
}

struct TrackMarks {
    var failed: Set<Int>
    var evidence: Set<Int>
    var superseded: Set<Int>
    var verdict: String?
}

/// Live, connecting, the recording's position, or driving: one chip, one truth.
private struct SourceChip: View {
    let state: ScreenSourceState

    var body: some View {
        HStack(spacing: 5) {
            switch state {
            case .live:
                LiveMark(size: 9)
                Text("Live").foregroundStyle(Palette.live)
            case .connecting:
                ProgressView().controlSize(.mini)
                Text("Connecting...")
            case .recording(let position, let total):
                Image(systemName: "film")
                    .font(.caption)
                Text("Recording \(Chrome.clock(position)) of \(Chrome.clock(total))")
                    .monospacedDigit()
            case .driving:
                Image(systemName: "cursorarrow.click.2")
                Text("Live, you have control").foregroundStyle(Palette.driving)
            }
        }
        .font(.callout.weight(.medium))
        .foregroundStyle(.secondary)
        .padding(.horizontal, 8)
        .padding(.vertical, 3)
        .background(.fill.quaternary, in: Capsule())
        .fixedSize()
        .help(help)
    }

    private var help: String {
        switch state {
        case .live: "The machine's screen as it happens"
        case .connecting: "Asking the machine for its live screen; the recording shows meanwhile"
        case .recording: "A recorded frame; times are since the run started"
        case .driving: "Your mouse and keys go to the machine"
        }
    }
}

/// The last few steps under the player, so the Screen answers "what did it just do".
/// While following live it is the newest; in the past it is the steps up to the frame.
private struct RecentSteps: View {
    let store: RunStore
    let runId: String
    let steps: [Step]
    let upTo: Int?

    var body: some View {
        let shown = Array(steps.filter { upTo == nil || $0.seq <= upTo! }.suffix(4))
        if !shown.isEmpty {
            VStack(alignment: .leading, spacing: 0) {
                HStack {
                    Text(upTo == nil ? "Recent steps" : "Steps up to here")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.secondary)
                    Spacer()
                    Button("All Steps") { store.requestSeek(runId: runId, step: shown.last?.seq ?? 0, inSteps: true) }
                        .buttonStyle(.link)
                        .font(.caption)
                        .help("Open the Steps stage (\(Keys.steps))")
                }
                .padding(.bottom, Space.xs)
                ForEach(shown.reversed(), id: \.self) { step in
                    RecentStepRow(step: step) {
                        store.requestSeek(runId: runId, step: step.seq, inSteps: true)
                    }
                }
            }
            .padding(.horizontal, Space.l)
        }
    }
}

private struct RecentStepRow: View {
    let step: Step
    let open: () -> Void

    var body: some View {
        let entry = ToolCatalog.entry(for: step.tool)
        Button(action: open) {
            HStack(spacing: Space.s) {
                Image(systemName: step.outcome.isFailure ? "exclamationmark.triangle.fill" : entry.symbol)
                    .font(.caption)
                    .foregroundStyle(step.outcome.isFailure ? AnyShapeStyle(Palette.failure) : AnyShapeStyle(.secondary))
                    .frame(width: 16)
                Text("\(step.seq)")
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
                    .frame(width: 28, alignment: .trailing)
                Text(entry.title)
                    .font(.callout)
                    .frame(width: 96, alignment: .leading)
                Text(StepSummary.line(for: step))
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .frame(maxWidth: .infinity, alignment: .leading)
                if case .exit(let code) = step.outcome {
                    Text("exit \(code)").font(.caption.weight(.semibold)).foregroundStyle(Palette.failure)
                } else if case .error = step.outcome {
                    Text("error").font(.caption.weight(.semibold)).foregroundStyle(Palette.failure)
                }
                Text(Chrome.shortTime(step.at))
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
                    .help("\(Chrome.stamp(step.at)) (\(Chrome.zone))")
            }
            .padding(.vertical, 3)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .hoverHighlight(radius: Radius.chip)
        .help("Open step \(step.seq) in Steps")
    }
}

/// A seek track with a tick wherever a new step began, and marks for failed steps and
/// the verdicts' evidence. Idle stretches are squeezed so steps do not bunch up; marks
/// closer than a few points merge into one, with the count in its tooltip.
private struct FrameTrack: View {
    let frames: [Frame]
    let index: Int
    let marks: TrackMarks
    var enabled: Bool = true
    @Binding var hoverIndex: Int?
    let onSeek: (Int) -> Void

    private static let trackHeight: CGFloat = 4

    var body: some View {
        GeometryReader { geometry in
            let width = geometry.size.width
            let timeline = FrameTimeline(frames: frames, width: width)
            let playhead = timeline.x(of: index)
            ZStack(alignment: .leading) {
                Capsule()
                    .fill(.fill.secondary)
                    .frame(height: Self.trackHeight)

                Capsule()
                    .fill(enabled ? AnyShapeStyle(Color.accentColor) : AnyShapeStyle(.secondary))
                    .frame(width: max(playhead, Self.trackHeight), height: Self.trackHeight)

                ForEach(timeline.ticks(for: frames), id: \.index) { tick in
                    Rectangle()
                        .fill(Color.primary.opacity(0.35))
                        .frame(width: 1, height: 9)
                        .offset(x: min(tick.x, width - 1))
                }

                let merged = FrameTimeline.cluster(
                    timeline.marks(for: frames, failed: marks.failed, evidence: marks.evidence.union(marks.superseded),
                                   verdict: marks.verdict),
                    minGap: 8
                )
                ForEach(merged, id: \.first.step) { group in
                    markView(group.first, superseded: marks.superseded.contains(group.first.step) && !marks.evidence.contains(group.first.step))
                        .frame(width: 11, height: 11)
                        .contentShape(Rectangle())
                        .help(markHelp(group))
                        .offset(x: min(max(group.first.x - 5.5, 0), width - 11), y: -9)
                }

                if let hoverIndex, enabled {
                    Rectangle()
                        .fill(.secondary)
                        .frame(width: 1, height: 14)
                        .offset(x: min(max(timeline.x(of: hoverIndex), 0), width - 1))
                        .allowsHitTesting(false)
                }

                // Clamped so the playhead does not hang off either end.
                Circle()
                    .fill(.white)
                    .overlay(Circle().strokeBorder(.black.opacity(0.15), lineWidth: 0.5))
                    .frame(width: 12, height: 12)
                    .shadow(color: .black.opacity(0.25), radius: 1.5, y: 0.5)
                    .offset(x: min(max(playhead - 6, 0), max(width - 12, 0)))
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

    @ViewBuilder
    private func markView(_ mark: FrameTimeline.Mark, superseded: Bool) -> some View {
        switch mark.kind {
        case .failure:
            Circle()
                .fill(Palette.failure)
                .frame(width: 7, height: 7)
        case .evidence(let verdict):
            Image(systemName: superseded ? "diamond" : "diamond.fill")
                .font(.system(size: 8, weight: .bold))
                .foregroundStyle(superseded ? AnyShapeStyle(.secondary) : AnyShapeStyle(Palette.outcome(verdict)))
        }
    }

    private func markHelp(_ group: FrameTimeline.MarkGroup) -> String {
        let steps = group.marks.map { "\($0.step)" }.joined(separator: ", ")
        if group.marks.count > 1 { return "Steps \(steps)" + (group.marks.contains { $0.kind == .failure } ? ", some errored" : "") }
        switch group.first.kind {
        case .failure: return "Step \(steps) errored"
        case .evidence: return marks.superseded.contains(group.first.step) && !marks.evidence.contains(group.first.step)
            ? "Step \(steps) was cited by an earlier, superseded verdict"
            : "Step \(steps) is cited by the verdict"
        }
    }
}
