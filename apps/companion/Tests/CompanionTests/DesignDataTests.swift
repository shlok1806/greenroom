import Foundation
import XCTest

/// The design data at the repo's `design/` (themes and tokens) is read by the app and, later,
/// the web dashboard. A theme that fails its contrast does not ship (ADR 0004, ADR 0008).
final class DesignDataTests: XCTestCase {
    private struct Theme: Decodable {
        let name: String
        let background: String
        let foreground: String
        let cursorColor: String
        let cursorText: String
        let selectionBackground: String
        let selectionForeground: String
        let palette: [String]
        let brand: String
        let brandText: String
        let chromeTint: String

        enum CodingKeys: String, CodingKey {
            case name, background, foreground, palette
            case cursorColor = "cursor-color"
            case cursorText = "cursor-text"
            case selectionBackground = "selection-background"
            case selectionForeground = "selection-foreground"
            case brand = "greenroom-brand"
            case brandText = "greenroom-brand-text"
            case chromeTint = "greenroom-chrome-tint"
        }
    }

    private struct Tokens: Decodable {
        struct Cell: Decodable {
            let font: String
            let size: Double
            let lineHeight: Double
        }

        struct Reading: Decodable {
            let size: Double
            let lineHeight: Double
        }

        struct Spacing: Decodable {
            let unit: Double
            let scale: [Double]
        }

        struct Radii: Decodable {
            let sm: Double
            let md: Double
            let lg: Double
        }

        let cell: Cell
        let type: [String: String]
        let reading: Reading
        let roles: [String: Int]
        let themeKeys: [String: String]
        let spacing: Spacing
        let radii: Radii
    }

    private static let themeFiles = [
        "greenroom-dark", "greenroom-light", "greenroom-dark-hc", "greenroom-light-hc",
    ]
    private static let requiredRoles = ["pass", "failure", "attention", "live", "driving", "dim"]
    private static let requiredFaces = ["mono", "reading", "readingHeading"]
    private static let requiredThemeKeys = ["brand", "brandText", "chromeTint"]
    private static let minHueDistance = 25.0

    /// `apps/companion/Tests/CompanionTests/<this file>` -> repo root -> `design/`.
    private var designDirectory: URL {
        URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()  // CompanionTests
            .deletingLastPathComponent()  // Tests
            .deletingLastPathComponent()  // companion
            .deletingLastPathComponent()  // apps
            .deletingLastPathComponent()  // repo root
            .appendingPathComponent("design")
    }

    private func load<T: Decodable>(_ type: T.Type, _ path: String) throws -> T {
        let data = try Data(contentsOf: designDirectory.appendingPathComponent(path))
        return try JSONDecoder().decode(T.self, from: data)
    }

    private func tokens() throws -> Tokens { try load(Tokens.self, "tokens.json") }

    private func theme(_ file: String) throws -> Theme {
        try load(Theme.self, "themes/\(file).json")
    }

    func testThemesHaveEveryKeyAndSixteenValidColours() throws {
        for file in Self.themeFiles {
            let theme = try theme(file)
            XCTAssertFalse(theme.name.isEmpty, file)
            XCTAssertEqual(theme.palette.count, 16, "\(file) palette")
            let colours =
                [
                    theme.background, theme.foreground, theme.cursorColor, theme.cursorText,
                    theme.selectionBackground, theme.selectionForeground,
                    theme.brand, theme.brandText, theme.chromeTint,
                ] + theme.palette
            for colour in colours {
                XCTAssertNotNil(Self.rgb(colour), "\(file): \(colour) is not #rrggbb")
            }
        }
    }

    func testTokensNameEveryRoleAndFace() throws {
        let tokens = try tokens()
        XCTAssertGreaterThan(tokens.cell.size, 0)
        XCTAssertGreaterThanOrEqual(tokens.cell.lineHeight, 1)
        XCTAssertFalse(tokens.cell.font.isEmpty)
        for role in Self.requiredRoles {
            let slot = try XCTUnwrap(tokens.roles[role], "role \(role) missing")
            XCTAssertTrue((0...15).contains(slot), "role \(role) slot \(slot)")
        }
        for (role, slot) in tokens.roles {
            XCTAssertTrue((0...15).contains(slot), "role \(role) slot \(slot)")
        }
        for face in Self.requiredFaces {
            XCTAssertFalse(tokens.type[face, default: ""].isEmpty, "face \(face) missing")
        }
    }

    func testTokensDeclareReadingSpacingRadiiAndThemeKeys() throws {
        let tokens = try tokens()

        XCTAssertGreaterThanOrEqual(tokens.reading.size, 14)
        XCTAssertLessThanOrEqual(tokens.reading.size, 15)
        XCTAssertGreaterThanOrEqual(tokens.reading.lineHeight, 1.4)

        for key in Self.requiredThemeKeys {
            XCTAssertFalse(tokens.themeKeys[key, default: ""].isEmpty, "themeKeys \(key) missing")
        }
        XCTAssertEqual(tokens.themeKeys["brand"], "greenroom-brand")
        XCTAssertEqual(tokens.themeKeys["brandText"], "greenroom-brand-text")
        XCTAssertEqual(tokens.themeKeys["chromeTint"], "greenroom-chrome-tint")

        XCTAssertEqual(tokens.spacing.scale, tokens.spacing.scale.sorted())
        XCTAssertGreaterThan(tokens.spacing.unit, 0)
        XCTAssertLessThan(tokens.radii.sm, tokens.radii.md)
        XCTAssertLessThan(tokens.radii.md, tokens.radii.lg)
    }

    /// High contrast: WCAG AAA 7:1 for text and every role, including dim. Normal: 4.5:1
    /// for the foreground and every role, including dim (ADR 0008 raised dim from 3:1).
    func testContrastMeetsThresholds() throws {
        let roles = try tokens().roles
        for file in Self.themeFiles {
            let theme = try theme(file)
            let highContrast = file.hasSuffix("-hc")
            let needed = highContrast ? 7.0 : 4.5
            let background = try XCTUnwrap(Self.rgb(theme.background))

            let text = Self.contrast(try XCTUnwrap(Self.rgb(theme.foreground)), background)
            XCTAssertGreaterThanOrEqual(text, needed, "\(file) foreground \(text)")

            let cursor = Self.contrast(
                try XCTUnwrap(Self.rgb(theme.cursorText)), try XCTUnwrap(Self.rgb(theme.cursorColor)))
            XCTAssertGreaterThanOrEqual(cursor, needed, "\(file) cursor \(cursor)")

            let selection = Self.contrast(
                try XCTUnwrap(Self.rgb(theme.selectionForeground)),
                try XCTUnwrap(Self.rgb(theme.selectionBackground)))
            XCTAssertGreaterThanOrEqual(selection, needed, "\(file) selection \(selection)")

            for (role, slot) in roles {
                guard theme.palette.indices.contains(slot) else {
                    XCTFail("\(file) \(role) slot \(slot) outside \(theme.palette.count) colours")
                    continue
                }
                let colour = try XCTUnwrap(Self.rgb(theme.palette[slot]))
                let ratio = Self.contrast(colour, background)
                XCTAssertGreaterThanOrEqual(ratio, needed, "\(file) \(role) (slot \(slot)) \(ratio)")
            }
        }
    }

    /// The brand is used on large, calm surfaces and actions, not small marks, so it
    /// only needs the large-surface (non-text) minimum, in every theme alike.
    func testBrandMeetsLargeSurfaceContrastOnBackground() throws {
        for file in Self.themeFiles {
            let theme = try theme(file)
            let background = try XCTUnwrap(Self.rgb(theme.background))
            let brand = try XCTUnwrap(Self.rgb(theme.brand))
            let ratio = Self.contrast(brand, background)
            XCTAssertGreaterThanOrEqual(ratio, 3.0, "\(file) brand-on-background \(ratio)")
        }
    }

    /// `brandText` is the button-label colour drawn on top of `brand`; it needs the same
    /// text threshold as everything else, scaled by the theme's contrast level.
    func testBrandTextMeetsTextContrastOnBrand() throws {
        for file in Self.themeFiles {
            let theme = try theme(file)
            let highContrast = file.hasSuffix("-hc")
            let brand = try XCTUnwrap(Self.rgb(theme.brand))
            let brandText = try XCTUnwrap(Self.rgb(theme.brandText))
            let ratio = Self.contrast(brandText, brand)
            XCTAssertGreaterThanOrEqual(
                ratio, highContrast ? 7 : 4.5, "\(file) brand-text-on-brand \(ratio)")
        }
    }

    /// `chromeTint` sits behind ordinary chrome text (the sidebar, the top bar), so the
    /// theme's foreground must still clear the normal text threshold on top of it.
    func testForegroundStaysReadableOnChromeTint() throws {
        for file in Self.themeFiles {
            let theme = try theme(file)
            let highContrast = file.hasSuffix("-hc")
            let foreground = try XCTUnwrap(Self.rgb(theme.foreground))
            let tint = try XCTUnwrap(Self.rgb(theme.chromeTint))
            let ratio = Self.contrast(foreground, tint)
            XCTAssertGreaterThanOrEqual(
                ratio, highContrast ? 7 : 4.5, "\(file) foreground-on-chromeTint \(ratio)")
        }
    }

    /// Olive brand and pass emerald must read as different hues, not by eye (ADR 0008).
    func testBrandAndPassAreDistinguishableHues() throws {
        let roles = try tokens().roles
        let passSlot = try XCTUnwrap(roles["pass"])
        for file in Self.themeFiles {
            let theme = try theme(file)
            guard theme.palette.indices.contains(passSlot) else {
                XCTFail("\(file) pass slot \(passSlot) outside \(theme.palette.count) colours")
                continue
            }
            let brand = try XCTUnwrap(Self.rgb(theme.brand))
            let pass = try XCTUnwrap(Self.rgb(theme.palette[passSlot]))
            let distance = Self.hueDistance(Self.hue(brand), Self.hue(pass))
            XCTAssertGreaterThanOrEqual(
                distance, Self.minHueDistance, "\(file) brand/pass hue distance \(distance)")
        }
    }

    func testContrastFormulaMatchesKnownValues() throws {
        let black = try XCTUnwrap(Self.rgb("#000000"))
        let white = try XCTUnwrap(Self.rgb("#ffffff"))
        XCTAssertEqual(Self.contrast(black, white), 21, accuracy: 0.001)
        XCTAssertEqual(Self.contrast(white, white), 1, accuracy: 0.001)
        // #767676 on white is the classic 4.54:1.
        XCTAssertEqual(Self.contrast(try XCTUnwrap(Self.rgb("#767676")), white), 4.54, accuracy: 0.01)
        XCTAssertNil(Self.rgb("#12345"))
        XCTAssertNil(Self.rgb("123456"))
        XCTAssertNil(Self.rgb("#12345g"))
    }

    func testHueFormulaMatchesKnownValues() throws {
        XCTAssertEqual(Self.hue(try XCTUnwrap(Self.rgb("#ff0000"))), 0, accuracy: 0.01)
        XCTAssertEqual(Self.hue(try XCTUnwrap(Self.rgb("#00ff00"))), 120, accuracy: 0.01)
        XCTAssertEqual(Self.hue(try XCTUnwrap(Self.rgb("#0000ff"))), 240, accuracy: 0.01)
        XCTAssertEqual(Self.hue(try XCTUnwrap(Self.rgb("#808080"))), 0, accuracy: 0.01)
        XCTAssertEqual(Self.hueDistance(10, 350), 20, accuracy: 0.01)
        XCTAssertEqual(Self.hueDistance(0, 180), 180, accuracy: 0.01)
    }

    // MARK: - WCAG 2 relative luminance

    private static func rgb(_ hex: String) -> [Double]? {
        guard hex.count == 7, hex.first == "#" else { return nil }
        let digits = hex.dropFirst()
        guard digits.allSatisfy(\.isHexDigit), let value = Int(digits, radix: 16) else {
            return nil
        }
        return [16, 8, 0].map { Double((value >> $0) & 0xFF) / 255 }
    }

    private static func luminance(_ rgb: [Double]) -> Double {
        let linear = rgb.map { $0 <= 0.04045 ? $0 / 12.92 : pow(($0 + 0.055) / 1.055, 2.4) }
        return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2]
    }

    private static func contrast(_ a: [Double], _ b: [Double]) -> Double {
        let (la, lb) = (luminance(a), luminance(b))
        return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
    }

    // MARK: - HSL hue

    /// The hue angle (0 to 360) of an `[r, g, b]` triple in 0...1. Achromatic colours
    /// (grey, black, white) return 0.
    private static func hue(_ rgb: [Double]) -> Double {
        let (r, g, b) = (rgb[0], rgb[1], rgb[2])
        let maxV = max(r, g, b)
        let minV = min(r, g, b)
        let delta = maxV - minV
        guard delta > 0 else { return 0 }

        var h: Double
        if maxV == r {
            h = 60 * (((g - b) / delta).truncatingRemainder(dividingBy: 6))
        } else if maxV == g {
            h = 60 * (((b - r) / delta) + 2)
        } else {
            h = 60 * (((r - g) / delta) + 4)
        }
        if h < 0 { h += 360 }
        return h
    }

    /// The shorter angular distance between two hues, 0 to 180 degrees.
    private static func hueDistance(_ a: Double, _ b: Double) -> Double {
        let d = abs(a - b).truncatingRemainder(dividingBy: 360)
        return min(d, 360 - d)
    }
}
