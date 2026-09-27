import AppKit

// No Dock icon, no menu bar, never frontmost: the person's session is not touched.
let application = NSApplication.shared
application.setActivationPolicy(.prohibited)

Task { @MainActor in
    do {
        // The native redesign (companion ADR 0019) renders without a daemon for its components.
        if let output = ProcessInfo.processInfo.environment["GREENROOM_REDESIGN_SNAPSHOTS"], !output.isEmpty {
            try await RedesignHarness().run(output: output, scenarios: RedesignHarness.scenarios())
            exit(0)
        }
        try await SnapshotHarness().run()
        exit(0)
    } catch {
        FileHandle.standardError.write(Data("snapshots failed: \(error)\n".utf8))
        exit(1)
    }
}
application.run()
