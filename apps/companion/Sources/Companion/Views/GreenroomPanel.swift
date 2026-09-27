import SwiftUI

/// The Greenroom section (root ADR 0033), opened from Cmd-K, the More menu and the app menu:
/// which builds run, whether main is ahead of them, and Update. Drawn over the window like the
/// palette, never as a system sheet (a sheet whose presenter goes away leaves the window
/// unable to take a click). Everything it says comes from `Updates` and `BuildsSummary`.
struct GreenroomPanel: View {
    let store: RunStore
    let keyboard: KeyboardModel
    var maximumHeight: CGFloat = 640

    @Environment(\.theme) private var theme

    static let width: CGFloat = 600
    private static let headerHeight: CGFloat = 48

    private var updates: Updates { store.updates }

    var body: some View {
        let summary = updates.summary
        VStack(alignment: .leading, spacing: 0) {
            header
            Hairline()
            ScrollView {
                VStack(alignment: .leading, spacing: Space.xl) {
                    if let run = updates.run {
                        UpdateRunView(run: run)
                    } else {
                        status(summary)
                    }
                    ledger
                    if updates.run == nil, case .checked(let check, _) = updates.check, !check.commits.isEmpty {
                        incoming(check)
                    }
                }
                .padding(Space.l)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .overlayScrollers()
            .frame(maxHeight: maximumHeight)
            .fixedSize(horizontal: false, vertical: true)
            Hairline()
            footer(summary)
        }
        .frame(width: Self.width)
        .background(theme.background, in: RoundedRectangle(cornerRadius: Radius.lg, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Radius.lg, style: .continuous).strokeBorder(theme.hairline))
        .shadow(color: .black.opacity(theme.id.isDark ? 0.5 : 0.14), radius: 24, y: 10)
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Builds and updates")
    }

    private var header: some View {
        HStack(spacing: Space.m) {
            Wordmark()
            Text("builds and updates")
                .monoStyle(size: TypeScale.monoSmall)
                .foregroundStyle(.secondary)
            Spacer()
            Button {
                keyboard.perform(.closeGreenroom, in: .greenroom)
            } label: {
                HStack(spacing: Space.s) {
                    KeyLabel(label: ActionRegistry.label(.closeGreenroom))
                    Text("close")
                }
            }
            .buttonStyle(.quiet(small: true))
        }
        .padding(.horizontal, Space.l)
        .frame(height: Self.headerHeight)
    }

    // MARK: - Where the builds stand

    private func status(_ summary: BuildsSummary) -> some View {
        VStack(alignment: .leading, spacing: Space.s) {
            HStack(alignment: .firstTextBaseline, spacing: Space.m) {
                StatusText(text: summary.title, tone: tone(summary.headline), size: TypeScale.mono)
                if case .checked(_, let at) = updates.check {
                    TimelineView(.periodic(from: .now, by: 30)) { context in
                        Text("checked \(Chrome.relative(at, now: context.date))")
                            .monoStyle(size: TypeScale.monoSmall)
                            .foregroundStyle(.secondary)
                    }
                }
            }
            if let detail = summary.detail {
                Text(detail)
                    .readingStyle(size: TypeScale.readingSmall)
                    .foregroundStyle(.secondary)
            }
            if summary.mismatch {
                StatusText(text: "The app and greenroom were built from different commits.", tone: .attention)
            }
        }
    }

    private func tone(_ headline: BuildsSummary.Headline) -> RunFacts.Tone {
        switch headline {
        case .checking: .neutral
        case .upToDate: .pass
        case .updates, .rebuild: .attention
        case .unknown: .quiet
        }
    }

    /// The two builds and what the daemon runs with, one fact a row.
    private var ledger: some View {
        let daemon = updates.daemon
        return VStack(alignment: .leading, spacing: Space.s) {
            SectionLabel(title: "Running")
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: Space.l, verticalSpacing: Space.xs) {
                row("App", build(updates.app.stamp))
                if let daemon {
                    row("greenroom", build(daemon.stamp))
                    row("Verifier", daemon.brain)
                    row("Vision", daemon.vision)
                    row("Image", "input helper \(daemon.inputHelper.map(String.init) ?? "?"), recipe \(daemon.imageRecipe.map(String.init) ?? "?")")
                } else {
                    row("greenroom", updates.daemonError ?? "not read yet", quiet: true)
                }
                row("Checkout", updates.checkout ?? "none", quiet: updates.checkout == nil)
            }
        }
    }

    private func build(_ stamp: BuildStamp) -> String {
        guard stamp.known else { return "not stamped (not installed by its script)" }
        guard let at = stamp.builtAt else { return stamp.label }
        return "\(stamp.label), built \(Chrome.day(at)) \(Chrome.shortTime(at))"
    }

    private func row(_ label: String, _ value: String, quiet: Bool = false) -> some View {
        GridRow {
            Text(label)
                .monoStyle(size: TypeScale.monoSmall)
                .foregroundStyle(.secondary)
                .gridColumnAlignment(.trailing)
            Text(value)
                .monoStyle(size: TypeScale.monoSmall)
                .foregroundStyle(quiet ? AnyShapeStyle(.secondary) : AnyShapeStyle(theme.foreground))
                .textSelection(.enabled)
                .lineLimit(2)
                .truncationMode(.middle)
        }
    }

    /// What Update brings, newest first, as `update.sh --check` listed it.
    private func incoming(_ check: UpdateCheck) -> some View {
        VStack(alignment: .leading, spacing: Space.s) {
            SectionLabel(title: "New on main", count: check.ahead)
            VStack(alignment: .leading, spacing: Space.xs) {
                ForEach(check.commits) { commit in
                    HStack(alignment: .firstTextBaseline, spacing: Space.m) {
                        Text(commit.sha)
                            .monoStyle(size: TypeScale.monoSmall)
                            .foregroundStyle(.secondary)
                        Text(commit.subject)
                            .readingStyle(size: TypeScale.readingSmall)
                            .lineLimit(2)
                    }
                }
            }
        }
    }

    // MARK: - Update

    @ViewBuilder
    private func footer(_ summary: BuildsSummary) -> some View {
        HStack(spacing: Space.m) {
            if let busy = updates.confirming {
                Text(confirmWords(busy))
                    .readingStyle(size: TypeScale.readingSmall)
                    .fixedSize(horizontal: false, vertical: true)
                Spacer(minLength: Space.s)
                Button("Cancel") { updates.cancelUpdate() }
                    .buttonStyle(.quiet(small: true))
                Button("Update Anyway") { Task { await updates.updateAnyway() } }
                    .buttonStyle(.primary)
            } else if let run = updates.run, !run.running {
                Text(finishedWords(run))
                    .readingStyle(size: TypeScale.readingSmall)
                    .foregroundStyle(.secondary)
                Spacer(minLength: Space.s)
                Button("Done") { updates.dismissRun() }
                    .buttonStyle(.quiet(small: true))
            } else {
                Text(summary.whyNot ?? "Update pulls main, rebuilds greenroom, then reinstalls and reopens this app.")
                    .readingStyle(size: TypeScale.readingSmall)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                Spacer(minLength: Space.s)
                Button("Check Now") { Task { await updates.refresh() } }
                    .buttonStyle(.quiet(small: true))
                    .disabled(updates.check == .checking || updates.run?.running == true || updates.checkout == nil)
                if summary.canUpdate {
                    Button("Update") {
                        let store = store
                        Task { await updates.requestUpdate { await store.runsWithVerifierTurn() } }
                    }
                    .buttonStyle(.primary)
                }
            }
        }
        .padding(.horizontal, Space.l)
        .padding(.vertical, Space.m)
    }

    private func confirmWords(_ busy: [String]) -> String {
        let names = busy.map { "\u{201C}\($0)\u{201D}" }.joined(separator: ", ")
        let runs = busy.count == 1 ? "run" : "runs"
        return "The verifier is working on the \(runs) \(names). Updating restarts greenroom and cuts that work off."
    }

    private func finishedWords(_ run: UpdateRun) -> String {
        switch run.outcome {
        case .done: "Updated. This app reopens on the new build."
        case .refused: "Nothing changed."
        case .failed: "greenroom and this app are still the builds they were."
        case .running: ""
        }
    }
}

/// An update as it runs: each step with its mark, then the output as it streams, following
/// the newest line. A failed step shows its own output.
private struct UpdateRunView: View {
    let run: UpdateRun

    @Environment(\.theme) private var theme
    @Environment(\.ground) private var ground

    private static let outputHeight: CGFloat = 180

    var body: some View {
        VStack(alignment: .leading, spacing: Space.m) {
            headline
            VStack(alignment: .leading, spacing: Space.xs) {
                ForEach(Array(run.steps.enumerated()), id: \.offset) { index, step in
                    stepRow(step.name, index: index)
                }
            }
            output
        }
    }

    @ViewBuilder
    private var headline: some View {
        switch run.outcome {
        case .running:
            StatusText(text: "Updating", tone: .neutral, size: TypeScale.mono)
        case .done:
            StatusText(text: "Updated", tone: .pass, size: TypeScale.mono)
        case .refused(let why):
            VStack(alignment: .leading, spacing: Space.xs) {
                StatusText(text: "Update refused", tone: .attention, size: TypeScale.mono)
                Text(why.prefix(1).uppercased() + why.dropFirst() + ".")
                    .readingStyle(size: TypeScale.readingSmall)
            }
        case .failed(let step, let status):
            VStack(alignment: .leading, spacing: Space.xs) {
                StatusText(text: "Update failed", tone: .failure, size: TypeScale.mono)
                Text("It stopped at \u{201C}\(step)\u{201D} (exit \(status)).")
                    .readingStyle(size: TypeScale.readingSmall)
            }
        }
    }

    private func stepRow(_ name: String, index: Int) -> some View {
        let last = index == run.steps.count - 1
        let failedHere: Bool = {
            if case .failed = run.outcome { return last }
            return false
        }()
        return HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            Group {
                if failedHere {
                    Text("✗").foregroundStyle(theme.color(.failure, on: ground))
                } else if last, run.running {
                    Spinner(size: TypeScale.monoSmall)
                } else {
                    Text("✓").foregroundStyle(.secondary)
                }
            }
            .frame(width: Space.l, alignment: .leading)
            Text(name)
                .foregroundStyle(failedHere ? theme.color(.failure, on: ground) : theme.foreground)
        }
        .monoStyle(size: TypeScale.monoSmall)
    }

    /// The failing step's output after a failure, else everything, following the end.
    @ViewBuilder
    private var output: some View {
        let lines: [String] = {
            if case .failed = run.outcome { return run.failingOutput }
            return run.lines.filter { !$0.hasPrefix("step: ") }
        }()
        if !lines.isEmpty {
        ScrollViewReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(Array(lines.enumerated()), id: \.offset) { index, line in
                        Text(line.isEmpty ? " " : line)
                            .monoStyle(size: TypeScale.monoSmall)
                            .foregroundStyle(.secondary)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .id(index)
                    }
                }
                .textSelection(.enabled)
                .padding(Space.m)
            }
            .overlayScrollers()
            .frame(maxHeight: Self.outputHeight)
            .fixedSize(horizontal: false, vertical: true)
            .panel()
            .onChange(of: lines.count, initial: true) {
                if let last = lines.indices.last { proxy.scrollTo(last, anchor: .bottom) }
            }
        }
        .accessibilityLabel("Update output")
        }
    }
}
