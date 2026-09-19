// Draws the companion's app icon and writes it as a PNG.
//
//     swift scripts/make-icon.swift out.png
//
// It exists so no binary artwork is checked in: `scripts/bundle.sh` runs this,
// then sips and iconutil turn the PNG into AppIcon.icns.

import AppKit
import CoreGraphics
import Foundation

let side = 1024

guard CommandLine.arguments.count == 2 else {
    FileHandle.standardError.write(Data("usage: make-icon.swift <out.png>\n".utf8))
    exit(2)
}
let output = URL(fileURLWithPath: CommandLine.arguments[1])

let space = CGColorSpace(name: CGColorSpace.sRGB)!
let image = NSImage(size: NSSize(width: side, height: side))
image.lockFocus()

guard let context = NSGraphicsContext.current?.cgContext else {
    FileHandle.standardError.write(Data("no drawing context\n".utf8))
    exit(1)
}

// macOS leaves the outer tenth of an icon as breathing room.
let inset = CGFloat(side) * 0.09
let square = CGRect(
    x: inset,
    y: inset,
    width: CGFloat(side) - inset * 2,
    height: CGFloat(side) - inset * 2
)
let corner = square.width * 0.22

// A green stage: lit at the top left, dark at the bottom right.
context.saveGState()
context.addPath(CGPath(roundedRect: square, cornerWidth: corner, cornerHeight: corner, transform: nil))
context.clip()
let gradient = CGGradient(
    colorsSpace: space,
    colors: [
        CGColor(srgbRed: 0.24, green: 0.76, blue: 0.45, alpha: 1),
        CGColor(srgbRed: 0.04, green: 0.35, blue: 0.25, alpha: 1),
    ] as CFArray,
    locations: [0, 1]
)!
context.drawLinearGradient(
    gradient,
    start: CGPoint(x: square.minX, y: square.maxY),
    end: CGPoint(x: square.maxX, y: square.minY),
    options: []
)
context.restoreGState()

// A white "G", drawn with the system font so it needs no bundled typeface.
let letter = "G" as NSString
let font = NSFont.systemFont(ofSize: square.width * 0.66, weight: .bold)
let attributes: [NSAttributedString.Key: Any] = [
    .font: font,
    .foregroundColor: NSColor.white,
]
let measured = letter.size(withAttributes: attributes)
// `draw(at:)` places the line box, not the glyph. Centring the cap height
// instead keeps the G optically in the middle rather than riding high.
letter.draw(
    at: NSPoint(
        x: square.midX - measured.width / 2,
        y: square.midY + font.descender - font.capHeight / 2
    ),
    withAttributes: attributes
)

image.unlockFocus()

guard let tiff = image.tiffRepresentation,
      let bitmap = NSBitmapImageRep(data: tiff),
      let png = bitmap.representation(using: .png, properties: [:])
else {
    FileHandle.standardError.write(Data("could not encode the PNG\n".utf8))
    exit(1)
}

try png.write(to: output)
print("wrote \(output.path)")
