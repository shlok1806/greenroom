import AppKit
import AVKit
import SwiftUI

// The states that take the whole run pane or float over the window (Figma wireframes 09, 11,
// 12, 13a, 13b, 14): the evidence viewer, take control, the palette, settings, no runs yet,
// and Greenroom not running.

/// The evidence viewer (wireframe 11): the selected check's frame large, with its mark, the
/// claim beside its proof, the key frames captioned, and the recording one click away.
struct EvidenceViewer: View {
    @Bindable var shell: ShellModel
    var summary: Summary
    @State private var player: AVPlayer?
    @State private var loadingRecording = false

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: Gap.x8) {
                IconButton(icon: .close, name: "Close the evidence (esc)") { shell.evidenceOpen = false }
                Text("Evidence").textStyle(.bodyEmphasis).foregroundStyle(Palette.text)
                Text(summary.name).textStyle(.body).foregroundStyle(Palette.textSecondary).lineLimit(1)
                Spacer()
                if summary.lastFrame != nil {
                    ToolbarButton(icon: player == nil ? .play : .expand,
                                  title: player == nil ? "Play recording, \(Clock.elapsed(summary.elapsedSeconds))" : "Back to the frame") {
                        Task { await playRecording() }
                    }
                    .disabled(loadingRecording)
                }
            }
            .padding(.leading, Gap.x16)
            .padding(.trailing, Gap.x12)
            .frame(height: Metrics.toolbarHeight)
            .overlay(alignment: .bottom) { Rectangle().fill(Palette.border).frame(height: 1) }
            StatusHeader(shell: shell, summary: summary)
            GeometryReader { geo in
                let width = min(geo.size.width - Gap.x48 * 2, (geo.size.height - 200) * 4 / 3)
                VStack(alignment: .leading, spacing: Gap.x12) {
                    if let player {
                        VideoPlayer(player: player)
                            .aspectRatio(4 / 3, contentMode: .fit)
                            .clipShape(RoundedRectangle(cornerRadius: Corner.row))
                            .frame(width: max(240, width))
                    } else {
                        StageView(shell: shell, summary: summary, windowClass: .wide)
                            .frame(width: max(240, width) + Gap.x32 * 2)
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            .background(Palette.bgStage)
        }
        .onDisappear { player?.pause() }
    }

    private func playRecording() async {
        if player != nil {
            player?.pause()
            player = nil
            return
        }
        loadingRecording = true
        defer { loadingRecording = false }
        guard let data = await shell.store.recording(runId: summary.runId) else { return }
        let url = FileManager.default.temporaryDirectory.appending(path: "greenroom-\(summary.runId).mp4")
        do {
            try data.write(to: url)
            let player = AVPlayer(url: url)
            self.player = player
            player.play()
        } catch {
            shell.store.report(error)
        }
    }
}

/// Take control (wireframe 09): the screen full-bleed, one bar that says you have it, and the
/// way out where the eye is.
struct TakeControlView: View {
    @Bindable var shell: ShellModel
    var summary: Summary

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: Gap.x8) {
                Circle().fill(Palette.accent).frame(width: 6, height: 6)
                Text("You have control").textStyle(.bodyEmphasis).foregroundStyle(Palette.text)
                Text("\(summary.name). The verifier waits for you.").textStyle(.body).foregroundStyle(Palette.textSecondary).lineLimit(1)
                Spacer()
                Button("Give control back") { shell.perform(SummaryAction(id: SummaryAction.giveBack, label: "Give control back")) }
                    .buttonStyle(ActionButtonStyle(kind: .primary, loading: shell.busy == SummaryAction.giveBack))
            }
            .padding(.leading, 96)
            .padding(.trailing, Gap.x12)
            .frame(height: Metrics.toolbarHeight)
            .background(Palette.bg)
            .overlay(alignment: .bottom) { Rectangle().fill(Palette.border).frame(height: 1) }
            ZStack {
                Color.black
                if let runId = shell.runId {
                    LivePicture(shell: shell, runId: runId)
                }
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel("You have control of the Mac")
    }
}

/// The palette over the window, on an 18% scrim (wireframe 12).
struct PaletteOverlay: View {
    @Bindable var shell: ShellModel

    var body: some View {
        ZStack(alignment: .top) {
            Palette.scrim.opacity(0.18)
                .ignoresSafeArea()
                .onTapGesture { shell.paletteOpen = false }
                .accessibilityHidden(true)
            CommandPaletteView(isPresented: $shell.paletteOpen, options: PaletteOptions.of(shell))
                .padding(.top, 112)
        }
    }
}

/// What the palette offers: this run's actions with their keys, then every run to go to.
@MainActor
enum PaletteOptions {
    static func of(_ shell: ShellModel) -> [PaletteOption] {
        var out: [PaletteOption] = []
        if let s = shell.summary {
            let (primary, secondary) = shell.actions(for: s)
            for action in [primary].compactMap({ $0 }) + secondary {
                out.append(PaletteOption(id: "a-\(action.id)", title: action.label, section: "This run",
                                         icon: Keys.icon(for: action),
                                         keys: action == primary ? "⌘↩" : Keys.shortcut(for: action)) { shell.perform(action) })
            }
            out.append(PaletteOption(id: "activity", title: "Open activity", section: "This run", icon: .activity, keys: "A") { shell.activityOpen = true })
            out.append(PaletteOption(id: "message", title: "Message the verifier", section: "This run", icon: .message, keys: "M") { shell.openComposer(.message) })
            out.append(PaletteOption(id: "evidence", title: "Open the evidence", section: "This run", icon: .video, keys: "E") { shell.evidenceOpen = true })
            out.append(PaletteOption(id: "copy", title: "Copy run ID", section: "This run", icon: .copy) {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(s.runId, forType: .string)
            })
        }
        out.append(PaletteOption(id: "settings", title: "Settings", section: "Greenroom", icon: .settings, keys: "⌘,") { shell.settingsOpen = true })
        for run in shell.store.board?.runs ?? [] where run.runId != shell.runId {
            out.append(PaletteOption(id: "r-\(run.runId)", title: run.name, section: "Go to run",
                                     glyph: (run.state.glyph, run.tone.color)) { shell.select(run: run.runId) })
        }
        return out
    }
}

/// Settings and updates (wireframe 14), a sheet over the window.
struct SettingsSheet: View {
    @Bindable var shell: ShellModel
    @AppStorage("appearance", store: AppDefaults.shared) private var appearance = "system"

    var body: some View {
        let updates = shell.store.updates
        let summary = updates.summary
        VStack(alignment: .leading, spacing: 0) {
            Text("Settings").textStyle(.title).foregroundStyle(Palette.text)
                .padding(.horizontal, Gap.x24).padding(.top, Gap.x24).padding(.bottom, Gap.x12)
            row {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Updates").textStyle(.body).foregroundStyle(Palette.text)
                    Text(summary.detail ?? summary.title).textStyle(.caption).foregroundStyle(Palette.textSecondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Spacer()
                if updates.run?.running == true {
                    Button("Updating") {}.buttonStyle(ActionButtonStyle(kind: .primary, loading: true))
                } else if summary.canUpdate {
                    Button("Update and restart") {
                        Task { await updates.requestUpdate { await shell.store.runsWithVerifierTurn() } }
                    }
                    .buttonStyle(ActionButtonStyle(kind: .primary))
                } else {
                    Button("Check now") { Task { await updates.refresh() } }.buttonStyle(ActionButtonStyle(kind: .secondary))
                }
            }
            row {
                Text("Appearance").textStyle(.body).foregroundStyle(Palette.text)
                Spacer()
                Picker("Appearance", selection: $appearance) {
                    Text("System").tag("system")
                    Text("Light").tag("light")
                    Text("Dark").tag("dark")
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .fixedSize()
            }
            row {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Macs at once").textStyle(.body).foregroundStyle(Palette.text)
                    Text("Each run gets its own Mac").textStyle(.caption).foregroundStyle(Palette.textSecondary)
                }
                Spacer()
                Text(shell.store.board.map { "\($0.macs.total)" } ?? "Unknown").textStyle(.body).foregroundStyle(Palette.text)
            }
            row {
                Text("Server").textStyle(.body).foregroundStyle(Palette.text)
                Spacer()
                Text(shell.store.daemonAddress).textStyle(.body).foregroundStyle(Palette.textSecondary)
            }
            HStack {
                Spacer()
                Button("Done") { shell.settingsOpen = false }.buttonStyle(ActionButtonStyle(kind: .secondary))
            }
            .padding(Gap.x16)
        }
        .frame(width: 480)
        .background(RoundedRectangle(cornerRadius: Corner.sheet).fill(Palette.bgRaised))
        .clipShape(RoundedRectangle(cornerRadius: Corner.sheet))
        .overlay(RoundedRectangle(cornerRadius: Corner.sheet).strokeBorder(Palette.border, lineWidth: 1))
        .shadow(color: .black.opacity(Elevation.raisedOpacity), radius: Elevation.raisedRadius / 2, y: Elevation.raisedY)
        .task { await updates.refresh() }
    }

    private func row(@ViewBuilder _ content: () -> some View) -> some View {
        HStack(spacing: Gap.x12) { content() }
            .padding(.horizontal, Gap.x24)
            .padding(.vertical, Gap.x12)
            .overlay(alignment: .top) { Rectangle().fill(Palette.border).frame(height: 1) }
    }
}

/// No runs yet (wireframe 13a): the one command that connects a coding agent.
struct NoRunsView: View {
    var address: String

    var body: some View {
        CenteredMessage(title: "No runs yet",
                        text: "Add Greenroom to your coding agent once. It starts a run whenever it wants its work checked on a real Mac.",
                        command: "claude mcp add --transport http greenroom \(address)") {
            EmptyView()
        }
    }
}

/// Greenroom is not running (wireframe 13b): the command that starts it; the window
/// reconnects by itself.
struct DaemonOfflineView: View {
    var refusal: String?

    var body: some View {
        CenteredMessage(title: refusal == nil ? "Greenroom isn't running" : "Greenroom refused the connection",
                        text: refusal ?? "Start it, and this window reconnects by itself.",
                        command: refusal == nil ? "launchctl kickstart gui/$(id -u)/com.greenroom.daemon" : nil) {
            HStack(spacing: Gap.x8) {
                StatusGlyph(kind: .checking, color: .accent, size: 14)
                Text("Trying again").textStyle(.caption).foregroundStyle(Palette.textSecondary)
                Spacer()
                Button("Open log") {
                    NSWorkspace.shared.open(FileManager.default.homeDirectoryForCurrentUser.appending(path: ".greenroom/daemon.log"))
                }
                .buttonStyle(ActionButtonStyle(kind: .plain))
            }
        }
    }
}

/// A title, a sentence, a command to copy and whatever follows, centred in the pane.
struct CenteredMessage<Footer: View>: View {
    var title: String
    var text: String
    var command: String?
    @ViewBuilder var footer: () -> Footer
    @State private var copied = false

    var body: some View {
        VStack(alignment: .leading, spacing: Gap.x12) {
            Text(title).textStyle(.display).foregroundStyle(Palette.text)
            Text(text).textStyle(.body).foregroundStyle(Palette.textSecondary).fixedSize(horizontal: false, vertical: true)
            if let command {
                HStack(alignment: .center, spacing: Gap.x12) {
                    Text(command).font(.system(size: 12, design: .monospaced)).foregroundStyle(Palette.text)
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    ToolbarButton(icon: copied ? .check : .copy, title: copied ? "Copied" : "Copy") {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(command, forType: .string)
                        copied = true
                    }
                }
                .padding(Gap.x12)
                .background(RoundedRectangle(cornerRadius: Corner.row).fill(Palette.bgSelected))
                .overlay(RoundedRectangle(cornerRadius: Corner.row).strokeBorder(Palette.border, lineWidth: 1))
            }
            footer().padding(.top, Gap.x12)
        }
        .frame(width: 520)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Palette.bg)
    }
}
