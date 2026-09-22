import XCTest

@testable import Companion

/// A daemon that records what it was asked and can hold `input` open, the
/// way a slow guest does.
private actor FakeControlClient: ControlClient {
    enum Call: Equatable {
        case take
        case release
        case input([InputAction])
    }

    private(set) var calls: [Call] = []
    var takeAnswers: [Result<ControlResponse, DaemonError>] = []
    var inputError: DaemonError?
    private var holdInput = false
    private var holdTake = false
    private var held: [CheckedContinuation<Void, Never>] = []

    static let lease = ControlResponse(
        control: ControlLease(holder: "human", since: Date(), expires: Date().addingTimeInterval(60), actions: 0),
        screen: GuestScreen(width: 1024, height: 768)
    )

    func answerTakes(_ answers: [Result<ControlResponse, DaemonError>]) { takeAnswers = answers }
    func failInput(_ error: DaemonError) { inputError = error }
    func hold() { holdInput = true }
    func holdTakes() { holdTake = true }

    func letGo() {
        holdInput = false
        holdTake = false
        held.forEach { $0.resume() }
        held = []
    }

    var inputs: [[InputAction]] {
        calls.compactMap { if case .input(let batch) = $0 { batch } else { nil } }
    }

    var waiting: Int { held.count }

    func takeControl(runId: String) async throws -> ControlResponse {
        calls.append(.take)
        if holdTake {
            await withCheckedContinuation { held.append($0) }
        }
        guard !takeAnswers.isEmpty else { return Self.lease }
        return try takeAnswers.removeFirst().get()
    }

    func releaseControl(runId: String) async throws {
        calls.append(.release)
    }

    func input(runId: String, actions: [InputAction]) async throws -> InputResult {
        calls.append(.input(actions))
        if holdInput {
            await withCheckedContinuation { held.append($0) }
        }
        if let inputError { throw inputError }
        return InputResult(actions: actions.count, screen: GuestScreen(width: 1024, height: 768))
    }
}

@MainActor
private final class FakeHost: PilotHost {
    var errors: [String] = []
    var cleared = 0

    func report(_ error: Error) {
        errors.append((error as? LocalizedError)?.errorDescription ?? "\(error)")
    }

    func clearError() { cleared += 1 }
    var reloadedRuns: [String] = []

    func reloadTranscript(_ runId: String) async {}
    func reloadRun(_ runId: String) async { reloadedRuns.append(runId) }
}

@MainActor
final class ControlPilotTests: XCTestCase {
    private let client = FakeControlClient()
    private let host = FakeHost()

    private func pilot(renewal: Duration = .seconds(60)) -> ControlPilot {
        ControlPilot(runId: "run-1", client: client, host: host, renewal: renewal)
    }

    private func until(_ condition: () async -> Bool, file: StaticString = #filePath, line: UInt = #line) async {
        for _ in 0..<500 {
            if await condition() { return }
            try? await Task.sleep(for: .milliseconds(2))
        }
        XCTFail("timed out", file: file, line: line)
    }

    private func down(_ x: Double) -> InputAction { InputAction(type: .down, x: x, y: 0.5, button: "left", clicks: 1) }
    private func move(_ x: Double) -> InputAction { InputAction(type: .move, x: x, y: 0.5) }

    func testAReleaseWhileABatchIsInFlightStillLetsGoOfTheButton() async {
        let pilot = pilot()
        await pilot.take()
        XCTAssertTrue(pilot.active)

        await client.hold()
        pilot.send([down(0.1)])
        await until { await self.client.waiting == 1 }
        pilot.send([move(0.2)])

        let releasing = Task { await pilot.release() }
        await client.letGo()
        await releasing.value

        let calls = await client.calls
        XCTAssertEqual(calls.first, .take)
        XCTAssertEqual(calls.last, .release)
        let inputs = await client.inputs
        XCTAssertEqual(inputs.first, [down(0.1)])
        // The queued move lands, then the button comes up where the pointer was.
        XCTAssertEqual(inputs.dropFirst().flatMap { $0 }, [
            move(0.2),
            InputAction(type: .up, x: 0.2, y: 0.5, button: "left", clicks: 1),
        ])
        XCTAssertFalse(pilot.active)
    }

    func testAReleaseWithNoButtonHeldSendsNoInput() async {
        let pilot = pilot()
        await pilot.take()
        await pilot.release()
        let calls = await client.calls
        XCTAssertEqual(calls, [.take, .release])
    }

    func testInputQueuedDuringARequestGoesAsOneCoalescedBatch() async {
        let pilot = pilot()
        await pilot.take()

        await client.hold()
        pilot.send([move(0.1)])
        await until { await self.client.waiting == 1 }
        pilot.send([move(0.2)])
        pilot.send([move(0.3)])
        pilot.send([InputAction(type: .type, text: "a")])
        pilot.send([InputAction(type: .type, text: "b")])
        await client.letGo()
        await until { await self.client.inputs.count == 2 }

        let inputs = await client.inputs
        XCTAssertEqual(inputs, [
            [move(0.1)],
            [move(0.3), InputAction(type: .type, text: "ab")],
        ])
    }

    func testAFailedRenewalStopsDrivingAndGivesTheScreenBack() async {
        let pilot = pilot(renewal: .milliseconds(50))
        await client.answerTakes([.success(FakeControlClient.lease), .failure(.status(code: 409, body: "machine is gone"))])
        await pilot.take()
        await client.hold()
        pilot.send([down(0.4)])
        await until { await self.client.waiting == 1 }
        pilot.send([move(0.5)])

        await until { !pilot.active }
        await client.letGo()
        await until { await self.client.calls.last == .release }

        XCTAssertEqual(host.errors, ["The daemon answered 409: machine is gone"])
        // The button is let go where the person last pointed, and nothing
        // queued before the failure is replayed.
        let inputs = await client.inputs
        XCTAssertEqual(inputs.last, [InputAction(type: .up, x: 0.5, y: 0.5, button: "left", clicks: 1)])
        XCTAssertFalse(inputs.contains([move(0.5)]))

        // Driving again after the failure needs a fresh take.
        pilot.send([move(0.6)])
        let count = await client.inputs.count
        XCTAssertEqual(count, inputs.count)
    }

    func testAMachineThatStoppedEndsControlWithAReasonAndRereadsTheRun() async {
        let pilot = pilot()
        await pilot.take()
        await client.failInput(.status(code: 409, body: "VM \"gr-run-1\" is not running"))
        pilot.send([move(0.1)])
        await until { self.host.reloadedRuns == ["run-1"] }

        XCTAssertFalse(pilot.active)
        let calls = await client.calls
        XCTAssertEqual(calls.last, .release)
        XCTAssertEqual(pilot.endedReason, "VM \"gr-run-1\" is not running")
        XCTAssertEqual(host.errors, ["The daemon answered 409: VM \"gr-run-1\" is not running"])

        // Taking control again starts with a clean slate.
        await pilot.take()
        XCTAssertNil(pilot.endedReason)
    }

    func testAPersonGivingControlBackIsNotAnEndedReason() async {
        let pilot = pilot()
        await pilot.take()
        await pilot.release()
        XCTAssertNil(pilot.endedReason)
        XCTAssertEqual(host.reloadedRuns, [])
    }

    func testAnAnswerWithoutALeaseIsARefusal() async {
        let pilot = pilot(renewal: .milliseconds(5))
        await client.answerTakes([.success(ControlResponse(control: nil, screen: nil))])
        await pilot.take()

        XCTAssertFalse(pilot.active)
        XCTAssertEqual(host.errors, [ControlError.refused.errorDescription!])
        // No heartbeat: nothing renews a lease that was never given.
        try? await Task.sleep(for: .milliseconds(50))
        let calls = await client.calls
        XCTAssertEqual(calls, [.take])
    }

    func testAReleaseDuringATakeGivesTheLeaseBackOnceItArrives() async {
        let pilot = pilot()
        await client.holdTakes()
        let taking = Task { await pilot.take() }
        await until { await self.client.waiting == 1 }
        await pilot.release()
        await client.letGo()
        await taking.value
        XCTAssertFalse(pilot.active)
        let calls = await client.calls
        XCTAssertEqual(calls, [.take, .release])
    }
}
