// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "Companion",
    platforms: [.macOS(.v15)],
    targets: [
        .executableTarget(
            name: "Companion",
            path: "Sources/Companion"
        ),
        .testTarget(
            name: "CompanionTests",
            dependencies: ["Companion"],
            path: "Tests/CompanionTests"
        ),
    ]
)
