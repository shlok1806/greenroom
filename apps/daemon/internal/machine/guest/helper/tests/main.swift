// Host tests for logic/ (daemon ADR 0005), run by swift_test.go. Each test is a function in a
// file of this directory, listed in `tests` below; `expect` records a failure without stopping.

import Foundation

var failures: [String] = []
var current = ""

func expect(_ condition: @autoclosure () -> Bool, _ message: @autoclosure () -> String,
            file: String = #fileID, line: Int = #line) {
    if !condition() {
        failures.append("\(current): \(file):\(line): \(message())")
    }
}

func expectEqual<T: Equatable>(_ got: T, _ want: T, _ what: String = "", file: String = #fileID, line: Int = #line) {
    if got != want {
        failures.append("\(current): \(file):\(line): \(what.isEmpty ? "" : what + ": ")got \(got), want \(want)")
    }
}

let tests: [(String, () -> Void)] = [
    ("cut keeps a short string whole", testCutKeepsAShortStringWhole),
    ("cut marks a long string", testCutMarksALongString),
    ("a frame round trips", testAFrameRoundTrips),
    ("a frame over the limit is refused before its payload", testAFrameOverTheLimitIsRefusedBeforeItsPayload),
    ("a truncated frame throws", testATruncatedFrameThrows),
    ("frame types match the ADR", testFrameTypesMatchTheADR),
    ("a blob splits into numbered chunks", testABlobSplitsIntoNumberedChunks),
    ("a blob of exactly one chunk and an empty blob", testABlobOfExactlyOneChunkAndAnEmptyBlob),
    ("a request decodes", testARequestDecodes),
    ("a request without args has an empty object", testARequestWithoutArgsHasAnEmptyObject),
    ("a malformed request is answered when it has an id", testAMalformedRequestIsAnsweredWhenItHasAnID),
    ("deadlines are bounded", testDeadlinesAreBounded),
    ("only the ADR's codes retry", testOnlyTheADRsCodesRetry),
    ("a decoding error names the field", testADecodingErrorNamesTheField),
    ("a rect argument needs four numbers and a size", testARectArgumentNeedsFourNumbersAndASize),
    ("a region becomes pixels rounded outward", testARegionBecomesPixelsRoundedOutward),
    ("a region is clipped to the image", testARegionIsClippedToTheImage),
    ("an image is scaled down to maxWidth only", testAnImageIsScaledDownToMaxWidthOnly),
    ("a stall is reported once and its recovery", testAStallIsReportedOnceAndItsRecovery),
    ("kinds stall apart", testKindsStallApart),
    ("a stall lasts until its longest work ends", testAStallLastsUntilItsLongestWorkEnds),
    ("a tail buffer keeps the end", testATailBufferKeepsTheEnd),
]

for (name, test) in tests {
    current = name
    test()
}
for failure in failures {
    print("FAIL \(failure)")
}
if failures.isEmpty {
    print("\(tests.count) tests passed")
    exit(0)
}
print("\(failures.count) failures in \(tests.count) tests")
exit(1)
