// The `sh` op (daemon ADR 0005 point 12): a script the daemon wrote (the capture-approval
// check), never tool input, run with a timeout in its own process group.

import Foundation

/// The most output kept per stream; the tail is kept, where errors are.
private let shellOutputLimit = 64 << 10

/// How long a script may run when the request does not say.
private let defaultShellTimeoutMs = 10_000

private struct ShellArgs: Decodable {
    var script: String
    var args: [String]?
    var timeoutMs: Int?
}

func registerShellOp() {
    register("sh", .shell) { call in
        let args = try call.args(ShellArgs.self)
        // The call's own deadline bounds the script too, with room to answer before it (half a
        // second, or a quarter of a short deadline).
        let requested = Double(args.timeoutMs.map { max(1, $0) } ?? defaultShellTimeoutMs) / 1000
        let left = call.remaining
        let timeout = max(0.05, min(requested, left - min(0.5, left / 4)))
        let result = try runShell(args.script, args.args ?? [], timeout: timeout, call: call)
        return [
            "stdout": result.stdout, "stderr": result.stderr,
            "exit": result.exit, "timedOut": result.timedOut,
        ]
    }
}

/// Runs `/bin/sh -c script greenroom-sh args...` with stdin from /dev/null, in a new process
/// group so the timeout reaches everything it started: TERM to the group at `timeout`, KILL
/// 2 s later. A CANCEL does the same and answers `cancelled`.
private func runShell(_ script: String, _ args: [String], timeout: TimeInterval, call: Call) throws
    -> (stdout: String, stderr: String, exit: Int, timedOut: Bool) {
    var outPipe: [Int32] = [-1, -1]
    var errPipe: [Int32] = [-1, -1]
    guard pipe(&outPipe) == 0 else { throw AgentFailure("internal", "sh: pipe: \(String(cString: strerror(errno)))") }
    guard pipe(&errPipe) == 0 else {
        close(outPipe[0]); close(outPipe[1])
        throw AgentFailure("internal", "sh: pipe: \(String(cString: strerror(errno)))")
    }
    var readEnds = [outPipe[0], errPipe[0]]
    defer { for fd in readEnds where fd >= 0 { close(fd) } }

    var actions: posix_spawn_file_actions_t?
    posix_spawn_file_actions_init(&actions)
    defer { posix_spawn_file_actions_destroy(&actions) }
    posix_spawn_file_actions_addopen(&actions, 0, "/dev/null", O_RDONLY, 0)
    posix_spawn_file_actions_adddup2(&actions, outPipe[1], 1)
    posix_spawn_file_actions_adddup2(&actions, errPipe[1], 2)

    var attributes: posix_spawnattr_t?
    posix_spawnattr_init(&attributes)
    defer { posix_spawnattr_destroy(&attributes) }
    // Its own group, for the timeout's kill; only fds 0 to 2, so the script never holds the
    // channel's pipes; and default signals, since the agent ignores SIGPIPE and a child
    // inherits that.
    posix_spawnattr_setflags(&attributes, Int16(POSIX_SPAWN_SETPGROUP | POSIX_SPAWN_CLOEXEC_DEFAULT | POSIX_SPAWN_SETSIGDEF | POSIX_SPAWN_SETSIGMASK))
    posix_spawnattr_setpgroup(&attributes, 0)
    var defaults = sigset_t()
    sigemptyset(&defaults)
    sigaddset(&defaults, SIGPIPE)
    posix_spawnattr_setsigdefault(&attributes, &defaults)
    var mask = sigset_t()
    sigemptyset(&mask)
    posix_spawnattr_setsigmask(&attributes, &mask)

    let argv = ["/bin/sh", "-c", script, "greenroom-sh"] + args
    var cArgs: [UnsafeMutablePointer<CChar>?] = argv.map { strdup($0) } + [nil]
    defer { for p in cArgs { free(p) } }
    var pid: pid_t = 0
    let spawned = posix_spawn(&pid, "/bin/sh", &actions, &attributes, &cArgs, environ)
    close(outPipe[1])
    close(errPipe[1])
    guard spawned == 0 else {
        throw AgentFailure("internal", "sh: cannot start /bin/sh: \(String(cString: strerror(spawned)))")
    }

    var out = TailBuffer(limit: shellOutputLimit)
    var err = TailBuffer(limit: shellOutputLimit)
    var status: Int32 = 0
    var exited = false
    var timedOut = false
    var cancelled = false
    var killAt: Date?
    var giveUpAt: Date?
    let termAt = Date().addingTimeInterval(timeout)
    var buffer = [UInt8](repeating: 0, count: 16 << 10)

    while true {
        if !exited {
            let r = waitpid(pid, &status, WNOHANG)
            if r == pid || (r < 0 && errno != EINTR) { exited = true }
        }
        if exited && readEnds.allSatisfy({ $0 < 0 }) { break }
        let now = Date()
        if killAt == nil, now >= termAt || call.cancelled {
            cancelled = call.cancelled
            timedOut = !cancelled
            kill(-pid, SIGTERM)
            killAt = now.addingTimeInterval(2)
        }
        if let at = killAt, giveUpAt == nil, now >= at {
            kill(-pid, SIGKILL)
            // Something outside the group may still hold the pipes; stop reading soon after.
            giveUpAt = now.addingTimeInterval(1)
        }
        if let at = giveUpAt, now >= at { break }

        var fds = readEnds.filter { $0 >= 0 }.map { pollfd(fd: $0, events: Int16(POLLIN), revents: 0) }
        if fds.isEmpty {
            usleep(20_000) // Output closed; waiting for the exit.
            continue
        }
        let ready = poll(&fds, nfds_t(fds.count), 50)
        if ready <= 0 { continue }
        for entry in fds where entry.revents != 0 {
            let n = read(entry.fd, &buffer, buffer.count)
            if n < 0 && errno == EINTR { continue }
            if n <= 0 {
                close(entry.fd)
                if let i = readEnds.firstIndex(of: entry.fd) { readEnds[i] = -1 }
                continue
            }
            let bytes = Data(buffer[0..<n])
            if entry.fd == outPipe[0] { out.append(bytes) } else { err.append(bytes) }
        }
    }
    if !exited {
        // Only after KILL to its group, so this returns.
        while waitpid(pid, &status, 0) < 0 && errno == EINTR {}
    }
    if cancelled {
        throw AgentFailure("cancelled", "sh was cancelled by the daemon; the script was stopped")
    }
    // wait(2)'s status: the low 7 bits are the signal that ended it, else the exit code is above.
    let signal = status & 0x7f
    let code = signal == 0 ? Int((status >> 8) & 0xff) : 128 + Int(signal)
    return (out.text, err.text, code, timedOut)
}
