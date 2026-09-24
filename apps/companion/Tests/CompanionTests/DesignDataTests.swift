import Foundation
import XCTest

/// The design data at the repo's `design/` (themes and tokens) is read by the app and, later,
/// the web dashboard. A theme that fails its contrast does not ship (ADR 0004).
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

        enum CodingKeys: String, CodingKey {
            case name, background, foreground, palette
            case cursorColor = "cursor-color"
            case cursorText = "cursor-text"
            case selectionBackground = "selection-background"
            case selectionForeground = "selection-foreground"
        }
    }

    private struct Tokens: Decodable {
        struct Cell: Decodable {
            let font: String
            let size: Double
            let lineHeight: Double
        }

        let cell: Cell
        let type: [String: String]
        let roles: [String: Int]
    }

    private static let themeFiles = [
        "greenroom-dark", "greenroom-light", "greenroom-dark-hc", "greenroom-light-hc",
    ]
    private static let requiredRoles = ["pass", "failure", "attention", "live", "driving", "dim"]
    private static let requiredFaces = [
        "chrome", "coder", "verifier", "human", "humanFallback", "machine",
    ]

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
            let colours = [
                theme.background, theme.foreground, theme.cursorColor, theme.cursorText,
                theme.selectionBackground, theme.selectionForeground,
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

    /// High contrast: WCAG AAA 7:1 for text and every role. Normal: 4.5:1 for text and
    /// roles, 3:1 for dim text (bright black).
    func testContrastMeetsThresholds() throws {
        let roles = try tokens().roles
        for file in Self.themeFiles {
            let theme = try theme(file)
            let highContrast = file.hasSuffix("-hc")
            let background = try XCTUnwrap(Self.rgb(theme.background))

            let text = Self.contrast(try XCTUnwrap(Self.rgb(theme.foreground)), background)
            XCTAssertGreaterThanOrEqual(text, highContrast ? 7 : 4.5, "\(file) foreground \(text)")

            let cursor = Self.contrast(
                try XCTUnwrap(Self.rgb(theme.cursorText)), try XCTUnwrap(Self.rgb(theme.cursorColor)))
            XCTAssertGreaterThanOrEqual(cursor, highContrast ? 7 : 4.5, "\(file) cursor \(cursor)")

            let selection = Self.contrast(
                try XCTUnwrap(Self.rgb(theme.selectionForeground)),
                try XCTUnwrap(Self.rgb(theme.selectionBackground)))
            XCTAssertGreaterThanOrEqual(
                selection, highContrast ? 7 : 4.5, "\(file) selection \(selection)")

            for (role, slot) in roles {
                let colour = try XCTUnwrap(Self.rgb(theme.palette[slot]))
                let ratio = Self.contrast(colour, background)
                let needed = highContrast ? 7 : (role == "dim" ? 3 : 4.5)
                XCTAssertGreaterThanOrEqual(ratio, needed, "\(file) \(role) (slot \(slot)) \(ratio)")
            }
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
}
