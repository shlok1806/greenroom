import CoreGraphics
import Foundation

private func found(_ text: String, in field: String) -> Bool {
    guard let pattern = try? textPattern(text) else { return false }
    return matches(pattern, field)
}

func testPlainTextIsFoundAnywhereWhateverTheCase() {
    expect(found("open run", in: "Open run"), "another case")
    expect(found("run", in: "Open run 12"), "a part")
    expect(!found("runs", in: "Open run"), "not there")
    expect(!found("run", in: ""), "an empty field holds nothing")
    // Characters a regular expression would read are plain here.
    expect(found("$15.12", in: "Tip: $15.12"), "a price")
    expect(!found("$15.12", in: "Tip: $15112"), "the dot is a dot")
    expect(found("a/b", in: "path a/b/c"), "a slash inside")
    expect(found("/", in: "a/b"), "a lone slash")
    expect(found("//", in: "https://example.com"), "two slashes are too short to hold a pattern")
    expect(found("/usr", in: "/usr/bin"), "a path that does not end with a slash")
}

func testTextBetweenSlashesIsARegularExpression() {
    expect(found("/^Run \\d+$/", in: "Run 12"), "anchors and classes")
    expect(!found("/^Run \\d+$/", in: "Open Run 12"), "anchored")
    expect(!found("/run/", in: "Run 12"), "case sensitive as written")
    expect(found("/run/i", in: "Run 12"), "i ignores case")
    expect(found("/\\$\\d+\\.\\d{2}/", in: "Total $24.00 each"), "escapes")
}

func testABadRegularExpressionSaysSo() {
    for bad in ["/(/", "/[a-/", "/*/"] {
        do {
            _ = try textPattern(bad)
            expect(false, "\(bad) was accepted")
        } catch let error as PatternError {
            expect(error.message.contains("not a regular expression"), "\(bad): \(error.message)")
            expect(error.message.contains("plain text"), "\(bad) says what to do instead")
        } catch {
            expect(false, "\(bad) threw \(error)")
        }
    }
    do {
        _ = try textPattern("//i")
        expect(false, "an empty pattern was accepted")
    } catch let error as PatternError {
        expect(error.message.contains("empty"), error.message)
    } catch {
        expect(false, "an empty pattern threw \(error)")
    }
}

func testARoleMatchesWithOrWithoutItsPrefix() {
    expect(roleMatches(wanted: "Button", role: "AXButton"), "the wire's spelling")
    expect(roleMatches(wanted: "AXButton", role: "AXButton"), "AX's spelling")
    expect(roleMatches(wanted: "button", role: "AXButton"), "another case")
    expect(roleMatches(wanted: " Button ", role: "AXButton"), "white space around it")
    expect(!roleMatches(wanted: "Button", role: "AXRadioButton"), "a whole role, not a part")
    expect(roleMatches(wanted: nil, role: "AXButton") && roleMatches(wanted: "", role: "AXGroup"), "no role asked for")
}

func testASignatureChangesWithWhatItRead() {
    func signature(_ build: (inout Signature) -> Void) -> UInt64 {
        var s = Signature()
        build(&s)
        return s.value
    }
    let frame = CGRect(x: 10, y: 20, width: 100, height: 24)
    let base = signature { $0.add("AXButton"); $0.add("Save"); $0.add(frame); $0.add(true) }
    expectEqual(signature { $0.add("AXButton"); $0.add("Save"); $0.add(frame); $0.add(true) }, base, "the same reads")
    expect(signature { $0.add("AXButton"); $0.add("Saved"); $0.add(frame); $0.add(true) } != base, "another name")
    expect(signature { $0.add("AXButton"); $0.add("Save"); $0.add(frame.offsetBy(dx: 0, dy: 1)); $0.add(true) } != base, "it moved")
    expect(signature { $0.add("AXButton"); $0.add("Save"); $0.add(frame); $0.add(false) } != base, "another state")
    expect(signature { $0.add("AXButton"); $0.add("Save"); $0.add(frame); $0.add(nil as Bool?) } != base, "no state")
    expect(signature { $0.add("AXButtonS"); $0.add("ave"); $0.add(frame); $0.add(true) } != base, "where one string ends matters")
    // Layout that jitters by a fraction of a point is not a change.
    expectEqual(signature { $0.add("AXButton"); $0.add("Save"); $0.add(frame.offsetBy(dx: 0.1, dy: 0)); $0.add(true) }, base)
    expect(signature { $0.add(nil as CGRect?) } != signature { $0.add(CGRect.zero) }, "no frame is not an empty one")
    // FNV-1a's value for nothing read: the same in every process.
    expectEqual(Signature().value, 0xcbf2_9ce4_8422_2325)
}
