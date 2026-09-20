import AppKit
import SwiftUI
import UniformTypeIdentifiers

/// One run: what it is, and the three ways of looking at it.
struct RunView: View {
    @Bindable var store: RunStore
    let runId: String

    @State private var tab: Tab = .transcript
    @State private var confirmingDestroy = false

    enum Tab: String, CaseIterable, Identifiable {
        case transcript = "Transcript"
        case screen = "Screen"
        case steps = "Steps"

        var id: String { rawValue }
    }

    private var detail: RunDetail? { store.details[runId] }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            Picker("View", selection: $tab) {
                ForEach(Tab.allCases) { Text($0.rawValue).tag($0) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .padding(10)
            Divider()
            content
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
        .navigationTitle(runId)
        .toolbar {
            ToolbarItemGroup {
                Button {
                    Task { await store.screenshot(runId: runId) }
                } label: {
                    Label("Screenshot", systemImage: "camera")
                }
                .disabled(!isReady)

                Button {
                    Task { await saveRecording() }
                } label: {
                    Label("Save recording...", systemImage: "film")
                }
                .disabled((store.frames[runId] ?? []).isEmpty)

                Button(role: .destructive) {
                    confirmingDestroy = true
                } label: {
                    Label("Destroy", systemImage: "trash")
                }
                .disabled(detail?.machine == nil)
            }
        }
        .confirmationDialog(
            "Destroy this machine?",
            isPresented: $confirmingDestroy,
            titleVisibility: .visible
        ) {
            Button("Destroy", role: .destructive) {
                Task { await store.destroy(runId: runId) }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("The machine is deleted and the run ends. The coding agent is told in the conversation.")
        }
        .task(id: runId) {
            await store.select(runId)
        }
        .onChange(of: store.seekRequest) {
            guard let request = store.seekRequest, request.runId == runId else { return }
            tab = .screen
        }
    }

    /// Fetches the run's recording and writes it wherever the person picks.
    /// A 404 (no `ffmpeg` on the daemon host) surfaces its own text in
    /// `lastError` rather than a generic failure.
    @MainActor
    private func saveRecording() async {
        guard let data = await store.recording(runId: runId) else { return }
        let panel = NSSavePanel()
        panel.nameFieldStringValue = "\(runId).mp4"
        panel.allowedContentTypes = [.mpeg4Movie]
        guard panel.runModal() == .OK, let url = panel.url else { return }
        do {
            try data.write(to: url)
        } catch {
            store.lastError = error.localizedDescription
        }
    }

    private var isReady: Bool {
        if case .ready = detail?.machine?.status { return true }
        return false
    }

    @ViewBuilder
    private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .top, spacing: 16) {
                StatusBadge(status: detail?.status ?? .unknown(""))
                    .padding(.top, 2)
                field("Image", detail?.image ?? "")
                if let ip = detail?.address {
                    field("IP", ip)
                }
                if let boot = detail?.machine?.bootSeconds {
                    field("Boot", String(format: "%.1f s", boot))
                }
                field("Steps", String(detail?.steps ?? 0))
                Spacer()
                if let verdict = detail?.verdict {
                    VerdictBadge(state: verdict)
                }
            }
            if let error = detail?.machine?.error, !error.isEmpty {
                Text(error)
                    .font(.callout)
                    .foregroundStyle(.red)
            }
            if let summary = detail?.verdict.summary, !summary.isEmpty {
                Text(summary)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .lineLimit(3)
            }
        }
        .padding(12)
    }

    private func field(_ label: String, _ value: String) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(label.uppercased())
                .font(.caption2)
                .foregroundStyle(.tertiary)
            Text(value)
                .font(.caption.monospaced())
        }
    }

    @ViewBuilder
    private var content: some View {
        switch tab {
        case .transcript:
            TranscriptView(store: store, runId: runId)
        case .screen:
            ScreenView(store: store, runId: runId)
        case .steps:
            StepsView(store: store, runId: runId)
        }
    }
}
