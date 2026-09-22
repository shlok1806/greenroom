import SwiftUI

/// Every run, newest first, grouped by day: one dense row each.
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
        // One shared clock keeps the relative ages fresh.
        TimelineView(.periodic(from: .now, by: 30)) { tick in
            List(selection: $store.selectedRunId) {
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

    /// The daemon already sorts newest first, so this only cuts at day changes.
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
                // The age never truncates; the row below gives way instead.
                Text(Chrome.relative(run.lastActivity, now: now))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .fixedSize()
            }
            HStack(spacing: 6) {
                // The verdict keeps its width; the counts drop terms to fit.
                if let verdict = run.verdict {
                    VerdictBadge(state: verdict, style: .compact)
                }
                ViewThatFits(in: .horizontal) {
                    ForEach(shapes, id: \.self) { shape in
                        Text(shape).lineLimit(1)
                    }
                }
                .font(.caption2)
                .foregroundStyle(.tertiary)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .padding(.vertical, 2)
        .help(run.runId)
    }

    private var shapes: [String] {
        Chrome.shapes(steps: run.steps, frames: run.frames, messages: run.messages)
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
        .background(.bar)
    }
}
