import Foundation

func testARequestDecodes() {
    let payload = Data(#"{"id":12,"op":"ui","args":{"app":"TipSplit","limit":40},"deadlineMs":8000,"reader":"verifier","input":false}"#.utf8)
    guard let request = try? parseRequest(payload) else {
        expect(false, "the request did not decode")
        return
    }
    expectEqual(request.id, 12)
    expectEqual(request.op, "ui")
    expectEqual(request.deadlineMs, 8000)
    expectEqual(request.reader, "verifier")
    expectEqual(request.input, false)
    let args = try? JSONSerialization.jsonObject(with: request.args) as? [String: Any]
    expectEqual(args?["app"] as? String, "TipSplit", "args.app")
    expectEqual(args?["limit"] as? Int, 40, "args.limit")
}

func testARequestWithoutArgsHasAnEmptyObject() {
    let request = try? parseRequest(Data(#"{"id":1,"op":"screen","deadlineMs":1000}"#.utf8))
    expectEqual(request.map { String(decoding: $0.args, as: UTF8.self) }, "{}")
    expectEqual(request?.input, false, "input defaults to false")
    let null = try? parseRequest(Data(#"{"id":1,"op":"screen","args":null}"#.utf8))
    expectEqual(null.map { String(decoding: $0.args, as: UTF8.self) }, "{}", "null args")
}

func testAMalformedRequestIsAnsweredWhenItHasAnID() {
    func outcome(_ json: String) -> EnvelopeError? {
        do {
            _ = try parseRequest(Data(json.utf8))
            return nil
        } catch {
            return error as? EnvelopeError
        }
    }
    expectEqual(outcome(#"{"id":3,"args":{}}"#), .bad(id: 3, message: "the request names no op"))
    expectEqual(outcome(#"{"id":3,"op":"ui","args":[1]}"#), .bad(id: 3, message: "args must be a JSON object"))
    expectEqual(outcome(#"{"op":"ui"}"#), .unanswerable("a REQUEST without an integer id"))
    expectEqual(outcome(#"{"id":true,"op":"ui"}"#), .unanswerable("a REQUEST without an integer id"))
    expectEqual(outcome(#"{"id":1.5,"op":"ui"}"#), .unanswerable("a REQUEST without an integer id"))
    expectEqual(outcome("[1]"), .unanswerable("a REQUEST that is not a JSON object"))
    expectEqual(outcome("not json"), .unanswerable("a REQUEST that is not a JSON object"))
}

func testDeadlinesAreBounded() {
    expectEqual(clampDeadlineMs(nil), maxDeadlineMs, "missing")
    expectEqual(clampDeadlineMs(0), maxDeadlineMs, "zero")
    expectEqual(clampDeadlineMs(-5), maxDeadlineMs, "negative")
    expectEqual(clampDeadlineMs(2500), 2500, "in range")
    expectEqual(clampDeadlineMs(120_000), maxDeadlineMs, "over the cap")
    expectEqual(maxDeadlineMs, 45_000, "the ADR's cap")
    expectEqual((try? parseRequest(Data(#"{"id":1,"op":"x","deadlineMs":90000}"#.utf8)))?.deadlineMs, maxDeadlineMs, "clamped on decode")
}

func testOnlyTheADRsCodesRetry() {
    expectEqual(agentErrorCodes.filter { $0.value }.keys.sorted(), ["capture_failed", "deadline", "not_responding"])
    expectEqual(retryable("unknown code"), false, "an unknown code")
    expectEqual(agentErrorCodes.keys.sorted(), [
        "ambiguous", "ax_error", "bad_request", "cancelled", "capture_failed", "deadline", "internal",
        "not_found", "not_responding", "not_trusted", "paused", "refused", "stale_ref", "unknown_op",
    ], "every ADR code")
}

func testADecodingErrorNamesTheField() {
    struct Args: Decodable {
        struct Item: Decodable { var type: String }
        var format: String
        var items: [Item]?
    }
    func message(_ json: String) -> String {
        do {
            _ = try JSONDecoder().decode(Args.self, from: Data(json.utf8))
            return ""
        } catch {
            return describeDecodingError(error)
        }
    }
    expectEqual(message(#"{}"#), "args.format is required")
    expectEqual(message(#"{"format":3}"#), "args.format should be a string")
    expectEqual(message(#"{"format":null}"#), "args.format should be a string, not null")
    expectEqual(message(#"{"format":"png","items":[{"type":1}]}"#), "args.items[0].type should be a string")
}
