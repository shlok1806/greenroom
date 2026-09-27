import SwiftUI

// The Cmd-K palette (Figma Mockups 12, Palette row 4:72). Adapted from Ghostty's
// `macos/Sources/Features/Command Palette/CommandPalette.swift` (MIT, Mitchell Hashimoto and
// Ghostty contributors; see ACKNOWLEDGEMENTS.md): the query field that turns arrows, Return
// and Escape into events, the option list with keyboard selection that scrolls to follow it,
// hover selection, and the match (a substring first, then word initials) and ranking. Restyled
// to the Figma file: sections ("This run", "Go to run"), a keycap per row, 560 pt wide.

/// One thing the palette can do.
struct PaletteOption: Identifiable {
    let id: String
    let title: String
    /// The section it is listed under.
    let section: String
    var systemImage: String?
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

    /// The options that match the query, best first (Ghostty's `filteredAndSorted`): a title
    /// match beats a section match.
    var filtered: [PaletteOption] { PaletteMatch.filter(options, query: query) }

    private var selectedOption: PaletteOption? {
        let list = filtered
        guard !list.isEmpty else { return nil }
        return list[min(selectedIndex ?? 0, list.count - 1)]
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
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
        HStack(spacing: Gap.x12) {
            Image(systemName: "magnifyingglass").font(.system(size: 13)).foregroundStyle(Palette.textSecondary)
            TextField("Search runs and actions", text: $query)
                .textFieldStyle(.plain)
                .font(.system(size: 15))
                .foregroundStyle(Palette.text)
                .focused($focused)
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
            Keycap(keys: "esc")
        }
        .padding(.horizontal, Gap.x16)
        .frame(height: 48)
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
                    LazyVStack(alignment: .leading, spacing: 0) {
                        ForEach(Array(options.enumerated()), id: \.element.id) { index, option in
                            if index == 0 || options[index - 1].section != option.section {
                                Text(option.section).textStyle(.captionEmphasis).foregroundStyle(Palette.textSecondary)
                                    .padding(.horizontal, Gap.x12)
                                    .padding(.top, index == 0 ? Gap.x8 : Gap.x12)
                                    .padding(.bottom, Gap.x4)
                                    .accessibilityAddTraits(.isHeader)
                            }
                            Button { run(option) } label: {
                                PaletteRowView(systemImage: option.systemImage, glyph: option.glyph, label: option.title,
                                               keys: option.keys, selected: index == selectedIndex || hoveredID == option.id,
                                               enabled: option.disabledReason == nil)
                            }
                            .buttonStyle(.plain)
                            .help(option.disabledReason ?? "")
                            .onHover { hoveredID = $0 ? option.id : nil }
                            .id(option.id)
                            .accessibilityLabel(option.title)
                            .accessibilityHint(option.disabledReason ?? "")
                        }
                    }
                    .padding(Gap.x8)
                }
                .frame(maxHeight: 380)
                .onChange(of: selectedIndex) { _, index in
                    guard index < options.count else { return }
                    proxy.scrollTo(options[index].id)
                }
            }
        }
    }
}

/// Ghostty's matching, kept pure for tests: `matchedIndices` (a substring, else the first
/// letters of words) and its ranking by where the match is.
enum PaletteMatch {
    static func filter(_ options: [PaletteOption], query: String) -> [PaletteOption] {
        guard !query.isEmpty else { return options }
        return options.enumerated().compactMap { index, option -> (Int, Int, PaletteOption)? in
            if matches(option.title, query) { return (2, index, option) }
            if matches(option.section, query) { return (1, index, option) }
            return nil
        }
        .sorted { ($0.0, -$0.1) > ($1.0, -$1.1) }
        .map(\.2)
    }

    /// A case-insensitive substring, else every query letter the first letter of a word in order.
    static func matches(_ text: String, _ query: String) -> Bool {
        guard !query.isEmpty else { return true }
        if text.range(of: query, options: .caseInsensitive) != nil { return true }
        var remaining = Substring(query.lowercased())
        for word in text.split(whereSeparator: \.isWhitespace) {
            guard let first = remaining.first else { break }
            if word.first?.lowercased() == String(first) { remaining = remaining.dropFirst() }
        }
        return remaining.isEmpty
    }
}
