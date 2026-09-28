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
                Circle().fill(Palette.accent).frame(width: 8, height: 8).clonePart("Ellipse")
                    .padding(.trailing, 4)
                Text("You have control").textStyle(.bodyEmphasis).foregroundStyle(Palette.text).clonePart("Text[0]")
                    .padding(.trailing, 4)
                Text("\(summary.name). The verifier waits for you.").textStyle(.body).foregroundStyle(Palette.textSecondary).lineLimit(1)
                    .clonePart("Text[1]")
                Spacer()
                Button("Give control back") { shell.perform(SummaryAction(id: SummaryAction.giveBack, label: "Give control back")) }
                    .buttonStyle(ActionButtonStyle(kind: .primary, loading: shell.busy == SummaryAction.giveBack))
                    .cloneScope("Button")
            }
            // Figma 09: the dot 108 in (past the traffic lights), 12 to the words, 12 between them.
            .padding(.leading, 108)
            .padding(.trailing, Gap.x12)
            .frame(height: Metrics.toolbarHeight)
            .background(Palette.bg)
            .overlay(alignment: .bottom) { Rectangle().fill(Palette.border).frame(height: 1) }
            .cloneScope("Toolbar")
            ZStack {
                Color.black
                if let runId = shell.runId {
                    LivePicture(shell: shell, runId: runId)
                }
            }
            .cloneScope("Screen area")
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
                .cloneScope("Scrim")
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
            if shell.canTakeControl(s) {
                out.append(PaletteOption(id: "take", title: "Take control", section: "This run", icon: .pointer, keys: "T") {
                    shell.perform(SummaryAction(id: SummaryAction.takeControl, label: "Take control"))
                })
            }
            out.append(PaletteOption(id: "activity", title: "Open activity", section: "This run", icon: .activity, keys: "A") { shell.activityOpen = true })
            out.append(PaletteOption(id: "checks", title: "Show the checks", section: "This run", icon: .check) { shell.show(.checks) })
            // Only while something will answer, as the toolbar offers it: a Mac that is gone
            // takes no messages (seen in Greenroom run 20260928-144042-10fc05f5e0a43287).
            if s.machine.isUp {
                out.append(PaletteOption(id: "message", title: "Message the verifier", section: "This run", icon: .message, keys: "M") { shell.openComposer(.message) })
                out.append(PaletteOption(id: "task", title: "New task for the verifier", section: "This run", icon: .message) { shell.openComposer(.task) })
            } else {
                out.append(PaletteOption(id: "conversation", title: "Read the conversation", section: "This run", icon: .message, keys: "M") { shell.show(.message) })
            }
            out.append(PaletteOption(id: "evidence", title: "Open the evidence", section: "This run", icon: .video, keys: "E") { shell.evidenceOpen = true })
            out.append(PaletteOption(id: "details", title: "Run details", section: "This run", icon: .info) { shell.detailsOpen = true })
            out.append(PaletteOption(id: "copy", title: "Copy run ID", section: "This run", icon: .copy) { shell.copyRunID() })
            if shell.canCapture {
                out.append(PaletteOption(id: "capture", title: "Capture a screenshot", section: "This run", icon: .camera, keys: "C") { shell.capture() })
            }
            if shell.canExport {
                out.append(PaletteOption(id: "export", title: "Save the recording", section: "This run", icon: .download) { shell.exportRecording() })
            }
            if shell.canDestroy {
                out.append(PaletteOption(id: "destroy", title: "Destroy the Mac", section: "This run", icon: .trash) { shell.confirmingDestroy = true })
            }
            let t = shell.timeline()
            if !t.frames.isEmpty {
                out.append(PaletteOption(id: "play", title: shell.playing ? "Pause the recording" : "Play the recording", section: "Player",
                                         icon: shell.playing ? .pause : .play, keys: "Space") { shell.togglePlay() })
                out.append(PaletteOption(id: "speed", title: "Play at \(shell.speed >= 4 ? 1 : Int(shell.speed) * 2)×", section: "Player",
                                         icon: .play, keys: "F") { shell.toggleSpeed() })
                out.append(PaletteOption(id: "frame-next", title: "Next frame", section: "Player", icon: .skipForward, keys: "→") { shell.moveFrame(by: 1) })
                out.append(PaletteOption(id: "frame-previous", title: "Previous frame", section: "Player", icon: .skipBack, keys: "←") { shell.moveFrame(by: -1) })
            }
            if !t.failures.isEmpty {
                out.append(PaletteOption(id: "failure-next", title: "Next failure", section: "Player", icon: .skipForward, keys: "N") { shell.jumpToFailure() })
                out.append(PaletteOption(id: "failure-previous", title: "Previous failure", section: "Player", icon: .skipBack, keys: "⇧N") {
                    shell.jumpToFailure(forward: false)
                })
            }
            if t.live {
                out.append(PaletteOption(id: "live", title: "Go live", section: "Player", icon: .video, keys: "L") { shell.goLive() })
            }
            out.append(PaletteOption(id: "zoom", title: shell.zoomed ? "Show the inspector" : "Picture only", section: "Player", icon: .expand, keys: "Z") {
                shell.toggleZoom()
            })
        }
        for run in shell.store.board?.runs ?? [] where run.runId != shell.runId {
            out.append(PaletteOption(id: "r-\(run.runId)", title: run.name, section: "Go to run",
                                     glyph: (run.state.glyph, run.tone.color)) { shell.select(run: run.runId) })
        }
        // After the runs, as Figma 12 lists this run's actions, then the runs to go to.
        if shell.store.verdictUndo.pending != nil {
            out.append(PaletteOption(id: "undo", title: "Undo the accept or reject", section: "Greenroom", icon: .restart, keys: "U") { shell.undoVerdictChoice() })
        }
        out.append(PaletteOption(id: "refresh", title: "Refresh", section: "Greenroom", icon: .restart, keys: "⌘R") { shell.refresh() })
        out.append(PaletteOption(id: "settings", title: "Settings, builds and updates", section: "Greenroom", icon: .settings, keys: "⌘,") { shell.settingsOpen = true })
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
                    if summary.mismatch {
                        Text("The app and greenroom were built from different commits.").textStyle(.caption).foregroundStyle(Palette.wait)
                    }
                }
                Spacer()
                if let busy = updates.confirming {
                    Button("Cancel") { updates.cancelUpdate() }.buttonStyle(ActionButtonStyle(kind: .secondary))
                    Button("Update anyway") { Task { await updates.updateAnyway() } }
                        .buttonStyle(ActionButtonStyle(kind: .primary))
                        .help("The verifier is working on \(busy.joined(separator: ", ")). Updating restarts greenroom and cuts that work off.")
                } else if updates.run?.running == true {
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
            if let run = updates.run {
                UpdateProgress(run: run) { updates.dismissRun() }
                    .padding(.horizontal, Gap.x24)
                    .padding(.bottom, Gap.x12)
            }
            BuildsLedger(updates: updates)
                .padding(.horizontal, Gap.x24)
                .padding(.bottom, Gap.x12)
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

/// The builds (the old window's Builds and Updates): the app's and greenroom's commits, what
/// the verifier runs with, the checkout, and what is new on main.
struct BuildsLedger: View {
    let updates: Updates

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: Gap.x12, verticalSpacing: 4) {
                row("App", build(updates.app.stamp))
                if let daemon = updates.daemon {
                    row("Greenroom", build(daemon.stamp))
                    row("Verifier", daemon.brain)
                    row("Vision", daemon.vision)
                    row("Image", "input helper \(daemon.inputHelper.map(String.init) ?? "?"), recipe \(daemon.imageRecipe.map(String.init) ?? "?")")
                } else {
                    row("Greenroom", updates.daemonError ?? "not read yet")
                }
                row("Checkout", updates.checkout ?? "none")
            }
            if updates.run == nil, case .checked(let check, _) = updates.check, !check.commits.isEmpty {
                Text("New on main: \(check.ahead)").textStyle(.captionEmphasis).foregroundStyle(Palette.textSecondary).padding(.top, 4)
                ForEach(check.commits.prefix(8)) { commit in
                    HStack(alignment: .firstTextBaseline, spacing: Gap.x8) {
                        Text(commit.sha).font(.system(size: 11, design: .monospaced)).foregroundStyle(Palette.textSecondary)
                        Text(commit.subject).textStyle(.caption).foregroundStyle(Palette.text).lineLimit(1)
                    }
                }
            }
        }
    }

    private func build(_ stamp: BuildStamp) -> String {
        guard stamp.known else { return "not stamped (not installed by its script)" }
        guard let at = stamp.builtAt else { return stamp.label }
        return "\(stamp.label), built \(at.formatted(date: .abbreviated, time: .shortened))"
    }

    private func row(_ label: String, _ value: String) -> some View {
        GridRow {
            Text(label).textStyle(.caption).foregroundStyle(Palette.textSecondary)
            Text(value).textStyle(.caption).foregroundStyle(Palette.text).textSelection(.enabled).lineLimit(2).truncationMode(.middle)
        }
    }
}

/// An update as it runs: the step it is on, the last lines of its output, and how it ended.
struct UpdateProgress: View {
    let run: UpdateRun
    var dismiss: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text(headline).textStyle(.bodyEmphasis).foregroundStyle(Palette.text)
                Spacer()
                if !run.running { Button("Done", action: dismiss).buttonStyle(ActionButtonStyle(kind: .plain)) }
            }
            ScrollView {
                Text(run.lines.suffix(40).joined(separator: "\n"))
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundStyle(Palette.textSecondary)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .defaultScrollAnchor(.bottom)
            .visibleScroller()
            .frame(height: 120)
            .background(RoundedRectangle(cornerRadius: Corner.control).fill(Palette.bgSelected))
        }
    }

    private var headline: String {
        switch run.outcome {
        case .running: return "Updating" + (run.step.map { ": \($0)" } ?? "")
        case .done: return "Updated. This app reopens on the new build."
        case .refused(let why): return "Update refused: \(why)"
        case .failed(let step, let status): return "Update failed at \(step) (exit \(status))"
        }
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
