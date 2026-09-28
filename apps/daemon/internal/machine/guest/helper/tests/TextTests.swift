import Foundation

func testCutKeepsAShortStringWhole() {
    let (text, wasCut) = cut("Each pays")
    expectEqual(text, "Each pays")
    expectEqual(wasCut, false)
}

func testCutMarksALongString() {
    let long = String(repeating: "a", count: textLimit + 5)
    let (text, wasCut) = cut(long)
    expectEqual(text.count, textLimit, "cut length")
    expectEqual(wasCut, true)
}
