import SwiftUI

/// Cmd-K (ADR 0005): every action with its key, filtered live as you type. Disabled ones
/// show dim with why; choosing one runs it, and the key shown teaches it. With nothing
/// matching it says what was searched and offers the nearest action, searching the runs
/// (Beautiful UI's Search, ported as behaviour). Its keys (arrows, Return, esc) come from
/// the registry's palette entries through `KeyRouter`; the field types the rest.
struct CommandPalette: View {
    @Bindable var keyboard: KeyboardModel

    @FocusState private var fieldFocused: Bool
    @Environment(\.theme) private var theme

    static let width: CGFloat = 560
    private static let listHeight: CGFloat = 340

    var body: some View {
        let items = keyboard.paletteItems()
        let selected = items.isEmpty ? nil : min(keyboard.paletteIndex, items.count - 1)
        VStack(spacing: 0) {
            field
            Hairline()
            if items.isEmpty {
                empty
            } else {
                list(items, selected: selected)
            }
            Hairline()
            footer(count: items.count, empty: items.isEmpty)
        }
        .frame(width: Self.width)
        .background(theme.background, in: RoundedRectangle(cornerRadius: Radius.lg, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Radius.lg, style: .continuous).strokeBorder(theme.hairline))
        .shadow(color: .black.opacity(theme.id.isDark ? 0.5 : 0.14), radius: 24, y: 10)
        // Once the field is in the window: set while it is still being added, focus lands
        // on the window's first text field instead (the run search).
        .task {
            fieldFocused = true
            try? await Task.sleep(for: .milliseconds(30))
            fieldFocused = true
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Command palette")
    }

    private var field: some View {
        HStack(spacing: Space.m) {
            Text("›")
                .font(Typeface.monoBold.font(size: TypeScale.mono))
                .foregroundStyle(theme.brand)
            TextField("Type a command", text: $keyboard.paletteQuery)
                .textFieldStyle(.plain)
                .font(Typeface.readingRegular.font(size: TypeScale.reading))
                .focused($fieldFocused)
            KeyLabel(label: ActionRegistry.label(.palette))
                .foregroundStyle(.secondary)
        }
        .padding(.horizontal, Space.l)
        .frame(height: 48)
    }

    private func list(_ items: [PaletteItem], selected: Int?) -> some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(spacing: 2) {
                    ForEach(Array(items.enumerated()), id: \.element.id) { index, item in
                        PaletteRow(item: item, selected: index == selected)
                            .onHover { inside in
                                if inside, keyboard.paletteIndex != index { keyboard.paletteIndex = index }
                            }
                            .onTapGesture { keyboard.runPaletteItem(item) }
                    }
                }
                .padding(Space.s)
            }
            .overlayScrollers()
            .frame(maxHeight: Self.listHeight)
            .fixedSize(horizontal: false, vertical: true)
            .onChange(of: keyboard.paletteIndex) {
                // By the row's `ForEach` identity, the only one a lazy stack knows.
                if let selected { proxy.scrollTo(items[selected].id) }
            }
        }
    }

    /// Says what was searched, then the nearest thing to do about it.
    private var empty: some View {
        let query = keyboard.paletteQuery.trimmingCharacters(in: .whitespaces)
        return VStack(spacing: Space.s) {
            Text("No matching command")
                .headingStyle()
            Text("Nothing in the commands matches \u{201C}\(query)\u{201D}.")
                .readingStyle(size: TypeScale.readingSmall)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
            if !keyboard.store.runs.isEmpty, !query.isEmpty {
                Button {
                    keyboard.runPaletteSelection()
                } label: {
                    HStack(spacing: Space.s) {
                        KeyLabel(label: ActionRegistry.label(.paletteRun))
                        Text("Search the runs for \u{201C}\(query)\u{201D}")
                    }
                }
                .buttonStyle(.quiet(small: true))
                .padding(.top, Space.xs)
            }
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, Space.xl)
        .padding(.horizontal, Space.l)
    }

    private func footer(count: Int, empty: Bool) -> some View {
        HStack(spacing: Space.l) {
            Text(empty ? "0 commands" : count == 1 ? "1 command" : "\(count) commands")
                .monospacedDigit()
            Spacer()
            footerKey("↑↓", "move")
            footerKey("⏎", empty ? "search runs" : "run")
            footerKey("esc", "close")
        }
        .font(Typeface.readingRegular.font(size: TypeScale.small))
        .foregroundStyle(.secondary)
        .padding(.horizontal, Space.l)
        .frame(height: 32)
    }

    private func footerKey(_ key: String, _ title: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            KeyLabel(label: key)
                .foregroundStyle(theme.foreground)
            Text(title)
        }
    }
}

/// A command: its title (and why not, when it cannot run here), its group, its key. The
/// selection is filled with the brand, as the open run is.
private struct PaletteRow: View {
    let item: PaletteItem
    let selected: Bool

    @Environment(\.theme) private var theme

    var body: some View {
        let ink = selected ? theme.brandText : item.enabled ? theme.foreground : theme.dim
        let quiet = selected ? theme.brandText.opacity(0.8) : theme.dim
        HStack(alignment: .firstTextBaseline, spacing: Space.m) {
            Text(item.title)
                .font(Typeface.readingMedium.font(size: TypeScale.readingSmall))
                .foregroundStyle(ink)
                .lineLimit(1)
                .layoutPriority(1)
            if let reason = item.reason {
                Text(reason)
                    .font(Typeface.readingRegular.font(size: TypeScale.small))
                    .foregroundStyle(quiet)
                    .lineLimit(1)
                    .truncationMode(.tail)
            }
            Spacer(minLength: Space.s)
            Text(item.group.lowercased())
                .font(Typeface.monoRegular.font(size: TypeScale.monoSmall))
                .foregroundStyle(quiet)
                .fixedSize()
            KeyLabel(label: item.key)
                .foregroundStyle(ink)
                .frame(minWidth: 36, alignment: .trailing)
                .fixedSize()
        }
        .padding(.horizontal, Space.m)
        .frame(height: 32)
        .background(selected ? theme.brand : .clear, in: RoundedRectangle(cornerRadius: Radius.sm, style: .continuous))
        .contentShape(Rectangle())
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}
