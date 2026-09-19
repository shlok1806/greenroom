import AppKit
import Combine
import SwiftUI

/// The machine's screen, as the newest screenshot the run recorded. Live mode
/// asks the daemon for a fresh one every two seconds, and only while this tab
/// is on screen and the machine is ready (ADR 0007).
struct ScreenView: View {
    @Bindable var store: RunStore
    let runId: String

    @State private var image: NSImage?
    @State private var shownArtifact: String?
    @State private var live = false
    @State private var visible = false
    @State private var busy = false

    private let tick = Timer.publish(every: 2, on: .main, in: .common).autoconnect()

    private var machineIsReady: Bool {
        if case .ready = store.details[runId]?.machine?.status { return true }
        return false
    }

    var body: some View {
        VStack(spacing: 0) {
            controls
            Divider()
            picture
        }
        .onAppear {
            visible = true
            Task { await load() }
        }
        .onDisappear { visible = false }
        .onReceive(tick) { _ in
            guard live, visible, machineIsReady, !busy else { return }
            Task { await capture() }
        }
        .onChange(of: store.steps[runId]?.count ?? 0) {
            Task { await load() }
        }
    }

    private var controls: some View {
        HStack(spacing: 12) {
            Toggle("Live", isOn: $live)
                .toggleStyle(.switch)
                .disabled(!machineIsReady)
            Button {
                Task { await capture() }
            } label: {
                Label("Capture now", systemImage: "camera")
            }
            .disabled(!machineIsReady || busy)
            Spacer()
            if let shownArtifact {
                Text(shownArtifact)
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
            }
        }
        .padding(10)
    }

    // A screenshot is always shown when there is one. Whether the machine can
    // take another is a banner, not a reason to hide the last one.
    @ViewBuilder
    private var picture: some View {
        if let image {
            VStack(spacing: 0) {
                if !machineIsReady {
                    banner("The machine is not running. This is the last screenshot the run recorded.")
                }
                Image(nsImage: image)
                    .resizable()
                    .scaledToFit()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .padding(12)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(Color(nsColor: .underPageBackgroundColor))
        } else if machineIsReady {
            ContentUnavailableView(
                "No screenshot yet",
                systemImage: "camera",
                description: Text("Take one with Capture now, or turn Live on.")
            )
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            ContentUnavailableView(
                "No screenshot",
                systemImage: "display.trianglebadge.exclamationmark",
                description: Text("This run recorded no screenshot, and its machine is not running.")
            )
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private func banner(_ text: String) -> some View {
        HStack(spacing: 8) {
            Image(systemName: "info.circle")
            Text(text)
            Spacer()
        }
        .font(.callout)
        .foregroundStyle(.secondary)
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .background(.quaternary)
    }

    private func load() async {
        guard let latest = store.latestScreenshot(runId) else { return }
        guard latest.name != shownArtifact else { return }
        guard let data = await store.artifact(runId: runId, name: latest.name) else { return }
        if let decoded = NSImage(data: data) {
            image = decoded
            shownArtifact = latest.name
        }
    }

    private func capture() async {
        busy = true
        defer { busy = false }
        await store.screenshot(runId: runId)
        await load()
    }
}
