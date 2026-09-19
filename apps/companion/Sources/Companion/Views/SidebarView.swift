import SwiftUI

/// Every run the daemon knows about, newest first.
struct SidebarView: View {
    @Bindable var store: RunStore

    var body: some View {
        // One timer for the whole list: without it the relative times in the rows are
        // formatted once and then sit there, so "13 s ago" is still "13 s ago" minutes
        // later. The rows are cheap, and nothing in the store changes, so re-running
        // this body every 30 s costs only the labels it exists to refresh.
        TimelineView(.periodic(from: .now, by: 30)) { tick in
            List(store.runs, selection: Binding(
                get: { store.selectedRunId },
                set: { newValue in
                    store.selectedRunId = newValue
                    if let newValue {
                        Task { await store.select(newValue) }
                    }
                }
            )) { run in
                RunRow(run: run, now: tick.date).tag(run.runId)
            }
        }
        .listStyle(.sidebar)
        .navigationTitle("Runs")
        .overlay {
            if store.runs.isEmpty {
                ContentUnavailableView(
                    "No runs",
                    systemImage: "cube",
                    description: Text("A run appears here when a coding agent creates a machine.")
                )
            }
        }
        .safeAreaInset(edge: .bottom) {
            ConnectionFooter(store: store)
        }
    }
}

private struct RunRow: View {
    let run: RunSummary
    /// The list's shared clock, so every row reads the same "now".
    let now: Date

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            Text(run.runId)
                .font(.callout.monospaced())
                .lineLimit(1)
                .truncationMode(.middle)
            HStack(spacing: 6) {
                // The badges keep their full width; the time label is what gives way
                // when the sidebar is dragged narrow.
                StatusBadge(status: run.status)
                    .layoutPriority(1)
                if let verdict = run.verdict {
                    VerdictBadge(state: verdict, style: .compact)
                        .layoutPriority(1)
                }
                Spacer(minLength: 4)
                Text(Chrome.relative(run.lastActivity, now: now))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .minimumScaleFactor(0.8)
                    .layoutPriority(0)
            }
        }
        .padding(.vertical, 3)
    }
}

private struct ConnectionFooter: View {
    let store: RunStore

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Divider()
            HStack(spacing: 6) {
                Image(systemName: store.connected ? "bolt.horizontal.circle.fill" : "bolt.horizontal.circle")
                    .foregroundStyle(store.connected ? .green : .secondary)
                Text(store.connected ? "Live" : "Reconnecting")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if let error = store.lastError {
                Text(error)
                    .font(.caption2)
                    .foregroundStyle(.red)
                    .lineLimit(2)
            }
        }
        .padding(.horizontal, 10)
        .padding(.bottom, 8)
    }
}
