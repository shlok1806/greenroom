import SwiftUI

// The icons the Figma file draws (Components page, "Icon/<name>", 16 x 16, stroke 1.33333,
// round caps and joins), which are Lucide's (lucide-icons/lucide, ISC; see ACKNOWLEDGEMENTS.md).
// The path data below is each Figma component exported as SVG, so the app draws the same
// outlines the design does, at any size. SF Symbols are not used: they are not what the design
// draws (docs/22 C05).

/// One icon of the design.
enum Icon: String, CaseIterable, Sendable {
    case search, settings, more, activity, message, play, expand, sidebar, close, restart, pointer
    case chevronRight = "chevron-right"
    case chevronDown = "chevron-down"
    case send, copy, video, logs, camera, eye, keyboard, download, machine, think, check

    /// SVG path data on a 16-unit box, every subpath of the icon.
    var pathData: String {
        switch self {
        case .search:
            "M7.33333 12C9.91066 12 12 9.91067 12 7.33334C12 4.75601 9.91066 2.66667 7.33333 2.66667C4.756 2.66667 2.66667 4.75601 2.66667 7.33334C2.66667 9.91067 4.756 12 7.33333 12Z M13.3333 13.3333L10.6667 10.6667"
        case .settings:
            "M13.3333 4.66667H7.33333 M9.33333 11.3333H3.33333 M11.3333 13.3333C12.4379 13.3333 13.3333 12.4379 13.3333 11.3333C13.3333 10.2288 12.4379 9.33333 11.3333 9.33333C10.2288 9.33333 9.33333 10.2288 9.33333 11.3333C9.33333 12.4379 10.2288 13.3333 11.3333 13.3333Z M4.66667 6.66667C5.77124 6.66667 6.66667 5.77124 6.66667 4.66667C6.66667 3.5621 5.77124 2.66667 4.66667 2.66667C3.5621 2.66667 2.66667 3.5621 2.66667 4.66667C2.66667 5.77124 3.5621 6.66667 4.66667 6.66667Z"
        case .more:
            "M8 8.66666C8.36819 8.66666 8.66667 8.36818 8.66667 7.99999C8.66667 7.63181 8.36819 7.33333 8 7.33333C7.63181 7.33333 7.33334 7.63181 7.33334 7.99999C7.33334 8.36818 7.63181 8.66666 8 8.66666Z M12.6667 8.66666C13.0349 8.66666 13.3333 8.36818 13.3333 7.99999C13.3333 7.63181 13.0349 7.33333 12.6667 7.33333C12.2985 7.33333 12 7.63181 12 7.99999C12 8.36818 12.2985 8.66666 12.6667 8.66666Z M3.33333 8.66666C3.70152 8.66666 4 8.36818 4 7.99999C4 7.63181 3.70152 7.33333 3.33333 7.33333C2.96514 7.33333 2.66666 7.63181 2.66666 7.99999C2.66666 8.36818 2.96514 8.66666 3.33333 8.66666Z"
        case .activity:
            "M5.33333 4H14M5.33333 8H14M5.33333 12H14M2 4H2.00667M2 8H2.00667M2 12H2.00667"
        case .message:
            "M14 10C14 10.7333 13.4 11.3333 12.6667 11.3333H4.66667L2 14V3.33333C2 2.6 2.6 2 3.33333 2H12.6667C13.4 2 14 2.6 14 3.33333V10Z"
        case .play:
            "M4 2L13.3333 8L4 14V2Z"
        case .expand:
            "M14 6V2H10M14 2L9.33333 6.66667M2 10V14H6M2 14L6.66667 9.33333"
        case .sidebar:
            "M12.6667 2H3.33333C2.59695 2 2 2.59695 2 3.33333V12.6667C2 13.403 2.59695 14 3.33333 14H12.6667C13.403 14 14 13.403 14 12.6667V3.33333C14 2.59695 13.403 2 12.6667 2Z M6 2V14"
        case .close:
            "M12 4L4 12M4 4L12 12"
        case .restart:
            "M2 8C2 11.3133 4.68667 14 8 14C11.3133 14 14 11.3133 14 8C14 4.68667 11.3133 2 8 2C6.33333 2 4.83333 2.66667 3.73333 3.73333L2 5.33333 M2 2V5.33333H5.33333"
        case .pointer:
            "M2.69332 3.12667C2.59999 2.86667 2.86666 2.60001 3.12666 2.69334L13.7933 7.02667C14.0667 7.13334 14.0467 7.53334 13.7533 7.66001L9.66666 8.71334C9.19999 8.83334 8.83332 9.20001 8.71332 9.66667L7.65999 13.7533C7.53332 14.0467 7.13332 14.0667 7.02666 13.7933L2.69332 3.12667Z"
        case .chevronRight:
            "M6 12L10 8L6 4"
        case .chevronDown:
            "M4 6L8 10L12 6"
        case .send:
            "M12.6666 8L7.99998 3.33333L3.33331 8M7.99998 3.33333V12.6667"
        case .copy:
            "M13.3333 5.33333H6.66665C5.93027 5.33333 5.33331 5.93028 5.33331 6.66666V13.3333C5.33331 14.0697 5.93027 14.6667 6.66665 14.6667H13.3333C14.0697 14.6667 14.6666 14.0697 14.6666 13.3333V6.66666C14.6666 5.93028 14.0697 5.33333 13.3333 5.33333Z M2.66665 10.6667C1.93331 10.6667 1.33331 10.0667 1.33331 9.33333V2.66666C1.33331 1.93333 1.93331 1.33333 2.66665 1.33333H9.33331C10.0666 1.33333 10.6666 1.93333 10.6666 2.66666"
        case .video:
            "M9.33331 4H2.66665C1.93027 4 1.33331 4.59695 1.33331 5.33333V10.6667C1.33331 11.403 1.93027 12 2.66665 12H9.33331C10.0697 12 10.6666 11.403 10.6666 10.6667V5.33333C10.6666 4.59695 10.0697 4 9.33331 4Z M14.6667 5.33333L10.6667 7.99999L14.6667 10.6667V5.33333Z"
        case .logs:
            "M2.66669 11.3333L6.66669 7.33333L2.66669 3.33333M8.00002 12.6667H13.3334"
        case .camera:
            "M9.66665 2.66667H6.33331L4.66665 4.66667H2.66665C1.93331 4.66667 1.33331 5.26667 1.33331 6.00001V12C1.33331 12.7333 1.93331 13.3333 2.66665 13.3333H13.3333C14.0666 13.3333 14.6666 12.7333 14.6666 12V6.00001C14.6666 5.26667 14.0666 4.66667 13.3333 4.66667H11.3333L9.66665 2.66667Z M8 10.6667C9.10457 10.6667 10 9.77124 10 8.66667C10 7.5621 9.10457 6.66667 8 6.66667C6.89543 6.66667 6 7.5621 6 8.66667C6 9.77124 6.89543 10.6667 8 10.6667Z"
        case .eye:
            "M1.33331 8C1.33331 8 3.33331 3.33333 7.99998 3.33333C12.6666 3.33333 14.6666 8 14.6666 8C14.6666 8 12.6666 12.6667 7.99998 12.6667C3.33331 12.6667 1.33331 8 1.33331 8Z M8 10C9.10457 10 10 9.10457 10 8C10 6.89543 9.10457 6 8 6C6.89543 6 6 6.89543 6 8C6 9.10457 6.89543 10 8 10Z"
        case .keyboard:
            "M13.3333 4H2.66665C1.93027 4 1.33331 4.59695 1.33331 5.33333V10.6667C1.33331 11.403 1.93027 12 2.66665 12H13.3333C14.0697 12 14.6666 11.403 14.6666 10.6667V5.33333C14.6666 4.59695 14.0697 4 13.3333 4Z M4 6.66667H4.00667M6.66667 6.66667H6.67333M9.33333 6.66667H9.34M12 6.66667H12.0067M4.66667 9.33334H11.3333"
        case .download:
            "M14 10V12.6667C14 13.4 13.4 14 12.6667 14H3.33333C2.6 14 2 13.4 2 12.6667V10M11.3333 6.66667L8 10L4.66667 6.66667M8 10V2"
        case .machine:
            "M13.3333 2H2.66665C1.93027 2 1.33331 2.59695 1.33331 3.33333V10C1.33331 10.7364 1.93027 11.3333 2.66665 11.3333H13.3333C14.0697 11.3333 14.6666 10.7364 14.6666 10V3.33333C14.6666 2.59695 14.0697 2 13.3333 2Z M5.33331 14H10.6666M7.99998 11.3333V14"
        case .think:
            "M8.00002 10.3333C9.28868 10.3333 10.3334 9.28867 10.3334 8.00001C10.3334 6.71134 9.28868 5.66667 8.00002 5.66667C6.71136 5.66667 5.66669 6.71134 5.66669 8.00001C5.66669 9.28867 6.71136 10.3333 8.00002 10.3333Z M8.00002 1.66667V3.33334M8.00002 12.6667V14.3333M1.66669 8.00001H3.33335M12.6667 8.00001H14.3334"
        case .check:
            "M13.3334 4L6.00002 11.3333L2.66669 8"
        }
    }

    /// The stroke width on the 16-unit box (Lucide's 2 on 24).
    static let strokeWidth: CGFloat = 1.33333

    /// The outline on the 16-unit box.
    var path: Path { SVGPath.path(pathData) }
}

/// An icon drawn at `size` points in the current foreground style.
struct IconView: View {
    var icon: Icon
    var size: CGFloat = 16

    var body: some View {
        IconShape(icon: icon)
            .stroke(style: StrokeStyle(lineWidth: Icon.strokeWidth * size / 16, lineCap: .round, lineJoin: .round))
            .frame(width: size, height: size)
            .accessibilityHidden(true)
    }
}

struct IconShape: Shape {
    var icon: Icon

    func path(in rect: CGRect) -> Path {
        let scale = min(rect.width, rect.height) / 16
        return icon.path.applying(CGAffineTransform(scaleX: scale, y: scale).translatedBy(x: rect.minX / scale, y: rect.minY / scale))
    }
}

/// SVG path data to a `Path`: the commands Figma and Lucide write (M, L, H, V, C, S, Q, Z, in
/// absolute and relative form). Arcs are not supported, since Figma exports curves as cubics;
/// an unknown command ends the parse, leaving what was read.
enum SVGPath {
    static func path(_ data: String) -> Path {
        var path = Path()
        var scanner = Scanner(data)
        var current = CGPoint.zero
        var start = CGPoint.zero
        var lastControl: CGPoint?
        var command: Character = "M"
        while !scanner.isAtEnd {
            if let next = scanner.command() { command = next } else if !scanner.atNumber { break }
            let relative = command.isLowercase
            func point() -> CGPoint? {
                guard let x = scanner.number(), let y = scanner.number() else { return nil }
                return relative ? CGPoint(x: current.x + x, y: current.y + y) : CGPoint(x: x, y: y)
            }
            switch command.uppercased() {
            case "M":
                guard let p = point() else { return path }
                path.move(to: p)
                (current, start, lastControl) = (p, p, nil)
                // Further pairs after a move are lines.
                command = relative ? "l" : "L"
            case "L":
                guard let p = point() else { return path }
                path.addLine(to: p)
                (current, lastControl) = (p, nil)
            case "H":
                guard let x = scanner.number() else { return path }
                current = CGPoint(x: relative ? current.x + x : x, y: current.y)
                path.addLine(to: current)
                lastControl = nil
            case "V":
                guard let y = scanner.number() else { return path }
                current = CGPoint(x: current.x, y: relative ? current.y + y : y)
                path.addLine(to: current)
                lastControl = nil
            case "C":
                guard let c1 = point(), let c2 = point(), let p = point() else { return path }
                path.addCurve(to: p, control1: c1, control2: c2)
                (current, lastControl) = (p, c2)
            case "S":
                guard let c2 = point(), let p = point() else { return path }
                let c1 = lastControl.map { CGPoint(x: 2 * current.x - $0.x, y: 2 * current.y - $0.y) } ?? current
                path.addCurve(to: p, control1: c1, control2: c2)
                (current, lastControl) = (p, c2)
            case "Q":
                guard let c = point(), let p = point() else { return path }
                path.addQuadCurve(to: p, control: c)
                (current, lastControl) = (p, c)
            case "Z":
                path.closeSubpath()
                (current, lastControl) = (start, nil)
            default:
                return path
            }
        }
        return path
    }

    private struct Scanner {
        private let characters: [Character]
        private var index = 0

        init(_ text: String) { characters = Array(text) }

        var isAtEnd: Bool {
            mutating get {
                skipSeparators()
                return index >= characters.count
            }
        }

        var atNumber: Bool {
            mutating get {
                skipSeparators()
                guard index < characters.count else { return false }
                let c = characters[index]
                return c.isNumber || c == "-" || c == "+" || c == "."
            }
        }

        private mutating func skipSeparators() {
            while index < characters.count, characters[index] == " " || characters[index] == "," || characters[index].isNewline { index += 1 }
        }

        mutating func command() -> Character? {
            skipSeparators()
            guard index < characters.count, characters[index].isLetter, characters[index] != "e", characters[index] != "E" else { return nil }
            defer { index += 1 }
            return characters[index]
        }

        mutating func number() -> CGFloat? {
            skipSeparators()
            var text = ""
            var seenDot = false
            while index < characters.count {
                let c = characters[index]
                if c.isNumber {
                    text.append(c)
                } else if c == "-" || c == "+" {
                    if text.isEmpty || text.last == "e" || text.last == "E" { text.append(c) } else { break }
                } else if c == "." {
                    if seenDot { break }
                    seenDot = true
                    text.append(c)
                } else if (c == "e" || c == "E"), !text.isEmpty {
                    text.append(c)
                } else {
                    break
                }
                index += 1
            }
            return Double(text).map { CGFloat($0) }
        }
    }
}
