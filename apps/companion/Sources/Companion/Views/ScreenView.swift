import AppKit
import Combine
import SwiftUI

/// A player over the frames the daemon captured (ADR 0008), the live stream while
/// following a ready machine (ADR 0011), and while the lease is held, the machine's
/// mouse and keys (ADR 0009). The stage: a quiet title row (the machine and where the
/// picture comes from), the always-dark well, and the player under it. Nothing but the
/// effects below is drawn over the picture; position lives under the track. A well with no picture says why in
/// words, or shows the loader while the machine boots or the live screen connects.
///
/// The signature moments (ADR 0006) are effect layers over the picture, each once per
/// event and none under Reduce Motion: the first picture after the well waited for it
/// resolves out of glyphs (the boot reveal); a machine destroyed while watched dissolves
/// into glyphs and holds as a still before the recording shows (the power-down); and where
/// the agent clicked or typed ripples over the picture, or holds still on a paused step
/// (click marks, `m`). None of them delays a key or a click.
struct ScreenView: View {
    let store: RunStore
    let runId: String
    /// On screen. The layout keeps a covered or zoomed-away screen in the tree, so its
    /// player and the lease live on; it stops the live stream and takes no input then.
    var visible = true
    /// Has the keyboard: its label takes the brand, as the steps' does.
    var hasKeys = false

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
    /// When the live screen began connecting, for the loader's timer.
    @State private var connectingSince: Date?
    @AppStorage("showsConversation") private var showsConversation = true
    @AppStorage(ClickMarks.storageKey) private var showsClickMarks = true
    /// The boot reveal or the power-down playing now, if either is.
    @State private var moment: ScreenMoment?
    /// The well waited for a picture during this open of the run, so its first one reveals.
    @State private var revealArmed = false
    @State private var revealPlayed = false
    /// Transient click marks over the picture.
    @State private var ripples: [ClickMarkLayer.Ripple] = []
    /// The newest step already seen, and of which run, so only steps that arrive while
    /// watching ripple (never the ones read when a run opens).
    @State private var seenStep: (runId: String, seq: Int?)?
    @State private var wellSize: CGSize = .zero
    @FocusState private var focused: Bool
    @Environment(\.theme) private var theme
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.momentFreeze) private var momentFreeze
    @Environment(\.displayScale) private var displayScale

    private var facts: RunFacts { store.facts(runId) }
    private var driving: Bool { pilot?.active == true }

    /// Following live on a ready machine streams the screen (ADR 0011); the
    /// past, and any moment the stream is down, come from the recording.
    private var wantsLive: Bool { onScreen && visible && player.live && machineIsReady }
    private var showsLive: Bool { wantsLive && live?.phase == .playing && live?.pixelSize != nil }

    private let tick = Timer.publish(every: 0.1, on: .main, in: .common).autoconnect()

    /// One answer for "is there a machine to watch or drive": the run's facts.
    private var machineIsReady: Bool { facts.machineReady }

    private var frameCount: Int { store.frames[runId]?.count ?? 0 }

    private var hasPlayer: Bool { !player.frames.isEmpty || machineIsReady || pilot?.endedReason != nil }

    var body: some View {
        stack
        .focusable(!driving)
        // Focus holds the keyboard on the stage, so no text field takes it when the Screen
        // opens; the keys themselves come through the router. A focus ring is noise here.
        .focusEffectDisabled()
        .focused($focused)
        // Space, the arrows and G, from the registry through the window's router. While
        // driving none is offered: the keys belong to the guest.
        .offersActions(.screen, screenActions, refresh: runId) { perform($0) }
        .offersActions(.run, machineIsReady && !player.live && !driving ? [.followLive] : [], refresh: runId) { _ in goLive() }
        .onAppear {
            // After the layout has placed it: a claim made before that is dropped, and the
            // window then gives the keyboard to the first text field (the run search).
            Task { @MainActor in focused = true }
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
            moment = nil
            revealArmed = false
            revealPlayed = false
            ripples = []
            seenStep = nil
            syncFrames()
            applyPendingSeek()
            syncLive()
        }
        .onChange(of: wantsLive) { was, now in
            // A machine going away while its live picture shows: catch the picture for the
            // power-down before the stream stops and clears it.
            if was, !now, !facts.isAlive { startPowerDown() }
            syncLive()
        }
        .onChange(of: facts.isAlive) { was, now in
            if was, !now { startPowerDown() }
        }
        .onChange(of: awaitingPicture, initial: true) { _, waiting in
            if waiting { revealArmed = true }
        }
        .onChange(of: pictureSize != nil) { _, shown in
            if shown { startReveal() }
        }
        .onChange(of: store.steps[runId]?.last?.seq, initial: true) { _, newest in
            stepsArrived(newest)
        }
        .onChange(of: playheadStep) { old, new in
            playheadMoved(from: old, to: new)
        }
        .onChange(of: sourceState == .connecting, initial: true) { _, connecting in
            connectingSince = connecting ? (connectingSince ?? Date()) : nil
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
        .preference(key: PlayheadStepKey.self, value: playheadStep)
    }

    /// The step at the playhead: the newest while following a live machine. A finished run
    /// "follows" only its last frame, which may be older than its last step (the destroy).
    private var playheadStep: Int? {
        let steps = store.steps[runId] ?? []
        if (player.live && machineIsReady) || player.current == nil { return steps.last?.seq }
        guard let frame = player.current else { return nil }
        return steps.last { $0.seq <= frame.step }?.seq
    }

    private var screenActions: Set<ActionID> {
        var ids: Set<ActionID> = []
        if returnPoint != nil { ids.insert(.backToVerdict) }
        guard !driving else { return ids }
        ids.insert(.clickMarks)
        if player.frames.count > 1 { ids.formUnion([.play, .previousFrame, .nextFrame, .speed]) }
        if machineIsReady ? !player.live : player.index < player.frames.count - 1 { ids.insert(.latest) }
        return ids
    }

    private func perform(_ id: ActionID) {
        switch id {
        case .play: player.playing.toggle()
        case .speed: player.speed = player.speed == .normal ? .fast : .normal
        case .previousFrame: step(by: -1)
        case .nextFrame: step(by: 1)
        case .latest:
            if machineIsReady {
                goLive()
            } else {
                player.playing = false
                player.index = max(0, player.frames.count - 1)
            }
        case .backToVerdict: backToVerdict()
        case .clickMarks:
            showsClickMarks.toggle()
            if !showsClickMarks { ripples = [] }
        default: break
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
                    .houseLightsLit(radius: Radius.md)
                    .padding(.horizontal, Space.l)
                    .padding(.top, Space.l)
                    .transition(.opacity.combined(with: .move(edge: .top)))
            } else if let evidenceStep, !facts.isAlive || returnPoint != nil, facts.verdict != nil {
                // A live run too, once evidence was opened from the card: the way back
                // (and Esc) must be there, not only Go Live.
                EvidenceBar(step: evidenceStep, back: backAction, record: {
                    store.requestSeek(runId: runId, step: evidenceStep, fromVerdict: true, inSteps: true)
                })
            }
            titleRow
                .padding(.horizontal, Space.l)
                .padding(.top, driving ? Space.m : Space.l)
                .padding(.bottom, Space.s)
            well
                .padding(.horizontal, Space.l)
                .padding(.bottom, hasPlayer ? Space.m : Space.l)
                .layoutPriority(1)
            // A booting machine or an empty run has nothing to play or take.
            if hasPlayer {
                PlayerBar(
                    player: $player,
                    steps: store.steps[runId] ?? [],
                    verdict: store.verdict(runId),
                    supersededEvidence: supersededEvidence,
                    hoverIndex: $hoverIndex,
                    driving: driving,
                    machineIsReady: machineIsReady,
                    liveFailure: liveFailure,
                    endedReason: driving ? nil : pilot?.endedReason,
                    goLive: goLive
                )
            }
            Spacer(minLength: Space.m)
        }
    }

    /// The machine by its image, quiet, and where the picture comes from. Only once there
    /// is a machine or a recording: before that the well says it all.
    private var titleRow: some View {
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            SectionLabel(title: "Screen", ink: hasKeys ? theme.brandInk(on: .background) : nil)
                .fixedSize()
            if let machineName {
                Text(machineName)
                    .monoStyle(size: TypeScale.monoSmall)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .help("The machine's image")
            }
            Spacer(minLength: Space.s)
            if hasPlayer {
                SourceLabel(state: hoverSource ?? sourceState)
            }
        }
        .frame(minHeight: 18)
    }

    private var machineName: String? {
        let image = store.details[runId]?.machine?.image ?? store.run(runId)?.image
        return image.flatMap { $0.isEmpty ? nil : $0 }
    }

    /// While hovering the track the source line previews the time under the pointer.
    private var hoverSource: ScreenSourceState? {
        guard let hoverIndex, !driving else { return nil }
        return .recording(position: FrameTimeline.offset(player.frames, at: hoverIndex, from: facts.started),
                          total: facts.duration(now: Date()))
    }

    // MARK: - Picture

    /// What `InputSurface` maps against: the picture actually on screen.
    private var pictureSize: CGSize? {
        showsLive ? live?.pixelSize : image?.size
    }

    // MARK: - Signature moments

    /// The well is waiting for its first picture: booting, connecting, or ready with
    /// nothing captured yet.
    private var awaitingPicture: Bool {
        guard pictureSize == nil else { return false }
        switch WellState.of(facts: facts, frames: store.frames[runId], connecting: sourceState == .connecting) {
        case .booting, .connecting, .waitingForFirstFrame: return true
        default: return false
        }
    }

    /// The picture on screen now, for a moment to sample: the live layer while it plays
    /// (read on its own queue), else the recording frame already decoded.
    private var pictureSource: GlyphStill.Source? {
        if let live, live.isRunning, live.phase == .playing { return .live(live.output) }
        return image.map { .frame(.init(image: $0)) }
    }

    /// The first picture after the well waited for it resolves out of its glyphs, once
    /// per open of the run.
    private func startReveal() {
        guard revealArmed, !revealPlayed, onScreen, visible, let pictureSize, let source = pictureSource else { return }
        revealArmed = false
        revealPlayed = true
        begin(.reveal, picture: pictureSize, source: source)
    }

    /// The machine went away while its picture showed: that picture dissolves into glyphs
    /// and holds as a still, then the well shows the recording as usual.
    private func startPowerDown() {
        guard moment?.kind != .powerDown, onScreen, visible, case .ended = facts.phase,
              let picture = live?.pixelSize ?? image?.size, let source = pictureSource else { return }
        begin(.powerDown, picture: picture, source: source)
    }

    /// Samples the picture off the main actor, then plays the moment from when its glyphs
    /// are ready. A reveal veils the picture meanwhile; if the glyphs are not ready soon,
    /// the moment is dropped and the picture simply shows. Reduce Motion plays nothing.
    private func begin(_ kind: GlyphMotion.Kind, picture: CGSize, source: GlyphStill.Source) {
        guard GlyphMotion.plays(reduceMotion: reduceMotion) else { return }
        let started = ScreenMoment(kind: kind, picture: picture)
        let id = started.id
        moment = started
        let cell = GlyphCell.size
        let grid = GlyphSampler.grid(picture: picture, view: wellSize, cell: cell)
        let scale = max(1, displayScale)
        let ground = theme.wellRGB
        let freeze = momentFreeze
        let tokens = DesignData.shared.tokens.motion
        Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(300))
            if moment?.id == id, moment?.still == nil, freeze == nil { moment = nil }
        }
        Task { @MainActor in
            let still = await GlyphStill.make(from: source, columns: grid.columns, rows: grid.rows,
                                              cell: cell, scale: scale, ground: ground)
            guard moment?.id == id else { return }
            guard let still else {
                moment = nil
                return
            }
            moment?.still = still
            moment?.start = Date()
            guard freeze == nil else { return }
            try? await Task.sleep(for: .seconds(GlyphMotion.duration(of: kind, tokens: tokens)))
            if moment?.id == id { moment = nil }
        }
    }

    // MARK: - Click marks

    /// A paused recording on a step that clicked or typed: its mark, held still.
    private var stillMark: ClickMarks.Target? {
        guard showsClickMarks, !player.playing, !(player.live && machineIsReady), !driving,
              let seq = playheadStep, let steps = store.steps[runId],
              let step = steps.last(where: { $0.seq == seq }) else { return nil }
        return ClickMarks.target(of: step, in: steps)
    }

    /// Steps arriving while the machine is followed live ripple where they clicked.
    private func stepsArrived(_ newest: Int?) {
        let seen = seenStep?.runId == runId ? seenStep?.seq : nil
        seenStep = (runId, newest)
        guard showsClickMarks, onScreen, visible, player.live, machineIsReady, !driving, pictureSize != nil else { return }
        ripple(ClickMarks.arrived(after: seen, in: store.steps[runId] ?? []).map(\.target))
    }

    /// A playing recording ripples at each step the playhead passes.
    private func playheadMoved(from old: Int?, to new: Int?) {
        guard showsClickMarks, player.playing, !(player.live && machineIsReady), !driving, pictureSize != nil else { return }
        ripple(ClickMarks.passed(from: old, to: new, in: store.steps[runId] ?? []).map(\.target))
    }

    private func ripple(_ targets: [ClickMarks.Target]) {
        guard !targets.isEmpty else { return }
        let now = Date()
        let lasts = Double(DesignData.shared.tokens.motion.clickMark.ms) / 1000
        ripples.removeAll { now.timeIntervalSince($0.start) >= lasts }
        ripples += targets.map { ClickMarkLayer.Ripple(fraction: $0.fraction, start: now) }
        Task { @MainActor in
            try? await Task.sleep(for: .seconds(lasts))
            let later = Date()
            ripples.removeAll { later.timeIntervalSince($0.start) >= lasts }
        }
    }

    /// The well hugs the picture's shape, so there is no letterbox to fill; before a
    /// picture arrives it is a 4:3 placeholder.
    private var well: some View {
        let shape = pictureSize ?? CGSize(width: 4, height: 3)
        return ZStack {
            theme.well
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
                // Covered by another pane, the surface lets go of the keyboard, so keys
                // never go to a machine the person cannot see; the lease stays.
                InputSurface(imageSize: pictureSize, active: driving && visible) { actions in
                    pilot?.send(actions)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                // The only thing drawn over the picture (ADR 0006 decision 5).
                if showsClickMarks, !driving, !ripples.isEmpty || stillMark != nil {
                    ClickMarkLayer(picture: pictureSize, ripples: ripples, still: stillMark?.fraction)
                }
            } else {
                emptyWell
            }
            if let moment {
                GlyphMomentLayer(moment: moment, ground: theme.well)
            }
        }
        .aspectRatio(shape, contentMode: .fit)
        .onGeometryChange(for: CGSize.self) { $0.size } action: { wellSize = $0 }
        .clipShape(RoundedRectangle(cornerRadius: Radius.lg, style: .continuous))
        .overlay {
            // The driving edge: the one lit frame while the house lights are down.
            RoundedRectangle(cornerRadius: Radius.lg, style: .continuous)
                .strokeBorder(driving ? theme.color(.driving) : theme.hairline, lineWidth: driving ? 3 : Space.hairline)
                .allowsHitTesting(false)
        }
        .houseLightsLit(radius: Radius.lg)
        .animation(PaneMotion.settle(reduceMotion: reduceMotion), value: driving)
        .frame(maxWidth: .infinity)
    }

    /// The well with no picture: why, in words, in the dark theme's ink (the well is dark
    /// in every theme). Work that takes a while shows the loader with its timer.
    @ViewBuilder
    private var emptyWell: some View {
        Group {
            switch WellState.of(facts: facts, frames: store.frames[runId], connecting: sourceState == .connecting) {
            case .booting:
                wellWords(loader: Loader(label: "Booting the machine", since: facts.started,
                                         ink: theme.wellInk, dim: theme.wellDim),
                          lines: bootLines,
                          message: "Its screen appears here once it is ready.")
            case .connecting:
                wellWords(loader: Loader(label: "Connecting to the screen", since: connectingSince ?? Date(),
                                         ink: theme.wellInk, dim: theme.wellDim),
                          lines: bootLines,
                          message: "The machine's screen shows here as it happens.")
            case .reading:
                HStack(spacing: Space.s) {
                    Spinner(size: TypeScale.small)
                    Text("Reading the recording")
                }
                .monoStyle(size: TypeScale.small)
                .foregroundStyle(theme.wellDim)
            case .loadingFrame:
                Spinner().foregroundStyle(theme.wellDim)
            case .failedToStart(let reason):
                wellWords(title: "The machine did not start", message: reason ?? "It failed while booting, so nothing was recorded.")
            case .waitingForFirstFrame:
                VStack(spacing: Space.l) {
                    wellWords(title: "No recording yet", message: "The first frame is taken a few seconds after the machine is ready.")
                    BootLinesView(lines: bootLines)
                }
            case .noFrames:
                wellWords(title: "No recording", message: "This run ended without any frames captured.")
            }
        }
        .multilineTextAlignment(.center)
        .padding(Space.l)
    }

    /// The loader, then the machine's boot so far as terminal lines; the sentence only
    /// while there are no lines to say it.
    private func wellWords(loader: Loader, lines: [BootLine], message: String) -> some View {
        // Lines read as a terminal: the loader sits on their left edge, not centred over them.
        VStack(alignment: lines.isEmpty ? .center : .leading, spacing: Space.l) {
            loader
            if lines.isEmpty {
                Text(message)
                    .readingStyle(size: TypeScale.readingSmall)
                    .foregroundStyle(theme.wellDim)
            } else {
                BootLinesView(lines: lines)
            }
        }
    }

    /// What the daemon has said about the machine coming up (`BootLog`).
    private var bootLines: [BootLine] {
        BootLog.lines(facts: facts, detail: store.details[runId], image: store.run(runId)?.image,
                      steps: store.steps[runId], connecting: sourceState == .connecting ? connectingSince : nil)
    }

    private func wellWords(title: String, message: String) -> some View {
        VStack(spacing: Space.xs) {
            Text(title)
                .headingStyle()
                .foregroundStyle(theme.wellInk)
            Text(message)
                .readingStyle(size: TypeScale.readingSmall)
                .foregroundStyle(theme.wellDim)
                .lineLimit(4)
        }
        .frame(maxWidth: 420)
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
        returnPoint = nil
        evidenceStep = nil
    }

    private func backToVerdict() {
        if let returnPoint {
            player.index = min(returnPoint.index, max(0, player.frames.count - 1))
            player.live = returnPoint.live
        }
        returnPoint = nil
        evidenceStep = nil
        // Back where the person was: the card keeps whatever they had open (its draft).
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

/// The machine coming up as terminal lines in the well (ADR 0006, Boot): a mark, what
/// happened, to what, and how long it took, or a tick and the time so far while it
/// goes. In the well's own ink, since the well is dark in every theme.
private struct BootLinesView: View {
    let lines: [BootLine]

    @Environment(\.theme) private var theme

    var body: some View {
        TimelineView(.periodic(from: .now, by: 1)) { context in
            Grid(alignment: .leading, horizontalSpacing: Space.s, verticalSpacing: Space.xs) {
                ForEach(Array(lines.enumerated()), id: \.offset) { _, line in
                    GridRow {
                        mark(line)
                        Text(line.word)
                            .monoStyle(.monoMedium, size: TypeScale.monoSmall)
                            .foregroundStyle(theme.wellInk)
                        Text(line.detail)
                            .monoStyle(size: TypeScale.monoSmall)
                            .foregroundStyle(theme.wellDim)
                            .lineLimit(1)
                            .truncationMode(.middle)
                            .frame(maxWidth: 260, alignment: .leading)
                        Text(time(line, now: context.date))
                            .monoStyle(size: TypeScale.monoSmall)
                            .monospacedDigit()
                            .foregroundStyle(theme.wellDim)
                            .gridColumnAlignment(.trailing)
                    }
                }
            }
        }
        .fixedSize()
        .accessibilityElement(children: .combine)
    }

    @ViewBuilder
    private func mark(_ line: BootLine) -> some View {
        switch line.state {
        case .done:
            Text("✓")
                .monoStyle(size: TypeScale.monoSmall)
                .foregroundStyle(theme.wellDim)
                .accessibilityLabel("done")
        case .working:
            Spinner(size: TypeScale.monoSmall)
                .foregroundStyle(theme.wellInk)
        }
    }

    private func time(_ line: BootLine, now: Date) -> String {
        switch line.state {
        case .done: line.seconds.map(BootLog.took) ?? ""
        case .working(let since): LoaderMotion.elapsed(now.timeIntervalSince(since))
        }
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
                Button("← Back to Verdict", action: back)
                    .buttonStyle(.quiet(small: true))
                    .help("Return to where you were (\(ActionRegistry.label(.back)))")
            }
            Text("◆ Step \(step), cited by the verdict")
                .monoStyle(size: TypeScale.monoSmall)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.tail)
            Spacer(minLength: 0)
            if let record {
                Button("Step Record", action: record)
                    .buttonStyle(.textLink)
                    .help("Open this step's input and output in Steps")
            }
        }
        .padding(.horizontal, Space.l)
        .padding(.top, Space.m)
    }
}

/// Above the well while driving: what is happening, and the way out. Never over the
/// picture, where it would cover the guest's own menu bar.
private struct DrivingBar: View {
    let screen: GuestScreen?

    @Environment(\.theme) private var theme

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            Text("You have control")
                .readingStyle(.readingSemiBold, size: TypeScale.readingSmall)
            Text("Your keys and clicks go to the machine\(screen.map { " (\($0.label))" } ?? "")")
                .readingStyle(size: TypeScale.small)
            Spacer(minLength: Space.s)
            Text("Give Back is in the top bar")
                .monoStyle(.monoMedium, size: TypeScale.monoSmall)
        }
        // The ground as ink: it clears the text threshold on the driving role in every theme.
        .foregroundStyle(theme.background)
        .padding(.horizontal, Space.m)
        .padding(.vertical, Space.s)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(theme.color(.driving), in: RoundedRectangle(cornerRadius: Radius.md, style: .continuous))
        .accessibilityElement(children: .contain)
        .accessibilityLabel("You have control of the machine")
    }
}
