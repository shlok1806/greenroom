import AppKit
import SwiftUI

/// Design tokens (`docs/design-spec.md`). Views take spacing, radii and state colours
/// from here, never as literals, so the app keeps one rhythm.
enum Space {
    static let xxs: CGFloat = 2
    static let xs: CGFloat = 4
    static let s: CGFloat = 8
    static let m: CGFloat = 12
    static let l: CGFloat = 16
    static let xl: CGFloat = 20
    static let xxl: CGFloat = 24
}

enum Radius {
    static let chip: CGFloat = 4
    static let control: CGFloat = 6
    static let card: CGFloat = 8
    static let bubble: CGFloat = 12
    static let well: CGFloat = 10
}

/// Each colour means one thing (design spec, "Colour"): green pass, red failure, orange
/// needs your attention, teal live. Driving uses the system accent, the colour of
/// "you are acting". Everything else is label, secondary and tertiary.
enum Palette {
    static let pass = Color.green
    static let failure = Color.red
    static let attention = Color.orange
    static let live = Color.teal
    static let driving = Color.accentColor

    static func tone(_ tone: RunFacts.Tone) -> Color {
        switch tone {
        case .pass: pass
        case .failure: failure
        case .attention: attention
        case .live: live
        case .neutral, .unsure: .secondary
        case .quiet: Color(nsColor: .tertiaryLabelColor)
        }
    }

    static func outcome(_ verdict: String?) -> Color {
        switch verdict {
        case "pass": pass
        case "fail": failure
        default: .secondary
        }
    }

    /// The surround of the screen: dark in dark mode, a quiet grey in light mode, so a
    /// letterbox never reads as a black hole on a white window.
    static let well = Color(nsColor: NSColor(name: nil) { appearance in
        appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
            ? NSColor(white: 0.07, alpha: 1)
            : NSColor(white: 0.93, alpha: 1)
    })

    static let hairline = Color(nsColor: .separatorColor)
}

extension Chrome {
    static func outcomeSymbol(_ verdict: String?) -> String {
        switch verdict {
        case "pass": "checkmark.seal.fill"
        case "fail": "xmark.seal.fill"
        case "inconclusive": "questionmark.diamond.fill"
        default: "seal"
        }
    }

    /// "Pass", "Fail", "Inconclusive"; a verdict with no outcome is just "Verdict".
    static func outcomeTitle(_ verdict: String?) -> String {
        guard let verdict, !verdict.isEmpty else { return "Verdict" }
        return verdict.prefix(1).uppercased() + verdict.dropFirst()
    }

    /// The agreement in words a reviewer acts on.
    static func agreementTitle(_ state: VerdictState) -> String {
        switch state.status {
        case .proposed: return "Awaiting review"
        case .accepted:
            guard let by = state.acceptedBy else { return "Accepted" }
            return by == .human ? "Accepted by you" : "Accepted by \(by == .coder ? "the coding agent" : by.text)"
        case .contested: return "Contested, needs you"
        case .rejected: return "Rejected"
        case .none: return ""
        case .unknown(let raw): return raw
        }
    }

    static func lifecycleTitle(_ status: RunStatus) -> String {
        switch status {
        case .booting: "Booting"
        case .ready: "Live"
        case .failed: "Failed"
        case .finished: "Finished"
        case .unknown(let raw): raw.isEmpty ? "Unknown" : raw
        }
    }
}

// MARK: - Components

/// Live gets its own mark: a broadcast glyph whose waves pulse, not a coloured dot
/// (and still under Reduce Motion).
struct LiveMark: View {
    var size: CGFloat = 10

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        Group {
            if reduceMotion {
                Image(systemName: "dot.radiowaves.left.and.right")
            } else {
                Image(systemName: "dot.radiowaves.left.and.right")
                    .symbolEffect(.variableColor.iterative.reversing, options: .repeating)
            }
        }
        .font(.system(size: size, weight: .semibold))
        .foregroundStyle(Palette.live)
        .accessibilityHidden(true)
    }
}

/// A run's state in words with the one colour that word earns: "Live", "Idle 6m",
/// "Review Pass", "Pass, accepted".
struct StatusText: View {
    let text: String
    let tone: RunFacts.Tone
    var font: Font = .caption.weight(.semibold)

    var body: some View {
        HStack(spacing: 4) {
            switch tone {
            case .live: LiveMark(size: 9)
            case .attention: Image(systemName: "exclamationmark.circle.fill").font(.system(size: 9, weight: .bold))
            case .pass: Image(systemName: "checkmark.seal.fill").font(.system(size: 9, weight: .bold))
            case .failure: Image(systemName: "xmark.seal.fill").font(.system(size: 9, weight: .bold))
            case .neutral: ProgressView().controlSize(.mini).scaleEffect(0.7).frame(width: 10, height: 10)
            case .unsure: Image(systemName: "questionmark.diamond.fill").font(.system(size: 9, weight: .bold))
            case .quiet: EmptyView()
            }
            Text(text)
        }
        .font(font)
        .foregroundStyle(Palette.tone(tone))
        .lineLimit(1)
        .fixedSize()
        .accessibilityElement(children: .combine)
    }
}

/// A sender's mark in the conversation. Shape and symbol tell speakers apart, not colour.
struct SenderAvatar: View {
    let from: MessageFrom
    var size: CGFloat = 22

    var body: some View {
        Image(systemName: symbol)
            .font(.system(size: size * 0.48, weight: .semibold))
            .foregroundStyle(from == .human ? AnyShapeStyle(.white) : AnyShapeStyle(.secondary))
            .frame(width: size, height: size)
            .background(fill, in: Circle())
            .accessibilityHidden(true)
    }

    private var symbol: String {
        switch from {
        case .coder: "chevron.left.forwardslash.chevron.right"
        case .verifier: "checkmark.shield"
        case .human: "person.fill"
        case .system, .unknown: "gearshape"
        }
    }

    private var fill: AnyShapeStyle {
        from == .human ? AnyShapeStyle(Color.accentColor) : AnyShapeStyle(.fill.secondary)
    }
}

extension MessageFrom {
    /// How the transcript names a sender. greenroom's own agent is "Verifier"; the
    /// person's agent is the "Coding agent".
    var displayName: String {
        switch self {
        case .coder: "Coding agent"
        case .verifier: "Verifier"
        case .human: "You"
        case .system: "greenroom"
        case .unknown(let raw): raw.isEmpty ? "Unknown" : raw
        }
    }
}

/// A small rounded label: `Step 12`, a file name.
struct Chip: View {
    let text: String
    var symbol: String?
    var tint: Color = .secondary

    var body: some View {
        HStack(spacing: 4) {
            if let symbol {
                Image(systemName: symbol)
                    .font(.system(size: 9, weight: .semibold))
            }
            Text(text)
                .lineLimit(1)
                .truncationMode(.middle)
        }
        .font(.caption.weight(.medium))
        .foregroundStyle(tint)
        .padding(.horizontal, 6)
        .padding(.vertical, 2)
        .background(tint.opacity(0.12), in: RoundedRectangle(cornerRadius: Radius.chip))
    }
}

/// Copies a command or snippet, and says so for a moment.
struct CopyButton: View {
    let text: String
    var label = "Copy"

    @State private var copied = false

    var body: some View {
        Button {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(text, forType: .string)
            copied = true
            Task {
                try? await Task.sleep(for: .seconds(1.5))
                copied = false
            }
        } label: {
            Label(copied ? "Copied" : label, systemImage: copied ? "checkmark" : "doc.on.doc")
        }
        .controlSize(.small)
        .help("Copy to the clipboard")
    }
}

/// A command a person will paste into a terminal, on one line that scrolls rather than
/// wraps mid-URL, with its copy button.
struct CommandBlock: View {
    let command: String

    var body: some View {
        HStack(spacing: Space.s) {
            ScrollView(.horizontal) {
                Text(command)
                    .font(.callout.monospaced())
                    .textSelection(.enabled)
                    .fixedSize()
            }
            .scrollIndicators(.never)
            .frame(maxWidth: .infinity, alignment: .leading)
            CopyButton(text: command)
        }
        .padding(.leading, Space.m)
        .padding(.trailing, Space.s)
        .padding(.vertical, Space.s)
        .background(.fill.quaternary, in: RoundedRectangle(cornerRadius: Radius.control))
        .overlay(RoundedRectangle(cornerRadius: Radius.control).strokeBorder(Palette.hairline))
    }
}

/// Hover feedback for plain buttons and rows.
struct HoverHighlight: ViewModifier {
    var radius: CGFloat = Radius.chip
    @State private var hovering = false

    func body(content: Content) -> some View {
        content
            .background(
                RoundedRectangle(cornerRadius: radius)
                    .fill(hovering ? AnyShapeStyle(.fill.tertiary) : AnyShapeStyle(.clear))
            )
            .onHover { hovering = $0 }
    }
}

extension View {
    func hoverHighlight(radius: CGFloat = Radius.chip) -> some View {
        modifier(HoverHighlight(radius: radius))
    }

    /// Scrollers that float over the content, whatever the system setting, so a legacy
    /// scroller never covers a row's trailing badge.
    func overlayScrollers() -> some View {
        background(OverlayScrollers())
    }
}

/// Finds the enclosing `NSScrollView` and keeps its scrollers in overlay style.
private struct OverlayScrollers: NSViewRepresentable {
    func makeNSView(context: Context) -> ScrollerStyler { ScrollerStyler() }
    func updateNSView(_ view: ScrollerStyler, context: Context) { view.apply() }

    final class ScrollerStyler: NSView {
        private var observer: NSObjectProtocol?

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            guard window != nil else {
                if let observer { NotificationCenter.default.removeObserver(observer) }
                observer = nil
                return
            }
            apply()
            guard observer == nil else { return }
            observer = NotificationCenter.default.addObserver(
                forName: NSScroller.preferredScrollerStyleDidChangeNotification, object: nil, queue: .main
            ) { [weak self] _ in
                MainActor.assumeIsolated { self?.apply() }
            }
        }

        func apply() {
            // Now and again shortly: a List builds its table after the first update.
            for delay in [0.0, 0.3, 1.0] {
                DispatchQueue.main.asyncAfter(deadline: .now() + delay) { [weak self] in
                    guard let self else { return }
                    for scroll in self.scrollViews() where scroll.scrollerStyle != .overlay {
                        scroll.scrollerStyle = .overlay
                    }
                }
            }
        }

        /// The scroll view this sits in, or the ones beside it in the same container.
        private func scrollViews() -> [NSScrollView] {
            if let enclosing = enclosingScrollView { return [enclosing] }
            var node: NSView? = superview
            for _ in 0..<4 {
                guard let current = node else { break }
                let found = Self.descendants(of: current).compactMap { $0 as? NSScrollView }
                if !found.isEmpty { return found }
                node = current.superview
            }
            return []
        }

        private static func descendants(of view: NSView) -> [NSView] {
            view.subviews + view.subviews.flatMap { descendants(of: $0) }
        }
    }
}
