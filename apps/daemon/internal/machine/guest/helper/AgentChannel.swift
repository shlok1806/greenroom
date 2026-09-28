// --agent: the long-lived guest agent on one framed channel (daemon ADR 0005). This file is the
// channel: HELLO, the reader thread, the health thread (idle exit and the watchdog) and
// EVENTs. Requests are AgentCalls.swift; the ops are AgentOps.swift and the files it names.

import ApplicationServices
import CoreGraphics
import Foundation

func agent() -> Never {
    // A write to a closed pipe is an error from write(2), and writeAll ends the agent on it.
    signal(SIGPIPE, SIG_IGN)
    // Every AX call without its own timeout gives up after 1 s rather than the system's 6 s,
    // so one hung app cannot hold a queue for long. Action paths set 0.5 s per element.
    AXUIElementSetMessagingTimeout(AXUIElementCreateSystemWide(), 1.0)

    registerAgentOps()
    sawFrame() // The idle clock starts now, not at its first read.
    send(AgentFrame.hello, jsonData([
        "version": 9,
        "source": helperSource,
        "protocol": agentProtocol,
        "pid": Int(getpid()),
        "trusted": [
            "accessibility": AXIsProcessTrusted(),
            "screen": CGPreflightScreenCaptureAccess(),
            "postEvent": CGPreflightPostEventAccess(),
        ],
        "screen": screenInfo(),
        "caps": registeredOps(),
    ]) ?? Data("{}".utf8))

    Thread { readFrames() }.start()
    Thread { watchHealth() }.start()
    // The main run loop, not dispatchMain: NSWorkspace keeps its running-application list
    // current from notifications delivered on it, and a long-lived process that never runs it
    // reads a list frozen at start.
    CFRunLoopRun()
    exit(0)
}

/// The main display in points, with the pixels a point measured from its display mode.
func screenInfo() -> [String: Any] {
    var scale = 1.0
    if let mode = CGDisplayCopyDisplayMode(display), mode.width > 0 {
        scale = Double(mode.pixelWidth) / Double(mode.width)
    }
    return ["width": Int(bounds.width), "height": Int(bounds.height), "scale": scale]
}

// MARK: - Reading

private let lastFrameLock = NSLock()
private var lastFrame = DispatchTime.now()

private func sawFrame() {
    lastFrameLock.lock()
    lastFrame = .now()
    lastFrameLock.unlock()
}

private func secondsSinceLastFrame() -> Double {
    lastFrameLock.lock()
    defer { lastFrameLock.unlock() }
    return Double(DispatchTime.now().uptimeNanoseconds - lastFrame.uptimeNanoseconds) / 1e9
}

/// The reader thread. It answers PING itself, so a busy agent still proves it is alive, and
/// hands everything else off without waiting on it.
private func readFrames() -> Never {
    var unknownTypes: Set<UInt8> = []
    while true {
        let frame: AgentFrameData
        do {
            guard let next = try readAgentFrame(readFull) else { exit(0) } // stdin EOF: the host is gone.
            frame = next
        } catch AgentFrameError.tooLarge(let length) {
            // The channel is out of step; nothing after this header can be trusted.
            agentLog("a frame of \(length) bytes is over the 4 MiB limit; ending the channel")
            exit(1)
        } catch {
            exit(0) // Truncated: stdin closed inside a frame.
        }
        sawFrame()
        switch frame.type {
        case AgentFrame.ping: send(AgentFrame.pong, frame.payload)
        case AgentFrame.request: handleRequest(frame.payload)
        case AgentFrame.cancel: handleCancel(frame.payload)
        case AgentFrame.pause: handlePause(frame.payload)
        case AgentFrame.resume: handleResume()
        default:
            if unknownTypes.insert(frame.type).inserted {
                agentLog("ignoring frames of unknown type 0x\(String(frame.type, radix: 16))")
            }
        }
    }
}

// MARK: - Health (daemon ADR 0005 points 8 and 9)

private let stallLock = NSLock()
private var stalls = StallTracker()

private func uptime() -> Double {
    Double(DispatchTime.now().uptimeNanoseconds) / 1e9
}

/// Runs body as work of `kind` (`ax` for a walk, `capture` for a capture), which the watchdog
/// reports as stalled while it runs over 10 s.
func busy<T>(_ kind: String, _ body: () throws -> T) rethrows -> T {
    stallLock.lock()
    let token = stalls.begin(kind, at: uptime())
    stallLock.unlock()
    defer {
        stallLock.lock()
        stalls.end(token)
        stallLock.unlock()
    }
    return try body()
}

/// Once a second: end the agent when no frame has come for 15 s (a dead host exec does not
/// always close stdin), and report stalls and recoveries.
private func watchHealth() -> Never {
    while true {
        sleep(1)
        if secondsSinceLastFrame() > idleExitSeconds { exit(0) }
        stallLock.lock()
        let events = stalls.tick(at: uptime())
        stallLock.unlock()
        for event in events {
            switch event {
            case let .stalled(kind, seconds): agentEvent("stalled", ["in": kind, "seconds": seconds])
            case let .recovered(kind): agentEvent("recovered", ["in": kind])
            }
        }
    }
}

// MARK: - Events

func agentEvent(_ kind: String, _ fields: [String: Any] = [:]) {
    var body = fields
    body["kind"] = kind
    if let payload = jsonData(body) { send(AgentFrame.event, payload) }
}

/// A diagnostic line for the daemon's log. For what is unusual, never once per request.
func agentLog(_ message: String) {
    agentEvent("log", ["message": message])
}
