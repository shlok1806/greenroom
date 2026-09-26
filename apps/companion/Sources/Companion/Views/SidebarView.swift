import SwiftUI

/// Every run, newest first: the ones that need a person, then the ones running, then
/// the rest by day. A row is named by a short, distinct title and says its state in
/// words. Drawn on the chrome tint; the open run is filled with the brand.
struct SidebarView: View {
    @Bindable var store: RunStore
    /// A run was opened with a click (the runs covering the run step aside).
    var onOpen: () -> Void = {}

    @State private var query = ""
    /// Pinned sections the person opened in full.
    @State private var showsAllPinned: Set<String> = []

    /// A pinned section shows its newest few: a hundred runs to review must not push
    /// Running and the days out of reach (audit R5). A search shows every match.
    static let pinnedShown = 5

    fileprivate func shown(_ section: RunSection) -> [RunSummary] {
        guard section.pinned, query.isEmpty, !showsAllPinned.contains(section.title) else { return section.runs }
        let head = Array(section.runs.prefix(Self.pinnedShown))
        // The open run stays in view even when it is past the fold.
        if let open = store.selectedRunId, !head.contains(where: { $0.runId == open }),
           let run = section.runs.first(where: { $0.runId == open }) {
            return head + [run]
        }
        return head
    }
    @FocusState private var searchFocused: Bool
    @Environment(\.theme) private var theme
    @Environment(\.keyboard) private var keyboard

    var body: some View {
        // One shared clock keeps idle times fresh.
        TimelineView(.periodic(from: .now, by: 30)) { tick in
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
                                ForEach(shown(section)) { run in
                                    RunRow(
                                        run: run,
                                        // Twins are told apart by the time under the title, never
                                        // by a time appended to it (audit R4).
                                        title: RunTitle.short(task: run.task, runId: run.runId),
                                        facts: store.facts(run.runId, now: tick.date),
                                        now: tick.date,
                                        selected: store.selectedRunId == run.runId
                                    ) {
                                        store.selectedRunId = run.runId
                                        onOpen()
                                    }
                                    .id(run.runId)
                                }
                                if section.pinned, section.runs.count > Self.pinnedShown, query.isEmpty {
                                    Button(showsAllPinned.contains(section.title)
                                           ? "Show fewer"
                                           : "Show all \(section.runs.count)") {
                                        showsAllPinned.formSymmetricDifference([section.title])
                                    }
                                    .buttonStyle(.textLink)
                                    .monoStyle(size: TypeScale.monoSmall)
                                    .foregroundStyle(.secondary)
                                    .padding(.horizontal, Space.s)
                                    .padding(.vertical, Space.xs)
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
        // `/` (or the palette's "search the runs for") puts the cursor here, also in a
        // list that opens because of it (the runs over a folded window).
        .onChange(of: keyboard?.searchRequest) { takeSearch() }
        .onAppear { takeSearch() }
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

    private func takeSearch() {
        guard let keyboard, keyboard.searchRequest != keyboard.searchHandled else { return }
        keyboard.searchHandled = keyboard.searchRequest
        if let text = keyboard.searchText { query = text }
        searchFocused = true
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
                Text("Search matches the task, id, time and state.")
                    .readingStyle(size: TypeScale.small)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }
            .padding(Space.l)
        }
    }

    /// Up and down move the selection through the rows in the order they are shown.
    private func move(by delta: Int, in sections: [RunSection]) {
        let order = sections.flatMap(shown).map(\.runId)
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

    private func sections(now: Date) -> [RunSection] {
        Self.sections(matches, store: store, now: now)
    }

    /// "Needs you" and "Running" first, then days. A run appears once, in the first
    /// section it belongs to. The daemon sorts newest first; this keeps that order.
    fileprivate static func sections(_ runs: [RunSummary], store: RunStore, now: Date) -> [RunSection] {
        var needsYou: [RunSummary] = []
        var running: [RunSummary] = []
        var rest: [RunSummary] = []
        for run in runs {
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

/// The run's thumbnail, then the short title in the reading face, when it started and how
/// big it is, with its state in a glyph and a word (both mono). The open run is filled
/// with the brand.
struct RunRow: View {
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
                // The title at the row's full width (companion ADR 0012: a thumbnail at this
                // size was the same grey tile on every row); the time, the scope of its
                // verdict and its state under it.
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
    /// one how long it has run. Errors are counted once, in the run's header. Longest
    /// first; the last is empty, so in a narrow column a long state ("Inconclusive, you
    /// accepted") keeps its words and the time gives way, never a clipped time.
    private var meta: [String] {
        var parts = [Chrome.shortTime(run.createdAt)]
        if facts.isAlive { parts.append("running \(Chrome.span(facts.duration(now: now)))") }
        // A verdict's checks say more about a run than its size, and outlast the time when
        // the row is narrow (companion ADR 0012).
        if let tally = Checklist(checks: run.verdict?.checks ?? []).rowTally {
            parts.insert(tally, at: 0)
        } else if facts.stepCount > 0 {
            parts.append(Chrome.plural(facts.stepCount, "step"))
        }
        return (1...parts.count).reversed().map { parts.prefix($0).joined(separator: " · ") } + [""]
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

// MARK: - The strip

/// The runs folded to a strip of status marks (medium windows, or wide with the runs
/// hidden; ADR 0004 decision 8): one mark per run in the list's order, its state in the
/// glyph, its title and state in the tooltip. A click on a mark opens that run; the top
/// button (or `g r`, esc, `/`) opens the whole list over the run.
struct RunsStrip: View {
    let store: RunStore
    let expand: () -> Void

    @Environment(\.theme) private var theme

    var body: some View {
        TimelineView(.periodic(from: .now, by: 30)) { tick in
            let sections = SidebarView.sections(store.runs, store: store, now: tick.date)
            let titles = RunTitle.distinct(store.runs)
            VStack(spacing: 0) {
                Button(action: expand) {
                    Text("»")
                        .font(Typeface.monoBold.font(size: TypeScale.mono))
                        .foregroundStyle(theme.foreground)
                        .frame(width: 32, height: 28)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .hoverHighlight(radius: Radius.sm)
                .help("Show the runs (\(ActionRegistry.label(.goRuns)))")
                .accessibilityLabel("Show the runs")
                .padding(.top, Space.m)
                .padding(.bottom, Space.s)
                ScrollView {
                    LazyVStack(spacing: Space.xs) {
                        ForEach(Array(sections.enumerated()), id: \.element.id) { index, section in
                            if index > 0 {
                                Hairline().frame(width: 16).padding(.vertical, Space.xs)
                            }
                            ForEach(section.runs) { run in
                                StripMark(
                                    title: titles[run.runId] ?? RunTitle.short(task: run.task, runId: run.runId),
                                    status: store.facts(run.runId, now: tick.date).rowStatus(now: tick.date),
                                    selected: store.selectedRunId == run.runId
                                ) {
                                    store.selectedRunId = run.runId
                                }
                            }
                        }
                    }
                    .padding(.bottom, Space.m)
                }
                .scrollIndicators(.never)
            }
            .frame(maxWidth: .infinity)
        }
        .ground(.chrome)
        .background(theme.chromeTint)
    }
}

/// One run in the strip: its state as a glyph on a small square, the brand when open.
private struct StripMark: View {
    let title: String
    let status: (text: String, tone: RunFacts.Tone)
    let selected: Bool
    let open: () -> Void

    @Environment(\.theme) private var theme
    @State private var hovering = false

    var body: some View {
        Button(action: open) {
            glyph
                .font(Typeface.monoBold.font(size: TypeScale.monoSmall))
                .foregroundStyle(selected ? theme.brandText : theme.tone(status.tone, on: .chrome))
                .frame(width: 32, height: 28)
                .background(
                    RoundedRectangle(cornerRadius: Radius.sm, style: .continuous)
                        .fill(selected ? theme.brand : hovering ? theme.highlight : .clear)
                )
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help("\(title)\n\(status.text)")
        .accessibilityLabel("\(title), \(status.text)")
        .accessibilityAddTraits(selected ? .isSelected : [])
    }

    @ViewBuilder
    private var glyph: some View {
        switch status.tone {
        case .live: Text("●")
        case .attention: Text("!")
        case .pass: Text("✓")
        case .failure: Text("✗")
        case .neutral: Spinner(size: TypeScale.monoSmall)
        case .unsure: Text("?")
        case .quiet: Text("·")
        }
    }
}
