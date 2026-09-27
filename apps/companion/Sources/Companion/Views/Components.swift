import AppKit
import SwiftUI

// The shared pieces of the window's language (ADR 0008, `docs/design-spec.md`): the 4/8 pt
// spacing scale and radii from `tokens.json`, quiet panels with 1 px hairlines, small
// uppercase mono labels, the tick spinner, status in a glyph and a word, and the button,
// toggle and switch styles. Colours come from the theme (`Design/Theme.swift`) by role;
// no view names a colour, a spacing or a radius of its own.

/// The spacing scale (`tokens.json` `spacing.scale`: 4, 8, 12, 16, 24, 32, 48).
enum Space {
    private static var scale: [Double] { DesignData.shared.tokens.spacing.scale }

    static var xs: CGFloat { scale[0] }
    static var s: CGFloat { scale[1] }
    static var m: CGFloat { scale[2] }
    static var l: CGFloat { scale[3] }
    static var xl: CGFloat { scale[4] }
    static var xxl: CGFloat { scale[5] }
    static var xxxl: CGFloat { scale[6] }
    /// A hairline's width.
    static let hairline: CGFloat = 1
}

/// Panes move on the settle spring (`tokens.json` `motion.settle`, ADR 0006): a resize
/// across a width class, a zoom, the runs opening over a folded window. Reduce Motion
/// makes every change instant.
enum PaneMotion {
    static func settle(reduceMotion: Bool) -> Animation? {
        guard !reduceMotion else { return nil }
        let spring = DesignData.shared.tokens.motion.settle
        return .spring(response: spring.response, dampingFraction: spring.dampingFraction)
    }
}

extension View {
    /// Keyboard focus on a pane, quietly: a 2 pt brand rule along its top edge (1 pt
    /// vanished into the hairline under the top bar). The pane's
    /// own label, where it has one, takes the brand too (`Theme.brandInk`).
    func focusRule(_ on: Bool) -> some View {
        modifier(FocusRule(on: on))
    }
}

private struct FocusRule: ViewModifier {
    let on: Bool
    @Environment(\.theme) private var theme

    func body(content: Content) -> some View {
        content.overlay(alignment: .top) {
            Rectangle()
                .fill(theme.brand)
                .frame(height: 2)
                .opacity(on ? 1 : 0)
                .allowsHitTesting(false)
                .accessibilityHidden(true)
        }
    }
}

/// Radii (`tokens.json` `radii`): a row or chip, a card, a sheet or the screen's frame.
enum Radius {
    private static var radii: DesignTokens.Radii { DesignData.shared.tokens.radii }

    static var sm: CGFloat { radii.sm }
    static var md: CGFloat { radii.md }
    static var lg: CGFloat { radii.lg }
}

extension Chrome {
    /// A verdict's outcome as a glyph, always beside its word.
    static func outcomeGlyph(_ verdict: String?) -> String {
        switch verdict {
        case "pass": "✓"
        case "fail": "✗"
        case "inconclusive": "?"
        default: "◇"
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
        case .rebooting: "Rebooting"
        case .ready: "Live"
        case .failed: "Failed"
        case .finished: "Finished"
        case .unknown(let raw): raw.isEmpty ? "Unknown" : raw
        }
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

// MARK: - Grounds and panels

/// Text here sits on `ground`: secondary text and every role are lifted, when they need
/// to be, to read at the text threshold on it (`Theme.legible`).
private struct GroundModifier: ViewModifier {
    let ground: Ground
    @Environment(\.theme) private var theme

    func body(content: Content) -> some View {
        let dim = theme.dim(on: ground)
        content
            .foregroundStyle(theme.foreground, dim, dim)
            .environment(\.ground, ground)
    }
}

extension View {
    func ground(_ ground: Ground) -> some View {
        modifier(GroundModifier(ground: ground))
    }

    /// A quiet panel: the surface fill, a hairline edge and a small radius.
    func panel(radius: CGFloat = Radius.md, edge: Color? = nil) -> some View {
        modifier(PanelModifier(radius: radius, edge: edge))
    }
}

private struct PanelModifier: ViewModifier {
    let radius: CGFloat
    let edge: Color?
    @Environment(\.theme) private var theme

    func body(content: Content) -> some View {
        let shape = RoundedRectangle(cornerRadius: radius, style: .continuous)
        content
            .ground(.surface)
            .background(theme.surface, in: shape)
            .overlay(shape.strokeBorder(edge ?? theme.hairline, lineWidth: Space.hairline).allowsHitTesting(false))
    }
}

/// A 1 px rule between panes or rows.
struct Hairline: View {
    var axis: Axis = .horizontal
    @Environment(\.theme) private var theme

    var body: some View {
        Rectangle()
            .fill(theme.hairline)
            .frame(width: axis == .vertical ? Space.hairline : nil, height: axis == .horizontal ? Space.hairline : nil)
            .accessibilityHidden(true)
    }
}

/// A small uppercase mono label over a group: "NEEDS YOU 2", "TODAY", "INPUT". Quiet,
/// with whitespace around it, never a drawn rule.
struct SectionLabel: View {
    let title: String
    var count: Int?
    /// The label of a pane with the keyboard takes the brand (`Theme.brandInk`).
    var ink: Color?

    var body: some View {
        HStack(spacing: Space.s) {
            Text(title.uppercased())
            if let count {
                Text("\(count)").monospacedDigit()
            }
        }
        .font(Typeface.monoMedium.font(size: TypeScale.label))
        .tracking(0.8)
        .foregroundStyle(ink.map { AnyShapeStyle($0) } ?? AnyShapeStyle(.secondary))
        .lineLimit(1)
        .accessibilityElement(children: .combine)
    }
}

// MARK: - Motion accents

/// The tick: a braille spinner, 80 ms a frame (`tokens.json` `motion.spinner`); a static
/// `…` under Reduce Motion.
struct Spinner: View {
    var size: CGFloat = TypeScale.mono

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var spinner: DesignTokens.Motion.Spinner { DesignData.shared.tokens.motion.spinner }

    var body: some View {
        Group {
            if reduceMotion {
                Text(spinner.reduceMotion)
            } else {
                TimelineView(.periodic(from: .now, by: Double(spinner.intervalMs) / 1000)) { context in
                    let tick = Int(context.date.timeIntervalSinceReferenceDate * 1000) / spinner.intervalMs
                    Text(spinner.frames[tick % spinner.frames.count])
                }
            }
        }
        .font(Typeface.monoRegular.font(size: size))
        .accessibilityHidden(true)
    }
}

/// Live gets its own mark: a dot in the live role that breathes (still under Reduce
/// Motion). Always beside the word "Live".
struct LiveMark: View {
    var size: CGFloat = TypeScale.monoSmall

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.theme) private var theme
    @Environment(\.ground) private var ground
    @State private var dimmed = false

    var body: some View {
        Text("●")
            .font(Typeface.monoRegular.font(size: size))
            .foregroundStyle(theme.color(.live, on: ground))
            .opacity(dimmed ? 0.35 : 1)
            .onAppear {
                guard !reduceMotion else { return }
                withAnimation(.easeInOut(duration: 1).repeatForever(autoreverses: true)) { dimmed = true }
            }
            .accessibilityHidden(true)
    }
}

/// A run's state in a glyph and a word, in the one colour that word earns: "● Live",
/// "! Idle 6m", "✓ Pass, accepted". Mono, since it is chrome.
struct StatusText: View {
    let text: String
    let tone: RunFacts.Tone
    var size: CGFloat = TypeScale.monoSmall
    /// On a brand-filled row the whole row is `brandText`; the glyph and word still say it.
    var ink: Color?

    @Environment(\.theme) private var theme
    @Environment(\.ground) private var ground

    var body: some View {
        HStack(spacing: Space.xs) {
            switch tone {
            case .live:
                if ink == nil { LiveMark(size: size) } else { Text("●") }
            case .attention: Text("!").fontWeight(.bold)
            case .pass: Text("✓")
            case .failure: Text("✗")
            case .neutral: Spinner(size: size)
            case .unsure: Text("?")
            case .quiet: EmptyView()
            case .done: Text("■")
            }
            Text(text)
        }
        .font(Typeface.monoMedium.font(size: size))
        .foregroundStyle(ink ?? theme.tone(tone, on: ground))
        .lineLimit(1)
        .fixedSize()
        .accessibilityElement(children: .combine)
    }
}

/// An empty pane: what is missing in the heading cut, and why in the reading face,
/// centred. Never a system `ContentUnavailableView` icon.
struct QuietEmpty: View {
    let title: String
    let message: String

    var body: some View {
        VStack(spacing: Space.xs) {
            Text(title)
                .headingStyle()
            Text(message)
                .readingStyle(size: TypeScale.readingSmall)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
        }
        .frame(maxWidth: 360)
        .padding(Space.l)
        .accessibilityElement(children: .combine)
    }
}

/// A small label: `Step 12`, a file name, a claimed value.
struct Chip: View {
    let text: String
    var tint: Color?

    @Environment(\.theme) private var theme

    var body: some View {
        Text(text)
            .lineLimit(1)
            .truncationMode(.middle)
            .font(Typeface.monoMedium.font(size: TypeScale.monoSmall))
            .foregroundStyle(tint ?? theme.foreground)
            .padding(.horizontal, Space.s)
            .padding(.vertical, 2)
            .background(theme.surface, in: RoundedRectangle(cornerRadius: Radius.sm, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Radius.sm, style: .continuous).strokeBorder(theme.hairline))
    }
}

/// Copies a command or snippet, and says so for a moment.
struct CopyButton: View {
    let text: String
    var label = "Copy"

    @State private var copied = false

    var body: some View {
        Button(copied ? "Copied" : label) {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(text, forType: .string)
            copied = true
            Task {
                try? await Task.sleep(for: .seconds(1.5))
                copied = false
            }
        }
        .buttonStyle(.quiet(small: true))
        .help("Copy to the clipboard")
    }
}

/// A command a person will paste into a terminal, on one line that scrolls rather than
/// wraps mid-URL, with its copy button.
struct CommandBlock: View {
    let command: String

    var body: some View {
        // The whole command, wrapped: a hidden horizontal scroll cut it with no sign that
        // more followed (audit P5).
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            Text(command)
                .monoStyle()
                .lineLimit(4)
                .textSelection(.enabled)
                // Its own height; every use sits in a bounded, scrolling column.
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: .infinity, alignment: .leading)
            CopyButton(text: command)
        }
        .padding(.leading, Space.m)
        .padding(.trailing, Space.s)
        .padding(.vertical, Space.s)
        .panel()
    }
}

/// Hover feedback for plain buttons and rows.
struct HoverHighlight: ViewModifier {
    var radius: CGFloat = Radius.sm
    @State private var hovering = false
    @Environment(\.theme) private var theme

    func body(content: Content) -> some View {
        content
            .background(
                RoundedRectangle(cornerRadius: radius, style: .continuous)
                    .fill(hovering ? theme.highlight : .clear)
            )
            .onHover { hovering = $0 }
    }
}

/// A text field's frame: the ground, a hairline, the brand edge while focused.
private struct FieldFrame: ViewModifier {
    let focused: Bool
    var radius: CGFloat = Radius.md
    @Environment(\.theme) private var theme

    func body(content: Content) -> some View {
        let shape = RoundedRectangle(cornerRadius: radius, style: .continuous)
        content
            .background(theme.background, in: shape)
            .overlay(shape.strokeBorder(focused ? theme.brand : theme.hairline, lineWidth: focused ? 1.5 : Space.hairline)
                .allowsHitTesting(false))
    }
}

extension View {
    func hoverHighlight(radius: CGFloat = Radius.sm) -> some View {
        modifier(HoverHighlight(radius: radius))
    }

    func fieldFrame(focused: Bool, radius: CGFloat = Radius.md) -> some View {
        modifier(FieldFrame(focused: focused, radius: radius))
    }

    /// Scrollers that float over the content, whatever the system setting, so a legacy
    /// scroller never covers a row's trailing badge.
    func overlayScrollers() -> some View {
        background(OverlayScrollers())
    }
}

// MARK: - Buttons, toggles, switches

/// The default button: a quiet panel with a hairline, the label in the reading face.
struct QuietButtonStyle: ButtonStyle {
    var small = false
    var tint: Role?
    /// Holds something open (the More menu): drawn pressed, with a brand edge.
    var active = false

    func makeBody(configuration: Configuration) -> some View {
        QuietButton(configuration: configuration, small: small, tint: tint, active: active)
    }

    private struct QuietButton: View {
        let configuration: ButtonStyleConfiguration
        let small: Bool
        let tint: Role?
        let active: Bool

        @Environment(\.theme) private var theme
        @Environment(\.isEnabled) private var enabled
        @State private var hovering = false

        var body: some View {
            let shape = RoundedRectangle(cornerRadius: Radius.sm, style: .continuous)
            configuration.label
                .font(Typeface.readingMedium.font(size: small ? TypeScale.small : TypeScale.readingSmall))
                .foregroundStyle(tint.map { theme.color($0, on: .surface) } ?? theme.foreground)
                .lineLimit(1)
                .padding(.horizontal, small ? Space.s : Space.m)
                .frame(minHeight: small ? 22 : 28)
                .background(configuration.isPressed || hovering || active ? theme.highlight : theme.surface, in: shape)
                .overlay(shape.strokeBorder(active ? theme.brand : theme.hairline, lineWidth: Space.hairline))
                .contentShape(shape)
                .opacity(enabled ? 1 : 0.45)
                .onHover { hovering = $0 && enabled }
        }
    }
}

/// The one primary action in a place (Accept, Send, Take control): olive, `brandText`.
struct PrimaryButtonStyle: ButtonStyle {
    var small = false

    func makeBody(configuration: Configuration) -> some View {
        PrimaryButton(configuration: configuration, small: small)
    }

    private struct PrimaryButton: View {
        let configuration: ButtonStyleConfiguration
        let small: Bool

        @Environment(\.theme) private var theme
        @Environment(\.isEnabled) private var enabled

        var body: some View {
            let shape = RoundedRectangle(cornerRadius: Radius.sm, style: .continuous)
            configuration.label
                .font(Typeface.readingSemiBold.font(size: small ? TypeScale.small : TypeScale.readingSmall))
                .foregroundStyle(theme.brandText)
                .lineLimit(1)
                .padding(.horizontal, small ? Space.s : Space.m)
                .frame(minHeight: small ? 22 : 28)
                .background(theme.brand, in: shape)
                .overlay(shape.fill(Color.black.opacity(configuration.isPressed ? 0.18 : 0)))
                .contentShape(shape)
                .opacity(enabled ? 1 : 0.45)
        }
    }
}

/// A button that reads as a link: the label alone, underlined on hover.
struct TextLinkButtonStyle: ButtonStyle {
    var size: CGFloat = TypeScale.readingSmall

    func makeBody(configuration: Configuration) -> some View {
        TextLink(configuration: configuration, size: size)
    }

    private struct TextLink: View {
        let configuration: ButtonStyleConfiguration
        let size: CGFloat

        @Environment(\.isEnabled) private var enabled
        @State private var hovering = false

        var body: some View {
            configuration.label
                .font(Typeface.readingMedium.font(size: size))
                .underline(hovering && enabled)
                .opacity(configuration.isPressed ? 0.6 : enabled ? 1 : 0.45)
                .contentShape(Rectangle())
                .onHover { hovering = $0 }
        }
    }
}

extension ButtonStyle where Self == QuietButtonStyle {
    static var quiet: QuietButtonStyle { QuietButtonStyle() }
    static func quiet(small: Bool = false, tint: Role? = nil, active: Bool = false) -> QuietButtonStyle {
        QuietButtonStyle(small: small, tint: tint, active: active)
    }
}

extension ButtonStyle where Self == PrimaryButtonStyle {
    static var primary: PrimaryButtonStyle { PrimaryButtonStyle() }
    static func primary(small: Bool) -> PrimaryButtonStyle { PrimaryButtonStyle(small: small) }
}

extension ButtonStyle where Self == TextLinkButtonStyle {
    static var textLink: TextLinkButtonStyle { TextLinkButtonStyle() }
}

/// A checkbox: a small square that fills with the brand when on.
struct CheckToggleStyle: ToggleStyle {
    func makeBody(configuration: Configuration) -> some View {
        CheckToggle(configuration: configuration)
    }

    private struct CheckToggle: View {
        let configuration: ToggleStyleConfiguration

        @Environment(\.theme) private var theme
        @Environment(\.isEnabled) private var enabled

        var body: some View {
            Button {
                configuration.isOn.toggle()
            } label: {
                HStack(spacing: Space.s) {
                    ZStack {
                        RoundedRectangle(cornerRadius: 3, style: .continuous)
                            .fill(configuration.isOn ? theme.brand : theme.background)
                        RoundedRectangle(cornerRadius: 3, style: .continuous)
                            .strokeBorder(configuration.isOn ? theme.brand : theme.dim, lineWidth: Space.hairline)
                        if configuration.isOn {
                            Text("✓")
                                .font(.system(size: TypeScale.mark, weight: .bold))
                                .foregroundStyle(theme.brandText)
                        }
                    }
                    .frame(width: 14, height: 14)
                    configuration.label
                        .font(Typeface.readingRegular.font(size: TypeScale.readingSmall))
                }
                .contentShape(Rectangle())
                .opacity(enabled ? 1 : 0.45)
            }
            .buttonStyle(.plain)
            .accessibilityAddTraits(configuration.isOn ? .isSelected : [])
        }
    }
}

extension ToggleStyle where Self == CheckToggleStyle {
    static var check: CheckToggleStyle { CheckToggleStyle() }
}

/// A row of choices where one is on: the stage's tabs, the playback speed. The chosen one
/// is filled with the brand.
struct SegmentedSwitch<Value: Hashable>: View {
    let options: [(value: Value, title: String)]
    @Binding var selection: Value
    var small = false

    @Environment(\.theme) private var theme
    @Environment(\.isEnabled) private var enabled

    var body: some View {
        HStack(spacing: 2) {
            ForEach(options.indices, id: \.self) { index in
                let option = options[index]
                let on = option.value == selection
                Button {
                    selection = option.value
                } label: {
                    Text(option.title)
                        .font((on ? Typeface.readingSemiBold : .readingMedium).font(size: small ? TypeScale.small : TypeScale.readingSmall))
                        .foregroundStyle(on ? theme.brandText : theme.foreground)
                        .padding(.horizontal, small ? Space.s : Space.m)
                        .frame(minHeight: small ? 20 : 24)
                        .background(on ? theme.brand : .clear, in: RoundedRectangle(cornerRadius: Radius.sm - 1, style: .continuous))
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityAddTraits(on ? .isSelected : [])
            }
        }
        .padding(2)
        .background(theme.surface, in: RoundedRectangle(cornerRadius: Radius.sm + 1, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Radius.sm + 1, style: .continuous).strokeBorder(theme.hairline))
        .opacity(enabled ? 1 : 0.45)
        .fixedSize()
    }
}

// MARK: - Scrollers

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
