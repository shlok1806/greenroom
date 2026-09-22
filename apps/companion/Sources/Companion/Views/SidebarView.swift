import SwiftUI

/// Every run the daemon knows about, newest first.
///
/// The list is the thing a person reads most and changes least, so it is built
/// to be read: one dense row per run, grouped by the day it started, with the
/// clock time carrying the identity and the shape of the run (steps, frames,
/// messages) on the line under it. Status is a dot rather than the word
/// "finished" twenty times down the column; colour is spent only where it
/// means something.
struct SidebarView: View {
    @Bindable var store: RunStore

    @State private var query = ""

    var body: some View {
        VStack(spacing: 0) {
            list
            ConnectionFooter(store: store)
        }
    }

    private var list: some View {
        // One timer for the whole list: without it the relative times in the
        // rows are formatted once and then sit there, so "13 s ago" is still
        // "13 s ago" minutes later. The rows are cheap and nothing in the
        // store changes, so re-running body every 30 s costs only the labels
        // it exists to refresh.
        TimelineView(.periodic(from: .now, by: 30)) { tick in
            List(selection: selection) {
                ForEach(groups(now: tick.date), id: \.day) { group in
                    Section {
                        ForEach(group.runs) { run in
                            RunRow(run: run, now: tick.date).tag(run.runId)
                        }
                    } header: {
                        Text(group.day)
                            .font(.caption2.weight(.semibold))
                            .foregroundStyle(.tertiary)
                    }
                }
            }
            .listStyle(.sidebar)
            .overlay {
                if store.runs.isEmpty {
                    ContentUnavailableView(
                        "No runs",
                        systemImage: "cube",
                        description: Text("A run appears here when a coding agent creates a machine.")
                    )
                } else if matches.isEmpty {
                    ContentUnavailableView.search(text: query)
                }
            }
        }
        .searchable(text: $query, placement: .sidebar, prompt: "Find a run")
        .navigationTitle("Runs")
    }

    private var selection: Binding<String?> {
        Binding(
            get: { store.selectedRunId },
            set: { newValue in
                store.selectedRunId = newValue
                if let newValue { Task { await store.select(newValue) } }
            }
        )
    }

    private var matches: [RunSummary] {
        let needle = query.trimmingCharacters(in: .whitespaces).lowercased()
        guard !needle.isEmpty else { return store.runs }
        return store.runs.filter {
            $0.runId.lowercased().contains(needle)
                || $0.image.lowercased().contains(needle)
                || ($0.verdict?.verdict ?? "").lowercased().contains(needle)
        }
    }

    private struct Group {
        var day: String
        var runs: [RunSummary]
    }

    /// Runs in the order the daemon sent them, cut into days. The daemon
    /// already sorts newest first, so this only has to notice where one day
    /// ends rather than sort anything itself.
    private func groups(now: Date) -> [Group] {
        var out: [Group] = []
        for run in matches {
            let day = Chrome.day(run.createdAt, now: now)
            if out.last?.day == day {
                out[out.count - 1].runs.append(run)
            } else {
                out.append(Group(day: day, runs: [run]))
            }
        }
        return out
    }
}

private struct RunRow: View {
    let run: RunSummary
    /// The list's shared clock, so every row reads the same "now".
    let now: Date

    private var hash: String { Chrome.runHash(run.runId) }

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 6) {
                StatusDot(status: run.status)
                Text(Chrome.timeOfDay(run.createdAt))
                    .font(.system(.callout, design: .monospaced))
                if !hash.isEmpty {
                    Text(hash)
                        .font(.system(.caption, design: .monospaced))
                        .foregroundStyle(.tertiary)
                }
                Spacer(minLength: 4)
                // The age never gives way: it is the shortest thing in the
                // row and the one a reader scans down. What gives way is the
                // verdict word beneath it.
                Text(Chrome.relative(run.lastActivity, now: now))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .fixedSize()
            }
            HStack(spacing: 6) {
                // The verdict comes first and keeps its width: it is the one
                // thing on the row worth a colour, and squeezing it behind
                // the counts truncated it to "2…".
                if let verdict = run.verdict {
                    VerdictBadge(state: verdict, style: .compact)
                }
                Text(shape)
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
                    .lineLimit(1)
                    .truncationMode(.tail)
                Spacer(minLength: 0)
            }
        }
        .padding(.vertical, 2)
        .help(run.runId)
    }

    /// What there is to look at in this run, in the order a person cares:
    /// what it did, what it recorded, what was said.
    private var shape: String {
        var parts: [String] = []
        if run.steps > 0 { parts.append(Self.countAndNoun(run.steps, "step")) }
        if let frames = run.frames, frames > 0 { parts.append(Self.countAndNoun(frames, "frame")) }
        if run.messages > 0 { parts.append(Self.countAndNoun(run.messages, "msg")) }
        return parts.isEmpty ? "empty" : parts.joined(separator: " · ")
    }

    private static func countAndNoun(_ value: Int, _ noun: String) -> String {
        "\(Chrome.count(value)) \(noun)\(value == 1 ? "" : "s")"
    }
}

private struct ConnectionFooter: View {
    let store: RunStore

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            Divider()
            HStack(spacing: 6) {
                Circle()
                    .fill(store.connected ? Color.green : Color.secondary)
                    .frame(width: 6, height: 6)
                Text(store.connected ? "Live" : "Reconnecting")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Spacer(minLength: 0)
            }
            .padding(.horizontal, 12)
            if let error = store.lastError {
                Text(error)
                    .font(.caption2)
                    .foregroundStyle(.red)
                    .lineLimit(2)
                    .padding(.horizontal, 12)
            }
        }
        .padding(.bottom, 8)
        // Without a background of its own the footer is a transparent strip
        // and the last row of the list reads through it.
        .background(.bar)
    }
}
