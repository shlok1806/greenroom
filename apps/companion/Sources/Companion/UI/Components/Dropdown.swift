import AppKit
import Observation
import SwiftUI

// The app's own menus and segmented controls (redesign 7): no NSMenu, no system Picker. The
// dropdown is shadcn/ui's DropdownMenu (MIT, `apps/v4/registry/new-york-v4/ui/dropdown-menu.tsx`,
// on Radix's menu behaviour: arrows move, Return runs, Esc closes, typing jumps to the item
// that starts with it, the pointer highlights) drawn with the Figma tokens and opened with
// Beautiful UI's `pop-in` (opacity 0 to 1, scale 0.95 to 1, 160 ms on the design's curve). The
// segmented control is Beautiful UI's SegmentedControl (docs/22 C14, spec
// `segmented-control.json`): a 2 pt inset track, a raised thumb that slides 200 ms. See
// ACKNOWLEDGEMENTS.md.

/// One row of a dropdown.
struct DropdownItem: Identifiable {
    let id: String
    let title: String
    var icon: Icon?
    /// The key that does the same outside the menu, as a keycap shows it.
    var keys: String?
    var checked = false
    var destructive = false
    var disabled = false
    /// A rule above this row.
    var separated = false
    let action: () -> Void
}

/// The one open dropdown, drawn by `DropdownLayer` over the whole window so nothing clips it.
@Observable
@MainActor
final class DropdownCenter {
    struct Open {
        var id: String
        /// The trigger's frame in the window.
        var anchor: CGRect
        var items: [DropdownItem]
        var width: CGFloat
        /// Right edges line up (a trigger at the window's right), else left edges.
        var alignTrailing: Bool
    }

    private(set) var open: Open?
    /// The row the keyboard or the pointer is on.
    var highlighted: Int?
    @ObservationIgnored private var typed = ""
    @ObservationIgnored private var typedAt = Date.distantPast

    var isOpen: Bool { open != nil }

    func toggle(_ id: String, anchor: CGRect, items: [DropdownItem], width: CGFloat = 240, alignTrailing: Bool = true) {
        if open?.id == id {
            close()
            return
        }
        open = Open(id: id, anchor: anchor, items: items, width: width, alignTrailing: alignTrailing)
        highlighted = nil
    }

    func close() {
        open = nil
        highlighted = nil
    }

    /// Runs a row and closes the menu.
    func run(_ index: Int) {
        guard let item = open?.items[safe: index], !item.disabled else { return }
        close()
        item.action()
    }

    /// Down and Up move over the rows that can run, wrapping.
    func move(by delta: Int) {
        guard let items = open?.items, items.contains(where: { !$0.disabled }) else { return }
        var index = highlighted ?? (delta > 0 ? -1 : items.count)
        repeat {
            index = (index + delta + items.count) % items.count
        } while items[index].disabled
        highlighted = index
    }

    /// Typing jumps to the first row starting with what was typed (within a second).
    func typeAhead(_ characters: String, now: Date = Date()) {
        guard let items = open?.items else { return }
        typed = now.timeIntervalSince(typedAt) < 1 ? typed + characters.lowercased() : characters.lowercased()
        typedAt = now
        if let index = items.firstIndex(where: { !$0.disabled && $0.title.lowercased().hasPrefix(typed) }) {
            highlighted = index
        }
    }

    /// The menu's keys while it is open; true when the key was the menu's. Every key is,
    /// so nothing reaches the window behind an open menu.
    func handle(keyCode: UInt16, characters: String, flags: NSEvent.ModifierFlags) -> Bool {
        switch keyCode {
        case 53, 48: close()                                       // esc, tab
        case 125: move(by: 1)                                      // down
        case 126: move(by: -1)                                     // up
        case 115: highlighted = nil; move(by: 1)                   // home: the first row
        case 119: highlighted = nil; move(by: -1)                  // end: the last row
        case 36, 76, 49: if let highlighted { run(highlighted) }   // return, enter, space
        default:
            if flags.intersection([.command, .control, .option]).isEmpty, !characters.isEmpty {
                typeAhead(characters)
            } else {
                close()
                return false
            }
        }
        return true
    }
}

extension Array {
    subscript(safe index: Int) -> Element? { indices.contains(index) ? self[index] : nil }
}

/// A button that opens a dropdown under it.
struct DropdownButton<Label: View>: View {
    let center: DropdownCenter
    let id: String
    var width: CGFloat = 240
    var alignTrailing = true
    let items: () -> [DropdownItem]
    @ViewBuilder var label: () -> Label
    @State private var frame: CGRect = .zero

    var body: some View {
        Button {
            center.toggle(id, anchor: frame, items: items(), width: width, alignTrailing: alignTrailing)
        } label: {
            label()
        }
        .onGeometryChange(for: CGRect.self) { $0.frame(in: .named(DropdownLayer.space)) } action: { frame = $0 }
        .accessibilityAddTraits(.isButton)
        .accessibilityValue(center.open?.id == id ? "open" : "closed")
    }
}

/// Draws the open dropdown over the window: a click outside closes it.
struct DropdownLayer: View {
    nonisolated static let space = "dropdown-layer"
    let center: DropdownCenter
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        GeometryReader { geo in
            if let open = center.open {
                ZStack(alignment: .topLeading) {
                    Color.black.opacity(0.001)
                        .onTapGesture { center.close() }
                        .accessibilityHidden(true)
                    DropdownPanel(center: center, open: open)
                        .frame(width: open.width)
                        .offset(x: x(open, in: geo.size), y: min(open.anchor.maxY + 4, geo.size.height - 40))
                        .transition(reduceMotion ? .identity : .asymmetric(
                            insertion: .opacity.combined(with: .scale(scale: 0.95, anchor: open.alignTrailing ? .topTrailing : .topLeading))
                                .animation(Motion.easeOut(0.16)),
                            removal: .opacity.animation(Motion.easeOut(0.1))))
                }
            }
        }
        .allowsHitTesting(center.isOpen)
    }

    private func x(_ open: DropdownCenter.Open, in size: CGSize) -> CGFloat {
        let raw = open.alignTrailing ? open.anchor.maxX - open.width : open.anchor.minX
        return min(max(8, raw), size.width - open.width - 8)
    }
}

/// The menu itself: 4 pt inside, rows 28 tall, radius 8, raised, a hairline; the highlighted
/// row filled, a destructive row in the failure colour, keys as keycaps.
struct DropdownPanel: View {
    let center: DropdownCenter
    let open: DropdownCenter.Open

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(Array(open.items.enumerated()), id: \.element.id) { index, item in
                if item.separated && index > 0 {
                    Rectangle().fill(Palette.border).frame(height: 1).padding(.vertical, 4).padding(.horizontal, -4)
                }
                row(item, index: index)
            }
        }
        .padding(4)
        .background(RoundedRectangle(cornerRadius: Corner.sheet).fill(Palette.bgRaised))
        .overlay(RoundedRectangle(cornerRadius: Corner.sheet).strokeBorder(Palette.border, lineWidth: 1))
        .shadow(color: .black.opacity(Elevation.raisedOpacity), radius: Elevation.raisedRadius / 2, y: Elevation.raisedY)
        .accessibilityElement(children: .contain)
        .accessibilityAddTraits(.isModal)
        .accessibilityIdentifier("dropdown")
    }

    private func row(_ item: DropdownItem, index: Int) -> some View {
        let on = center.highlighted == index
        let ink = item.disabled ? Palette.textTertiary : (item.destructive ? Palette.fail : Palette.text)
        return HStack(spacing: Gap.x8) {
            Group {
                if item.checked {
                    IconView(icon: .check, size: 14)
                } else if let icon = item.icon {
                    IconView(icon: icon, size: 14)
                } else {
                    Color.clear
                }
            }
            .frame(width: 14, height: 14)
            .foregroundStyle(item.destructive ? Palette.fail : Palette.textSecondary)
            Text(item.title).textStyle(.body).foregroundStyle(ink).lineLimit(1)
            Spacer(minLength: Gap.x8)
            if let keys = item.keys {
                Keycap(keys: keys)
            }
        }
        .padding(.horizontal, Gap.x8)
        .frame(height: 28)
        .background(RoundedRectangle(cornerRadius: Corner.control).fill(on && !item.disabled ? Palette.bgSelected : .clear))
        .contentShape(Rectangle())
        .onHover { inside in if inside { center.highlighted = index } else if center.highlighted == index { center.highlighted = nil } }
        .onTapGesture { center.run(index) }
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isButton)
        .accessibilityLabel(item.title)
        .accessibilityAction { center.run(index) }
    }
}

/// Beautiful UI's SegmentedControl in the design's tokens: a 2 pt inset track, the chosen
/// option's thumb raised with a hairline, sliding 200 ms; labels Body, the chosen one in the
/// text colour, the others secondary. With Full Keyboard Access each option takes Tab and Space.
struct SegmentedControl<Value: Hashable>: View {
    let options: [(value: Value, title: String)]
    @Binding var selection: Value
    var height: CGFloat = 26
    @Namespace private var thumb
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.isEnabled) private var isEnabled

    var body: some View {
        HStack(spacing: 0) {
            ForEach(Array(options.enumerated()), id: \.offset) { _, option in
                let on = option.value == selection
                Button {
                    withAnimation(reduceMotion ? nil : Motion.easeOut(0.2)) { selection = option.value }
                } label: {
                    Text(option.title).textStyle(on ? .bodyEmphasis : .body)
                        .foregroundStyle(on ? Palette.text : Palette.textSecondary)
                        .padding(.horizontal, 10)
                        .frame(height: height - 4)
                        .background {
                            if on {
                                RoundedRectangle(cornerRadius: Corner.control - 1)
                                    .fill(Palette.bgRaised)
                                    .overlay(RoundedRectangle(cornerRadius: Corner.control - 1).strokeBorder(Palette.border, lineWidth: 1))
                                    .shadow(color: .black.opacity(0.06), radius: 1, y: 1)
                                    .matchedGeometryEffect(id: "thumb", in: thumb)
                            }
                        }
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityAddTraits(on ? [.isButton, .isSelected] : .isButton)
            }
        }
        .padding(2)
        .background(RoundedRectangle(cornerRadius: Corner.control + 1).fill(Palette.border.opacity(0.6)))
        .opacity(isEnabled ? 1 : 0.5)
        .fixedSize()
    }
}
