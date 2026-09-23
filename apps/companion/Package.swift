// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "Companion",
    platforms: [.macOS(.v15)],
    products: [
        .executable(name: "Companion", targets: ["CompanionApp"]),
    ],
    targets: [
        // Everything but the entry point, so the snapshot tool can host the real views.
        .target(
            name: "Companion",
            path: "Sources/Companion"
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
            path: "Tests/CompanionTests"
        ),
    ]
)
