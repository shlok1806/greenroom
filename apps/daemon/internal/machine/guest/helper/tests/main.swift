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
