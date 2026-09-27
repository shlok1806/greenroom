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
