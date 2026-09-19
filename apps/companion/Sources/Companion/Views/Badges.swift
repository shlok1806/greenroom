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

    /// A short, readable age. `now` is a parameter so the thresholds can be tested,
    /// and so a view can hand every row the same tick of the clock.
    static func relative(_ date: Date, now: Date = Date()) -> String {
        let seconds = max(0, now.timeIntervalSince(date))
        switch seconds {
        case ..<10:
            return "just now"
        case ..<60:
            return "\(Int(seconds)) s ago"
        case ..<3600:
            return "\(Int(seconds) / 60) min ago"
        case ..<86_400:
            return "\(Int(seconds) / 3600) hr ago"
        case ..<604_800:
            let days = Int(seconds) / 86_400
            return days == 1 ? "1 day ago" : "\(days) days ago"
        default:
            let weeks = Int(seconds) / 604_800
            return weeks == 1 ? "1 wk ago" : "\(weeks) wk ago"
        }
    }

    /// The suffix a closed verdict carries in the narrow sidebar. An open
    /// (`proposed`) verdict gets none: the word alone says everything.
    static func glyph(for status: VerdictStatus) -> String? {
        switch status {
        case .accepted: return "checkmark"
        case .contested: return "exclamationmark"
        case .rejected: return "xmark"
        case .proposed, .none, .unknown: return nil
        }
    }

    static func duration(_ milliseconds: Int) -> String {
        if milliseconds < 1000 { return "\(milliseconds) ms" }
        return String(format: "%.1f s", Double(milliseconds) / 1000)
    }
}

/// The lifecycle of a run, as an icon and a word. Never truncates: the word is
/// short enough to fit even the narrow sidebar, so it is allowed its full width.
struct StatusBadge: View {
    let status: RunStatus

    var body: some View {
        Label(status.text, systemImage: Chrome.symbol(for: status))
            .labelStyle(.titleAndIcon)
            .font(.caption)
            .foregroundStyle(Chrome.color(for: status))
            .fixedSize()
    }
}

/// Where the verdict stands, when there is one.
///
/// The `.full` style spells out both words and belongs in the wide run header.
/// The `.compact` style is for the sidebar, where "inconclusive, proposed" used to
/// truncate to "incon... prop...": it shows a coloured dot, the verdict word, and a
/// glyph for a closed status, with the full wording in the tooltip.
struct VerdictBadge: View {
    enum Style {
        case full
        case compact
    }

    let state: VerdictState
    var style: Style = .full

    private var description: String {
        guard let verdict = state.verdict else { return state.status.text }
        return "\(verdict), \(state.status.text)"
    }

    var body: some View {
        if case .none = state.status {
            EmptyView()
        } else {
            content
                .padding(.horizontal, style == .compact ? 5 : 6)
                .padding(.vertical, 1)
                .background(Chrome.color(for: state.status).opacity(0.12), in: Capsule())
                .help(description)
        }
    }

    @ViewBuilder
    private var content: some View {
        switch style {
        case .full:
            HStack(spacing: 4) {
                if let verdict = state.verdict {
                    Text(verdict)
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(Chrome.color(forVerdict: verdict))
                        .fixedSize()
                }
                Text(state.status.text)
                    .font(.caption)
                    .foregroundStyle(Chrome.color(for: state.status))
                    .fixedSize()
            }
        case .compact:
            HStack(spacing: 3) {
                Circle()
                    .fill(Chrome.color(forVerdict: state.verdict))
                    .frame(width: 6, height: 6)
                Text(state.verdict ?? state.status.text)
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(Chrome.color(forVerdict: state.verdict))
                    .fixedSize()
                if let glyph = Chrome.glyph(for: state.status) {
                    Image(systemName: glyph)
                        .font(.caption2.weight(.semibold))
                        .foregroundStyle(Chrome.color(for: state.status))
                        .fixedSize()
                }
            }
        }
    }
}
