import SwiftUI

/// Small shared pieces of chrome: how a status looks, how a time reads.
enum Chrome {
    static func symbol(for status: RunStatus) -> String {
        switch status {
        case .booting: return "hourglass"
        case .ready: return "checkmark.circle.fill"
        case .failed: return "exclamationmark.triangle.fill"
        case .finished: return "archivebox"
        case .unknown: return "questionmark.circle"
        }
    }

    static func color(for status: RunStatus) -> Color {
        switch status {
        case .booting: return .orange
        case .ready: return .green
        case .failed: return .red
        case .finished: return .secondary
        case .unknown: return .secondary
        }
    }

    static func color(for status: VerdictStatus) -> Color {
        switch status {
        case .accepted: return .green
        case .proposed: return .blue
        case .contested: return .orange
        case .rejected: return .red
        case .none, .unknown: return .secondary
        }
    }

    static func color(forVerdict verdict: String?) -> Color {
        switch verdict {
        case "pass": return .green
        case "fail": return .red
        case "inconclusive": return .orange
        default: return .secondary
        }
    }

    static func relative(_ date: Date) -> String {
        date.formatted(.relative(presentation: .numeric, unitsStyle: .abbreviated))
    }

    static func duration(_ milliseconds: Int) -> String {
        if milliseconds < 1000 { return "\(milliseconds) ms" }
        return String(format: "%.1f s", Double(milliseconds) / 1000)
    }
}

/// The lifecycle of a run, as an icon and a word.
struct StatusBadge: View {
    let status: RunStatus

    var body: some View {
        Label(status.text, systemImage: Chrome.symbol(for: status))
            .labelStyle(.titleAndIcon)
            .font(.caption)
            .foregroundStyle(Chrome.color(for: status))
    }
}

/// Where the verdict stands, when there is one.
struct VerdictBadge: View {
    let state: VerdictState

    var body: some View {
        if case .none = state.status {
            EmptyView()
        } else {
            HStack(spacing: 4) {
                if let verdict = state.verdict {
                    Text(verdict)
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(Chrome.color(forVerdict: verdict))
                }
                Text(state.status.text)
                    .font(.caption)
                    .foregroundStyle(Chrome.color(for: state.status))
            }
            .padding(.horizontal, 6)
            .padding(.vertical, 1)
            .background(Chrome.color(for: state.status).opacity(0.12), in: Capsule())
        }
    }
}
