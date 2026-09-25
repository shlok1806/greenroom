import AppKit
import CoreText
import SwiftUI

/// The faces bundled with the app (OFL, `Resources/Fonts/OFL-LICENSE-*.txt`), registered
/// for this process only. Nothing is installed on the Mac.
enum BundledFonts {
    /// The font files registered, once per process. `CompanionMain` touches it before the
    /// first window; `Typeface` touches it before naming a face, so the tests and the
    /// snapshot harness get the faces too.
    static let registered: [URL] = {
        let files = (try? FileManager.default.contentsOfDirectory(
            at: AppResources.fontsDirectory, includingPropertiesForKeys: nil
        )) ?? []
        let fonts = files.filter { $0.pathExtension.lowercased() == "otf" }.sorted { $0.path < $1.path }
        for url in fonts {
            var error: Unmanaged<CFError>?
            // Registering twice (a second process in the same session) reports "already
            // registered"; the face is there either way.
            _ = CTFontManagerRegisterFontsForURL(url as CFURL, .process, &error)
            error?.release()
        }
        return fonts
    }()

    static func ensureRegistered() { _ = registered }

    /// Whether a face is available by its PostScript name, rather than a fallback.
    static func resolves(_ postScriptName: String) -> Bool {
        ensureRegistered()
        return NSFont(name: postScriptName, size: 13)?.fontName == postScriptName
    }
}

/// The two faces (ADR 0008): Monaspace Neon for chrome and data, Mona Sans for anything
/// read as sentences, with its Expanded cut for headings and its italics for Markdown's
/// emphasis (never a slanted fake). Families come from
/// `tokens.json` `type`; sizes from `cell` and `reading`.
enum Typeface: Sendable, CaseIterable {
    case monoRegular, monoMedium, monoBold
    case readingRegular, readingMedium, readingSemiBold, readingBold
    case readingItalic, readingSemiBoldItalic
    case headingSemiBold, headingBold

    var postScriptName: String {
        let type = DesignData.shared.tokens.type
        let mono = type.mono.filter { !$0.isWhitespace }
        let reading = type.reading.filter { !$0.isWhitespace }
        switch self {
        case .monoRegular: return "\(mono)-Regular"
        case .monoMedium: return "\(mono)-Medium"
        case .monoBold: return "\(mono)-Bold"
        case .readingRegular: return "\(reading)-Regular"
        case .readingMedium: return "\(reading)-Medium"
        case .readingSemiBold: return "\(reading)-SemiBold"
        case .readingBold: return "\(reading)-Bold"
        case .readingItalic: return "\(reading)-Italic"
        case .readingSemiBoldItalic: return "\(reading)-SemiBoldItalic"
        // `readingHeading`: the same family, its wider (Expanded) cut.
        case .headingSemiBold: return "\(reading)Expanded-SemiBold"
        case .headingBold: return "\(reading)Expanded-Bold"
        }
    }

    var isMono: Bool {
        switch self {
        case .monoRegular, .monoMedium, .monoBold: true
        default: false
        }
    }

    func font(size: CGFloat) -> Font {
        BundledFonts.ensureRegistered()
        return Font.custom(postScriptName, fixedSize: size)
    }

    func nsFont(size: CGFloat) -> NSFont {
        BundledFonts.ensureRegistered()
        return NSFont(name: postScriptName, size: size) ?? .systemFont(ofSize: size)
    }

    /// The gap to add between lines so they sit `lineHeight` times the size apart: the
    /// face's own line (ascent, descent, leading) is most of it already. Never negative.
    func lineSpacing(size: CGFloat, lineHeight: Double) -> CGFloat {
        let font = nsFont(size: size)
        let natural = font.ascender - font.descender + font.leading
        return max(0, (size * lineHeight - natural).rounded(.toNearestOrEven))
    }
}

/// The type scale. Views ask for a style, never for a face or a point size.
enum TypeScale {
    private static var tokens: DesignTokens { DesignData.shared.tokens }

    /// Reading text: verifier replies, notes, the task, the verdict reason (14.5 pt).
    static var reading: CGFloat { tokens.reading.size }
    static var readingLineHeight: Double { tokens.reading.lineHeight }
    /// Secondary sentences: hints, explanations under an action.
    static let readingSmall: CGFloat = 13
    /// The quietest sentences and data: a hint under a field, a tool-call row.
    static let small: CGFloat = 12
    /// The mono cell: rows of data, ids, times, commands, code (13 pt).
    static var mono: CGFloat { tokens.cell.size }
    static var monoLineHeight: Double { tokens.cell.lineHeight }
    static let monoSmall: CGFloat = 11.5
    /// Small uppercase section labels.
    static let label: CGFloat = 10.5
    /// A glyph drawn as a mark (a track's evidence diamond, a checkbox's tick), in the
    /// system face, which has every symbol.
    static let mark: CGFloat = 10
    /// A pane's or an empty state's title, in the heading cut.
    static let title: CGFloat = 17
}

extension View {
    /// Reading text at the reading size and line height (Mona Sans).
    func readingStyle(_ face: Typeface = .readingRegular, size: CGFloat = TypeScale.reading) -> some View {
        font(face.font(size: size))
            .lineSpacing(face.lineSpacing(size: size, lineHeight: TypeScale.readingLineHeight))
    }

    /// Chrome and data (Monaspace Neon), on the mono grid's line height.
    func monoStyle(_ face: Typeface = .monoRegular, size: CGFloat = TypeScale.mono) -> some View {
        font(face.font(size: size))
            .lineSpacing(face.lineSpacing(size: size, lineHeight: TypeScale.monoLineHeight))
    }

    /// A heading in the wider cut of the reading face.
    func headingStyle(size: CGFloat = TypeScale.reading, _ face: Typeface = .headingSemiBold) -> some View {
        font(face.font(size: size))
    }
}
