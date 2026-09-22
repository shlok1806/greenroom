import AppKit
import Combine
import SwiftUI

/// A player over the frames the daemon captured (ADR 0008), and with "Take
/// control" on, the machine's screen (ADR 0009). Nothing is drawn over the
/// picture except the driving badge; position lives under the track.
struct ScreenView: View {
    let store: RunStore
    let runId: String

    @State private var player = PlayerModel()
    @State private var image: NSImage?
    @State private var busy = false
    /// So a seek request is honoured once, not on every return to the tab.
    @State private var appliedSeekNonce = 0
    @State private var lastTick: Date?
    @State private var pilot: ControlPilot?
    @FocusState private var focused: Bool

    private var driving: Bool { pilot?.active == true }

    private let tick = Timer.publish(every: 0.1, on: .main, in: .common).autoconnect()

    private var machineIsReady: Bool { store.details[runId]?.status == .ready }

    var body: some View {
        VStack(spacing: 0) {
            picture
            Divider()
            controls
        }
        .focusable(!driving)
        // Focus is only for the arrow and space keys; a ring round the whole tab is noise.
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
        .onAppear {
            focused = true
            pilot = store.pilot(for: runId)
            syncFrames()
            // A seek from the Steps tab is raised before this view exists.
            applyPendingSeek()
            lastTick = nil
        }
        // Leaving the tab or the run gives the screen back.
        .onDisappear {
            lastTick = nil
            let leaving = pilot
            Task { await leaving?.release() }
        }
        .onChange(of: runId) { old, new in
            let leaving = store.pilot(for: old)
            Task { await leaving.release() }
            pilot = store.pilot(for: new)
            // This view is not rebuilt per run, so reset by hand.
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

    private var emptyDetail: String {
        guard player.frames.isEmpty else { return "The frame is on its way from the daemon." }
        if machineIsReady { return "Recording starts when the machine is ready." }
        return "This run ended without any frames captured."
    }

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
                        .help("Send this window's mouse and keyboard to the machine, "
                            + "Command shortcuts included. Switch this off to give it back. "
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

            // Outside the ready check: a stopped machine hides the switch but keeps the reason.
            if !driving, let reason = pilot?.endedReason {
                Label("Control ended: \(reason)", systemImage: "exclamationmark.triangle.fill")
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .lineLimit(2)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .trailing)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
    }

    /// Time, frame and step.
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

    /// Taking control pins the player to the newest frame: nobody can click the past.
    private var controlBinding: Binding<Bool> {
        Binding(
            get: { driving },
            set: { wanted in
                guard let pilot else { return }
                Task {
                    if wanted {
                        goLive()
                        player.playing = false
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
                if newValue { goLive() } else { player.live = false }
            }
        )
    }

    private func goLive() {
        player.live = true
        if !player.frames.isEmpty { player.index = player.frames.count - 1 }
    }

    // MARK: - Loading

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
        if player.live || previousFile == nil {
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
        // A cancelled `.task(id:)` still resumes here; never paint a stale frame.
        guard !Task.isCancelled, player.shows(frame.file) else { return }
        image = loaded
    }

    private func capture() async {
        busy = true
        defer { busy = false }
        await store.screenshot(runId: runId)
    }
}

/// A seek track with a tick wherever a new step began. Dragging anywhere seeks.
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

                // Clamped so the playhead does not hang off either end.
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
