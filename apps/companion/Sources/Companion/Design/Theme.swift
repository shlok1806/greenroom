import AppKit
import SwiftUI

/// A meaning a colour carries (ADR 0004, `tokens.json` `roles`). Views colour by role,
/// never by slot or hex; each role is one ANSI slot of the theme.
enum Role: String, CaseIterable, Sendable {
    case pass, failure, attention, live, driving, dim
}

/// One of the first-party themes, named by its file in `design/themes/`.
enum ThemeID: String, CaseIterable, Sendable {
    case dark = "greenroom-dark"
    case light = "greenroom-light"
    case darkHighContrast = "greenroom-dark-hc"
    case lightHighContrast = "greenroom-light-hc"

    var isDark: Bool { self == .dark || self == .darkHighContrast }
    var isHighContrast: Bool { self == .darkHighContrast || self == .lightHighContrast }

    /// The same appearance with or without high contrast.
    func contrast(_ high: Bool) -> ThemeID {
        switch (isDark, high) {
        case (true, false): .dark
        case (true, true): .darkHighContrast
        case (false, false): .light
        case (false, true): .lightHighContrast
        }
    }
}

/// What a person chose in View > Theme, saved with `@AppStorage(ThemePreference.key)`.
/// `system` follows the Mac's appearance (ADR 0008 decision 19); every choice but an
/// explicit high-contrast one follows Increase Contrast.
enum ThemePreference: String, CaseIterable, Sendable {
    case system
    case dark
    case light
    case darkHighContrast = "dark-hc"
    case lightHighContrast = "light-hc"

    static let key = "theme"

    var title: String {
        switch self {
        case .system: "Match System"
        case .dark: "Dark"
        case .light: "Light"
        case .darkHighContrast: "Dark, High Contrast"
        case .lightHighContrast: "Light, High Contrast"
        }
    }

    /// The theme to draw, given the Mac's appearance and Increase Contrast.
    func resolve(systemIsDark: Bool, increasedContrast: Bool) -> ThemeID {
        switch self {
        case .system: (systemIsDark ? ThemeID.dark : .light).contrast(increasedContrast)
        case .dark: ThemeID.dark.contrast(increasedContrast)
        case .light: ThemeID.light.contrast(increasedContrast)
        case .darkHighContrast: .darkHighContrast
        case .lightHighContrast: .lightHighContrast
        }
    }

    /// The appearance the window takes, so menus, scrollers and text fields match the
    /// theme. Nil follows the system.
    var colorScheme: ColorScheme? {
        switch self {
        case .system: nil
        case .dark, .darkHighContrast: .dark
        case .light, .lightHighContrast: .light
        }
    }
}

/// A colour from a theme file, `#rrggbb`, in sRGB.
struct RGB: Equatable, Hashable, Sendable {
    let red: Double
    let green: Double
    let blue: Double

    init(red: Double, green: Double, blue: Double) {
        self.red = red
        self.green = green
        self.blue = blue
    }

    init?(hex: String) {
        guard hex.count == 7, hex.first == "#" else { return nil }
        let digits = hex.dropFirst()
        guard digits.allSatisfy(\.isHexDigit), let value = Int(digits, radix: 16) else { return nil }
        red = Double((value >> 16) & 0xFF) / 255
        green = Double((value >> 8) & 0xFF) / 255
        blue = Double(value & 0xFF) / 255
    }

    /// `self` moved `amount` (0 to 1) of the way to `other`, for quiet surfaces and
    /// hairlines derived from a theme's own ground and ink.
    func mixed(with other: RGB, _ amount: Double) -> RGB {
        RGB(red: red + (other.red - red) * amount,
            green: green + (other.green - green) * amount,
            blue: blue + (other.blue - blue) * amount)
    }

    /// WCAG 2 relative luminance.
    var luminance: Double {
        func channel(_ value: Double) -> Double {
            value <= 0.03928 ? value / 12.92 : pow((value + 0.055) / 1.055, 2.4)
        }
        return 0.2126 * channel(red) + 0.7152 * channel(green) + 0.0722 * channel(blue)
    }

    /// WCAG 2 contrast ratio, 1 to 21.
    func contrast(with other: RGB) -> Double {
        let (a, b) = (luminance, other.luminance)
        return (max(a, b) + 0.05) / (min(a, b) + 0.05)
    }

    var color: Color { Color(.sRGB, red: red, green: green, blue: blue, opacity: 1) }
    var nsColor: NSColor { NSColor(srgbRed: red, green: green, blue: blue, alpha: 1) }
}

/// An ANSI theme plus the brand (ADR 0004, 0008): ground, ink, 16 slots, the role
/// mapping from the tokens, and `brand`, `brandText`, `chromeTint`. Read from the
/// environment (`\.theme`).
struct Theme: Equatable, Sendable {
    let id: ThemeID
    let name: String
    let backgroundRGB: RGB
    let foregroundRGB: RGB
    /// Slots 0 to 15: 0 to 7 normal, 8 to 15 bright. Only reached through a role.
    let palette: [RGB]
    let roles: [Role: Int]
    let brandRGB: RGB
    let brandTextRGB: RGB
    let chromeTintRGB: RGB
    /// Behind the machine's screen: dark in every theme (ADR 0008 decision 19).
    let wellRGB: RGB
    /// Words inside the well (the machine starting, "no recording"): the dark theme's
    /// foreground and dim, whatever the window's theme.
    let wellInkRGB: RGB
    let wellDimRGB: RGB

    init(id: ThemeID, file: ThemeFile, roles: [Role: Int], well: String, wellInk: String, wellDim: String) throws {
        func rgb(_ key: String, _ value: String) throws -> RGB {
            guard let rgb = RGB(hex: value) else {
                throw DesignData.LoadError.badColour(file: id.rawValue, key: key, value: value)
            }
            return rgb
        }
        guard file.palette.count == 16 else {
            throw DesignData.LoadError.paletteSize(file: id.rawValue, count: file.palette.count)
        }
        self.id = id
        name = file.name
        backgroundRGB = try rgb("background", file.background)
        foregroundRGB = try rgb("foreground", file.foreground)
        palette = try file.palette.enumerated().map { try rgb("palette \($0.offset)", $0.element) }
        self.roles = roles
        brandRGB = try rgb("brand", file.brand)
        brandTextRGB = try rgb("brandText", file.brandText)
        chromeTintRGB = try rgb("chromeTint", file.chromeTint)
        wellRGB = try rgb("well", well)
        wellInkRGB = try rgb("well ink", wellInk)
        wellDimRGB = try rgb("well dim", wellDim)
    }

    // MARK: Ground and ink

    var background: Color { backgroundRGB.color }
    var foreground: Color { foregroundRGB.color }

    func rgb(_ role: Role) -> RGB { palette[roles[role] ?? 0] }
    func color(_ role: Role) -> Color { rgb(role).color }

    /// Secondary text: times, hashes, counts, labels. Never the only carrier of a state.
    var dim: Color { color(.dim) }

    // MARK: The brand

    /// Primary buttons, the selected run, the active tab, the cursor, the wordmark.
    var brand: Color { brandRGB.color }
    /// Text on `brand`.
    var brandText: Color { brandTextRGB.color }
    /// Behind the sidebar and the top bar only; content stays on `background`.
    var chromeTint: Color { chromeTintRGB.color }

    /// The brand as text on `ground` (a focused pane's label): moved toward the foreground
    /// just far enough to read, as `legible` does for a role.
    func brandInk(on ground: Ground) -> Color { legible(brandRGB, on: ground).color }

    // MARK: Derived surfaces (no hue of their own)

    /// A quiet panel: a card, a code block, a chip. Barely off the ground; a
    /// high-contrast theme draws panels by their hairline alone.
    var surface: Color { surfaceRGB.color }

    var surfaceRGB: RGB {
        id.isHighContrast ? backgroundRGB : backgroundRGB.mixed(with: foregroundRGB, id.isDark ? 0.045 : 0.035)
    }

    /// A 1 px edge at rest. In a high-contrast theme, the dim role itself.
    var hairline: Color {
        id.isHighContrast ? dim : backgroundRGB.mixed(with: foregroundRGB, id.isDark ? 0.14 : 0.16).color
    }

    /// Behind a hovered row or a pressed quiet button, under which every role stays
    /// readable.
    var highlight: Color {
        backgroundRGB.mixed(with: foregroundRGB, id.isHighContrast ? 0.12 : 0.07).color
    }

    var well: Color { wellRGB.color }
    var wellInk: Color { wellInkRGB.color }
    var wellDim: Color { wellDimRGB.color }

    // MARK: Grounds

    /// The text threshold every role and the dim text meet (ADR 0008): 4.5:1, 7:1 in a
    /// high-contrast theme.
    var textContrast: Double { id.isHighContrast ? 7 : 4.5 }

    func rgb(_ ground: Ground) -> RGB {
        switch ground {
        case .background: backgroundRGB
        case .surface: surfaceRGB
        case .chrome: chromeTintRGB
        }
    }

    /// `ink` as drawn on `ground`: unchanged when it already clears the text threshold
    /// there, else moved toward the foreground just far enough to. The themes are
    /// measured against the background (`DesignDataTests`); the tinted chrome and the
    /// quiet panels are a little lighter or darker, and secondary text must still read
    /// at 4.5:1 (7:1 in `-hc`) on them.
    func legible(_ ink: RGB, on ground: Ground) -> RGB {
        let under = rgb(ground)
        guard ink.contrast(with: under) < textContrast else { return ink }
        for step in 1...20 {
            let moved = ink.mixed(with: foregroundRGB, Double(step) * 0.05)
            if moved.contrast(with: under) >= textContrast { return moved }
        }
        return foregroundRGB
    }

    func color(_ role: Role, on ground: Ground) -> Color { legible(rgb(role), on: ground).color }

    func dim(on ground: Ground) -> Color { color(.dim, on: ground) }

    // MARK: Meanings

    /// A run's state colour (`RunFacts.Tone`): booting, inconclusive and everything else
    /// with no meaning take the foreground; quiet takes dim.
    func tone(_ tone: RunFacts.Tone, on ground: Ground = .background) -> Color {
        switch tone {
        case .pass: color(.pass, on: ground)
        case .failure: color(.failure, on: ground)
        case .attention: color(.attention, on: ground)
        case .live: color(.live, on: ground)
        case .neutral, .unsure: foreground
        case .quiet: dim(on: ground)
        }
    }

    /// A verdict's outcome: pass or failure, else the foreground.
    func outcome(_ verdict: String?, on ground: Ground = .background) -> Color {
        switch verdict {
        case "pass": color(.pass, on: ground)
        case "fail": color(.failure, on: ground)
        default: foreground
        }
    }

    /// The thin left edge of a message group: who is speaking, as a second cue beside
    /// the sender's name (ADR 0008). Only the person takes the brand.
    func edge(_ from: MessageFrom) -> Color {
        switch from {
        case .human: brand
        case .verifier: foreground
        case .coder, .system, .unknown: hairline
        }
    }
}

/// What text is drawn on: the window's ground, a quiet panel, or the tinted chrome (the
/// sidebar and the top bar). Set with `.ground(_:)`; roles read it to stay legible.
enum Ground: Sendable, CaseIterable {
    case background
    case surface
    case chrome
}

private struct GroundKey: EnvironmentKey {
    static let defaultValue = Ground.background
}

/// Spelled out rather than `@Entry`: that macro's plugin ships only with Xcode, and the
/// CI runner builds with the Command Line Tools.
private struct ThemeKey: EnvironmentKey {
    static var defaultValue: Theme { DesignData.shared.theme(.dark) }
}

extension EnvironmentValues {
    /// The theme every view draws in; `ThemedRoot` sets it from the person's choice, the
    /// appearance and Increase Contrast.
    var theme: Theme {
        get { self[ThemeKey.self] }
        set { self[ThemeKey.self] = newValue }
    }

    /// What the text here sits on (`Ground`).
    var ground: Ground {
        get { self[GroundKey.self] }
        set { self[GroundKey.self] = newValue }
    }
}
