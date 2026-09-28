import SwiftUI

// The Cmd-K palette (Figma Mockups 12, Palette row 4:72). Adapted from Ghostty's
// `macos/Sources/Features/Command Palette/CommandPalette.swift` (MIT, Mitchell Hashimoto and
// Ghostty contributors; see ACKNOWLEDGEMENTS.md): the query field that turns arrows, Return
// and Escape into events, the option list with keyboard selection that scrolls to follow it,
// and hover selection. The ranking is cmdk's (`CommandScore`). Restyled to the Figma file:
// sections ("This run", "Go to run"), a keycap per row, 560 pt wide.

/// One thing the palette can do.
struct PaletteOption: Identifiable {
    let id: String
    let title: String
    /// The section it is listed under.
    let section: String
    var icon: Icon?
    var glyph: (GlyphKind, ToneColor)?
    /// The key that does the same thing outside the palette, as a keycap shows it.
    var keys: String?
    /// Nil when it can run; else why not, shown as its tooltip.
    var disabledReason: String?
    let action: () -> Void
}

struct CommandPaletteView: View {
    @Binding var isPresented: Bool
    var options: [PaletteOption]
    @State private var rawQuery = ""
    @State private var selectedIndex: Int?
    @State private var hoveredID: String?

    private var query: String { rawQuery.trimmingCharacters(in: .whitespacesAndNewlines) }

    /// The options that match the query, best first.
    var filtered: [PaletteOption] { PaletteMatch.filter(options, query: query) }

    private var selectedOption: PaletteOption? {
        let list = filtered
        guard !list.isEmpty else { return nil }
        return list[min(selectedIndex ?? 0, list.count - 1)]
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            VStack(spacing: 0) {
            PaletteQuery(query: $rawQuery) { event in
                switch event {
                case .exit:
                    isPresented = false
                case .submit:
                    guard let option = selectedOption, option.disabledReason == nil else { return }
                    isPresented = false
                    option.action()
                case .move(let delta):
                    let count = filtered.count
                    guard count > 0 else { return }
                    let current = selectedIndex ?? (delta > 0 ? -1 : count)
                    selectedIndex = ((current + delta) % count + count) % count
                }
            }
            .onChange(of: query) { _, _ in selectedIndex = 0 }

            Rectangle().fill(Palette.border).frame(height: 1)
            }
            .cloneScope("Query")

            PaletteTable(options: filtered, query: query, selectedIndex: selectedIndex ?? 0, hoveredID: $hoveredID) { option in
                guard option.disabledReason == nil else { return }
                isPresented = false
                option.action()
            }
        }
        .frame(width: Metrics.paletteWidth)
        .background(RoundedRectangle(cornerRadius: Corner.sheet).fill(Palette.bgRaised))
        .clipShape(RoundedRectangle(cornerRadius: Corner.sheet))
        .overlay(RoundedRectangle(cornerRadius: Corner.sheet).strokeBorder(Palette.border, lineWidth: 1))
        .shadow(color: .black.opacity(Elevation.raisedOpacity), radius: Elevation.raisedRadius / 2, y: Elevation.raisedY)
        .onChange(of: isPresented) { _, shown in if !shown { rawQuery = "" } }
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Command palette")
        .cloneScope("Palette")
    }
}

/// The query field: arrows move the selection, Return runs it, Escape closes (Ghostty's
/// `CommandPaletteQuery`).
private struct PaletteQuery: View {
    enum Event { case exit, submit, move(Int) }

    @Binding var query: String
    var onEvent: (Event) -> Void
    @FocusState private var focused: Bool

    var body: some View {
        // Figma 12: 16 in, the icon 10 from the field, the field 10 from the esc keycap, 51 tall
        // over the 1 pt rule.
        HStack(spacing: 10) {
            IconView(icon: .search).foregroundStyle(Palette.textSecondary)
            TextField("Search runs and actions", text: $query)
                .textFieldStyle(.plain)
                .font(TypeStyle.title.font.weight(.regular))
                .foregroundStyle(Palette.text)
                .focused($focused)
                .frame(height: TypeStyle.title.lineHeight)
                .clonePart("Text")
                .onExitCommand { onEvent(.exit) }
                .onMoveCommand { direction in
                    switch direction {
                    case .up: onEvent(.move(-1))
                    case .down: onEvent(.move(1))
                    default: break
                    }
                }
                .onSubmit { onEvent(.submit) }
                .onAppear {
                    // Ghostty: claim focus on the next turn, once the field is in the window.
                    DispatchQueue.main.async { focused = true }
                }
            Keycap(keys: "esc").cloneScope("Keycap")
        }
        .padding(.horizontal, Gap.x16)
        .frame(height: 51)
    }
}

private struct PaletteTable: View {
    var options: [PaletteOption]
    var query: String
    var selectedIndex: Int
    @Binding var hoveredID: String?
    var run: (PaletteOption) -> Void

    var body: some View {
        if options.isEmpty {
            VStack(spacing: Gap.x4) {
                Text("No matches for \u{201C}\(query)\u{201D}").textStyle(.body).foregroundStyle(Palette.text)
                Text("Try a run name or an action").textStyle(.caption).foregroundStyle(Palette.textSecondary)
            }
            .frame(maxWidth: .infinity)
            .padding(Gap.x24)
        } else {
            ScrollViewReader { proxy in
                ScrollView {
                    // Figma 12: a section heading 30 tall (12 above its words, 4 below, 20 in),
                    // rows 36 tall 8 in from the palette's sides, one after another, 8 below the last.
                    LazyVStack(alignment: .leading, spacing: 0) {
                        ForEach(Array(options.enumerated()), id: \.element.id) { index, option in
                            if index == 0 || options[index - 1].section != option.section {
                                let section = Set(options[..<index].map(\.section)).count
                                Text(option.section).textStyle(.captionEmphasis).foregroundStyle(Palette.textSecondary)
                                    .clonePart("Text")
                                    .padding(.leading, 20)
                                    .padding(.top, Gap.x12)
                                    .padding(.bottom, Gap.x4)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                    .accessibilityAddTraits(.isHeader)
                                    .cloneScope("Section[\(section)]")
                            }
                            Button { run(option) } label: {
                                PaletteRowView(icon: option.icon, glyph: option.glyph, label: option.title,
                                               keys: option.keys, selected: index == selectedIndex || hoveredID == option.id,
                                               enabled: option.disabledReason == nil)
                            }
                            .buttonStyle(.plain)
                            .cloneScope("Palette row")
                            .padding(.horizontal, Gap.x8)
                            .cloneScope("Frame[\(index)]")
                            .help(option.disabledReason ?? "")
                            .onHover { hoveredID = $0 ? option.id : nil }
                            .id(option.id)
                            .accessibilityLabel(option.title)
                            .accessibilityHint(option.disabledReason ?? "")
                        }
                    }
                    .padding(.bottom, Gap.x8)
                }
                .scrollIndicators(.never)
                .frame(maxHeight: 392)
                .onChange(of: selectedIndex) { _, index in
                    guard index < options.count else { return }
                    proxy.scrollTo(options[index].id)
                }
            }
        }
    }
}

/// Which options a query shows and in what order: cmdk's ranking (`CommandScore`), the
/// section counting as the option's keywords, as cmdk's `keywords` do. An empty query shows
/// everything in its own order; equal scores keep their order.
enum PaletteMatch {
    static func filter(_ options: [PaletteOption], query: String) -> [PaletteOption] {
        guard !query.isEmpty else { return options }
        var scored: [(score: Double, index: Int, option: PaletteOption)] = []
        for (index, option) in options.enumerated() {
            let score = CommandScore.score(option.title, query, aliases: [option.section])
            if score > 0 { scored.append((score, index, option)) }
        }
        scored.sort { a, b in a.score != b.score ? a.score > b.score : a.index < b.index }
        return scored.map(\.option)
    }
}
