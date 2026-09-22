import SwiftUI

/// Shared formatting. Status and verdict are the only things allowed a strong
/// colour, so a failed run or a contested verdict is what the eye lands on.
enum Chrome {
    // MARK: - Status

    static func symbol(for status: RunStatus) -> String {
        switch status {
        case .booting: "hourglass"
        case .ready: "checkmark.circle.fill"
        case .failed: "exclamationmark.triangle.fill"
        case .finished: "archivebox"
        case .unknown: "questionmark.circle"
        }
    }

    static func color(for status: RunStatus) -> Color {
        switch status {
        case .booting: .orange
        case .ready: .green
        case .failed: .red
        case .finished, .unknown: .secondary
        }
    }

    static func color(for status: VerdictStatus) -> Color {
        switch status {
        case .accepted: .green
        case .proposed: .blue
        case .contested: .orange
        case .rejected: .red
        case .none, .unknown: .secondary
        }
    }

    static func color(forVerdict verdict: String?) -> Color {
        switch verdict {
        case "pass": .green
        case "fail": .red
        case "inconclusive": .orange
        default: .secondary
        }
    }

    /// A closed verdict's glyph where there is no room for the word.
    static func glyph(for status: VerdictStatus) -> String? {
        switch status {
        case .accepted: "checkmark"
        case .contested: "exclamationmark"
        case .rejected: "xmark"
        case .proposed, .none, .unknown: nil
        }
    }

    // MARK: - Time

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

    static func timeOfDay(_ date: Date) -> String {
        timeOfDayFormatter.string(from: date)
    }

    static func stamp(_ date: Date) -> String {
        stampFormatter.string(from: date)
    }

    static func day(_ date: Date, now: Date = Date(), calendar: Calendar = .current) -> String {
        if calendar.isDate(date, inSameDayAs: now) { return "Today" }
        if let yesterday = calendar.date(byAdding: .day, value: -1, to: now),
           calendar.isDate(date, inSameDayAs: yesterday) {
            return "Yesterday"
        }
        return dayFormatter.string(from: date)
    }

    /// `0:07`, `12:03`, `1:23:45`. Minutes alone would read "476:12" for eight hours.
    static func clock(_ seconds: TimeInterval) -> String {
        let total = Int(max(0, seconds).rounded())
        let hours = total / 3600
        let minutes = (total % 3600) / 60
        let secs = total % 60
        if hours > 0 {
            return String(format: "%d:%02d:%02d", hours, minutes, secs)
        }
        return String(format: "%d:%02d", minutes, secs)
    }

    static func duration(_ milliseconds: Int) -> String {
        if milliseconds < 1000 { return "\(milliseconds) ms" }
        return String(format: "%.1f s", Double(milliseconds) / 1000)
    }

    // MARK: - Numbers and names

    /// "2.4k" rather than four digits.
    static func count(_ value: Int) -> String {
        switch value {
        case ..<1000: "\(value)"
        case ..<10_000: String(format: "%.1fk", Double(value) / 1000)
        case ..<1_000_000: "\(value / 1000)k"
        default: String(format: "%.1fM", Double(value) / 1_000_000)
        }
    }

    /// Steps, frames, messages, longest first, each candidate dropping the
    /// last term, so a narrow row loses whole terms instead of mid-word.
    static func shapes(steps: Int, frames: Int?, messages: Int) -> [String] {
        var parts: [String] = []
        if steps > 0 { parts.append(countAndNoun(steps, "step")) }
        if let frames, frames > 0 { parts.append(countAndNoun(frames, "frame")) }
        if messages > 0 { parts.append(countAndNoun(messages, "msg")) }
        guard !parts.isEmpty else { return ["empty"] }
        return (1...parts.count).reversed().map { parts.prefix($0).joined(separator: " · ") }
    }

    private static func countAndNoun(_ value: Int, _ noun: String) -> String {
        "\(count(value)) \(noun)\(value == 1 ? "" : "s")"
    }

    /// The hash tail of a `yyyymmdd-hhmmss-<hash>` run id. The id's clock is
    /// UTC, so displayed times always come from `createdAt`, never the id.
    static func runHash(_ runId: String) -> String {
        let parts = runId.split(separator: "-", maxSplits: 2, omittingEmptySubsequences: false)
        guard parts.count > 2 else { return "" }
        return String(parts[2].prefix(6))
    }

    // MARK: - Formatters

    private static let timeOfDayFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.dateFormat = "HH:mm:ss"
        return formatter
    }()

    private static let stampFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.dateStyle = .medium
        formatter.timeStyle = .medium
        return formatter
    }()

    private static let dayFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.setLocalizedDateFormatFromTemplate("MMMd")
        return formatter
    }()
}

struct StatusDot: View {
    let status: RunStatus

    var body: some View {
        Circle()
            .fill(Chrome.color(for: status))
            .frame(width: 7, height: 7)
            .help(status.text)
    }
}

/// Shows nothing for an empty status rather than a lone question mark.
struct StatusBadge: View {
    let status: RunStatus

    var body: some View {
        if status != .unknown("") {
            Label(status.text, systemImage: Chrome.symbol(for: status))
                .labelStyle(.titleAndIcon)
                .font(.caption)
                .foregroundStyle(Chrome.color(for: status))
                .fixedSize()
        }
    }
}

/// `.full` for the run header; `.compact` for the run list, with the full
/// wording in the tooltip.
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
        if state.status != VerdictStatus.none {
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
                    .frame(width: 5, height: 5)
                Text(state.verdict ?? state.status.text)
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(Chrome.color(forVerdict: state.verdict))
                    .fixedSize()
                if let glyph = Chrome.glyph(for: state.status) {
                    Image(systemName: glyph)
                        .font(.system(size: 7, weight: .bold))
                        .foregroundStyle(Chrome.color(for: state.status))
                        .fixedSize()
                }
            }
        }
    }
}

/// The small uppercase caption used above values and blocks.
struct SectionCaption: View {
    let title: String

    var body: some View {
        Text(title.uppercased())
            .font(.system(size: 9, weight: .semibold))
            .tracking(0.6)
            .foregroundStyle(.tertiary)
    }
}

/// One label above one value, the unit the run header is built from.
struct FieldLabel: View {
    let label: String
    let value: String
    var monospaced = true

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            SectionCaption(title: label)
            Text(value)
                .font(monospaced ? .caption.monospaced() : .caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.middle)
        }
        .help(value)
    }
}
