// swift-tools-version: 6.0
// THROWAWAY prototype of the glyph-native Companion UI. Never merged, never shipped.
import PackageDescription

let package = Package(
    name: "GlyphPrototype",
    platforms: [.macOS(.v15)],
    targets: [
        .executableTarget(
            name: "GlyphPrototype",
            path: "Sources/GlyphPrototype",
            resources: [.process("Resources")],
            swiftSettings: [.swiftLanguageMode(.v6)]
        ),
    ]
)
