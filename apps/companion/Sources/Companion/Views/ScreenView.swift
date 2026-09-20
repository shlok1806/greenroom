import AppKit
import Combine
import SwiftUI

/// The machine's recording (ADR 0008): a scrubber over the frames the daemon
/// captured, live or finished. The app never polls for a screenshot; it only
/// ever shows the frames the daemon already took.
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
    /// really passed rather than how much the timer was asked for.
    @State private var lastTick: Date?
    @FocusState private var focused: Bool

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
        .focusable()
        .focused($focused)
        .onKeyPress(.leftArrow) {
            player.live = false
            player.index = max(0, player.index - 1)
            return .handled
        }
        .onKeyPress(.rightArrow) {
            player.live = false
            player.index = min(max(player.frames.count - 1, 0), player.index + 1)
            return .handled
        }
        .onKeyPress(.space) {
            player.playing.toggle()
            return .handled
        }
        .onAppear {
            focused = true
            syncFrames()
            // A click in the Steps tab asks for the seek and *then* brings this
            // view into being, so the request is already waiting by the time
            // `onChange` could have seen it. Without this the jump silently
            // became "show the newest frame, live".
            applyPendingSeek()
            lastTick = nil
        }
        .onDisappear {
            lastTick = nil
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
            ZStack(alignment: .topLeading) {
                Color.black
                Image(nsImage: image)
                    .resizable()
                    .scaledToFit()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                overlayLabel
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            ContentUnavailableView(
                "No frames yet",
                systemImage: "film",
                description: Text("Recording starts when the machine is ready.")
            )
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private var overlayLabel: some View {
        HStack(spacing: 8) {
            if let frame = player.current {
                Text(elapsed(frame))
                    .monospacedDigit()
                Text("step \(frame.step)")
            }
        }
        .font(.caption.weight(.semibold))
        .foregroundStyle(.white)
        .padding(.horizontal, 8)
        .padding(.vertical, 4)
        .background(.black.opacity(0.55), in: Capsule())
        .padding(10)
    }

    private func elapsed(_ frame: Frame) -> String {
        guard let start = store.details[runId]?.createdAt else { return "" }
        let seconds = max(0, Int(frame.at.timeIntervalSince(start)))
        return String(format: "%02d:%02d", seconds / 60, seconds % 60)
    }

    // MARK: - Controls

    private var controls: some View {
        VStack(spacing: 8) {
            scrubber
            HStack(spacing: 12) {
                Button {
                    player.playing.toggle()
                } label: {
                    Image(systemName: player.playing ? "pause.fill" : "play.fill")
                }
                .disabled(player.frames.count < 2)

                Picker("Speed", selection: $player.speed) {
                    Text("1x").tag(PlayerModel.Speed.normal)
                    Text("4x").tag(PlayerModel.Speed.fast)
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(width: 90)

                Toggle("Live", isOn: liveBinding)
                    .toggleStyle(.switch)
                    .disabled(!machineIsReady)

                Spacer()

                Button {
                    Task { await capture() }
                } label: {
                    Label("Capture now", systemImage: "camera")
                }
                .disabled(!machineIsReady || busy)

                if let frame = player.current {
                    Text(frame.file)
                        .font(.caption.monospaced())
                        .foregroundStyle(.secondary)
                }
            }
        }
        .padding(10)
    }

    private var scrubber: some View {
        Slider(
            value: Binding(
                get: { Double(player.index) },
                set: { newValue in
                    player.live = false
                    player.index = min(max(Int(newValue.rounded()), 0), max(player.frames.count - 1, 0))
                }
            ),
            in: 0...Double(max(player.frames.count - 1, 0))
        )
        .disabled(player.frames.count < 2)
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
    /// The nonce is what makes it once: coming back to this tab must not
    /// re-run a jump the person has since scrubbed away from.
    private func applyPendingSeek() {
        guard let request = store.seekRequest,
              request.runId == runId,
              request.nonce != appliedSeekNonce
        else { return }
        appliedSeekNonce = request.nonce
        player.seek(toStep: request.step)
    }

    private func loadCurrentImage() async {
        guard let frame = player.current else {
            image = nil
            return
        }
        image = await store.frameImage(runId: runId, file: frame.file)
    }

    private func capture() async {
        busy = true
        defer { busy = false }
        await store.screenshot(runId: runId)
    }
}
