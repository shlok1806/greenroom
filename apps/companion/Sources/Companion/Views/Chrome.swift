import SwiftUI

/// Shared formatting: times, counts, names. State colours and symbols are in
/// `Theme.swift`.
enum Chrome {
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

    /// "now", "45s", "32m", "5h", "3d", "2w": for a row with no room for words.
    static func age(_ date: Date, now: Date = Date()) -> String {
        let seconds = Int(max(0, now.timeIntervalSince(date)))
        switch seconds {
        case ..<10: return "now"
        case ..<60: return "\(seconds)s"
        case ..<3600: return "\(seconds / 60)m"
        case ..<86_400: return "\(seconds / 3600)h"
        case ..<604_800: return "\(seconds / 86_400)d"
        default: return "\(seconds / 604_800)w"
        }
    }

    /// "CDT": the zone the app's times are in. The machine's own clock may differ (UTC).
    static var zone: String {
        TimeZone.current.abbreviation() ?? TimeZone.current.identifier
    }

    /// "1 step", "68 steps".
    static func plural(_ value: Int, _ noun: String) -> String {
        "\(count(value)) \(noun)\(value == 1 ? "" : "s")"
    }

    /// "20:08": a row's start time; the full stamp is in the header.
    static func shortTime(_ date: Date) -> String {
        shortTimeFormatter.string(from: date)
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

    /// "640 ms", "42.9 s", "5m 0s", "1h 2m": a step's length, read at a glance.
    static func duration(_ milliseconds: Int) -> String {
        if milliseconds < 1000 { return "\(milliseconds) ms" }
        if milliseconds < 60_000 { return String(format: "%.1f s", Double(milliseconds) / 1000) }
        let seconds = milliseconds / 1000
        if seconds < 3600 { return "\(seconds / 60)m \(seconds % 60)s" }
        return "\(seconds / 3600)h \((seconds % 3600) / 60)m"
    }

    /// "8s", "4m", "2h 5m": how long ago, for the status line.
    static func span(_ seconds: TimeInterval) -> String {
        let total = Int(max(0, seconds))
        switch total {
        case ..<60: return "\(total)s"
        case ..<3600: return "\(total / 60)m"
        case ..<86_400:
            let minutes = (total % 3600) / 60
            return minutes == 0 ? "\(total / 3600)h" : "\(total / 3600)h \(minutes)m"
        default: return "\(total / 86_400)d"
        }
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

    private static let shortTimeFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.dateFormat = "HH:mm"
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
