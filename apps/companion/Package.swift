// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "Companion",
    platforms: [.macOS(.v15)],
    products: [
        .executable(name: "Companion", targets: ["CompanionApp"]),
    ],
    // The allowlist (companion ADR 0007). swift-markdown parses messages only; the
    // transcript draws what it parses in the app's own faces (`MarkdownView`).
    dependencies: [
        .package(url: "https://github.com/swiftlang/swift-markdown.git", exact: "0.9.0"),
    ],
    targets: [
        // Everything but the entry point, so the snapshot tool can host the real views.
        // `Resources/Fonts`: Mona Sans and Monaspace Neon with their OFL licences.
        // `Resources/Design`: a copy of the repo's `design/`, kept equal to it by
        // `DesignBundleTests` (`scripts/sync-design.sh` writes it).
        .target(
            name: "Companion",
            dependencies: [.product(name: "Markdown", package: "swift-markdown")],
            path: "Sources/Companion",
            resources: [.copy("Resources/Fonts"), .copy("Resources/Design")]
        ),
        .executableTarget(
            name: "CompanionApp",
            dependencies: ["Companion"],
            path: "Sources/CompanionApp"
        ),
        // Renders the window's states for design review (`CLAUDE.md`). Never bundled.
        .executableTarget(
            name: "CompanionSnapshots",
            dependencies: ["Companion"],
            path: "Sources/CompanionSnapshots"
        ),
        .testTarget(
            name: "CompanionTests",
            dependencies: ["Companion"],
            path: "Tests/CompanionTests",
            // Golden data the tests read in place (`MotionTests`): cmdk's scores, Motion's springs.
            exclude: ["Golden"]
        ),
    ]
)
