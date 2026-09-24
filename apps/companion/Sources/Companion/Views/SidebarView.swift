import SwiftUI

/// Every run, newest first: the ones that need a person, then the ones running, then
/// the rest by day. A row is named by a short, distinct title and says its state in words.
struct SidebarView: View {
    @Bindable var store: RunStore

    @State private var query = ""

    var body: some View {
        // One shared clock keeps idle times fresh.
        TimelineView(.periodic(from: .now, by: 30)) { tick in
            let titles = RunTitle.distinct(store.runs)
            List(selection: $store.selectedRunId) {
                ForEach(sections(now: tick.date)) { section in
                    Section {
                        ForEach(section.runs) { run in
                            RunRow(
                                run: run,
                                title: titles[run.runId] ?? RunTitle.short(task: run.task, runId: run.runId),
                                facts: store.facts(run.runId, now: tick.date),
                                now: tick.date
                            )
                            .tag(run.runId)
                        }
                    } header: {
                        SectionTitle(title: section.title, count: section.pinned ? section.runs.count : nil)
                    }
                }
            }
            .listStyle(.sidebar)
            .overlayScrollers()
            .overlay { overlay }
        }
        .modifier(SearchWhenThereIsSomethingToSearch(enabled: !store.runs.isEmpty, query: $query))
        // Only while the event stream is down or an action failed: a healthy connection
        // needs no words, and an unreachable daemon is explained in the detail.
        .safeAreaInset(edge: .bottom, spacing: 0) {
            if case .online = store.connection, !store.connected || store.lastError != nil {
                ConnectionFooter(store: store)
                    .background(.bar)
                    .overlay(alignment: .top) { Divider() }
            }
        }
        .navigationTitle("Runs")
    }

    @ViewBuilder
    private var overlay: some View {
        if store.runs.isEmpty {
            switch store.connection {
            case .connecting:
                ProgressView().controlSize(.small)
            case .offline, .refused:
                Text("Not connected")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            case .online:
                // The detail welcomes a person with no runs; the list stays quiet.
                Text("No runs yet")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
        } else if matches.isEmpty {
            ContentUnavailableView.search(text: query)
        }
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

    private struct RunSection: Identifiable {
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

private struct SectionTitle: View {
    let title: String
    let count: Int?

    var body: some View {
        HStack(spacing: 6) {
            Text(title)
            if let count {
                Text("\(count)")
                    .monospacedDigit()
                    .foregroundStyle(.tertiary)
            }
        }
    }
}

/// `searchable` has no disabled state; a field with nothing to search is not offered.
private struct SearchWhenThereIsSomethingToSearch: ViewModifier {
    let enabled: Bool
    @Binding var query: String

    func body(content: Content) -> some View {
        if enabled {
            content.searchable(text: $query, placement: .sidebar, prompt: "Find a run")
        } else {
            content
        }
    }
}

/// The short title, then when it started and how big it is, with its state in words.
private struct RunRow: View {
    let run: RunSummary
    let title: String
    let facts: RunFacts
    let now: Date

    var body: some View {
        let status = facts.rowStatus(now: now)
        VStack(alignment: .leading, spacing: 3) {
            Text(title)
                .font(.body)
                .lineLimit(2)
                .truncationMode(.tail)
                .frame(maxWidth: .infinity, alignment: .leading)
            HStack(spacing: Space.s) {
                ViewThatFits(in: .horizontal) {
                    ForEach(meta, id: \.self) { line in
                        Text(line).lineLimit(1)
                    }
                }
                .font(.subheadline.monospacedDigit())
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
                StatusText(text: status.text, tone: status.tone)
            }
        }
        .padding(.vertical, Space.xs)
        .help(help(status: status.text))
        .accessibilityElement(children: .combine)
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
                Label("Reconnecting to live updates", systemImage: "arrow.triangle.2.circlepath")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if let error = store.lastError {
                Label(error, systemImage: "exclamationmark.triangle")
                    .font(.caption)
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
