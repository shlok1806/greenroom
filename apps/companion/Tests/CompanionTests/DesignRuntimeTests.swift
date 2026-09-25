import AppKit
import SwiftUI
import XCTest

@testable import Companion

/// The design data as the app reads it at runtime (ADR 0008): the bundled copy of
/// `design/`, the themes and their roles, the choice of theme, the bundled faces and the
/// type scale. `DesignDataTests` gates the JSON itself.
final class DesignRuntimeTests: XCTestCase {
    /// `apps/companion/Tests/CompanionTests/<this file>` -> repo root -> `design/`.
    private var repoDesign: URL {
        URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .appendingPathComponent("design")
    }

    private func jsonFiles(under directory: URL) throws -> [String] {
        let tokens = ["tokens.json"]
        let themes = try FileManager.default.contentsOfDirectory(atPath: directory.appendingPathComponent("themes").path)
            .filter { $0.hasSuffix(".json") }
            .map { "themes/\($0)" }
        return (tokens + themes).sorted()
    }

    // MARK: The bundled copy

    /// The app ships a copy of `design/` (SwiftPM bundles only files inside a target).
    /// Run `scripts/sync-design.sh` after changing `design/`.
    func testTheBundledDesignDataIsTheRepositorysDesignData() throws {
        let bundled = AppResources.designDirectory
        let repo = try jsonFiles(under: repoDesign)
        XCTAssertEqual(try jsonFiles(under: bundled), repo, "run scripts/sync-design.sh")
        for file in repo {
            let a = try Data(contentsOf: repoDesign.appendingPathComponent(file))
            let b = try Data(contentsOf: bundled.appendingPathComponent(file))
            XCTAssertEqual(a, b, "\(file) differs from design/; run scripts/sync-design.sh")
        }
    }

    // MARK: Themes

    func testEveryThemeLoadsWithItsRolesOnTheTokensSlots() throws {
        let data = try DesignData.load(from: repoDesign)
        let tokens = data.tokens
        for id in ThemeID.allCases {
            let theme = data.theme(id)
            let raw = try JSONSerialization.jsonObject(with: Data(contentsOf: repoDesign.appendingPathComponent("themes/\(id.rawValue).json"))) as! [String: Any]
            let palette = raw["palette"] as! [String]
            XCTAssertEqual(theme.palette.count, 16, id.rawValue)
            for role in Role.allCases {
                let slot = try XCTUnwrap(tokens.roles[role.rawValue], role.rawValue)
                XCTAssertEqual(theme.rgb(role), RGB(hex: palette[slot]), "\(id.rawValue) \(role)")
            }
            XCTAssertEqual(theme.backgroundRGB, RGB(hex: raw["background"] as! String))
            XCTAssertEqual(theme.foregroundRGB, RGB(hex: raw["foreground"] as! String))
            XCTAssertEqual(theme.brandRGB, RGB(hex: raw[tokens.themeKeys.brand] as! String), id.rawValue)
            XCTAssertEqual(theme.brandTextRGB, RGB(hex: raw[tokens.themeKeys.brandText] as! String), id.rawValue)
            XCTAssertEqual(theme.chromeTintRGB, RGB(hex: raw[tokens.themeKeys.chromeTint] as! String), id.rawValue)
        }
    }

    /// The machine's screen is a picture of a monitor: dark in every theme.
    func testTheScreenWellIsDarkInEveryTheme() throws {
        let data = try DesignData.load(from: repoDesign)
        for id in ThemeID.allCases {
            let theme = data.theme(id)
            XCTAssertLessThan(theme.wellRGB.luminance, 0.02, id.rawValue)
            XCTAssertGreaterThanOrEqual(theme.wellInkRGB.contrast(with: theme.wellRGB), theme.textContrast, id.rawValue)
            XCTAssertGreaterThanOrEqual(theme.wellDimRGB.contrast(with: theme.wellRGB), theme.textContrast, id.rawValue)
        }
    }

    /// Secondary text and every role read at 4.5:1 (7:1 in `-hc`) on every ground a view
    /// puts them on: the window, a quiet panel, the tinted chrome.
    func testEveryRoleIsLegibleOnEveryGround() throws {
        let data = try DesignData.load(from: repoDesign)
        for id in ThemeID.allCases {
            let theme = data.theme(id)
            for role in Role.allCases {
                // Measured against the background by `DesignDataTests`: never moved there.
                XCTAssertEqual(theme.legible(theme.rgb(role), on: .background), theme.rgb(role), "\(id.rawValue) \(role)")
                for ground in Ground.allCases {
                    let ink = theme.legible(theme.rgb(role), on: ground)
                    XCTAssertGreaterThanOrEqual(ink.contrast(with: theme.rgb(ground)), theme.textContrast,
                                                "\(id.rawValue) \(role) on \(ground)")
                }
            }
            XCTAssertGreaterThanOrEqual(theme.brandTextRGB.contrast(with: theme.brandRGB), theme.textContrast, id.rawValue)
        }
    }

    func testAThemeWithoutTheBrandKeysIsRefusedByName() throws {
        let scratch = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: scratch.appendingPathComponent("themes"), withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: scratch) }
        try FileManager.default.copyItem(at: repoDesign.appendingPathComponent("tokens.json"), to: scratch.appendingPathComponent("tokens.json"))
        for id in ThemeID.allCases {
            let source = repoDesign.appendingPathComponent("themes/\(id.rawValue).json")
            var raw = try JSONSerialization.jsonObject(with: Data(contentsOf: source)) as! [String: Any]
            if id == .light { raw["greenroom-brand"] = nil }
            try JSONSerialization.data(withJSONObject: raw).write(to: scratch.appendingPathComponent("themes/\(id.rawValue).json"))
        }
        XCTAssertThrowsError(try DesignData.load(from: scratch)) { error in
            XCTAssertEqual(error as? DesignData.LoadError, .themeKey(file: "greenroom-light", key: "greenroom-brand"))
        }
    }

    // MARK: Which theme

    func testTheThemeFollowsTheAppearanceAndIncreaseContrast() {
        let system = ThemePreference.system
        XCTAssertEqual(system.resolve(systemIsDark: true, increasedContrast: false), .dark)
        XCTAssertEqual(system.resolve(systemIsDark: false, increasedContrast: false), .light)
        XCTAssertEqual(system.resolve(systemIsDark: true, increasedContrast: true), .darkHighContrast)
        XCTAssertEqual(system.resolve(systemIsDark: false, increasedContrast: true), .lightHighContrast)
    }

    func testAChosenThemeHoldsItsAppearanceButStillFollowsIncreaseContrast() {
        XCTAssertEqual(ThemePreference.dark.resolve(systemIsDark: false, increasedContrast: false), .dark)
        XCTAssertEqual(ThemePreference.dark.resolve(systemIsDark: false, increasedContrast: true), .darkHighContrast)
        XCTAssertEqual(ThemePreference.light.resolve(systemIsDark: true, increasedContrast: false), .light)
        XCTAssertEqual(ThemePreference.light.resolve(systemIsDark: true, increasedContrast: true), .lightHighContrast)
        XCTAssertEqual(ThemePreference.darkHighContrast.resolve(systemIsDark: false, increasedContrast: false), .darkHighContrast)
        XCTAssertEqual(ThemePreference.lightHighContrast.resolve(systemIsDark: true, increasedContrast: false), .lightHighContrast)
        XCTAssertNil(ThemePreference.system.colorScheme)
        XCTAssertEqual(ThemePreference.darkHighContrast.colorScheme, .dark)
        XCTAssertEqual(ThemePreference.light.colorScheme, .light)
    }

    // MARK: Faces and the type scale

    /// Every face the views name is the bundled one, not a fallback.
    func testEveryBundledFaceRegistersByItsPostScriptName() {
        for face in Typeface.allCases {
            XCTAssertTrue(BundledFonts.resolves(face.postScriptName), face.postScriptName)
        }
        XCTAssertEqual(Typeface.readingRegular.postScriptName, "MonaSans-Regular")
        XCTAssertEqual(Typeface.headingSemiBold.postScriptName, "MonaSansExpanded-SemiBold")
        XCTAssertEqual(Typeface.monoRegular.postScriptName, "MonaspaceNeon-Regular")
    }

    func testTheFontsShipWithTheirLicences() throws {
        let files = try FileManager.default.contentsOfDirectory(atPath: AppResources.fontsDirectory.path)
        XCTAssertTrue(files.contains("OFL-LICENSE-MonaSans.txt"))
        XCTAssertTrue(files.contains("OFL-LICENSE-Monaspace.txt"))
    }

    /// Prose sits about 1.45 times its size apart (fix from the prototype review: the
    /// prototype added half the size on top of the face's own line, near 1.9).
    func testReadingTextIsSetAtItsLineHeightNotLooser() {
        let size = TypeScale.reading
        let font = Typeface.readingRegular.nsFont(size: size)
        let natural = font.ascender - font.descender + font.leading
        let line = natural + Typeface.readingRegular.lineSpacing(size: size, lineHeight: TypeScale.readingLineHeight)
        XCTAssertEqual(line / size, TypeScale.readingLineHeight, accuracy: 0.05)
        XCTAssertEqual(size, 14.5)
    }

    func testSpacingAndRadiiComeFromTheTokens() {
        let tokens = DesignData.shared.tokens
        XCTAssertEqual([Space.xs, Space.s, Space.m, Space.l, Space.xl, Space.xxl, Space.xxxl].map(Double.init), tokens.spacing.scale)
        XCTAssertEqual([Radius.sm, Radius.md, Radius.lg].map(Double.init), [tokens.radii.sm, tokens.radii.md, tokens.radii.lg])
        for step in tokens.spacing.scale {
            XCTAssertEqual(step.truncatingRemainder(dividingBy: tokens.spacing.unit), 0)
        }
    }
}
