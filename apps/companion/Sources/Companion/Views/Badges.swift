import SwiftUI

/// The shared pieces of chrome: what a status looks like, how a time reads,
/// how a run is named.
///
/// The palette is deliberately short. Status and verdict are the only things
/// on screen allowed a strong colour; everything else is the window's own
/// greys, so that a failed run or a contested verdict is the first thing the
/// eye lands on rather than one colour among many.
enum Chrome {
    // MARK: - Status

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

    /// The suffix a closed verdict carries where there is no room for the
    /// word. An open (`proposed`) verdict gets none: it is the common case.
    static func glyph(for status: VerdictStatus) -> String? {
        switch status {
        case .accepted: return "checkmark"
        case .contested: return "exclamationmark"
        case .rejected: return "xmark"
        case .proposed, .none, .unknown: return nil
        }
    }

    // MARK: - Time

    /// A short, readable age. `now` is a parameter so the thresholds can be
    /// tested, and so a list can hand every row the same tick of the clock.
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

    /// The time of day, to the second. A transcript where two hundred rows all
    /// read "2 days ago" tells a reader nothing about their order or their
    /// spacing; this is what actually separates them.
    static func timeOfDay(_ date: Date) -> String {
        timeOfDayFormatter.string(from: date)
    }

    /// Date and time together, for a header and for tooltips.
    static func stamp(_ date: Date) -> String {
        stampFormatter.string(from: date)
    }

    /// The heading a day's runs are grouped under.
    static func day(_ date: Date, now: Date = Date(), calendar: Calendar = .current) -> String {
        if calendar.isDate(date, inSameDayAs: now) { return "Today" }
        if let yesterday = calendar.date(byAdding: .day, value: -1, to: now),
           calendar.isDate(date, inSameDayAs: yesterday) {
            return "Yesterday"
        }
        return dayFormatter.string(from: date)
    }

    /// A span, as a clock reads it: `0:07`, `12:03`, `1:23:45`. The old
    /// `%02d:%02d` of minutes and seconds turned an eight-hour run into
    /// "476:12", which is not a time anybody reads.
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

    /// A count at a glance. Four digits of frames in a sidebar row is noise;
    /// "2.4k" is the same fact in half the space.
    static func count(_ value: Int) -> String {
        switch value {
        case ..<1000: return "\(value)"
        case ..<10_000: return String(format: "%.1fk", Double(value) / 1000)
        case ..<1_000_000: return "\(value / 1000)k"
        default: return String(format: "%.1fM", Double(value) / 1_000_000)
        }
    }

    /// What there is to look at in a run, in the order a person cares: what
    /// it did, what it recorded, what was said. Returned longest first, each
    /// candidate dropping the least useful term of the one before, so a row
    /// short of width loses whole terms instead of being cut mid-word.
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

    /// The part of a run id that only tells runs apart.
    ///
    /// Every id is `yyyymmdd-hhmmss-<hash>`, so twenty of them stacked up are
    /// twenty near-identical strings whose only difference is in the middle.
    /// The list shows the run's clock time and this hash after it, quieter;
    /// the whole id stays one copy away in the header.
    ///
    /// The time comes from `createdAt` and never from the id: the daemon
    /// names a run in UTC, so reading the clock out of the id put the list
    /// five hours away from every other time in the window.
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

/// A run's lifecycle as a single dot. Twenty rows that each say "finished" in
/// words are twenty rows of noise; the word belongs in the header, where there
/// is one of it.
struct StatusDot: View {
    let status: RunStatus
    var size: CGFloat = 7

    var body: some View {
        Circle()
            .fill(Chrome.color(for: status))
            .frame(width: size, height: size)
            .help(status.text)
    }
}

/// The lifecycle of a run, as an icon and a word, for the one place that has
/// room for it. A status the app cannot name shows nothing rather than a lone
/// question mark with an empty label beside it.
struct StatusBadge: View {
    let status: RunStatus

    var body: some View {
        if case .unknown(let raw) = status, raw.isEmpty {
            EmptyView()
        } else {
            Label(status.text, systemImage: Chrome.symbol(for: status))
                .labelStyle(.titleAndIcon)
                .font(.caption)
                .foregroundStyle(Chrome.color(for: status))
                .fixedSize()
        }
    }
}

/// Where the verdict stands, when there is one.
///
/// The `.full` style spells out both words and belongs in the run header. The
/// `.compact` style is for the run list, where "inconclusive, proposed" has no
/// room: it shows a coloured dot, the verdict word, and a glyph for a closed
/// status, with the full wording in the tooltip.
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

/// One label above one value, the unit the run header is built from.
struct FieldLabel: View {
    let label: String
    let value: String
    var monospaced = true

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(label.uppercased())
                .font(.system(size: 9, weight: .semibold))
                .tracking(0.6)
                .foregroundStyle(.tertiary)
            Text(value)
                .font(monospaced ? .caption.monospaced() : .caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.middle)
        }
        .help(value)
    }
}
