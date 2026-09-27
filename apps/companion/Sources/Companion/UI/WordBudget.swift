import CoreGraphics
import Foundation
import Vision

/// The text budget (docs/20 section 6, companion ADR 0019): words visible by default in a
/// 1280 x 800 window, counted in the run pane (the sidebar and the guest's own screen are
/// not counted). The snapshot harness and the tests render each state and read it back with
/// Apple's text recogniser, so the budget is measured on pixels, not on the source.
enum WordBudget {
    enum Screen: String, CaseIterable, Sendable {
        case runLive = "Run, live"
        case runVerdictWaiting = "Run, verdict waiting"
        case runFinished = "Run, finished"
        case booting = "Booting"
        case noRunOpen = "No run open"
        case firstLaunch = "First launch"

        /// The most words the state may show.
        var budget: Int {
            switch self {
            case .runLive: 60
            case .runVerdictWaiting: 70
            case .runFinished: 50
            case .booting: 20
            case .noRunOpen: 15
            case .firstLaunch: 35
            }
        }
    }

    /// A sidebar row: glyph, name, meta.
    static let runRow = 6
    /// A check row: the claim and its one fact.
    static let checkRow = 8
}

/// Reads the words a picture shows.
enum VisibleWords {
    struct Reading: Sendable {
        var lines: [String]
        var words: Int { lines.reduce(0) { $0 + VisibleWords.words(in: $1) } }
    }

    /// Words in a line of text as a reader counts them: runs of letters or digits, so
    /// "$10.00" and "4:18" are one word each and a lone mark is none.
    static func words(in line: String) -> Int {
        line.split(whereSeparator: \.isWhitespace).filter { token in
            token.contains { $0.isLetter || $0.isNumber }
        }.count
    }

    /// The text lines in `image`, top to bottom, read by Vision's accurate recogniser.
    static func read(_ image: CGImage, excluding: [CGRect] = []) throws -> Reading {
        let request = VNRecognizeTextRequest()
        request.recognitionLevel = .accurate
        request.usesLanguageCorrection = false
        request.minimumTextHeight = 0.004
        let handler = VNImageRequestHandler(cgImage: image, options: [:])
        try handler.perform([request])
        let size = CGSize(width: image.width, height: image.height)
        let observations = (request.results ?? []).filter { obs in
            // Vision's boxes are normalised with the origin bottom left.
            let box = obs.boundingBox
            let rect = CGRect(x: box.minX * size.width, y: (1 - box.maxY) * size.height, width: box.width * size.width, height: box.height * size.height)
            return !excluding.contains { $0.intersects(rect.insetBy(dx: rect.width * 0.25, dy: rect.height * 0.25)) }
        }
        let sorted = observations.sorted { $0.boundingBox.maxY > $1.boundingBox.maxY }
        return Reading(lines: sorted.compactMap { $0.topCandidates(1).first?.string })
    }
}
