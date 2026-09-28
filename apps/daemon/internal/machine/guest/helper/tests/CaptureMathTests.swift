import CoreGraphics
import Foundation

func testARectArgumentNeedsFourNumbersAndASize() {
    expectEqual(rectArgument([10, 20, 30, 40]), CGRect(x: 10, y: 20, width: 30, height: 40))
    expect(rectArgument([10, 20, 30]) == nil, "three numbers")
    expect(rectArgument([10, 20, 0, 40]) == nil, "zero width")
    expect(rectArgument([10, 20, 30, -1]) == nil, "negative height")
    expect(rectArgument([.nan, 20, 30, 40]) == nil, "not finite")
}

func testARegionBecomesPixelsRoundedOutward() {
    // 2 pixels a point: a region at fractional points still covers its edges.
    expectEqual(pixelRect(CGRect(x: 10.25, y: 5.5, width: 20, height: 10), scale: 2, width: 3000, height: 2000),
                CGRect(x: 20, y: 11, width: 41, height: 20))
    expectEqual(pixelRect(CGRect(x: 0, y: 0, width: 100, height: 50), scale: 1, width: 1024, height: 768),
                CGRect(x: 0, y: 0, width: 100, height: 50), "scale 1")
}

func testARegionIsClippedToTheImage() {
    expectEqual(pixelRect(CGRect(x: 1000, y: 700, width: 100, height: 100), scale: 1, width: 1024, height: 768),
                CGRect(x: 1000, y: 700, width: 24, height: 68), "clipped at the corner")
    expect(pixelRect(CGRect(x: 2000, y: 0, width: 10, height: 10), scale: 1, width: 1024, height: 768) == nil, "off the image")
    expect(pixelRect(CGRect(x: 0, y: 0, width: 10, height: 10), scale: 0, width: 1024, height: 768) == nil, "no scale")
}

func testAnImageIsScaledDownToMaxWidthOnly() {
    expectEqual(fittedSize(width: 2048, height: 1536, maxWidth: 1024).width, 1024)
    expectEqual(fittedSize(width: 2048, height: 1536, maxWidth: 1024).height, 768)
    expectEqual(fittedSize(width: 800, height: 600, maxWidth: 1024).width, 800, "never scaled up")
    expectEqual(fittedSize(width: 800, height: 600, maxWidth: nil).width, 800, "no maxWidth")
    expectEqual(fittedSize(width: 3000, height: 1, maxWidth: 10).height, 1, "never below a pixel")
}
