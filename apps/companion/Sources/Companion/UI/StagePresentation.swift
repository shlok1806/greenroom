import Foundation

// Pure rules for the checks column and the stage of a run (companion ADR 0019), each with a
// test in `RunPaneTests`.

/// One of the four steps of getting a Mac ready, in plain words (Figma wireframe 06): the
/// daemon's boot phases folded into what a person recognises. No image names, addresses or keys.
struct BootRow: Equatable, Sendable, Identifiable {
    var id: String { text }
    var text: String
    var glyph: GlyphKind
    var meta: String

    static let steps: [(text: String, phases: Set<String>)] = [
        ("Copied a fresh Mac", ["clone"]),
        ("Started macOS", ["start", "agent"]),
        ("Connecting to the screen", ["ip", "key", "settings", "helper", "checks"]),
        ("Handing it to the verifier", ["ssh"]),
    ]

    /// The rows for the phases so far. A step is done once a later step began, or the Mac is
    /// ready; it is working while any of its phases runs or it is the newest step begun.
    static func rows(_ phases: [BootPhase], ready: Bool = false) -> [BootRow] {
        let begun = steps.indices.filter { i in phases.contains { steps[i].phases.contains($0.phase.text) } }
        let newest = begun.max()
        return steps.indices.map { i in
            let (text, names) = steps[i]
            let mine = phases.filter { names.contains($0.phase.text) }
            let seconds = Int(mine.compactMap(\.seconds).reduce(0, +).rounded())
            if ready || (newest.map { i < $0 } ?? false) {
                return BootRow(text: text, glyph: .passed, meta: mine.isEmpty ? "" : Clock.elapsed(seconds))
            }
            if i == newest {
                let done = mine.allSatisfy { !$0.running } && i == steps.count - 1
                return BootRow(text: text, glyph: done ? .passed : .checking, meta: done ? Clock.elapsed(seconds) : "now")
            }
            return BootRow(text: text, glyph: .pending, meta: "")
        }
    }
}

/// One key frame under the picture.
struct FilmstripItem: Equatable, Sendable, Identifiable {
    var id: String { file }
    var file: String
    var step: Int
    var mark: FilmstripThumb.Mark
}

enum Filmstrip {
    /// Up to `count` frames, oldest first: the frame at each check's proof (a failed check's
    /// marked red), then frames spread evenly over the run, always ending on the newest.
    static func items(_ frames: [Frame], checks: [SummaryCheck], count: Int = 8) -> [FilmstripItem] {
        guard !frames.isEmpty, count > 0 else { return [] }
        let sorted = frames.sorted { $0.at < $1.at }
        var chosen: [String: FilmstripThumb.Mark] = [:]
        chosen[sorted[sorted.count - 1].file] = FilmstripThumb.Mark.none
        for check in checks where chosen.count < count {
            guard let step = check.picture?.step ?? check.step, let frame = sorted.first(where: { $0.step >= step }) ?? sorted.last else { continue }
            if check.state == .fail {
                chosen[frame.file] = .failed
            } else if chosen[frame.file] == nil {
                chosen[frame.file] = FilmstripThumb.Mark.none
            }
        }
        let room = count - chosen.count
        if room > 0, sorted.count > chosen.count {
            let gap = Double(sorted.count - 1) / Double(room + 1)
            var i = gap
            while chosen.count < count, i < Double(sorted.count - 1) {
                let file = sorted[Int(i)].file
                if chosen[file] == nil { chosen[file] = FilmstripThumb.Mark.none }
                i += max(gap, 1)
            }
        }
        return sorted.filter { chosen[$0.file] != nil }.map { FilmstripItem(file: $0.file, step: $0.step, mark: chosen[$0.file] ?? .none) }
    }

    /// The frame recorded at or after a step, for a check whose proof is a step.
    static func frame(atOrAfter step: Int, in frames: [Frame]) -> Frame? {
        let sorted = frames.sorted { $0.at < $1.at }
        return sorted.first { $0.step >= step } ?? sorted.last
    }
}

/// What the stage shows, worked out from the summary and what the person picked.
enum StageContent: Equatable, Sendable {
    /// The live screen (an open run with no outcome).
    case live
    /// A picture: a screenshot artifact or a recorded frame, with a mark on it.
    case picture(SummaryPicture, mark: SummaryBox?, markColor: ToneColor, dimmed: Bool)
    /// Nothing to show yet: the Mac is starting.
    case waiting(String)
    /// No picture at all.
    case none(String)

    static func of(_ s: Summary, check: SummaryCheck?, pickedFrame: String?, liveWanted: Bool, framesHeld: [Frame]) -> StageContent {
        if let pickedFrame {
            return .picture(SummaryPicture(kind: "frame", file: pickedFrame), mark: nil, markColor: .fail, dimmed: false)
        }
        switch s.state {
        case .starting:
            return .waiting("The screen appears here when the Mac is ready")
        case .notAnswering, .restarting:
            if let last = s.lastFrame { return .picture(last, mark: nil, markColor: .fail, dimmed: true) }
            return .none("No picture yet")
        default:
            break
        }
        if liveWanted { return .live }
        if let check, let picture = check.picture {
            let color: ToneColor = check.state == .pass ? .pass : .fail
            return .picture(picture, mark: check.mark, markColor: color, dimmed: false)
        }
        if let check, let step = check.step, let frame = Filmstrip.frame(atOrAfter: step, in: framesHeld) {
            return .picture(SummaryPicture(kind: "frame", file: frame.file, step: frame.step), mark: nil, markColor: .fail, dimmed: false)
        }
        if let last = s.lastFrame { return .picture(last, mark: nil, markColor: .fail, dimmed: false) }
        return .none("This run recorded no picture")
    }
}
