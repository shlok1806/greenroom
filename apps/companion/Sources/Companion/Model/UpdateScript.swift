import Foundation
import Synchronization

// The one place the app runs a program (root ADR 0033): `scripts/update.sh` in the checkout
// greenroom was installed from. Everything else the app does goes through the daemon's API;
// updating cannot, because the daemon has no update route by design (nothing reachable over
// the network may make the host build or run code).

/// What an update run says as it goes.
enum UpdateEvent: Equatable, Sendable {
    case line(String)
    /// The process ended with this status (a signal counts as 128 + its number).
    case exit(Int32)
}

enum UpdateScriptError: Error, LocalizedError, Equatable {
    /// The checkout has no `scripts/update.sh`.
    case missing(String)
    /// `--check` exited non-zero: its status and output.
    case failed(Int32, String)
    case timedOut
    case launch(String)

    var errorDescription: String? {
        switch self {
        case .missing(let path): "there is no \(path)"
        case .failed(_, let output):
            output.split(separator: "\n").last.map(String.init) ?? "update.sh --check failed"
        case .timedOut: "update.sh --check did not finish in 2 minutes"
        case .launch(let why): why
        }
    }
}

/// Runs `update.sh`. A protocol so `Updates` can be tested without a checkout.
protocol UpdateScript: Sendable {
    /// `update.sh --check`'s output.
    func check(checkout: String) async throws -> String
    /// Runs `update.sh`, writing its output to `log` and reading it back line by line. The
    /// process is not a child that dies with the app: the Companion's own install quits this
    /// app part way through, and the update must carry on without it.
    func update(checkout: String, log: URL) -> AsyncStream<UpdateEvent>
}

struct ScriptRunner: UpdateScript {
    /// How long `--check` (a `git fetch`) may take.
    var checkTimeout: Duration = .seconds(120)
    /// How often a running update's log is read.
    var pollInterval: Duration = .milliseconds(200)

    static func script(in checkout: String) -> URL {
        URL(fileURLWithPath: checkout, isDirectory: true).appending(path: "scripts/update.sh")
    }

    /// Where an update's output goes: the Mac's place for app logs, kept after the app quits.
    static var defaultLog: URL {
        FileManager.default.homeDirectoryForCurrentUser.appending(path: "Library/Logs/Greenroom/update.log")
    }

    private func process(checkout: String, arguments: [String]) throws -> Process {
        let script = Self.script(in: checkout)
        guard FileManager.default.fileExists(atPath: script.path) else {
            throw UpdateScriptError.missing(script.path)
        }
        let process = Process()
        // Through bash, so a checkout that lost the execute bit still runs.
        process.executableURL = URL(fileURLWithPath: "/bin/bash")
        process.arguments = [script.path] + arguments
        process.currentDirectoryURL = URL(fileURLWithPath: checkout, isDirectory: true)
        var environment = ProcessInfo.processInfo.environment
        // No terminal to answer a credential prompt: fail instead of waiting.
        environment["GIT_TERMINAL_PROMPT"] = "0"
        process.environment = environment
        process.standardInput = FileHandle.nullDevice
        return process
    }

    func check(checkout: String) async throws -> String {
        let process = try process(checkout: checkout, arguments: ["--check"])
        let pipe = Pipe()
        process.standardOutput = pipe
        process.standardError = pipe
        do {
            try process.run()
        } catch {
            throw UpdateScriptError.launch(error.localizedDescription)
        }
        let timeout = checkTimeout
        let timedOut = Mutex(false)
        let watchdog = Task.detached {
            try? await Task.sleep(for: timeout)
            guard !Task.isCancelled, process.isRunning else { return }
            timedOut.withLock { $0 = true }
            process.terminate()
        }
        defer { watchdog.cancel() }
        // Read to the end off the main actor: the pipe must be drained while the script runs.
        let output: Data = await Task.detached {
            let data = (try? pipe.fileHandleForReading.readToEnd()) ?? Data()
            process.waitUntilExit()
            return data
        }.value
        let text = String(decoding: output, as: UTF8.self)
        if timedOut.withLock({ $0 }) { throw UpdateScriptError.timedOut }
        guard process.terminationStatus == 0 else {
            throw UpdateScriptError.failed(process.terminationStatus, text)
        }
        return text
    }

    func update(checkout: String, log: URL) -> AsyncStream<UpdateEvent> {
        let poll = pollInterval
        return AsyncStream { continuation in
            let process: Process
            let writer: FileHandle
            let reader: FileHandle
            do {
                process = try self.process(checkout: checkout, arguments: [])
                try FileManager.default.createDirectory(at: log.deletingLastPathComponent(), withIntermediateDirectories: true)
                FileManager.default.createFile(atPath: log.path, contents: nil)
                writer = try FileHandle(forWritingTo: log)
                reader = try FileHandle(forReadingFrom: log)
                // A file, never a pipe: a pipe would break (SIGPIPE) the moment this app quits.
                process.standardOutput = writer
                process.standardError = writer
                try process.run()
            } catch {
                let why = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
                continuation.yield(.line("failed: start the update (\(why))"))
                continuation.yield(.exit(127))
                continuation.finish()
                return
            }
            try? writer.close()
            let task = Task.detached {
                var pending = Data()
                func drain() {
                    let chunk = (try? reader.readToEnd()) ?? Data()
                    pending.append(chunk)
                    while let newline = pending.firstIndex(of: UInt8(ascii: "\n")) {
                        let line = String(decoding: pending[pending.startIndex..<newline], as: UTF8.self)
                        pending.removeSubrange(pending.startIndex...newline)
                        continuation.yield(.line(line))
                    }
                }
                while process.isRunning {
                    drain()
                    try? await Task.sleep(for: poll)
                }
                drain()
                if !pending.isEmpty {
                    continuation.yield(.line(String(decoding: pending, as: UTF8.self)))
                }
                try? reader.close()
                let status = process.terminationReason == .uncaughtSignal
                    ? 128 + process.terminationStatus : process.terminationStatus
                continuation.yield(.exit(status))
                continuation.finish()
            }
            // Stopping the reader never stops the update.
            continuation.onTermination = { _ in task.cancel() }
        }
    }
}
