import SwiftUI

// Buttons (Figma Components: Button 3:218, Toolbar button 3:219, Icon button 3:223, Keycap
// 3:228). Kind x state, with the file's numbers: 28 tall, 12 at the sides, radius 6, Body
// Emphasis; hover and pressed fills from the variants; pressed scales to 0.97 over 120 ms;
// focused shows a 2 pt ring outside a 2 pt gap (keyboard focus only, never removed); disabled
// is 45% opacity; loading keeps the label and adds the checking ring (14 pt, 6 before the
// label) so the width does not jump while it sends.

/// Primary (one per state, top right of the header), secondary, or plain.
enum ActionKind: Sendable {
    case primary, secondary, plain
}

struct ActionButtonStyle: ButtonStyle {
    var kind: ActionKind
    var loading = false

    func makeBody(configuration: Configuration) -> some View {
        StyledButton(configuration: configuration, kind: kind, loading: loading)
    }

    private struct StyledButton: View {
        let configuration: ButtonStyleConfiguration
        let kind: ActionKind
        let loading: Bool
        @Environment(\.isEnabled) private var isEnabled
        @Environment(\.isFocused) private var isFocused
        @Environment(\.accessibilityReduceMotion) private var reduceMotion
        @Environment(\.colorScheme) private var scheme
        @State private var hovering = false

        var body: some View {
            HStack(spacing: 6) {
                if loading {
                    StatusGlyph(kind: .checking, color: .accent, size: 14, onPrimary: kind == .primary)
                }
                configuration.label
                    .textStyle(.bodyEmphasis)
                    .foregroundStyle(foreground)
                    .lineLimit(1)
                    .clonePart("Text")
            }
            .padding(.horizontal, Gap.x12)
            .frame(height: Metrics.buttonHeight)
            .background {
                RoundedRectangle(cornerRadius: Corner.control)
                    .fill(fill)
                    // Figma: drop shadow 0 1 blur 1 at 5%; SwiftUI's radius is half the blur.
                    .shadow(color: kind == .secondary ? .black.opacity(0.05) : .clear, radius: 0.5, y: 1)
            }
            .overlay {
                if kind == .secondary {
                    RoundedRectangle(cornerRadius: Corner.control).strokeBorder(Palette.border, lineWidth: 1)
                }
            }
            .focusRing(isFocused, radius: Corner.control)
            .opacity(isEnabled ? 1 : 0.45)
            .scaleEffect(configuration.isPressed && !reduceMotion ? Motion.pressedScale : 1)
            .animation(Motion.change(Motion.press, reduce: reduceMotion), value: configuration.isPressed)
            .animation(Motion.change(Motion.press, reduce: reduceMotion), value: hovering)
            .contentShape(Rectangle())
            .onHover { hovering = $0 }
        }

        private var foreground: Color {
            switch kind {
            case .primary: Palette.onPrimary
            case .secondary: Palette.text
            case .plain: Palette.textSecondary
            }
        }

        private var fill: Color {
            let pressed = configuration.isPressed && isEnabled
            let hovered = hovering && isEnabled
            switch kind {
            case .primary:
                // Figma: black at 86% on hover and 76% pressed; in dark, the light fill the same way.
                let base: Color = scheme == .dark ? Palette.primary : .black
                if pressed { return base.opacity(0.76) }
                return hovered ? base.opacity(0.86) : Palette.primary
            case .secondary:
                return pressed ? Palette.bgSelected : (hovered ? Palette.bgHover : Palette.bgRaised)
            case .plain:
                return pressed ? Palette.bgSelected : (hovered ? Palette.bgHover : .clear)
            }
        }
    }
}

/// A labeled toolbar action with its icon (Activity, Message, Recording): 28 tall, 8 before
/// the icon, 6 between icon and word, 10 after. `on` while its panel is open.
struct ToolbarButton: View {
    var icon: Icon
    var title: String
    var on = false
    var action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 6) {
                IconView(icon: icon).clonePart("Icon/\(icon.rawValue)")
                Text(title).textStyle(.body).lineLimit(1).clonePart("Text")
            }
            .foregroundStyle(Palette.textSecondary)
        }
        .buttonStyle(ToolbarButtonStyle(on: on))
    }
}

struct ToolbarButtonStyle: ButtonStyle {
    var on = false

    func makeBody(configuration: Configuration) -> some View {
        Styled(configuration: configuration, on: on)
    }

    private struct Styled: View {
        let configuration: ButtonStyleConfiguration
        let on: Bool
        @Environment(\.isEnabled) private var isEnabled
        @Environment(\.isFocused) private var isFocused
        @State private var hovering = false

        var body: some View {
            configuration.label
                .padding(.leading, Gap.x8)
                .padding(.trailing, 10)
                .frame(height: Metrics.buttonHeight)
                .background(RoundedRectangle(cornerRadius: Corner.control).fill(fill))
                .focusRing(isFocused, radius: Corner.control)
                .opacity(isEnabled ? 1 : 0.45)
                .contentShape(Rectangle())
                .onHover { hovering = $0 }
        }

        private var fill: Color {
            if on || configuration.isPressed { return Palette.bgSelected }
            return hovering && isEnabled ? Palette.bgHover : .clear
        }
    }
}

/// An icon-only button for universally known actions (search, more, close, settings): 28
/// square, the icon 16. Always has a tooltip with its name, and its name for VoiceOver.
struct IconButton: View {
    var icon: Icon
    var name: String
    var action: () -> Void

    var body: some View {
        Button(action: action) {
            IconView(icon: icon).clonePart("Icon/\(icon.rawValue)")
        }
        .buttonStyle(IconButtonStyle())
        .help(name)
        .accessibilityLabel(name)
    }
}

struct IconButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        Styled(configuration: configuration)
    }

    private struct Styled: View {
        let configuration: ButtonStyleConfiguration
        @Environment(\.isFocused) private var isFocused
        @State private var hovering = false

        var body: some View {
            configuration.label
                .foregroundStyle(Palette.textSecondary)
                .frame(width: Metrics.buttonHeight, height: Metrics.buttonHeight)
                .background(RoundedRectangle(cornerRadius: Corner.control)
                    .fill(configuration.isPressed ? Palette.bgSelected : (hovering ? Palette.bgHover : .clear)))
                .focusRing(isFocused, radius: Corner.control)
                .contentShape(Rectangle())
                .onHover { hovering = $0 }
        }
    }
}

extension View {
    /// The focus ring (Figma: a 2 pt spread in the surface colour, then 2 pt in the focus
    /// colour): 2 pt outside a 2 pt gap, shown only while the keyboard focuses the control.
    func focusRing(_ shown: Bool, radius: CGFloat) -> some View {
        overlay {
            if shown {
                let gap = Metrics.focusRingGap, width = Metrics.focusRingWidth
                ZStack {
                    RoundedRectangle(cornerRadius: radius + gap)
                        .strokeBorder(Palette.bg, lineWidth: gap)
                        .padding(-gap)
                    RoundedRectangle(cornerRadius: radius + gap + width)
                        .strokeBorder(Palette.focusRing, lineWidth: width)
                        .padding(-(gap + width))
                }
                .allowsHitTesting(false)
            }
        }
    }
}

/// A keycap: a shortcut shown only in menus, tooltips and the palette. 20 tall, 5 at the
/// sides, radius 4, Caption.
struct Keycap: View {
    var keys: String

    var body: some View {
        Text(keys)
            .textStyle(.caption)
            .foregroundStyle(Palette.textSecondary)
            .padding(.horizontal, 5)
            .frame(minWidth: 20)
            .frame(height: 20)
            .background(RoundedRectangle(cornerRadius: Corner.keycap).fill(Palette.bgHover))
            .accessibilityLabel("shortcut \(keys)")
    }
}
