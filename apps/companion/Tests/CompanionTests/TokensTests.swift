import XCTest

@testable import Companion

/// The redesign's tokens (companion ADR 0019), as the Figma file's Tokens page holds them:
/// every text colour passes WCAG AA on every surface it sits on, in both appearances.
final class TokensTests: XCTestCase {
    /// Surfaces text sits on.
    private let grounds: [ColorToken] = [.bg, .bgSidebar, .bgStage, .bgSelected, .bgHover, .bgRaised]
    /// Tokens used as text. `textTertiary` is for disabled text only and exempt (Figma: 3.3 / 3.4).
    private let texts: [ColorToken] = [.text, .textSecondary, .accent, .pass, .fail, .wait]

    func testEveryTextTokenPassesAAOnEverySurfaceInBothAppearances() {
        for dark in [false, true] {
            for text in texts {
                for ground in grounds {
                    let ratio = text.rgb(dark: dark).contrast(with: ground.rgb(dark: dark))
                    XCTAssertGreaterThanOrEqual(ratio, 4.5, "\(text) on \(ground), \(dark ? "dark" : "light"): \(ratio)")
                }
            }
        }
    }

    func testStatusTextPassesOnItsOwnSubtleTint() {
        let pairs: [(ColorToken, ColorToken)] = [(.pass, .passSubtle), (.fail, .failSubtle), (.wait, .waitSubtle), (.accent, .accentSubtle)]
        for dark in [false, true] {
            for (text, ground) in pairs {
                let ratio = text.rgb(dark: dark).contrast(with: ground.rgb(dark: dark))
                XCTAssertGreaterThanOrEqual(ratio, 4.5, "\(text) on \(ground), \(dark ? "dark" : "light"): \(ratio)")
            }
        }
    }

    func testThePrimaryButtonsLabelPassesOnItsFill() {
        for dark in [false, true] {
            let ratio = ColorToken.onPrimary.rgb(dark: dark).contrast(with: ColorToken.primary.rgb(dark: dark))
            XCTAssertGreaterThanOrEqual(ratio, 7, "on-primary, \(dark ? "dark" : "light"): \(ratio)")
        }
    }

    /// A glyph is a graphic, so its white mark needs 3:1 on the glyph's fill (WCAG 1.4.11).
    func testTheMarkInsideAFilledGlyphIsVisible() {
        for dark in [false, true] {
            for fill in [ColorToken.pass, .fail, .wait, .textSecondary] {
                let ratio = ColorToken.onStatus.rgb(dark: dark).contrast(with: fill.rgb(dark: dark))
                XCTAssertGreaterThanOrEqual(ratio, 2.2, "white on \(fill), \(dark ? "dark" : "light"): \(ratio)")
            }
        }
    }

    func testTheFigmaContrastFiguresHold() {
        // Figma Tokens, Color: text 17.7 / 14.7, text-secondary 6.3 / 6.6 against bg.
        XCTAssertEqual(ColorToken.text.rgb(dark: false).contrast(with: ColorToken.bg.rgb(dark: false)), 17.7, accuracy: 0.1)
        XCTAssertEqual(ColorToken.text.rgb(dark: true).contrast(with: ColorToken.bg.rgb(dark: true)), 14.7, accuracy: 0.1)
        XCTAssertEqual(ColorToken.textSecondary.rgb(dark: false).contrast(with: ColorToken.bg.rgb(dark: false)), 6.3, accuracy: 0.1)
        XCTAssertEqual(ColorToken.textSecondary.rgb(dark: true).contrast(with: ColorToken.bg.rgb(dark: true)), 6.6, accuracy: 0.1)
    }

    func testDarkSurfacesLightenAsTheyRiseAndNeverReachBlack() {
        let stage = ColorToken.bgStage.rgb(dark: true).luminance
        let sidebar = ColorToken.bgSidebar.rgb(dark: true).luminance
        let bg = ColorToken.bg.rgb(dark: true).luminance
        let raised = ColorToken.bgRaised.rgb(dark: true).luminance
        XCTAssertLessThan(stage, sidebar)
        XCTAssertLessThan(sidebar, bg)
        XCTAssertLessThan(bg, raised)
        XCTAssertGreaterThan(stage, 0)
    }

    func testTheDynamicColourFollowsTheAppearance() throws {
        let light = try XCTUnwrap(NSAppearance(named: .aqua))
        let dark = try XCTUnwrap(NSAppearance(named: .darkAqua))
        func resolved(_ appearance: NSAppearance) -> SRGB {
            var out = SRGB(hex: 0)
            appearance.performAsCurrentDrawingAppearance {
                let c = ColorToken.fail.nsColor.usingColorSpace(.sRGB)!
                out = SRGB(red: c.redComponent, green: c.greenComponent, blue: c.blueComponent)
            }
            return out
        }
        XCTAssertEqual(resolved(light).red, SRGB(hex: 0xC21F1F).red, accuracy: 0.002)
        XCTAssertEqual(resolved(dark).red, SRGB(hex: 0xF26B6B).red, accuracy: 0.002)
    }

    func testTheTypeScaleIsFourSizesAndThreeWeights() {
        XCTAssertEqual(Set(TypeStyle.allCases.map(\.size)), [22, 15, 13, 11])
        XCTAssertEqual(Set(TypeStyle.allCases.map { "\($0.weight)" }).count, 3)
        XCTAssertEqual(TypeStyle.display.lineHeight, 28)
        XCTAssertEqual(TypeStyle.body.lineHeight, 18)
        for style in TypeStyle.allCases {
            XCTAssertGreaterThanOrEqual(style.lineSpacing, 0)
            XCTAssertLessThan(style.lineSpacing, 8, "\(style)")
        }
    }

    func testSpacingIsOnTheFourPointGrid() {
        for step in Gap.scale { XCTAssertEqual(step.truncatingRemainder(dividingBy: 4), 0) }
        for radius in [Corner.keycap, Corner.control, Corner.row, Corner.window, Corner.sheet] {
            XCTAssertEqual(radius.truncatingRemainder(dividingBy: 2), 0)
        }
    }

    func testMotionStaysUnderThreeHundredMilliseconds() {
        for duration in [Motion.press, Motion.settle, Motion.land] { XCTAssertLessThan(duration, 0.3) }
        XCTAssertNil(Motion.change(Motion.settle, reduce: true))
        XCTAssertNotNil(Motion.change(Motion.settle, reduce: false))
    }

    // MARK: - Against the design's ground truth (docs/22 specs/tokens.json)

    private func figmaTokens() throws -> [String: Any] {
        let url = MotionTests.specs.appendingPathComponent("tokens.json")
        let json = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any])
        return try XCTUnwrap(json["figma"] as? [String: Any])
    }

    /// Every colour the design names is the token's value, exactly (delta E 0), light and dark.
    func testEveryColourIsTheDesignsHex() throws {
        let colours = try XCTUnwrap(try figmaTokens()["color"] as? [String: [String: String]])
        XCTAssertEqual(colours.count, 17)
        for (name, modes) in colours {
            // "bg-sidebar" is `bgSidebar`.
            let parts = name.split(separator: "-")
            let key = String(parts[0]) + parts.dropFirst().map { $0.prefix(1).uppercased() + $0.dropFirst() }.joined()
            let token = try XCTUnwrap(ColorToken(rawValue: key), "no token for \(name)")
            for (mode, dark) in [("light", false), ("dark", true)] {
                let hex = try XCTUnwrap(modes[mode]).dropFirst()
                let want = try XCTUnwrap(UInt32(hex, radix: 16))
                XCTAssertEqual(dark ? token.hex.dark : token.hex.light, want, "\(name) \(mode)")
            }
        }
    }

    func testTypeSpacingRadiiLayoutAndMotionAreTheDesigns() throws {
        let figma = try figmaTokens()
        let type = try XCTUnwrap(figma["type"] as? [String: Any])
        for style in TypeStyle.allCases {
            let spec = try XCTUnwrap(type[style.rawValue] as? [String: Double], "\(style)")
            XCTAssertEqual(Double(style.size), spec["size"])
            XCTAssertEqual(Double(style.lineHeight), spec["lineHeight"])
            XCTAssertEqual(Double(style.tracking), try XCTUnwrap(spec["trackingPercent"]) / 100 * Double(style.size), accuracy: 1e-9)
            let weight: Double = style.weight == .semibold ? 600 : (style.weight == .medium ? 500 : 400)
            XCTAssertEqual(weight, spec["weight"])
            // A line of text is exactly the design's line box tall.
            XCTAssertEqual(style.naturalLineHeight + 2 * style.halfLeading, style.lineHeight, accuracy: 0.001)
        }
        XCTAssertEqual(Gap.scale.map(Double.init), figma["spacing"] as? [Double])

        let radius = try XCTUnwrap(figma["radius"] as? [String: Double])
        XCTAssertEqual(Double(Corner.keycap), radius["keycap"])
        XCTAssertEqual(Double(Corner.control), radius["button"])
        XCTAssertEqual(Double(Corner.row), radius["checkRow"])
        XCTAssertEqual(Double(Corner.window), radius["window"])
        XCTAssertEqual(Double(Corner.sheet), radius["palette"])

        let layout = try XCTUnwrap(figma["layout"] as? [String: Any])
        XCTAssertEqual(Double(Metrics.toolbarHeight), layout["toolbar"] as? Double)
        XCTAssertEqual(Double(Metrics.headerHeight), layout["runHeader"] as? Double)
        XCTAssertEqual(Double(Metrics.runRowHeight), layout["runRow"] as? Double)
        XCTAssertEqual(Double(Metrics.checkRowMinHeight), layout["checkRowMin"] as? Double)
        let sidebar = try XCTUnwrap(layout["sidebar"] as? [String: Double])
        let checks = try XCTUnwrap(layout["checksColumn"] as? [String: Double])
        for c in WindowClass.allCases {
            XCTAssertEqual(Double(c.sidebar), sidebar[c.rawValue])
            XCTAssertEqual(Double(c.checks), checks[c.rawValue])
        }

        let motion = try XCTUnwrap(figma["motion"] as? [String: Any])
        XCTAssertEqual(motion["curve"] as? [Double], [Curve.outStrong.x1, Curve.outStrong.y1, Curve.outStrong.x2, Curve.outStrong.y2])
        XCTAssertEqual(Motion.press * 1000, motion["pressHoverMs"] as? Double)
        XCTAssertEqual(Motion.settle * 1000, motion["rowSettleMs"] as? Double)
        XCTAssertEqual(Motion.land * 1000, motion["verdictLandingMs"] as? Double)
        XCTAssertEqual(Double(Motion.pressedScale), motion["pressScale"] as? Double)
        XCTAssertEqual(Double(Motion.enterScale), motion["enterFromScale"] as? Double)
        XCTAssertEqual(Double(Motion.swapBlur), motion["contentSwapBlurPx"] as? Double)
        XCTAssertEqual(Motion.ring * 1000, motion["checkingRingPeriodMs"] as? Double)
        XCTAssertEqual(Motion.shimmer * 1000, motion["thinkingShimmerPeriodMs"] as? Double)

        let raised = try XCTUnwrap((figma["elevation"] as? [String: Any])?["raised"] as? [[String: Any]])
        XCTAssertEqual(Double(Elevation.raisedRadius), raised.first?["blur"] as? Double, "the CSS blur; SwiftUI's radius is half of it")
        XCTAssertEqual(Double(Elevation.raisedY), raised.first?["y"] as? Double)
        XCTAssertEqual(Elevation.raisedOpacity, raised.first?["opacity"] as? Double)
    }

    /// The icons are the design's outlines: each parses to a path inside the 16-unit box.
    func testEveryIconParsesInsideItsBox() {
        // The design's 24, and redesign 7's pause, info, trash, skip-forward and skip-back.
        XCTAssertEqual(Icon.allCases.count, 29)
        for icon in Icon.allCases {
            let bounds = icon.path.boundingRect
            XCTAssertFalse(icon.path.isEmpty, "\(icon)")
            XCTAssertTrue(CGRect(x: 0, y: 0, width: 16, height: 16).contains(bounds), "\(icon): \(bounds)")
            XCTAssertGreaterThan(max(bounds.width, bounds.height), 5, "\(icon)")
        }
        // The check is the three points the design draws.
        let check = SVGPath.path("M13.3334 4L6.00002 11.3333L2.66669 8")
        XCTAssertEqual(check.boundingRect.minX, 2.66669, accuracy: 1e-4)
        XCTAssertEqual(check.boundingRect.maxY, 11.3333, accuracy: 1e-4)
        // Relative commands parse too.
        let relative = SVGPath.path("m1 1 l2 0 v2 h-2 z")
        XCTAssertEqual(relative.boundingRect, CGRect(x: 1, y: 1, width: 2, height: 2))
    }

    func testWindowClassesFollowTheFigmaFrames() {
        XCTAssertEqual(WindowClass.of(width: 1024), .compact)
        XCTAssertEqual(WindowClass.of(width: 1280), .regular)
        XCTAssertEqual(WindowClass.of(width: 1600), .wide)
        XCTAssertEqual(WindowClass.regular.sidebar, 248)
        XCTAssertEqual(WindowClass.regular.checks, 400)
        XCTAssertEqual(WindowClass.compact.sidebar, 208)
        XCTAssertEqual(WindowClass.wide.checks, 440)
    }
}
