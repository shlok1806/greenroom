import Foundation

/// The design data (`design/` at the repo root, ADR 0004 and 0008) as the app reads it at
/// runtime: the four themes and the tokens. The app ships a copy in its resources
/// (`Resources/Design`, written by `scripts/sync-design.sh`, kept equal to `design/` by
/// `DesignBundleTests`).
struct DesignData: Sendable {
    let tokens: DesignTokens
    let themes: [ThemeID: Theme]

    func theme(_ id: ThemeID) -> Theme {
        // `load` refuses a directory missing any theme, so every id is present.
        themes[id]!
    }

    enum LoadError: Error, CustomStringConvertible, Equatable {
        case missing(String)
        case badColour(file: String, key: String, value: String)
        case paletteSize(file: String, count: Int)
        case role(String)
        case themeKey(file: String, key: String)

        var description: String {
            switch self {
            case .missing(let path): "design data missing: \(path)"
            case .badColour(let file, let key, let value): "\(file): \(key) \(value) is not #rrggbb"
            case .paletteSize(let file, let count): "\(file): \(count) palette colours, not 16"
            case .role(let role): "tokens.json: role \(role) has no slot 0 to 15"
            case .themeKey(let file, let key): "\(file): no \(key)"
            }
        }
    }

    /// Reads `tokens.json` and `themes/<id>.json` under `directory`.
    static func load(from directory: URL) throws -> DesignData {
        func read(_ path: String) throws -> Data {
            let url = directory.appendingPathComponent(path)
            guard let data = try? Data(contentsOf: url) else { throw LoadError.missing(url.path) }
            return data
        }
        let tokens = try JSONDecoder().decode(DesignTokens.self, from: read("tokens.json"))
        let roles = try tokens.roleSlots()
        var files: [ThemeID: ThemeFile] = [:]
        for id in ThemeID.allCases {
            files[id] = try ThemeFile.decode(read("themes/\(id.rawValue).json"), file: id.rawValue, keys: tokens.themeKeys)
        }
        var themes: [ThemeID: Theme] = [:]
        for id in ThemeID.allCases {
            // The screen well is a picture of a monitor: always the dark theme's ground,
            // with the dark theme's dim for any words in it.
            let dark = files[id.isHighContrast ? .darkHighContrast : .dark]!
            let wellDim = dark.palette.indices.contains(roles[.dim]!) ? dark.palette[roles[.dim]!] : dark.foreground
            themes[id] = try Theme(id: id, file: files[id]!, roles: roles, well: dark.background,
                                   wellInk: dark.foreground, wellDim: wellDim)
        }
        return DesignData(tokens: tokens, themes: themes)
    }

    /// The copy bundled with the app. A missing or broken copy is a build mistake that
    /// `DesignBundleTests` catches, so it stops the app with the reason.
    static let shared: DesignData = {
        do {
            return try load(from: AppResources.designDirectory)
        } catch {
            fatalError("The bundled design data did not load: \(error)")
        }
    }()
}

/// `design/tokens.json`, the parts the app reads.
struct DesignTokens: Decodable, Sendable, Equatable {
    struct Cell: Decodable, Sendable, Equatable {
        let font: String
        let size: Double
        let lineHeight: Double
    }

    struct Faces: Decodable, Sendable, Equatable {
        let mono: String
        let reading: String
        let readingHeading: String
    }

    struct Reading: Decodable, Sendable, Equatable {
        let size: Double
        let lineHeight: Double
    }

    struct ThemeKeys: Decodable, Sendable, Equatable {
        let brand: String
        let brandText: String
        let chromeTint: String
    }

    struct Spacing: Decodable, Sendable, Equatable {
        let unit: Double
        let scale: [Double]
    }

    struct Radii: Decodable, Sendable, Equatable {
        let sm: Double
        let md: Double
        let lg: Double
    }

    struct Motion: Decodable, Sendable, Equatable {
        struct Spinner: Decodable, Sendable, Equatable {
            let frames: [String]
            let intervalMs: Int
            let reduceMotion: String
        }

        /// A spring: SwiftUI's response (seconds) and damping fraction.
        struct Spring: Decodable, Sendable, Equatable {
            let response: Double
            let dampingFraction: Double
        }

        /// The 3 x 3 block-glyph loader (ADR 0006 `load`): a wavefront over a cycle, each
        /// column (and each row away from the middle) `stepMs` later than the last.
        struct Loader: Decodable, Sendable, Equatable {
            let grid: Int
            let cycleMs: Int
            let stepMs: Int
            let on: String
            let off: String
            let elapsedTickMs: Int
        }

        /// Where the agent clicked or typed: a ripple of `rings` cells that fades over `ms`
        /// (ADR 0006 decision 5).
        struct ClickMark: Decodable, Sendable, Equatable {
            let ms: Int
            let rings: Int
        }

        /// The first picture resolving out of its glyph rendering (ADR 0006, Boot).
        struct BootReveal: Decodable, Sendable, Equatable {
            let ms: Int
        }

        /// The picture dissolving into glyphs on destroy, then held as a still before the
        /// recording shows (ADR 0006, Power-down).
        struct PowerDown: Decodable, Sendable, Equatable {
            let dissolveMs: Int
            let holdMs: Int
        }

        /// Everything but the screen dims while the person drives (ADR 0006, Take control).
        struct HouseLights: Decodable, Sendable, Equatable {
            /// How far the rest of the window goes dark, 0 to 1.
            let dim: Double
        }

        let spinner: Spinner
        let loader: Loader
        let clickMark: ClickMark
        let bootReveal: BootReveal
        let powerDown: PowerDown
        let houseLights: HouseLights
        /// Resize, zoom, expand: panes move on this spring (ADR 0006).
        let settle: Spring
        /// How long an accept or dispute waits to be sent, so it can be undone (ADR 0005).
        let undoMs: Int
    }

    /// The window's width classes and pane sizes, in points (ADR 0004 decision 8, as 0008
    /// lets widths be points rather than cells).
    struct Layout: Decodable, Sendable, Equatable {
        /// At least this wide: runs, stage, conversation side by side.
        let wideMinWidth: Double
        /// At least this wide (and under wide): the runs fold to a strip of marks.
        let mediumMinWidth: Double
        let runsWidth: Double
        let runsMinWidth: Double
        let runsMaxWidth: Double
        let runsStripWidth: Double
        let stageMinWidth: Double
        let conversationMinWidth: Double
        let conversationWidth: Double
        let conversationMaxWidth: Double
        /// The conversation zoomed (or a narrow window's) keeps this measure, centred.
        let readingMaxWidth: Double
        /// The steps list under the screen keeps at least this, and this share of the stage.
        let stepsMinHeight: Double
        let stepsShare: Double
        /// The steps list's share of the stage when it is opened from the one-row track.
        let stepsExpandedShare: Double
        let stepsTrackHeight: Double
        /// The `?` help sheet takes at most this share of the window's height.
        let helpMaxShare: Double
    }

    let cell: Cell
    let type: Faces
    let reading: Reading
    let roles: [String: Int]
    let themeKeys: ThemeKeys
    let spacing: Spacing
    let radii: Radii
    let motion: Motion
    let layout: Layout

    /// Every role the app draws, on a slot 0 to 15.
    func roleSlots() throws -> [Role: Int] {
        var out: [Role: Int] = [:]
        for role in Role.allCases {
            guard let slot = roles[role.rawValue], (0...15).contains(slot) else {
                throw DesignData.LoadError.role(role.rawValue)
            }
            out[role] = slot
        }
        return out
    }
}

/// One `design/themes/*.json`: Ghostty's keys plus Greenroom's three brand keys, whose
/// names come from `tokens.json` `themeKeys`.
struct ThemeFile: Sendable, Equatable {
    let name: String
    let background: String
    let foreground: String
    let palette: [String]
    let brand: String
    let brandText: String
    let chromeTint: String

    private struct Ghostty: Decodable {
        let name: String
        let background: String
        let foreground: String
        let palette: [String]
    }

    static func decode(_ data: Data, file: String, keys: DesignTokens.ThemeKeys) throws -> ThemeFile {
        let ghostty = try JSONDecoder().decode(Ghostty.self, from: data)
        let extras = (try JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
        func extra(_ key: String) throws -> String {
            guard let value = extras[key] as? String else { throw DesignData.LoadError.themeKey(file: file, key: key) }
            return value
        }
        return ThemeFile(
            name: ghostty.name,
            background: ghostty.background,
            foreground: ghostty.foreground,
            palette: ghostty.palette,
            brand: try extra(keys.brand),
            brandText: try extra(keys.brandText),
            chromeTint: try extra(keys.chromeTint)
        )
    }
}

/// Where the app's bundled resources are: SwiftPM's resource bundle for this target.
enum AppResources {
    /// SwiftPM names a target's resource bundle `<package>_<target>.bundle`.
    static let bundleName = "Companion_Companion.bundle"

    /// In the `.app`, `scripts/bundle.sh` puts the resource bundle in `Contents/Resources`
    /// (where `codesign` seals it; the bundle root may hold nothing but `Contents`).
    /// SwiftPM's own `Bundle.module` looks beside the executable and in the build folder,
    /// which covers `swift run`, the tests and the snapshot harness, and stops the process
    /// when it finds nothing, so it is asked only after the app's own place.
    static let bundle: Bundle = {
        if let url = Bundle.main.resourceURL?.appendingPathComponent(bundleName),
           let bundle = Bundle(url: url) {
            return bundle
        }
        return Bundle.module
    }()

    static var designDirectory: URL {
        bundle.resourceURL!.appendingPathComponent("Design")
    }

    static var fontsDirectory: URL {
        bundle.resourceURL!.appendingPathComponent("Fonts")
    }
}
