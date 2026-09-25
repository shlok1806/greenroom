import SwiftUI

/// Every run, newest first: the ones that need a person, then the ones running, then
/// the rest by day. A row is named by a short, distinct title and says its state in
/// words. Drawn on the chrome tint; the open run is filled with the brand.
struct SidebarView: View {
    @Bindable var store: RunStore

    @State private var query = ""
    @FocusState private var searchFocused: Bool
    @Environment(\.theme) private var theme
    @Environment(\.keyboard) private var keyboard

    var body: some View {
        // One shared clock keeps idle times fresh.
        TimelineView(.periodic(from: .now, by: 30)) { tick in
            let titles = RunTitle.distinct(store.runs)
            let sections = sections(now: tick.date)
            VStack(spacing: 0) {
                if !store.runs.isEmpty {
                    searchField
                        .padding(.horizontal, Space.m)
                        .padding(.top, Space.m)
                }
                ScrollViewReader { proxy in
                    ScrollView {
                        LazyVStack(alignment: .leading, spacing: 2) {
                            ForEach(sections) { section in
                                SectionLabel(title: section.title, count: section.pinned ? section.runs.count : nil)
                                    .padding(.horizontal, Space.s)
                                    .padding(.top, Space.l)
                                    .padding(.bottom, Space.xs)
                                ForEach(section.runs) { run in
                                    RunRow(
                                        run: run,
                                        title: titles[run.runId] ?? RunTitle.short(task: run.task, runId: run.runId),
                                        facts: store.facts(run.runId, now: tick.date),
                                        now: tick.date,
                                        selected: store.selectedRunId == run.runId
                                    ) {
                                        store.selectedRunId = run.runId
                                    }
                                    .id(run.runId)
                                }
                            }
                        }
                        .padding(.horizontal, Space.s)
                        .padding(.bottom, Space.m)
                    }
                    .overlayScrollers()
                    .overlay { overlay }
                    // The row cut by the list's bottom edge fades into the tint, so it reads
                    // as more below rather than as a clipped row.
                    .overlay(alignment: .bottom) {
                        LinearGradient(colors: [theme.chromeTint.opacity(0), theme.chromeTint],
                                       startPoint: .top, endPoint: .bottom)
                            .frame(height: Space.xl)
                            .allowsHitTesting(false)
                    }
                    .onChange(of: store.selectedRunId) {
                        guard let selected = store.selectedRunId else { return }
                        withAnimation(.snappy(duration: 0.2)) { proxy.scrollTo(selected) }
                    }
                }
                .focusable()
                .focusEffectDisabled()
            }
        }
        // j and k (and the arrows) move the selection through the rows as shown.
        .offersActions(.sidebar, store.runs.isEmpty ? [] : [.moveDown, .moveUp]) { id in
            move(by: id == .moveDown ? 1 : -1, in: sections(now: Date()))
        }
        // `/` (or the palette's "search the runs for") puts the cursor here.
        .onChange(of: keyboard?.searchRequest) {
            if let text = keyboard?.searchText { query = text }
            searchFocused = true
        }
        // Only while the event stream is down or an action failed: a healthy connection
        // needs no words, and an unreachable daemon is explained in the detail.
        .safeAreaInset(edge: .bottom, spacing: 0) {
            if case .online = store.connection, !store.connected || store.lastError != nil {
                ConnectionFooter(store: store)
                    .overlay(alignment: .top) { Hairline() }
            }
        }
        .ground(.chrome)
        .background(theme.chromeTint)
    }

    private var searchField: some View {
        HStack(spacing: Space.s) {
            Text("/")
                .monoStyle(.monoMedium, size: TypeScale.monoSmall)
                .foregroundStyle(.secondary)
            TextField("Find a run", text: $query)
                .textFieldStyle(.plain)
                .font(Typeface.readingRegular.font(size: TypeScale.readingSmall))
                .focused($searchFocused)
                // esc leaves the search, and a search left behind would hide runs.
                .typingField(focused: searchFocused, sends: false) { query = "" }
            if !query.isEmpty {
                Button("Clear") { query = "" }
                    .buttonStyle(.textLink)
                    .foregroundStyle(.secondary)
            }
        }
        .padding(.horizontal, Space.s)
        .frame(height: 28)
        .fieldFrame(focused: searchFocused, radius: Radius.sm)
    }

    @ViewBuilder
    private var overlay: some View {
        if store.runs.isEmpty {
            Group {
                switch store.connection {
                case .connecting:
                    HStack(spacing: Space.s) {
                        Spinner(size: TypeScale.monoSmall)
                        Text("connecting")
                    }
                case .offline, .refused:
                    Text("Not connected")
                case .online:
                    // The detail welcomes a person with no runs; the list stays quiet.
                    Text("No runs yet")
                }
            }
            .monoStyle(size: TypeScale.monoSmall)
            .foregroundStyle(.secondary)
        } else if matches.isEmpty {
            VStack(spacing: Space.xs) {
                Text("No run matches \u{201C}\(query)\u{201D}")
                    .readingStyle(.readingMedium, size: TypeScale.readingSmall)
                Text("Search looks at the task, the id, the time and the state.")
                    .readingStyle(size: TypeScale.small)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }
            .padding(Space.l)
        }
    }

    /// Up and down move the selection through the rows in the order they are shown.
    private func move(by delta: Int, in sections: [RunSection]) {
        let order = sections.flatMap(\.runs).map(\.runId)
        guard !order.isEmpty else { return }
        let current = store.selectedRunId.flatMap { order.firstIndex(of: $0) }
        let next = current.map { min(max($0 + delta, 0), order.count - 1) } ?? (delta > 0 ? 0 : order.count - 1)
        store.selectedRunId = order[next]
    }

    private var matches: [RunSummary] {
        store.runs.filter { SidebarView.run($0, matches: query) }
    }

    /// Searches what a row shows: task, id, time, status, verdict, plus the image.
    static func run(_ run: RunSummary, matches query: String) -> Bool {
        let needle = query.trimmingCharacters(in: .whitespaces).lowercased()
        guard !needle.isEmpty else { return true }
        return [
            run.runId, run.image, run.status.text, Chrome.lifecycleTitle(run.status),
            Chrome.timeOfDay(run.createdAt), run.verdict?.verdict ?? "", run.task ?? "",
        ]
        .contains { $0.lowercased().contains(needle) }
    }

    fileprivate struct RunSection: Identifiable {
        var title: String
        var runs: [RunSummary]
        var pinned = false
        var id: String { title }
    }

    /// "Needs you" and "Running" first, then days. A run appears once, in the first
    /// section it belongs to. The daemon sorts newest first; this keeps that order.
    private func sections(now: Date) -> [RunSection] {
        var needsYou: [RunSummary] = []
        var running: [RunSummary] = []
        var rest: [RunSummary] = []
        for run in matches {
            let facts = store.facts(run.runId, now: now)
            if facts.needsYou {
                needsYou.append(run)
            } else if facts.isAlive {
                running.append(run)
            } else {
                rest.append(run)
            }
        }
        var out: [RunSection] = []
        if !needsYou.isEmpty { out.append(RunSection(title: "Needs you", runs: needsYou, pinned: true)) }
        if !running.isEmpty { out.append(RunSection(title: "Running", runs: running, pinned: true)) }
        for run in rest {
            let day = Chrome.day(run.createdAt, now: now)
            if out.last?.title == day, out.last?.pinned == false {
                out[out.count - 1].runs.append(run)
            } else {
                out.append(RunSection(title: day, runs: [run]))
            }
        }
        return out
    }
}

/// The short title in the reading face, then when it started and how big it is, with
/// its state in a glyph and a word (both mono). The open run is filled with the brand.
private struct RunRow: View {
    let run: RunSummary
    let title: String
    let facts: RunFacts
    let now: Date
    let selected: Bool
    let open: () -> Void

    @Environment(\.theme) private var theme
    @State private var hovering = false

    var body: some View {
        let status = facts.rowStatus(now: now)
        let ink: Color? = selected ? theme.brandText : nil
        Button(action: open) {
            VStack(alignment: .leading, spacing: Space.xs) {
                Text(title)
                    .readingStyle(.readingMedium, size: TypeScale.readingSmall)
                    .foregroundStyle(ink ?? theme.foreground)
                    .lineLimit(2)
                    .truncationMode(.tail)
                    .frame(maxWidth: .infinity, alignment: .leading)
                HStack(spacing: Space.s) {
                    ViewThatFits(in: .horizontal) {
                        ForEach(meta, id: \.self) { line in
                            Text(line).lineLimit(1)
                        }
                    }
                    .font(Typeface.monoRegular.font(size: TypeScale.monoSmall))
                    .monospacedDigit()
                    .foregroundStyle(ink ?? theme.dim(on: .chrome))
                    .frame(maxWidth: .infinity, alignment: .leading)
                    StatusText(text: status.text, tone: status.tone, ink: ink)
                }
            }
            .padding(.horizontal, Space.s)
            .padding(.vertical, Space.s)
            .background(
                RoundedRectangle(cornerRadius: Radius.sm, style: .continuous)
                    .fill(selected ? theme.brand : hovering ? theme.highlight : .clear)
            )
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help(help(status: status.text))
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }

    /// One time format everywhere in the list: when the run started, and for a running
    /// one how long it has run. Errors are counted once, in the run's header.
    private var meta: [String] {
        var parts = [Chrome.shortTime(run.createdAt)]
        if facts.isAlive { parts.append("running \(Chrome.span(facts.duration(now: now)))") }
        if facts.stepCount > 0 { parts.append(Chrome.plural(facts.stepCount, "step")) }
        return (1...parts.count).reversed().map { parts.prefix($0).joined(separator: " · ") }
    }

    private func help(status: String) -> String {
        var lines = [RunTitle.text(task: run.task, runId: run.runId), "", "\(status). Started \(Chrome.stamp(run.createdAt))."]
        if facts.isAlive { lines.append("Last activity \(Chrome.span(facts.idle(now: now))) ago.") }
        lines.append(run.runId)
        return lines.joined(separator: "\n")
    }
}

/// The event stream is down or an action failed. Informational: the detail says what to
/// do when the daemon itself is gone.
private struct ConnectionFooter: View {
    let store: RunStore

    var body: some View {
        VStack(alignment: .leading, spacing: Space.xs) {
            if !store.connected {
                HStack(spacing: Space.s) {
                    Spinner(size: TypeScale.monoSmall)
                    Text("Reconnecting to live updates")
                }
                .monoStyle(size: TypeScale.monoSmall)
                .foregroundStyle(.secondary)
            }
            if let error = store.lastError {
                Text(error)
                    .readingStyle(size: TypeScale.small)
                    .foregroundStyle(.secondary)
                    .lineLimit(3)
                    .textSelection(.enabled)
                    .help(error)
            }
        }
        .padding(.horizontal, Space.l)
        .padding(.vertical, Space.s)
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}
