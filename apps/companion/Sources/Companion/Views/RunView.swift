import AppKit
import SwiftUI
import UniformTypeIdentifiers

/// One run: a header strip of facts, then Transcript, Screen and Steps tabs.
struct RunView: View {
    let store: RunStore
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
    private var summary: RunSummary? { store.run(runId) }

    /// The detail lands after the run list, so fall back to the list's copy.
    private var status: RunStatus { detail?.status ?? summary?.status ?? .unknown("") }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            tabBar
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
                .disabled(detail?.status != .ready)

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

    // MARK: - Header

    private var header: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text(runId)
                    .font(.system(.title3, design: .monospaced))
                    .textSelection(.enabled)
                    .lineLimit(1)
                    .truncationMode(.middle)
                StatusBadge(status: status)
                Spacer(minLength: 12)
                if let verdict = detail?.verdict {
                    VerdictBadge(state: verdict)
                }
            }
            HStack(alignment: .top, spacing: 22) {
                FieldLabel(label: "Started", value: detail.map { Chrome.stamp($0.createdAt) } ?? "-", monospaced: false)
                FieldLabel(label: "Duration", value: runDuration)
                FieldLabel(label: "Image", value: detail?.image ?? "-")
                if let ip = detail?.address {
                    FieldLabel(label: "IP", value: ip)
                }
                if let boot = detail?.machine?.bootSeconds {
                    FieldLabel(label: "Boot", value: String(format: "%.1f s", boot))
                }
                Spacer(minLength: 0)
            }
            if let error = detail?.machine?.error, !error.isEmpty {
                Text(error)
                    .font(.callout)
                    .foregroundStyle(.red)
                    .textSelection(.enabled)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
    }

    /// A finished run stops counting when its machine went away.
    private var runDuration: String {
        guard let detail else { return "-" }
        let end = detail.destroyedAt ?? summary?.destroyedAt ?? summary?.lastActivity ?? Date()
        return Chrome.clock(end.timeIntervalSince(detail.createdAt))
    }

    // MARK: - Tabs

    private var tabBar: some View {
        HStack(spacing: 2) {
            ForEach(Tab.allCases) { item in
                TabButton(title: item.rawValue, count: count(for: item), selected: tab == item) {
                    tab = item
                }
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 10)
    }

    private func count(for tab: Tab) -> Int? {
        switch tab {
        case .transcript: store.messages[runId]?.count
        case .screen: store.frames[runId]?.count
        case .steps: store.steps[runId]?.count
        }
    }

    @ViewBuilder
    private var content: some View {
        switch tab {
        case .transcript:
            // `.id(runId)` rebuilds per run: message seqs restart at 1, so a
            // half-typed draft or dispute would otherwise post into the next run.
            TranscriptView(store: store, runId: runId)
                .id(runId)
        case .screen:
            // Resets itself by hand, since it has a lease to give back first.
            ScreenView(store: store, runId: runId)
        case .steps:
            StepsView(store: store, runId: runId)
                .id(runId)
        }
    }
}

private struct TabButton: View {
    let title: String
    let count: Int?
    let selected: Bool
    let action: () -> Void

    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 5) {
                Text(title)
                    .font(.callout.weight(selected ? .semibold : .regular))
                if let count, count > 0 {
                    Text(Chrome.count(count))
                        .font(.caption2.monospacedDigit())
                        .foregroundStyle(.secondary)
                        .padding(.horizontal, 4)
                        .padding(.vertical, 1)
                        .background(.quaternary, in: Capsule())
                }
            }
            .fixedSize()
            .padding(.horizontal, 10)
            .padding(.top, 8)
            .padding(.bottom, 7)
            .overlay(alignment: .bottom) {
                Rectangle()
                    .fill(selected ? Color.accentColor : .clear)
                    .frame(height: 2)
            }
            .foregroundStyle(selected ? AnyShapeStyle(.primary) : AnyShapeStyle(.secondary))
            .background(hovering && !selected ? AnyShapeStyle(.quinary) : AnyShapeStyle(.clear))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .accessibilityAddTraits(selected ? [.isSelected] : [])
    }
}
