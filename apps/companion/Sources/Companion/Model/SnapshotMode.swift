import Foundation

/// A snapshot launch: `GREENROOM_SNAPSHOT=<dir>`, debug builds only (`SnapshotHook`). It
/// is a camera, never a seat: the app never activates or takes a key window, only the
/// hook's own keys drive it (`KeyRouter`), it never writes to the daemon
/// (`DaemonClient.readOnly`) and it never writes the person's defaults (`AppDefaults`).
enum SnapshotMode {
    static let isActive: Bool = {
        #if DEBUG
        guard let directory = ProcessInfo.processInfo.environment["GREENROOM_SNAPSHOT"] else { return false }
        return !directory.isEmpty
        #else
        return false
        #endif
    }()
}

/// Where the app's settings live: the person's defaults, or in a snapshot a scratch
/// domain of its own, so a snapshot's selection, theme, focus and window frame never
/// reach the person's. Every `@AppStorage` names this store.
enum AppDefaults {
    /// `UserDefaults` is thread-safe (Apple documents it so) but not marked `Sendable`.
    nonisolated(unsafe) static let shared: UserDefaults = {
        guard SnapshotMode.isActive else { return .standard }
        try? FileManager.default.createDirectory(at: scratchDirectory, withIntermediateDirectories: true)
        return UserDefaults(suiteName: scratchSuite) ?? .standard
    }()

    /// One per process, in the temporary directory: snapshots side by side never share
    /// settings, and nothing lands in `~/Library/Preferences`. A suite named by an
    /// absolute path is a plist at that path.
    static let scratchDirectory = FileManager.default.temporaryDirectory
        .appending(path: "greenroom-snapshot-\(ProcessInfo.processInfo.processIdentifier)", directoryHint: .isDirectory)
    static var scratchSuite: String { scratchDirectory.appending(path: "defaults").path(percentEncoded: false) }

    /// Throws the snapshot's scratch settings away as it quits.
    static func discardScratch() {
        guard SnapshotMode.isActive else { return }
        shared.removePersistentDomain(forName: scratchSuite)
        try? FileManager.default.removeItem(at: scratchDirectory)
    }
}
