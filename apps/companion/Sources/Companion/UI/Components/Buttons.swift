import SwiftUI

// Buttons (Figma Components: Button 3:218, Toolbar button 3:219, Icon button 3:223). Kind x
// state: hover, pressed (scale 0.97), focused (a 2 pt ring 2 pt outside, keyboard focus only,
// never removed), disabled (45% opacity), loading (the label stays and the Checking spinner
// joins it, so the width never jumps). 28 pt tall.

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
        @State private var hovering = false

        var body: some View {
            HStack(spacing: 6) {
                if loading {
                    StatusGlyph(kind: .checking, color: kind == .primary ? .secondary : .accent, size: 14)
                }
                configuration.label
                    .textStyle(.bodyEmphasis)
                    .foregroundStyle(foreground)
                    .lineLimit(1)
            }
            .padding(.horizontal, Gap.x12)
            .frame(height: Metrics.buttonHeight)
            .background(RoundedRectangle(cornerRadius: Corner.control).fill(fill))
            .overlay {
                if kind == .secondary {
                    RoundedRectangle(cornerRadius: Corner.control).strokeBorder(Palette.border, lineWidth: 1)
                }
            }
            .shadow(color: kind == .secondary ? .black.opacity(0.05) : .clear, radius: 0.5, y: 1)
            .focusRing(isFocused, radius: Corner.control)
            .opacity(isEnabled ? 1 : 0.45)
            .scaleEffect(configuration.isPressed && !reduceMotion ? Motion.pressedScale : 1)
            .animation(Motion.change(Motion.press, reduce: reduceMotion), value: configuration.isPressed)
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
            let active = isEnabled && (configuration.isPressed || hovering)
            switch kind {
            case .primary:
                return configuration.isPressed ? Palette.primary.opacity(0.85) : (active ? Palette.primary.opacity(0.9) : Palette.primary)
            case .secondary:
                return configuration.isPressed ? Palette.bgSelected : (active ? Palette.bgHover : Palette.bgRaised)
            case .plain:
                return configuration.isPressed ? Palette.bgSelected : (active ? Palette.bgHover : .clear)
            }
        }
    }
}

/// A labeled toolbar action with its icon (Activity, Message, Recording). `on` while its panel is open.
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
                .labelStyle(ToolbarLabelStyle())
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

private struct ToolbarLabelStyle: LabelStyle {
    @Environment(\.redactsGuestScreen) private var counting

    func makeBody(configuration: Configuration) -> some View {
        HStack(spacing: 6) {
            // Hidden while the harness counts words: a text recogniser reads icons as letters.
            configuration.icon.frame(width: 16, height: 16).font(.system(size: 13)).opacity(counting ? 0 : 1)
            configuration.title.textStyle(.body)
        }
        .foregroundStyle(Palette.textSecondary)
    }
}

/// An icon-only button for universally known actions (search, more, close, settings). Always
/// has a tooltip with its name, and its name for VoiceOver.
struct IconButton: View {
    var systemImage: String
    var name: String
    var action: () -> Void
    @Environment(\.redactsGuestScreen) private var counting

    var body: some View {
        Button(action: action) {
            Image(systemName: systemImage)
                .font(.system(size: 13))
                .frame(width: 16, height: 16)
                .opacity(counting ? 0 : 1)
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
    /// The focus ring: 2 pt in the focus colour, 2 pt outside the control, shown only while
    /// the keyboard focuses it.
    func focusRing(_ shown: Bool, radius: CGFloat) -> some View {
        overlay {
            if shown {
                RoundedRectangle(cornerRadius: radius + Metrics.focusRingGap)
                    .strokeBorder(Palette.focusRing, lineWidth: Metrics.focusRingWidth)
                    .padding(-(Metrics.focusRingGap + Metrics.focusRingWidth))
                    .allowsHitTesting(false)
            }
        }
    }
}

/// A keycap: a shortcut shown only in menus, tooltips and the palette.
struct Keycap: View {
    var keys: String

    var body: some View {
        Text(keys)
            .textStyle(.caption)
            .foregroundStyle(Palette.textSecondary)
            .padding(.horizontal, 5)
            .frame(minWidth: 20, minHeight: 20)
            .background(RoundedRectangle(cornerRadius: Corner.keycap).fill(Palette.bgHover))
            .accessibilityLabel("shortcut \(keys)")
    }
}
