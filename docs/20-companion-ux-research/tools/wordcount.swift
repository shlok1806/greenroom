import Foundation
import Vision
import AppKit
for path in CommandLine.arguments.dropFirst() {
    guard let img = NSImage(contentsOfFile: path), let cg = img.cgImage(forProposedRect: nil, context: nil, hints: nil) else { continue }
    let req = VNRecognizeTextRequest()
    req.recognitionLevel = .accurate
    req.usesLanguageCorrection = false
    try? VNImageRequestHandler(cgImage: cg).perform([req])
    let lines = (req.results ?? []).compactMap { $0.topCandidates(1).first?.string }
    let words = lines.flatMap { $0.split(whereSeparator: { $0 == " " }) }.count
    let chars = lines.reduce(0) { $0 + $1.count }
    let name = (path as NSString).lastPathComponent
    print("\(name)\t\(lines.count)\t\(words)\t\(chars)")
    try? lines.joined(separator: "\n").write(toFile: "/tmp/gr-ux/ocr-\(name).txt", atomically: true, encoding: .utf8)
}
