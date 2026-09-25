import SwiftUI

// PROTOTYPE: the palette IS an ANSI theme (decision 6).

/// What a colour means. Each maps to exactly one ANSI slot, except `pass`, which is the
/// dedicated bluer, brighter emerald token (revision 21-22): distinct from ANSI green so
/// it never gets mistaken for `.live`'s cyan or the brand olive.
enum Role: Sendable, Hashable {
    case pass, failure, needsYou, live, driving, heading, border
}

enum ThemeID: String, CaseIterable, Sendable {
    case dark, light, darkHC = "dark-hc", lightHC = "light-hc"

    var title: String { rawValue }
    var isDark: Bool { self == .dark || self == .darkHC }
}

struct ThemePalette: Sendable, Equatable {
    var id: ThemeID
    var background: Color
    var foreground: Color
    var cursor: Color
    var cursorText: Color
    var selection: Color
    /// Brand olive (revision 21-22): large calm surfaces and actions only - primary
    /// buttons, selected run, active tab, the block cursor, the wordmark `greenroom█`.
    var brand: Color
    /// The label colour on a brand-filled surface (a button's text).
    var brandText: Color
    /// A very faint olive wash on the sidebar and top bar backgrounds only. Content
    /// panes (screen, steps, transcript) never take this tint, so screenshots read true.
    var chromeTint: Color
    /// The dedicated pass colour: a bluer, brighter emerald, visibly different from the
    /// brand olive and from any ANSI green.
    var passEmerald: Color
    /// A quiet panel surface: neutral, just barely distinct from the window background.
    var panel: Color
    /// The machine screen well: always dark, reads like a monitor, in both themes.
    var screenWell: Color
    /// The 16 ANSI colours: 0-7 normal, 8-15 bright.
    var ansi: [Color]

    func slot(_ i: Int) -> Color { ansi[max(0, min(15, i))] }

    func color(_ ink: Ink) -> Color {
        switch ink {
        case .fg: foreground
        case .dim: slot(8)
        case .bg: background
        case .cursor: cursor
        case .cursorText: cursorText
        case .selection: selection
        case .brand: brand
        case .brandText: brandText
        case .chromeTint: chromeTint
        case .panel: panel
        case .screenWell: screenWell
        case .alpha(let inner, let a): color(inner).opacity(a)
        case .slot(let i): slot(i)
        case .role(let role): color(role)
        }
    }

    /// Semantic roles: pass is the dedicated emerald (never ANSI green), failure red,
    /// needs you yellow, live cyan, driving magenta, headings blue, borders bright black.
    func color(_ role: Role) -> Color {
        switch role {
        case .pass: passEmerald
        case .failure: slot(1)
        case .needsYou: slot(3)
        case .live: slot(6)
        case .driving: slot(5)
        case .heading: slot(4)
        case .border: slot(8)
        }
    }

    static func named(_ id: ThemeID) -> ThemePalette {
        switch id {
        case .dark: .dark
        case .light: .light
        case .darkHC: .darkHC
        case .lightHC: .lightHC
        }
    }

    static let dark = ThemePalette(
        id: .dark,
        background: hex(0x111214), foreground: hex(0xD8D5CC), cursor: hex(0xE9E5DA), cursorText: hex(0x111214),
        selection: hex(0x24262B),
        // Olive lifted so it reads on near-black; dark label text keeps it legible as a button fill.
        brand: hex(0x8A9A4E), brandText: hex(0x111214), chromeTint: hex(0x8A9A4E).opacity(0.07),
        passEmerald: hex(0x33D6A6), panel: hex(0x17181B), screenWell: hex(0x050506),
        ansi: [
            hex(0x1C1E22), hex(0xE2717A), hex(0x9CC47F), hex(0xE3BF72),
            hex(0x6FA8E6), hex(0xC792DB), hex(0x5FBDC4), hex(0xB4B1A8),
            hex(0x82838C), hex(0xF08F96), hex(0xB6DB9A), hex(0xF0D394),
            hex(0x93C0F0), hex(0xDAAEE8), hex(0x86D3D8), hex(0xF2EFE6),
        ]
    )

    static let light = ThemePalette(
        id: .light,
        background: hex(0xF5F2EA), foreground: hex(0x2B2A27), cursor: hex(0x2B2A27), cursorText: hex(0xF5F2EA),
        selection: hex(0xE4DFD2),
        brand: hex(0x4B5320), brandText: hex(0xF5F2EA), chromeTint: hex(0x4B5320).opacity(0.06),
        passEmerald: hex(0x0C7A61), panel: hex(0xFFFFFF), screenWell: hex(0x131418),
        ansi: [
            hex(0x2B2A27), hex(0xC0363F), hex(0x3F7F2A), hex(0x94640A),
            hex(0x2F66B3), hex(0x9A3BA6), hex(0x16808A), hex(0xD9D4C7),
            hex(0x6B6560), hex(0xD4474F), hex(0x4F9437), hex(0xA9761A),
            hex(0x3D78C8), hex(0xAD4DB8), hex(0x22939C), hex(0xFFFFFF),
        ]
    )

    static let darkHC = ThemePalette(
        id: .darkHC,
        background: hex(0x000000), foreground: hex(0xFFFFFF), cursor: hex(0xFFFFFF), cursorText: hex(0x000000),
        selection: hex(0x2E3440),
        brand: hex(0xA8BC5E), brandText: hex(0x000000), chromeTint: hex(0xA8BC5E).opacity(0.10),
        passEmerald: hex(0x3FF0BC), panel: hex(0x000000), screenWell: hex(0x000000),
        ansi: [
            hex(0x000000), hex(0xFF5F5F), hex(0x5FFF87), hex(0xFFE14D),
            hex(0x7AB8FF), hex(0xFF7AFF), hex(0x3DF0FF), hex(0xE0E0E0),
            hex(0xA8A8A8), hex(0xFF8787), hex(0x87FFA8), hex(0xFFEC85),
            hex(0xA3CFFF), hex(0xFFA3FF), hex(0x85F7FF), hex(0xFFFFFF),
        ]
    )

    static let lightHC = ThemePalette(
        id: .lightHC,
        background: hex(0xFFFFFF), foreground: hex(0x000000), cursor: hex(0x000000), cursorText: hex(0xFFFFFF),
        selection: hex(0xD6DCF0),
        // Deeper olive than the light theme, so a white button label still clears 4.5:1.
        brand: hex(0x3A4118), brandText: hex(0xFFFFFF), chromeTint: hex(0x3A4118).opacity(0.08),
        passEmerald: hex(0x004D3D), panel: hex(0xFFFFFF), screenWell: hex(0x0A0A0D),
        ansi: [
            hex(0x000000), hex(0xA80000), hex(0x006400), hex(0x6E4A00),
            hex(0x0033AA), hex(0x7E007E), hex(0x005A66), hex(0xC8C8C8),
            hex(0x505050), hex(0xC00000), hex(0x007A00), hex(0x805800),
            hex(0x0040C8), hex(0x960096), hex(0x006E7A), hex(0xFFFFFF),
        ]
    )
}

func hex(_ v: UInt32) -> Color {
    Color(.sRGB,
          red: Double((v >> 16) & 0xFF) / 255,
          green: Double((v >> 8) & 0xFF) / 255,
          blue: Double(v & 0xFF) / 255,
          opacity: 1)
}
