// swift-tools-version:5.9
import PackageDescription

// A verifier bench fixture (bench/README.md). build.sh builds it without SwiftPM.
let package = Package(
    name: "UnitConvert",
    platforms: [.macOS(.v14)],
    targets: [.executableTarget(name: "UnitConvert", path: "Sources/UnitConvert")]
)
