// The guest agent's requests (daemon ADR 0005 points 4 to 6 and 14): the op registry, the call
// a handler sees, and the dispatcher that runs each call on its queue and answers it exactly
// once. Ops register here and nothing else changes: see AgentOps.swift's registerAgentOps.

import Foundation

/// An error an op answers with. `code` is one of the wire's codes (logic/Requests.swift);
/// `message` is what the daemon passes on to a model, so it says what to do next.
struct AgentFailure: Error {
    let code: String
    let message: String
    let retryable: Bool
    var detail: [String: Any]?

    init(_ code: String, _ message: String, retryable: Bool? = nil, detail: [String: Any]? = nil) {
        self.code = code
        self.message = message
        self.retryable = retryable ?? retryableByDefault(code)
        self.detail = detail
    }
}

/// logic/'s `retryable`, under a name AgentFailure's init cannot shadow.
private func retryableByDefault(_ code: String) -> Bool { retryable(code) }

/// Where an op runs.
enum OpKind {
    /// On a serial queue per key, so a hung app blocks only reads of it. `queueKey` runs off
    /// the reader thread and may ask AX which app is frontmost (see `appQueueKey`).
    case read(queueKey: (Call) -> String)
    /// On the one input queue: a desktop has one pointer and one keyboard. PAUSE refuses it.
    case input
    /// On the one capture queue.
    case capture
    /// Concurrently: a script the daemon wrote, bounded by its own timeout.
    case shell
}

struct Op {
    let kind: OpKind
    let handler: (Call) throws -> [String: Any]
}

private let registryLock = NSLock()
private var registry: [String: Op] = [:]

/// Adds an op. Called before HELLO (registerAgentOps), so `caps` lists it. A handler returns
/// its result object (JSON types only), attaches an image with `call.blob`, calls
/// `call.check()` at every wait point, and throws AgentFailure to answer an error.
func register(_ name: String, _ kind: OpKind, _ handler: @escaping (Call) throws -> [String: Any]) {
    registryLock.lock()
    defer { registryLock.unlock() }
    precondition(registry[name] == nil, "op \(name) registered twice")
    registry[name] = Op(kind: kind, handler: handler)
}

func registeredOp(_ name: String) -> Op? {
    registryLock.lock()
    defer { registryLock.unlock() }
    return registry[name]
}

/// Every registered op, sorted: HELLO's `caps`.
func registeredOps() -> [String] {
    registryLock.lock()
    defer { registryLock.unlock() }
    return registry.keys.sorted()
}

// MARK: - The call

/// One request as its handler sees it.
final class Call {
    let id: Int
    let op: String
    /// The seat whose refs the request uses: `coder`, `verifier` or `human`.
    let reader: String
    /// True for an op that posts events or changes a value: PAUSE refuses it.
    let input: Bool
    let received: DispatchTime
    let deadline: DispatchTime
    private let rawArgs: Data

    private let lock = NSLock()
    private var cancelFlag = false
    private var startedFlag = false
    private var answeredFlag = false
    private var attached: (data: Data, mime: String)?

    init(_ envelope: RequestEnvelope, input: Bool) {
        id = envelope.id
        op = envelope.op
        reader = envelope.reader
        self.input = input
        rawArgs = envelope.args
        received = .now()
        deadline = received + .milliseconds(envelope.deadlineMs)
    }

    /// The request's `args` decoded as T; a mismatch is `bad_request` naming the field.
    func args<T: Decodable>(_: T.Type) throws -> T {
        do {
            return try JSONDecoder().decode(T.self, from: rawArgs)
        } catch {
            throw AgentFailure("bad_request", "\(op): \(describeDecodingError(error))")
        }
    }

    var cancelled: Bool {
        lock.lock()
        defer { lock.unlock() }
        return cancelFlag
    }

    /// Seconds left before the deadline, never negative.
    var remaining: TimeInterval {
        let now = DispatchTime.now().uptimeNanoseconds
        let end = deadline.uptimeNanoseconds
        return end > now ? Double(end - now) / 1e9 : 0
    }

    /// Milliseconds since the request arrived.
    var elapsedMs: Int {
        Int((DispatchTime.now().uptimeNanoseconds - received.uptimeNanoseconds) / 1_000_000)
    }

    /// Throws when the call should stop here: CANCEL arrived, the deadline passed, or (for an
    /// input) the screen is paused for another holder. Call it at every wait point; an input
    /// already posted is never undone, so a handler that stops says what it had done.
    func check() throws {
        if cancelled {
            throw AgentFailure("cancelled", "\(op) was cancelled by the daemon")
        }
        if remaining <= 0 {
            throw AgentFailure("deadline", "\(op) ran past its deadline; try again, with a longer timeout if it allows one")
        }
        if input, let holder = pauseHolder(), holder != reader {
            throw pausedFailure(holder)
        }
    }

    /// Attaches the image the RESPONSE carries; its BLOB frames go out just before it, and not
    /// at all if the call is answered some other way (a deadline, a cancel).
    func blob(_ data: Data, mime: String) {
        lock.lock()
        defer { lock.unlock() }
        attached = (data, mime)
    }

    // The dispatcher's side.

    func cancel() -> (started: Bool, answered: Bool) {
        lock.lock()
        defer { lock.unlock() }
        cancelFlag = true
        return (startedFlag, answeredFlag)
    }

    /// Marks the call started, or false when it was answered already (cancelled or refused while
    /// it queued, or past its deadline) and the handler must not run.
    func start() -> Bool {
        lock.lock()
        defer { lock.unlock() }
        if answeredFlag { return false }
        startedFlag = true
        return true
    }

    var started: Bool {
        lock.lock()
        defer { lock.unlock() }
        return startedFlag
    }

    /// Claims the one answer, with the attached image; nil when it was answered already.
    func claimAnswer() -> (data: Data, mime: String)?? {
        lock.lock()
        defer { lock.unlock() }
        if answeredFlag { return nil }
        answeredFlag = true
        return .some(attached)
    }
}

// MARK: - Pause (daemon ADR 0005 point 14)

private let pauseLock = NSLock()
private var pausedBy: String?

func pauseHolder() -> String? {
    pauseLock.lock()
    defer { pauseLock.unlock() }
    return pausedBy
}

func pausedFailure(_ holder: String) -> AgentFailure {
    AgentFailure("paused", "the screen is taken by \(holder); inputs wait until it is given back", detail: ["holder": holder])
}

// MARK: - Dispatch

private let pendingLock = NSLock()
private var pending: [Int: Call] = [:]

private let inputQueue = DispatchQueue(label: "greenroom.agent.input")
private let captureQueue = DispatchQueue(label: "greenroom.agent.capture")
private let shellQueue = DispatchQueue(label: "greenroom.agent.shell", attributes: .concurrent)
/// Where a read's queue key is computed: never the reader thread, which must answer PING.
private let routeQueue = DispatchQueue(label: "greenroom.agent.route", attributes: .concurrent)
private let timerQueue = DispatchQueue(label: "greenroom.agent.deadlines")

private let readQueuesLock = NSLock()
private var readQueues: [String: DispatchQueue] = [:]

private func readQueue(_ key: String) -> DispatchQueue {
    readQueuesLock.lock()
    defer { readQueuesLock.unlock() }
    if let queue = readQueues[key] { return queue }
    let queue = DispatchQueue(label: "greenroom.agent.read.\(key)")
    readQueues[key] = queue
    return queue
}

/// Takes one REQUEST from the reader thread. It only routes: every op runs elsewhere.
func handleRequest(_ payload: Data) {
    let envelope: RequestEnvelope
    do {
        envelope = try parseRequest(payload)
    } catch EnvelopeError.bad(let id, let message) {
        answerUnqueued(id: id, AgentFailure("bad_request", message))
        return
    } catch {
        agentLog("dropped a request the agent cannot answer: \(error)")
        return
    }
    guard let op = registeredOp(envelope.op) else {
        answerUnqueued(id: envelope.id, AgentFailure(
            "unknown_op", "this guest agent has no op \(envelope.op); it answers \(registeredOps().joined(separator: ", "))"))
        return
    }
    var isInput = envelope.input
    if case .input = op.kind { isInput = true }
    let call = Call(envelope, input: isInput)

    pendingLock.lock()
    if pending[call.id] != nil {
        pendingLock.unlock()
        // Answering would give the id two RESPONSEs; the first call keeps it.
        agentLog("dropped request \(call.id): that id is still running")
        return
    }
    pending[call.id] = call
    pendingLock.unlock()

    // The dispatcher's own deadline: a handler stuck past it is answered for, and its late
    // result dropped.
    timerQueue.asyncAfter(deadline: call.deadline + .milliseconds(deadlineGraceMs)) {
        finish(call, .failure(AgentFailure(
            "deadline", "\(call.op) did not finish within its deadline of \(envelope.deadlineMs) ms; try again")))
    }

    if call.input, let holder = pauseHolder(), holder != call.reader {
        finish(call, .failure(pausedFailure(holder)))
        return
    }

    switch op.kind {
    case let .read(queueKey):
        routeQueue.async {
            readQueue(queueKey(call)).async { run(call, op.handler) }
        }
    case .input:
        inputQueue.async { run(call, op.handler) }
    case .capture:
        captureQueue.async { run(call, op.handler) }
    case .shell:
        shellQueue.async { run(call, op.handler) }
    }
}

/// CANCEL: a queued call is answered at once; a running one stops at its next check().
func handleCancel(_ payload: Data) {
    guard let body = (try? JSONSerialization.jsonObject(with: payload)) as? [String: Any],
          let id = (body["id"] as? NSNumber).flatMap({ Int(exactly: $0.doubleValue) })
    else { return }
    pendingLock.lock()
    let call = pending[id]
    pendingLock.unlock()
    // A call that already answered is gone from pending; its CANCEL has nothing to do.
    guard let call else { return }
    if !call.cancel().started {
        finish(call, .failure(AgentFailure("cancelled", "\(call.op) was cancelled by the daemon before it started")))
    }
}

/// PAUSE {holder}: inputs of every other reader are refused, queued ones at once and running
/// ones at their next check(). Reads go on.
func handlePause(_ payload: Data) {
    let body = (try? JSONSerialization.jsonObject(with: payload)) as? [String: Any]
    let holder = body?["holder"] as? String ?? ""
    pauseLock.lock()
    pausedBy = holder
    pauseLock.unlock()
    pendingLock.lock()
    let waiting = pending.values.filter { $0.input && $0.reader != holder && !$0.started }
    pendingLock.unlock()
    for call in waiting {
        finish(call, .failure(pausedFailure(holder)))
    }
}

func handleResume() {
    pauseLock.lock()
    pausedBy = nil
    pauseLock.unlock()
}

private func run(_ call: Call, _ handler: (Call) throws -> [String: Any]) {
    guard call.start() else { return }
    do {
        try call.check()
        finish(call, .success(try handler(call)))
    } catch let failure as AgentFailure {
        finish(call, .failure(failure))
    } catch let failure as Failure {
        finish(call, .failure(AgentFailure("internal", "\(call.op): \(failure.message)")))
    } catch {
        finish(call, .failure(AgentFailure("internal", "\(call.op): \(error)")))
    }
}

/// Sends the one RESPONSE of a call (with its BLOBs first) unless it was answered already.
private func finish(_ call: Call, _ outcome: Result<[String: Any], AgentFailure>) {
    guard let attached = call.claimAnswer() else { return }
    pendingLock.lock()
    pending[call.id] = nil
    pendingLock.unlock()

    var outcome = outcome
    if case let .success(result) = outcome, !JSONSerialization.isValidJSONObject(result) {
        outcome = .failure(AgentFailure("internal", "\(call.op) produced a result that is not JSON"))
    }
    var response: [String: Any] = ["id": call.id, "ms": call.elapsedMs]
    var blob: (data: Data, mime: String)?
    switch outcome {
    case let .success(result):
        response["ok"] = true
        response["result"] = result
        if let attached {
            blob = attached
            response["blob"] = ["bytes": attached.data.count, "mime": attached.mime]
        }
    case let .failure(failure):
        response["ok"] = false
        response["error"] = errorObject(failure)
    }
    guard var payload = jsonData(response) else {
        answerUnqueued(id: call.id, AgentFailure("internal", "\(call.op) produced a result that cannot be encoded"))
        return
    }
    if payload.count > maxAgentPayload {
        blob = nil
        payload = jsonData([
            "id": call.id, "ok": false, "ms": call.elapsedMs,
            "error": errorObject(AgentFailure("internal", "\(call.op) produced a result over 4 MiB; ask for less (a limit, a smaller region)")),
        ]) ?? Data()
    }
    if let blob {
        for chunk in blobChunks(id: UInt32(truncatingIfNeeded: call.id), data: blob.data) {
            send(AgentFrame.blob, chunk)
        }
    }
    send(AgentFrame.response, payload)
}

/// Answers a request that never became a pending call (malformed, unknown op).
private func answerUnqueued(id: Int, _ failure: AgentFailure) {
    let body: [String: Any] = ["id": id, "ok": false, "ms": 0, "error": errorObject(failure)]
    if let payload = jsonData(body) { send(AgentFrame.response, payload) }
}

private func errorObject(_ failure: AgentFailure) -> [String: Any] {
    var error: [String: Any] = ["code": failure.code, "message": failure.message, "retryable": failure.retryable]
    if let detail = failure.detail, JSONSerialization.isValidJSONObject(detail) { error["detail"] = detail }
    return error
}

/// JSON bytes of an object, or nil when it holds something JSON cannot say (which would
/// otherwise raise an Objective-C exception and kill the agent).
func jsonData(_ object: [String: Any]) -> Data? {
    guard JSONSerialization.isValidJSONObject(object) else { return nil }
    return try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
}
