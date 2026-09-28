import AppKit
import SwiftUI

// The design tokens of the native redesign (companion ADR 0019), copied from the Figma file's
// Tokens page (Greenroom Companion redesign, variables collection "Color", modes Light and
// Dark; text styles Display to Caption Emphasis). The Figma file is the source: change a value
// there first, then here. No view uses a colour, size, gap, radius or duration that is not
// named in this file.

/// One colour token with its Light and Dark values, as the Figma variables hold them.
enum ColorToken: String, CaseIterable, Sendable {
    case bg, bgSidebar, bgStage, bgSelected, bgHover, bgRaised
    case border
    case text, textSecondary, textTertiary
    case accent, accentSubtle
    case pass, passSubtle, fail, failSubtle, wait, waitSubtle
    case primary, onPrimary, onStatus
    case scrim, focusRing

    /// `0xRRGGBB` in sRGB, light and dark.
    var hex: (light: UInt32, dark: UInt32) {
        switch self {
        case .bg: (0xFFFFFF, 0x1B1B1E)
        case .bgSidebar: (0xF7F7F8, 0x151517)
        case .bgStage: (0xF2F2F4, 0x0F0F11)
        case .bgSelected: (0xEAEAEE, 0x2C2C31)
        case .bgHover: (0xF0F0F2, 0x242428)
        case .bgRaised: (0xFFFFFF, 0x232327)
        case .border: (0xE4E4E8, 0x2E2E33)
        case .text: (0x18181B, 0xEDEDF0)
        case .textSecondary: (0x5F5F68, 0xA0A0AA)
        case .textTertiary: (0x8E8E96, 0x6E6E78)
        case .accent: (0x2156D9, 0x6B9BF2)
        case .accentSubtle: (0xEAF1FE, 0x1D2A45)
        case .pass: (0x157034, 0x4CC47A)
        case .passSubtle: (0xEAF6EE, 0x1C3325)
        case .fail: (0xC21F1F, 0xF26B6B)
        case .failSubtle: (0xFDECEC, 0x3A1F21)
        case .wait: (0xA34B05, 0xE0A43A)
        case .waitSubtle: (0xFDF3E3, 0x3A2E17)
        case .primary: (0x18181B, 0xEDEDF0)
        case .onPrimary: (0xFFFFFF, 0x18181B)
        case .onStatus: (0xFFFFFF, 0xFFFFFF)
        case .scrim: (0x000000, 0x000000)
        case .focusRing: (0x2156D9, 0x6B9BF2)
        }
    }

    /// The value for one appearance.
    func rgb(dark: Bool) -> SRGB { SRGB(hex: dark ? hex.dark : hex.light) }

    /// A colour that follows the window's appearance.
    var nsColor: NSColor {
        let (light, dark) = (SRGB(hex: hex.light), SRGB(hex: hex.dark))
        return NSColor(name: NSColor.Name("gr.\(rawValue)")) { appearance in
            let isDark = appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
            return (isDark ? dark : light).nsColor
        }
    }

    var color: Color { Color(nsColor: nsColor) }
}

/// A colour as three sRGB channels from 0 to 1, with WCAG 2 contrast.
struct SRGB: Equatable, Sendable {
    var red: Double
    var green: Double
    var blue: Double

    init(hex: UInt32) {
        red = Double((hex >> 16) & 0xFF) / 255
        green = Double((hex >> 8) & 0xFF) / 255
        blue = Double(hex & 0xFF) / 255
    }

    init(red: Double, green: Double, blue: Double) {
        (self.red, self.green, self.blue) = (red, green, blue)
    }

    var nsColor: NSColor { NSColor(srgbRed: red, green: green, blue: blue, alpha: 1) }

    /// WCAG 2 relative luminance.
    var luminance: Double {
        func linear(_ c: Double) -> Double { c <= 0.04045 ? c / 12.92 : pow((c + 0.055) / 1.055, 2.4) }
        return 0.2126 * linear(red) + 0.7152 * linear(green) + 0.0722 * linear(blue)
    }

    /// WCAG 2 contrast ratio against another colour, 1 to 21.
    func contrast(with other: SRGB) -> Double {
        let (a, b) = (luminance, other.luminance)
        return (max(a, b) + 0.05) / (min(a, b) + 0.05)
    }

    /// This colour laid over `ground` at `opacity`.
    func over(_ ground: SRGB, opacity: Double) -> SRGB {
        SRGB(red: red * opacity + ground.red * (1 - opacity),
             green: green * opacity + ground.green * (1 - opacity),
             blue: blue * opacity + ground.blue * (1 - opacity))
    }
}

/// The palette views read: `Palette.text`, `Palette.fail`.
enum Palette {
    static let bg = ColorToken.bg.color
    static let bgSidebar = ColorToken.bgSidebar.color
    static let bgStage = ColorToken.bgStage.color
    static let bgSelected = ColorToken.bgSelected.color
    static let bgHover = ColorToken.bgHover.color
    static let bgRaised = ColorToken.bgRaised.color
    static let border = ColorToken.border.color
    static let text = ColorToken.text.color
    static let textSecondary = ColorToken.textSecondary.color
    static let textTertiary = ColorToken.textTertiary.color
    static let accent = ColorToken.accent.color
    static let accentSubtle = ColorToken.accentSubtle.color
    static let pass = ColorToken.pass.color
    static let passSubtle = ColorToken.passSubtle.color
    static let fail = ColorToken.fail.color
    static let failSubtle = ColorToken.failSubtle.color
    static let wait = ColorToken.wait.color
    static let waitSubtle = ColorToken.waitSubtle.color
    static let primary = ColorToken.primary.color
    static let onPrimary = ColorToken.onPrimary.color
    static let onStatus = ColorToken.onStatus.color
    static let scrim = ColorToken.scrim.color
    static let focusRing = ColorToken.focusRing.color
}

// MARK: - Type

/// The type scale: four sizes, three weights (Figma text styles). The app draws in the system
/// face, SF Pro, at the sizes and weights the file names; the file draws in Inter only because
/// Figma's renderer does not carry SF Pro. SF Pro tracks itself by size, so no tracking is added.
enum TypeStyle: String, CaseIterable, Sendable {
    /// 22 / 28 semibold: the status word, once per screen.
    case display
    /// 15 / 20 semibold: the tally, panel and sheet titles, the evidence caption.
    case title
    /// 13 / 18 regular: everything else.
    case body
    /// 13 / 18 medium: the selected row, the run name, buttons.
    case bodyEmphasis
    /// 11 / 14 regular: meta, times, key frame captions.
    case caption
    /// 11 / 14 semibold: group headings, in sentence case.
    case captionEmphasis

    var size: CGFloat {
        switch self {
        case .display: 22
        case .title: 15
        case .body, .bodyEmphasis: 13
        case .caption, .captionEmphasis: 11
        }
    }

    var lineHeight: CGFloat {
        switch self {
        case .display: 28
        case .title: 20
        case .body, .bodyEmphasis: 18
        case .caption, .captionEmphasis: 14
        }
    }

    var weight: Font.Weight {
        switch self {
        case .display, .title, .captionEmphasis: .semibold
        case .bodyEmphasis: .medium
        case .body, .caption: .regular
        }
    }

    /// Tracking in points: the Figma text style's letter spacing (a percentage of the size).
    /// Display -1.8%, Title -1%, Body -0.5%, Caption 0.
    var tracking: CGFloat {
        switch self {
        case .display: -0.018 * size
        case .title: -0.01 * size
        case .body, .bodyEmphasis: -0.005 * size
        case .caption, .captionEmphasis: 0
        }
    }

    var font: Font { .system(size: size, weight: weight) }

    var nsFont: NSFont {
        let weight: NSFont.Weight = switch self.weight {
        case .semibold: .semibold
        case .medium: .medium
        default: .regular
        }
        return NSFont.systemFont(ofSize: size, weight: weight)
    }

    /// The face's own line: ascender, descender and leading.
    var naturalLineHeight: CGFloat {
        let font = nsFont
        return font.ascender - font.descender + font.leading
    }

    /// The gap between lines that makes each line `lineHeight` tall.
    var lineSpacing: CGFloat { max(0, lineHeight - naturalLineHeight) }

    /// Half the leading, above the first line and below the last, as a CSS or Figma line box
    /// splits it: a line of text is then exactly `lineHeight` tall.
    var halfLeading: CGFloat { max(0, (lineHeight - naturalLineHeight) / 2) }
}

extension View {
    /// Sets a type style: its face, size, weight and tracking, a line box `lineHeight` tall
    /// per line (docs/22 section 3.5), and tabular figures so times and counts never shift.
    func textStyle(_ style: TypeStyle) -> some View {
        LineBox(style: style) {
            font(style.font)
                .tracking(style.tracking)
                .lineSpacing(style.lineSpacing)
                .monospacedDigit()
        }
    }
}

/// Makes text exactly as tall as the design's line boxes: `lineHeight` per line, the glyphs
/// centred in each (CSS and Figma split the leading half above and half below). A padding of
/// half the leading would land on fractions of a point, which layout rounds; this works out the
/// lines from the text's own height and gives the whole box.
struct LineBox: Layout {
    var style: TypeStyle

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        guard let text = subviews.first else { return .zero }
        let size = text.sizeThatFits(proposal)
        return CGSize(width: size.width, height: height(for: size.height))
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        guard let text = subviews.first else { return }
        let size = text.sizeThatFits(ProposedViewSize(width: bounds.width, height: nil))
        text.place(at: CGPoint(x: bounds.minX, y: bounds.minY + (height(for: size.height) - size.height) / 2),
                   proposal: ProposedViewSize(width: bounds.width, height: size.height))
    }

    /// The box for text this tall: whole lines of `lineHeight`.
    func height(for measured: CGFloat) -> CGFloat {
        guard measured > 0 else { return 0 }
        let natural = style.naturalLineHeight + style.lineSpacing
        let lines = max(1, ((measured + style.lineSpacing) / natural).rounded())
        return lines * style.lineHeight
    }
}

// MARK: - Space, radius, elevation, motion, layout

/// The spacing scale: 8 pt, with 4 for hairline gaps.
enum Gap {
    static let x4: CGFloat = 4
    static let x8: CGFloat = 8
    static let x12: CGFloat = 12
    static let x16: CGFloat = 16
    static let x24: CGFloat = 24
    static let x32: CGFloat = 32
    static let x48: CGFloat = 48
    /// Every step, for tests.
    static let scale: [CGFloat] = [x4, x8, x12, x16, x24, x32, x48]
}

/// Corner radii.
enum Corner {
    /// Keycaps, filmstrip thumbs.
    static let keycap: CGFloat = 4
    /// Buttons, run rows, marks.
    static let control: CGFloat = 6
    /// Check rows, frames, inputs.
    static let row: CGFloat = 8
    /// The window.
    static let window: CGFloat = 10
    /// The palette and sheets.
    static let sheet: CGFloat = 12
}

/// Elevation: everything in the window is flat, split by 1 px borders; only the palette, a
/// sheet and the restart progress are raised.
enum Elevation {
    static let raisedRadius: CGFloat = 64
    static let raisedY: CGFloat = 24
    static let raisedOpacity: Double = 0.22
}

/// The window's layout (Figma Tokens, Layout). Three window classes: compact (1024 x 680),
/// regular (1280 x 800) and wide (1600 x 1000).
enum WindowClass: String, CaseIterable, Sendable {
    case compact, regular, wide

    /// The class for a window this wide.
    static func of(width: CGFloat) -> WindowClass {
        if width < 1180 { return .compact }
        if width >= 1500 { return .wide }
        return .regular
    }

    var sidebar: CGFloat {
        switch self {
        case .compact: 208
        case .regular: 248
        case .wide: 272
        }
    }

    var checks: CGFloat {
        switch self {
        case .compact: 320
        case .regular: 400
        case .wide: 440
        }
    }

    /// How many Done runs the sidebar shows before "Show N more": three in a compact window,
    /// five otherwise (Figma 03 compact and regular).
    var doneShown: Int { self == .compact ? 3 : 5 }

    /// The stage's padding: horizontal, top. The same in every class (Figma 03 regular and
    /// compact both put the evidence 32 in from the stage's sides).
    var stagePadding: (horizontal: CGFloat, top: CGFloat) { (Gap.x32, Gap.x24) }
}

enum Metrics {
    static let toolbarHeight: CGFloat = 52
    static let headerHeight: CGFloat = 80
    static let runRowHeight: CGFloat = 32
    static let checkRowMinHeight: CGFloat = 40
    static let buttonHeight: CGFloat = 28
    static let glyph: CGFloat = 16
    static let headerGlyph: CGFloat = 20
    static let footerHeight: CGFloat = 44
    static let paletteWidth: CGFloat = 560
    static let paletteRowHeight: CGFloat = 36
    static let focusRingWidth: CGFloat = 2
    static let focusRingGap: CGFloat = 2
    /// Filmstrip thumbs, 4:3.
    static let thumbWidth: CGFloat = 68
    static let thumbHeight: CGFloat = 51
    static let windowMinimum = CGSize(width: 960, height: 620)
    static let windowDefault = CGSize(width: 1280, height: 800)
}
