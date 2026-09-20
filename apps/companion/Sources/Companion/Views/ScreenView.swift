import AppKit
import Combine
import SwiftUI

/// The machine's recording (ADR 0008): a scrubber over the frames the daemon
/// captured, live or finished. The app never polls for a screenshot; it only
/// ever shows the frames the daemon already took.
///
/// With "Take control" on it is also the machine's screen (ADR 0009): the
/// same picture, with the mouse and the keyboard going the other way.
struct ScreenView: View {
    @Bindable var store: RunStore
    let runId: String

    @State private var player = PlayerModel()
    @State private var image: NSImage?
    @State private var busy = false
    @State private var pilot: ControlPilot?
    @FocusState private var focused: Bool

    /// True while this app is driving the machine.
    private var driving: Bool { pilot?.active == true }

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
        // While the machine has the keyboard, the scrubber does not: an
        // arrow key is for the guest, not for the timeline.
        .onKeyPress(.leftArrow) {
            guard !driving else { return .ignored }
            player.live = false
            player.index = max(0, player.index - 1)
            return .handled
        }
        .onKeyPress(.rightArrow) {
            guard !driving else { return .ignored }
            player.live = false
            player.index = min(max(player.frames.count - 1, 0), player.index + 1)
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
        }
        // Leaving the tab, or the run, gives the screen back. A machine must
        // never be left believing somebody is at its keyboard.
        .onDisappear {
            let leaving = pilot
            Task { await leaving?.release() }
        }
        .onChange(of: runId) { old, new in
            let leaving = store.pilot(for: old)
            Task { await leaving.release() }
            pilot = store.pilot(for: new)
        }
        .onChange(of: machineIsReady) { _, ready in
            guard !ready, let pilot else { return }
            Task { await pilot.release() }
        }
        .onChange(of: store.frames[runId]?.count ?? 0) {
            syncFrames()
        }
        .onChange(of: store.seekRequest) {
            guard let request = store.seekRequest, request.runId == runId else { return }
            player.seek(toStep: request.step)
        }
        .onReceive(tick) { _ in
            player.advance(by: 0.1)
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
                // The events go through a layer over the picture, which is
                // inert until the daemon has granted the lease.
                InputSurface(imageSize: image.size, active: driving) { actions in
                    pilot?.send(actions)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                // The labels sit above the surface and must not swallow a
                // click meant for the machine underneath them.
                overlayLabel
                    .allowsHitTesting(false)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .overlay(alignment: .topTrailing) { drivingBadge.allowsHitTesting(false) }
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

    /// Says, unmissably, that clicks are going into the machine.
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
                    .disabled(!machineIsReady || driving)

                Toggle("Take control", isOn: controlBinding)
                    .toggleStyle(.switch)
                    .disabled(!machineIsReady || pilot?.busy == true)
                    .help("Send this window's mouse and keyboard to the machine. "
                        + "The run's conversation records that you took it.")

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
