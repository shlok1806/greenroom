import AppKit
import SwiftUI
import UniformTypeIdentifiers

/// One run: what it is, and the three ways of looking at it.
///
/// The header is a single strip of facts, the way a session inspector reads:
/// the run's name and where it stands, then the flat label-and-value row that
/// answers "what machine, since when, how long, how much". Under it the three
/// views are named by underlined tabs carrying their own counts, so the size
/// of the transcript, the recording and the evidence is known before opening
/// any of them.
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
    private var summary: RunSummary? { store.run(runId) }

    /// The detail arrives a moment after the run list, so until it does the
    /// header reads the list's own copy rather than showing nothing.
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
    /// A 404 (no `ffmpeg` on the daemon host) shows its own text in
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

    // MARK: - Header

    @ViewBuilder
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
            facts
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

    /// The label-and-value strip. It wraps rather than truncates, so a narrow
    /// window loses a line of height instead of the facts at the right.
    private var facts: some View {
        ViewThatFits(in: .horizontal) {
            HStack(alignment: .top, spacing: 22) { factFields }
            VStack(alignment: .leading, spacing: 8) {
                HStack(alignment: .top, spacing: 22) { factFields }
            }
        }
    }

    @ViewBuilder
    private var factFields: some View {
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

    /// How long the run lasted, or has lasted. A finished run stops counting
    /// at the moment its machine went away.
    private var runDuration: String {
        guard let detail else { return "-" }
        let end = detail.destroyedAt ?? summary?.destroyedAt ?? summary?.lastActivity ?? Date()
        return Chrome.clock(end.timeIntervalSince(detail.createdAt))
    }

    // MARK: - Tabs

    private var tabBar: some View {
        HStack(spacing: 2) {
            ForEach(Tab.allCases) { item in
                TabButton(
                    title: item.rawValue,
                    count: count(for: item),
                    selected: tab == item
                ) {
                    tab = item
                }
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 10)
    }

    private func count(for tab: Tab) -> Int? {
        switch tab {
        case .transcript: return store.messages[runId]?.count
        case .screen: return store.frames[runId]?.count
        case .steps: return store.steps[runId]?.count
        }
    }

    @ViewBuilder
    private var content: some View {
        switch tab {
        case .transcript:
            // Identified by the run, so that changing run rebuilds the tab
            // rather than handing the next run the last one's state. A
            // message is identified by its `seq`, and every run starts at 1,
            // so without this a half-typed dispute or answer stayed in the
            // box under the next run's message of the same number, and the
            // composer's own draft stayed in it too: Send would then have put
            // a note meant for one run into another run's conversation.
            // `ScreenView` does the same thing for itself, by hand, because
            // it also has a lease to give back on the way out.
            TranscriptView(store: store, runId: runId)
                .id(runId)
        case .screen:
            ScreenView(store: store, runId: runId)
        case .steps:
            // Same reason: a row's expanded state and its loaded thumbnail
            // belong to the run they were opened in.
            StepsView(store: store, runId: runId)
                .id(runId)
        }
    }
}

/// One tab: its name, how much is behind it, and a rule under the one that is
/// open. A segmented control stretched across the window was the loudest thing
/// on screen for a choice between three words.
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
