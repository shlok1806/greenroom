import CoreGraphics
import Foundation

private let view = CGRect(x: 120, y: 160, width: 400, height: 300)

func testAScrollBarsValueIsThePosition() {
    let middle = scrollPosition(vertical: 0.62, horizontal: nil, view: view, content: nil)
    expectEqual(middle, ScrollPosition(x: nil, y: 0.62, up: true, down: true))
    let top = scrollPosition(vertical: 0, horizontal: nil, view: view, content: nil)
    expectEqual(top, ScrollPosition(y: 0, down: true), "at the top it can only go down")
    let bottom = scrollPosition(vertical: 1, horizontal: nil, view: view, content: nil)
    expectEqual(bottom, ScrollPosition(y: 1, up: true), "at the bottom it can only go up")
    // A scroll view rests a hair off its end, and a bar can say a little more than 1.
    let resting = scrollPosition(vertical: 0.9995, horizontal: 1.2, view: view, content: nil)
    expectEqual(resting, ScrollPosition(x: 1, y: 0.9995, up: true, left: true))
    expectEqual(scrollPosition(vertical: .nan, horizontal: nil, view: view, content: nil), ScrollPosition(), "a value that is no number")
}

func testWithoutScrollBarsTheContentsFrameIsThePosition() {
    // 900 points of content in a 300 point view, scrolled 150 down: a quarter of the way.
    let content = CGRect(x: 120, y: 10, width: 400, height: 900)
    let position = scrollPosition(vertical: nil, horizontal: nil, view: view, content: content)
    expectEqual(position, ScrollPosition(y: 0.25, up: true, down: true))
    expect(position.scrolls, "it scrolls")

    let atTop = scrollPosition(vertical: nil, horizontal: nil, view: view, content: CGRect(x: 120, y: 160, width: 400, height: 900))
    expectEqual(atTop, ScrollPosition(y: 0, down: true))
    let atEnd = scrollPosition(vertical: nil, horizontal: nil, view: view, content: CGRect(x: 120, y: -440, width: 400, height: 900))
    expectEqual(atEnd, ScrollPosition(y: 1, up: true))

    // Wider than the view too: both axes scroll.
    let wide = scrollPosition(vertical: nil, horizontal: nil, view: view, content: CGRect(x: -80, y: 160, width: 800, height: 900))
    expectEqual(wide, ScrollPosition(x: 0.5, y: 0, down: true, left: true, right: true))
}

func testContentThatFitsDoesNotScroll() {
    let fits = CGRect(x: 120, y: 160, width: 400, height: 200)
    expectEqual(scrollPosition(vertical: nil, horizontal: nil, view: view, content: fits), ScrollPosition())
    // A scroll bar left over from longer content says nothing once the content fits.
    expectEqual(scrollPosition(vertical: 0.5, horizontal: nil, view: view, content: fits), ScrollPosition())
    expect(!scrollPosition(vertical: nil, horizontal: nil, view: view, content: nil).scrolls, "nothing is known")
}

func testTheScrollBarWinsOverTheRowsALazyListHasMade() {
    // A list of thousands of rows that has made only those near its view: the content's frame
    // starts at the view, the scroll bar says it is half way.
    let made = CGRect(x: 120, y: 160, width: 400, height: 600)
    expectEqual(scrollPosition(vertical: 0.5, horizontal: nil, view: view, content: made),
                ScrollPosition(y: 0.5, up: true, down: true))
}

func testAScrollAreasContentIsTheUnionOfItsChildren() {
    let rows = [
        CGRect(x: 120, y: 100, width: 400, height: 40),
        CGRect(x: 120, y: 140, width: 380, height: 40),
        CGRect(x: 120, y: 900, width: 400, height: 40),
        CGRect(x: 0, y: 0, width: 0, height: 0),
        CGRect(x: CGFloat.nan, y: 0, width: 10, height: 10),
    ]
    expectEqual(contentFrame(of: rows), CGRect(x: 120, y: 100, width: 400, height: 840))
    expect(contentFrame(of: []) == nil, "no children")
}
