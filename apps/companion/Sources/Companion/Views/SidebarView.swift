import SwiftUI

/// Every run the daemon knows about, newest first.
struct SidebarView: View {
    @Bindable var store: RunStore

    var body: some View {
        List(store.runs, selection: Binding(
            get: { store.selectedRunId },
            set: { newValue in
                store.selectedRunId = newValue
                if let newValue {
                    Task { await store.select(newValue) }
                }
            }
        )) { run in
            RunRow(run: run).tag(run.runId)
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

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            Text(run.runId)
                .font(.callout.monospaced())
                .lineLimit(1)
                .truncationMode(.middle)
            HStack(spacing: 8) {
                StatusBadge(status: run.status)
                if let verdict = run.verdict {
                    VerdictBadge(state: verdict)
                }
                Spacer(minLength: 4)
                Text(Chrome.relative(run.lastActivity))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
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
