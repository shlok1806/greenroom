import SwiftUI

/// The window's bottom row (ADR 0005): the keys that work right now, Bubble Tea `help`
/// style, keys in the mono face and what they do in the reading face. Only what
/// `HintBar.content` says works for the focused context; `?` expands it in place into
/// `KeyHelpPanel`. A hint is also a button, for a person reading it with the mouse.
struct HintBarView: View {
    let keyboard: KeyboardModel

    @Environment(\.theme) private var theme

    static let height: CGFloat = 30

    var body: some View {
        // Ticks only while an undo counts down; otherwise the row redraws on its own changes.
        TimelineView(.periodic(from: .now, by: keyboard.store.verdictUndo.pending == nil ? 3600 : 0.25)) { tick in
            let state = keyboard.state(now: tick.date)
            let content = HintBar.content(state)
            row(content, trailing: HintBar.trailing(state))
        }
        .frame(height: Self.height)
        .frame(maxWidth: .infinity)
        .ground(.chrome)
        .background(theme.chromeTint)
        .overlay(alignment: .top) { Hairline() }
    }

    @ViewBuilder
    private func row(_ content: HintBarContent, trailing: KeyHint?) -> some View {
        switch content.mode {
        case .driving:
            driving(focused: true)
        case .confirm:
            confirm(content)
        default:
            HStack(spacing: Space.l) {
                if content.mode == .drivingElsewhere {
                    drivingBadge
                }
                if let context = content.context {
                    Text(content.mode == .prefix ? context : context.uppercased())
                        .font(content.mode == .prefix ? Typeface.monoBold.font(size: TypeScale.monoSmall)
                            : Typeface.monoMedium.font(size: TypeScale.label))
                        .tracking(content.mode == .prefix ? 0 : 0.8)
                        .foregroundStyle(content.mode == .prefix ? theme.foreground : theme.dim(on: .chrome))
                        .fixedSize()
                        .accessibilityLabel(content.mode == .prefix ? "\(context), then" : "Keys for the \(context)")
                }
                if let undo = content.undo {
                    UndoHintView(undo: undo) { keyboard.perform(.undo) }
                }
                // Whole hints drop from the end until the row fits; none is cut mid-word.
                ViewThatFits(in: .horizontal) {
                    ForEach((0...content.hints.count).reversed(), id: \.self) { kept in
                        hints(Array(content.hints.prefix(kept)))
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                if let trailing {
                    HintItem(hint: trailing) { keyboard.perform(.palette) }
                }
            }
            .padding(.horizontal, Space.l)
        }
    }

    private func hints(_ hints: [KeyHint]) -> some View {
        HStack(spacing: Space.l) {
            ForEach(Array(hints.enumerated()), id: \.offset) { _, hint in
                HintItem(hint: hint) {
                    if let id = hint.id { keyboard.perform(id, in: hint.context) }
                }
            }
        }
        .fixedSize()
    }

    // MARK: - Modes

    private var drivingBadge: some View {
        Text("DRIVING")
            .font(Typeface.monoBold.font(size: TypeScale.label))
            .tracking(0.8)
            .foregroundStyle(theme.background)
            .padding(.horizontal, Space.s)
            .frame(height: 18)
            .background(theme.color(.driving), in: RoundedRectangle(cornerRadius: Radius.sm, style: .continuous))
            .fixedSize()
    }

    /// Every key goes to the machine; only a click on the switch gives it back.
    private func driving(focused: Bool) -> some View {
        HStack(spacing: Space.m) {
            drivingBadge
            Text("all keys → machine · click Give Back to return")
                .font(Typeface.monoMedium.font(size: TypeScale.monoSmall))
                .foregroundStyle(theme.color(.driving, on: .chrome))
                .lineLimit(1)
                .truncationMode(.tail)
            Spacer(minLength: 0)
        }
        .padding(.horizontal, Space.l)
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Driving: every key goes to the machine. Click Give Back to return.")
    }

    /// "Destroy the machine?" asked inline, never in a dialog: Return destroys, anything
    /// else keeps it.
    private func confirm(_ content: HintBarContent) -> some View {
        HStack(spacing: Space.l) {
            Text("Destroy the machine?")
                .font(Typeface.readingSemiBold.font(size: TypeScale.readingSmall))
                .foregroundStyle(theme.color(.failure, on: .chrome))
                .fixedSize()
            Text("The run ends. The coding agent sees it.")
                .font(Typeface.readingRegular.font(size: TypeScale.small))
                .foregroundStyle(theme.dim(on: .chrome))
                .lineLimit(1)
                .truncationMode(.tail)
            Spacer(minLength: Space.s)
            ForEach(Array(content.hints.enumerated()), id: \.offset) { _, hint in
                HintItem(hint: hint, tint: hint.id == .confirmDestroy ? .failure : nil) {
                    if let id = hint.id { keyboard.perform(id, in: .confirm) }
                }
            }
        }
        .padding(.horizontal, Space.l)
    }
}

/// One key and its word: `⏎ open`. The key in Monaspace Neon, the word in Mona Sans.
private struct HintItem: View {
    let hint: KeyHint
    var tint: Role?
    let action: () -> Void

    @Environment(\.theme) private var theme
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                KeyLabel(label: hint.key)
                    .foregroundStyle(tint.map { theme.color($0, on: .chrome) } ?? theme.foreground)
                Text(hint.title)
                    .font(Typeface.readingRegular.font(size: TypeScale.small))
                    .foregroundStyle(tint.map { theme.color($0, on: .chrome) } ?? theme.dim(on: .chrome))
                    .underline(hovering)
            }
            .fixedSize()
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .accessibilityLabel("\(hint.title), \(hint.key)")
    }
}

/// "accepting · u undo 4s": the choice is shown made, and nothing has been sent yet.
private struct UndoHintView: View {
    let undo: UndoHint
    let action: () -> Void

    @Environment(\.theme) private var theme

    var body: some View {
        Button(action: action) {
            HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                Text(undo.word)
                    .font(Typeface.readingSemiBold.font(size: TypeScale.small))
                    .foregroundStyle(theme.color(.attention, on: .chrome))
                Text("u")
                    .font(Typeface.monoBold.font(size: TypeScale.monoSmall))
                    .foregroundStyle(theme.foreground)
                Text("undo \(undo.seconds)s")
                    .font(Typeface.readingRegular.font(size: TypeScale.small))
                    .foregroundStyle(theme.dim(on: .chrome))
                    .monospacedDigit()
            }
            .fixedSize()
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help("Nothing is sent until the countdown ends")
        .accessibilityLabel("\(undo.word), undo within \(undo.seconds) seconds, u")
    }
}

// MARK: - Help

/// The hint bar expanded in place by `?`: every key, by group, dim where it does not work
/// right now. `?` or `esc` folds it.
struct KeyHelpPanel: View {
    let keyboard: KeyboardModel
    /// The sheet's tallest, over the panes (`tokens.json` `layout.helpMaxShare` of the
    /// window); past it the groups scroll.
    var maximumHeight: CGFloat = 320

    @Environment(\.theme) private var theme

    private static let keyColumn: CGFloat = 44
    /// The heading row and the sheet's padding, which do not scroll.
    private static let chrome: CGFloat = 56

    var body: some View {
        let groups = KeyHelp.groups(keyboard.state())
        VStack(alignment: .leading, spacing: Space.m) {
            HStack(alignment: .firstTextBaseline, spacing: Space.l) {
                SectionLabel(title: "All keys")
                Text("Dim keys do nothing here.")
                    .font(Typeface.readingRegular.font(size: TypeScale.small))
                    .foregroundStyle(theme.dim(on: .chrome))
                Spacer()
                HintItem(hint: KeyHint(id: .help, key: ActionRegistry.label(.help), title: "close")) {
                    keyboard.perform(.help)
                }
            }
            ScrollView(.vertical) {
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 160), spacing: Space.l, alignment: .topLeading)],
                          alignment: .leading, spacing: Space.l) {
                    ForEach(groups, id: \.group) { group in
                        column(group)
                    }
                }
            }
            .scrollBounceBehavior(.basedOnSize)
            .overlayScrollers()
            .frame(maxHeight: max(maximumHeight - Self.chrome, 80))
            .fixedSize(horizontal: false, vertical: true)
        }
        .padding(.horizontal, Space.l)
        .padding(.top, Space.m)
        .padding(.bottom, Space.m)
        .frame(maxWidth: .infinity, alignment: .leading)
        .ground(.chrome)
        .background(theme.chromeTint)
        .overlay(alignment: .top) { Hairline() }
        // A sheet over the panes, not a band that pushes them up.
        .shadow(color: .black.opacity(0.16), radius: 10, y: -2)
        .accessibilityElement(children: .contain)
        .accessibilityLabel("All keys")
    }

    private func column(_ group: KeyHelp.Group) -> some View {
        VStack(alignment: .leading, spacing: Space.xs) {
            Text(group.group.title)
                .font(Typeface.readingSemiBold.font(size: TypeScale.small))
                .foregroundStyle(theme.foreground)
                .padding(.bottom, Space.xs)
            ForEach(Array(group.hints.enumerated()), id: \.offset) { _, hint in
                let ink = hint.enabled ? theme.foreground : theme.dim(on: .chrome)
                HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                    KeyLabel(label: hint.key)
                        .foregroundStyle(ink)
                        .frame(width: Self.keyColumn, alignment: .leading)
                    Text(hint.title)
                        .font(Typeface.readingRegular.font(size: TypeScale.small))
                        .foregroundStyle(ink)
                        .lineLimit(2)
                }
                .accessibilityElement(children: .combine)
                .accessibilityValue(hint.enabled ? "" : "not available here")
            }
        }
        .frame(maxWidth: .infinity, alignment: .topLeading)
    }
}

// MARK: - Keys as text

/// A key's label as a person reads it: letters and words in Monaspace Neon, the Mac's key
/// symbols (arrows, ⏎, ⌘, ⇧, ⌫) in the system face, whose symbols stand at full height
/// where the mono face's are small or missing (it has no ⏎ or ⌘ at all).
struct KeyLabel: View {
    let label: String
    var size: CGFloat = TypeScale.monoSmall

    static let symbols: Set<Character> = ["↑", "↓", "←", "→", "⏎", "⇧", "⌘", "⌫", "⌥", "⌃", "↵"]

    var body: some View {
        Self.text(label, size: size)
    }

    static func text(_ label: String, size: CGFloat) -> Text {
        var out = Text("")
        var run = ""
        var runIsSymbol = false
        func flush() {
            guard !run.isEmpty else { return }
            out = out + (runIsSymbol
                ? Text(run).font(.system(size: size + 1, weight: .semibold))
                : Text(run).font(Typeface.monoBold.font(size: size)))
            run = ""
        }
        for character in label {
            let symbol = symbols.contains(character)
            if symbol != runIsSymbol { flush() }
            runIsSymbol = symbol
            run.append(character)
        }
        flush()
        return out
    }
}
