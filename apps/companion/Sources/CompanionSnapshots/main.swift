import AppKit

// No Dock icon, no menu bar, never frontmost: the person's session is not touched.
let application = NSApplication.shared
application.setActivationPolicy(.prohibited)

Task { @MainActor in
    do {
        try await SnapshotHarness().run()
        exit(0)
    } catch {
        FileHandle.standardError.write(Data("snapshots failed: \(error)\n".utf8))
        exit(1)
    }
}
application.run()
