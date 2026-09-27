import SwiftUI

/// The top bar's More menu (companion ADR 0016), drawn by the app rather than as a native
/// `NSMenu`: the theme's background with a hairline, titles in Mona Sans, keys as the hint
/// bar draws them, the selection filled with the brand. Its rows are `MoreMenu.items`, from
/// the registry; its keys come through `KeyRouter` like the palette's. `RootView` hangs it
/// under the button and closes it on a click outside.
struct MoreMenuView: View {
    let keyboard: KeyboardModel

    @Environment(\.theme) private var theme

    static let width: CGFloat = 296
    static let rowHeight: CGFloat = 30

    var body: some View {
        let items = keyboard.moreItems()
        VStack(alignment: .leading, spacing: 0) {
            ForEach(Array(items.enumerated()), id: \.element.id) { index, item in
                if MoreMenu.startsSection(index, in: items) {
                    Hairline()
                        .padding(.vertical, Space.xs)
                        .padding(.horizontal, Space.s)
                        .accessibilityHidden(true)
                }
                MoreRow(item: item, selected: item.id == keyboard.moreSelection) { keyboard.runMoreItem(item) }
                    .onHover { inside in
                        if inside, keyboard.moreSelection != item.id { keyboard.moreSelection = item.id }
                    }
            }
        }
        .padding(Space.xs)
        .frame(width: Self.width)
        .ground(.background)
        .background(theme.background, in: RoundedRectangle(cornerRadius: Radius.md, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Radius.md, style: .continuous).strokeBorder(theme.hairline))
        .shadow(color: .black.opacity(theme.id.isDark ? 0.45 : 0.14), radius: 16, y: 6)
        .accessibilityElement(children: .contain)
        .accessibilityLabel("More")
        .accessibilityAddTraits(.isModal)
    }
}

/// One row: the title, Builds and Updates' news, then the key as the hint bar shows it. The
/// selection is the brand; Destroy Machine's is the failure colour, and its words are too.
private struct MoreRow: View {
    let item: MoreItem
    let selected: Bool
    let action: () -> Void

    @Environment(\.theme) private var theme

    private var fill: Color {
        guard selected else { return .clear }
        return item.destructive ? theme.color(.failure) : theme.brand
    }

    /// On a filled row the fill's own text colour; otherwise the foreground, or the failure
    /// role for Destroy.
    private var ink: Color {
        if selected { return item.destructive ? theme.background : theme.brandText }
        return item.destructive ? theme.color(.failure, on: .background) : theme.foreground
    }

    var body: some View {
        Button(action: action) {
            HStack(alignment: .firstTextBaseline, spacing: Space.m) {
                Text(item.title)
                    .font(Typeface.readingMedium.font(size: TypeScale.readingSmall))
                    .foregroundStyle(ink)
                    .lineLimit(1)
                    .layoutPriority(1)
                Spacer(minLength: Space.s)
                if let badge = item.badge {
                    MoreBadgeView(badge: badge, ink: selected ? ink : nil)
                }
                if !item.key.isEmpty {
                    KeyLabel(label: item.key)
                        .foregroundStyle(ink)
                        .frame(minWidth: 28, alignment: .trailing)
                        .fixedSize()
                }
            }
            .padding(.horizontal, Space.m)
            .frame(height: MoreMenuView.rowHeight)
            .background(fill, in: RoundedRectangle(cornerRadius: Radius.sm, style: .continuous))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(item.badge.map { "\(item.title), \($0.spoken)" } ?? item.title)
        .accessibilityHint(item.key.isEmpty ? "" : "Key \(item.key)")
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

/// "3 new", "rebuild", "mismatch": news in words, in the attention role with a hairline of
/// it, as a small mono tag. On a filled row it takes the row's ink.
private struct MoreBadgeView: View {
    let badge: MoreBadge
    var ink: Color?

    @Environment(\.theme) private var theme

    var body: some View {
        let color = ink ?? theme.color(.attention, on: .background)
        Text(badge.text)
            .font(Typeface.monoMedium.font(size: TypeScale.label))
            .foregroundStyle(color)
            .padding(.horizontal, Space.xs)
            .frame(height: 16)
            .overlay(RoundedRectangle(cornerRadius: Radius.sm, style: .continuous).strokeBorder(color.opacity(0.6)))
            .fixedSize()
            .accessibilityHidden(true)
    }
}

/// "More ▾" in the top bar: opens and closes the menu, and tells `RootView` where it is, in
/// window points, so the menu hangs from it. While open it draws pressed, with a brand edge.
struct MoreButton: View {
    let keyboard: KeyboardModel

    var body: some View {
        let open = keyboard.moreOpen
        Button {
            if open { keyboard.closeMore() } else { keyboard.openMore(byKey: false) }
        } label: {
            Text(open ? "More ▴" : "More ▾")
        }
        .buttonStyle(.quiet(active: open))
        .fixedSize()
        .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: { frame in
            if keyboard.moreAnchor != frame { keyboard.moreAnchor = frame }
        }
        .onDisappear { keyboard.moreAnchor = nil }
        .help("Screenshot, export, the conversation, builds and updates, destroy (\(ActionRegistry.label(.more)))")
        .accessibilityLabel("More")
        .accessibilityValue(open ? "open" : "closed")
        .accessibilityHint("Key \(ActionRegistry.label(.more))")
    }
}
