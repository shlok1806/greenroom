import AppKit
import Combine
import SwiftUI

/// The machine's recording (ADR 0008): a player over the frames the daemon
/// captured, live or finished. The app never polls for a screenshot; it only
/// ever shows frames the daemon already took.
///
/// With "Take control" on it is also the machine's screen (ADR 0009): the same
/// picture, with the mouse and the keyboard going the other way.
///
/// The screen is the one thing on this window worth looking at, so it gets the
/// room: picture edge to edge, one track under it, and a single line of
/// controls. Everything that used to sit on top of the picture now sits beside
/// the track, because a label over the guest's menu bar hides the very thing
/// the person came to see.
struct ScreenView: View {
    @Bindable var store: RunStore
    let runId: String

    @State private var player = PlayerModel()
    @State private var image: NSImage?
    @State private var busy = false
    /// The nonce of the last seek this view acted on, so a request is honoured
    /// once and not again every time the tab comes back.
    @State private var appliedSeekNonce = 0
    /// When the last playback tick ran, so `advance` is told how much time
    /// really passed rather than how much the timer asked for.
    @State private var lastTick: Date?
    @State private var pilot: ControlPilot?
    @FocusState private var focused: Bool

    /// True while the app is driving the machine.
    var driving: Bool { pilot?.active == true }

    private let tick = Timer.publish(every: 0.1, on: .main, in: .common).autoconnect()

    private var machineIsReady: Bool {
        if case .ready = store.details[runId]?.machine?.status { return true }
        return false
    }

    var body: some View {
        VStack(spacing: 0) {
            picture
            Divider()
            controls
        }
        .focusable(!driving)
        .focused($focused)
        // While the machine has the keyboard the scrubber does not: an arrow
        // key is for the guest, not for the timeline.
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
        .onAppear {
            focused = true
            pilot = store.pilot(for: runId)
            syncFrames()
            // A click in the Steps tab asks for the seek and *then* brings
            // this view into being, so the request is already waiting by the
            // time `onChange` could have seen it. Without this the jump
            // silently became "show the newest frame, live".
            applyPendingSeek()
            lastTick = nil
        }
        // Leaving the tab, or the run, gives the screen back. A machine must
        // never be left believing somebody is at its keyboard.
        .onDisappear {
            lastTick = nil
            let leaving = pilot
            Task { await leaving?.release() }
        }
        .onChange(of: runId) { old, new in
            let leaving = store.pilot(for: old)
            Task { await leaving.release() }
            pilot = store.pilot(for: new)
            // The tab keeps its state across a change of run, so without this
            // the player went on showing the previous run's recording: its
            // frames, its position, and the picture already on screen. It only
            // corrected itself when the two runs happened to hold a different
            // number of frames.
            player = PlayerModel()
            image = nil
            appliedSeekNonce = 0
            lastTick = nil
            syncFrames()
            applyPendingSeek()
        }
        .onChange(of: machineIsReady) { _, ready in
            guard !ready, let pilot else { return }
            Task { await pilot.release() }
        }
        .onChange(of: store.frames[runId]?.count ?? 0) {
            syncFrames()
        }
        .onChange(of: store.seekRequest) {
            applyPendingSeek()
        }
        .onReceive(tick) { now in
            // Not a flat 0.1: a timer fires late under load, and crediting it
            // the interval it asked for instead of the time that passed makes
            // playback drift slow, most visibly at 4x.
            let elapsed = lastTick.map { now.timeIntervalSince($0) } ?? 0
            lastTick = now
            player.advance(by: min(elapsed, 1))
        }
        .task(id: player.current?.file) {
            await loadCurrentImage()
        }
    }

    // MARK: - Picture

    @ViewBuilder
    private var picture: some View {
        if let image {
            ZStack {
                Color.black
                Image(nsImage: image)
                    .resizable()
                    .interpolation(.medium)
                    .scaledToFit()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                // The events go through a layer over the picture, inert until
                // the daemon has granted a lease.
                InputSurface(imageSize: image.size, active: driving) { actions in
                    pilot?.send(actions)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .overlay(alignment: .topTrailing) {
                drivingBadge.allowsHitTesting(false)
            }
        } else {
            ContentUnavailableView(
                emptyTitle,
                systemImage: "film",
                description: Text(emptyDetail)
            )
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private var emptyTitle: String {
        player.frames.isEmpty ? "No recording" : "Loading the frame..."
    }

    /// A finished run will never record anything again, and telling its
    /// reviewer to wait for a machine that is gone is an empty state that
    /// says nothing true.
    private var emptyDetail: String {
        guard player.frames.isEmpty else { return "The frame is on its way from the daemon." }
        if machineIsReady { return "Recording starts when the machine is ready." }
        return "This run ended without any frames captured."
    }

    /// Says, unmissably, that the clicks are going into the machine.
    @ViewBuilder
    private var drivingBadge: some View {
        if driving {
            HStack(spacing: 6) {
                Image(systemName: "cursorarrow.click.2")
                Text("You have control")
                if let screen = pilot?.screen {
                    Text(screen.label)
                        .font(.caption2.monospaced())
                        .opacity(0.8)
                }
            }
            .font(.caption.weight(.semibold))
            .foregroundStyle(.white)
            .padding(.horizontal, 8)
            .padding(.vertical, 4)
            .background(.red.opacity(0.85), in: Capsule())
            .padding(10)
        }
    }

    // MARK: - Controls

    private var controls: some View {
        VStack(spacing: 8) {
            FrameTrack(
                frames: player.frames,
                index: player.index,
                enabled: !driving
            ) { newIndex in
                player.live = false
                player.index = newIndex
            }
            .frame(height: 22)

            HStack(spacing: 10) {
                Button {
                    player.playing.toggle()
                } label: {
                    Image(systemName: player.playing ? "pause.fill" : "play.fill")
                        .frame(width: 12)
                }
                .disabled(player.frames.count < 2 || driving)
                .help(player.playing ? "Pause" : "Play the recording")

                Picker("Speed", selection: $player.speed) {
                    Text("1x").tag(PlayerModel.Speed.normal)
                    Text("4x").tag(PlayerModel.Speed.fast)
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(width: 80)
                .disabled(driving)

                position

                Spacer(minLength: 8)

                if machineIsReady {
                    Toggle("Live", isOn: liveBinding)
                        .toggleStyle(.switch)
                        .controlSize(.small)
                        .disabled(driving)
                        .help("Follow the newest frame as the daemon captures it.")

                    Toggle("Take control", isOn: controlBinding)
                        .toggleStyle(.switch)
                        .controlSize(.small)
                        .disabled(pilot?.busy == true)
                        .fixedSize()
                        .help("Send this window's mouse and keyboard to the machine. "
                            + "The run's conversation records that you took it.")

                    Button {
                        Task { await capture() }
                    } label: {
                        Label("Capture", systemImage: "camera")
                    }
                    .disabled(busy)
                    .help("Ask the daemon for a screenshot now. It lands in the run as a step.")
                }
            }
            .frame(minHeight: 22)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
    }

    /// Where in the recording the picture is, in the three units a reviewer
    /// asks in: time, frame, and the step that was running.
    private var position: some View {
        HStack(spacing: 8) {
            Text("\(Chrome.clock(offset)) / \(Chrome.clock(FrameTimeline.duration(player.frames)))")
                .monospacedDigit()
            if let frame = player.current {
                Text("frame \(player.index + 1) of \(player.frames.count)")
                    .foregroundStyle(.secondary)
                    .monospacedDigit()
                if frame.step > 0 {
                    Text("step \(frame.step)")
                        .foregroundStyle(.secondary)
                        .monospacedDigit()
                }
            }
        }
        .font(.caption)
        .lineLimit(1)
        .help(player.current.map { "\(Chrome.stamp($0.at))\n\($0.file)" } ?? "")
    }

    private var offset: TimeInterval {
        FrameTimeline.offset(player.frames, at: player.index)
    }

    /// Taking control also pins the player to the newest frame: nobody can
    /// click on the past, so the picture has to be the present.
    private var controlBinding: Binding<Bool> {
        Binding(
            get: { driving },
            set: { wanted in
                guard let pilot else { return }
                Task {
                    if wanted {
                        player.live = true
                        player.playing = false
                        if !player.frames.isEmpty { player.index = player.frames.count - 1 }
                        await pilot.take()
                    } else {
                        await pilot.release()
                    }
                }
            }
        )
    }

    private var liveBinding: Binding<Bool> {
        Binding(
            get: { player.live },
            set: { newValue in
                player.live = newValue
                if newValue, !player.frames.isEmpty {
                    player.index = player.frames.count - 1
                }
            }
        )
    }

    // MARK: - Loading

    private func step(by delta: Int) {
        guard !player.frames.isEmpty else { return }
        player.live = false
        player.index = min(max(player.index + delta, 0), player.frames.count - 1)
    }

    /// Reconciles the player's own frame list with the store's. A single new
    /// frame arriving live is appended in place (so `live` keeps following
    /// it); anything else (the first load, a reconnect's full reload) resets
    /// the list wholesale while trying to keep the frame on screen.
    private func syncFrames() {
        let latest = store.frames[runId] ?? []
        guard latest != player.frames else { return }
        if latest.count == player.frames.count + 1, latest.dropLast().elementsEqual(player.frames), let new = latest.last {
            player.append(new)
            return
        }
        let previousFile = player.current?.file
        player.frames = latest
        if player.live || previousFile == nil {
            player.index = max(0, latest.count - 1)
        } else if let previousFile, let found = latest.firstIndex(where: { $0.file == previousFile }) {
            player.index = found
        } else {
            player.index = min(player.index, max(0, latest.count - 1))
        }
    }

    /// Acts on the store's standing seek request, at most once per request.
    /// The nonce is what makes it once: coming back to the tab must not re-run
    /// a jump the person has since scrubbed away from.
    private func applyPendingSeek() {
        guard let request = store.seekRequest,
              request.runId == runId,
              request.nonce != appliedSeekNonce else { return }
        appliedSeekNonce = request.nonce
        player.seek(toStep: request.step)
    }

    private func loadCurrentImage() async {
        guard let frame = player.current else {
            image = nil
            return
        }
        let loaded = await store.frameImage(runId: runId, file: frame.file)
        // Cancelling a `.task(id:)` does not unwind the load it started: it
        // resumes here and assigns anyway. Switching runs while a frame was
        // still on the wire therefore painted the previous run's screen under
        // the new run's name, and left it there when the new run had no
        // frames of its own to overwrite it with. Nothing is shown unless the
        // frame it was fetched for is still the frame being asked for.
        guard !Task.isCancelled, player.shows(frame.file) else { return }
        image = loaded
    }

    private func capture() async {
        busy = true
        defer { busy = false }
        await store.screenshot(runId: runId)
    }
}

/// The timeline: where the recording is, and where its steps are.
///
/// A slider was fine for a hundred frames and useless for two thousand, where
/// the only question a person has is "where did the work happen". The track
/// answers it by drawing a tick wherever a new step began, so a run reads as a
/// map rather than a length, and dragging anywhere on it seeks, rather than
/// only on a knob that has to be found first.
private struct FrameTrack: View {
    let frames: [Frame]
    let index: Int
    var enabled: Bool = true
    let onSeek: (Int) -> Void

    var body: some View {
        GeometryReader { geometry in
            let width = geometry.size.width
            let timeline = FrameTimeline(count: frames.count, width: width)
            let playhead = timeline.x(of: index)
            ZStack(alignment: .leading) {
                Capsule()
                    .fill(.quaternary)
                    .frame(height: 4)

                Capsule()
                    .fill(.secondary)
                    .frame(width: max(playhead, 0), height: 4)

                ForEach(timeline.ticks(for: frames), id: \.index) { tick in
                    Rectangle()
                        .fill(.tertiary)
                        .frame(width: 1, height: 9)
                        .offset(x: min(tick.x, width - 1))
                }

                // Held inside the track: at the last frame the playhead is at
                // the full width, and drawn from there it hangs off the end.
                Capsule()
                    .fill(enabled ? Color.accentColor : Color.secondary)
                    .frame(width: 3, height: 16)
                    .offset(x: min(max(playhead - 1.5, 0), max(width - 3, 0)))
                    .shadow(radius: 1)
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
            .opacity(frames.count > 1 ? 1 : 0.4)
        }
        .accessibilityElement()
        .accessibilityLabel("Recording position")
        .accessibilityValue("frame \(index + 1) of \(frames.count)")
    }
}
