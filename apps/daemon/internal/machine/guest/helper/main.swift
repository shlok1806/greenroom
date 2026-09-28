// greenroom-input: the one thing inside a guest that moves its mouse and
// presses its keys, reads its accessibility tree and captures its screen.
//
// The daemon compiles these files inside the machine once (see input.go), then
// calls the binary with one base64-encoded JSON argument, so no text the
// person types ever passes through a shell:
//
//     greenroom-input --version
//     greenroom-input --json-base64 <base64 of {"actions":[...]}>
//     greenroom-input --serve
//     greenroom-input --ui-base64 <base64 of {"app":"...","limit":N}>
//     greenroom-input --desktop
//     greenroom-input --agent
//
// --json-base64 writes one JSON object to stdout and exits 0, or writes an
// error object and exits 1. --serve streams the screen as H.264 and takes
// input batches on stdin until stdin closes (ADR 0011). --ui-base64 writes the
// accessibility tree of the frontmost (or a named) application (ADR 0012).
// --desktop writes what is on the screen and what is running: every on-screen
// window (CGWindowListCopyWindowInfo) and every regular application
// (NSWorkspace), for the dialog check (desktopcheck.go, ADR 0018).
// --agent is the long-lived guest agent on one framed channel (daemon ADR 0005).
// Coordinates are points on the main display, both ways; the daemon turns
// the fractions the companion sends into points before it gets here, and the
// frames this reports into fractions after.
//
// The events go in through CGEvent at the HID tap, which needs the
// Accessibility and PostEvent permissions. The greenroom image grants both to
// the Tart guest agent, and this binary inherits them because the agent
// starts it (docs/02-spike.md). Reading the accessibility tree needs the same
// Accessibility grant.
//
// Only this file may hold top-level code. Globals in the other files are
// initialized lazily, on first use, so nothing that talks to WindowServer runs
// before the arguments are read.

import Foundation

/// The version line: the helper version and the hash of its sources
/// (SourceHash.swift, written by the install script), so a changed source is
/// recompiled even when the version is the same (daemon ADR 0005).
let version = "greenroom-input 10 \(helperSource)"

// --version answers before anything else runs. The globals the other modes use
// (the main display, its bounds, the event source) talk to WindowServer as soon
// as they are initialised, and a wedged WindowServer then held `--version`
// forever, so the install check hung with it (issue #187).
if CommandLine.arguments.dropFirst().first == "--version" {
    print(version)
    exit(0)
}
if CommandLine.arguments.dropFirst().first == "--agent" {
    agent()
}

let arguments = Array(CommandLine.arguments.dropFirst())
if arguments.first == "--desktop" {
    emit(desktop(), to: FileHandle.standardOutput)
    exit(0)
}
if arguments.first == "--serve" {
    serve()
}
if arguments.count == 2, arguments[0] == "--ui-base64" {
    do {
        guard let payload = Data(base64Encoded: arguments[1]) else { throw Failure("--ui-base64 needs base64") }
        emit(try uiTree(JSONDecoder().decode(UIRequest.self, from: payload)), to: FileHandle.standardOutput)
        exit(0)
    } catch let failure as Failure {
        emit(["error": failure.message], to: FileHandle.standardError)
        exit(1)
    } catch {
        emit(["error": "\(error)"], to: FileHandle.standardError)
        exit(1)
    }
}

guard arguments.count == 2, arguments[0] == "--json-base64",
      let payload = Data(base64Encoded: arguments[1])
else {
    emit(["error": "usage: greenroom-input --json-base64 <base64 json>"], to: FileHandle.standardError)
    exit(1)
}

do {
    let request = try JSONDecoder().decode(Request.self, from: payload)
    try perform(request.actions)
    // A release that is never posted leaves the guest with a stuck button, so
    // a batch that ends mid-drag is the caller's business, not a leak here:
    // the daemon always sends the release in the same batch or a later one.
    emit([
        "ok": true,
        "actions": request.actions.count,
        "screen": ["width": Int(bounds.width), "height": Int(bounds.height)],
    ], to: FileHandle.standardOutput)
} catch let failure as Failure {
    emit(["error": failure.message], to: FileHandle.standardError)
    exit(1)
} catch {
    emit(["error": "\(error)"], to: FileHandle.standardError)
    exit(1)
}
