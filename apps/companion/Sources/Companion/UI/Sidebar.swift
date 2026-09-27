import SwiftUI

/// The runs sidebar (Figma Mockups 01 to 04, companion ADR 0019): runs grouped Needs you,
/// Running and Done, one glyph, a name and one short meta each; "Show N more" under Done; the
/// footer says how many Macs are free. A lazy `List` (an `NSTableView` underneath) builds only
/// the rows on screen, so 2,000 runs scroll as smoothly as 20.
struct RunsSidebar: View {
    var board: SummaryBoard?
    var connection: ConnectionState
    var selected: String?
    var width: CGFloat
    var select: (String) -> Void
    var openSettings: () -> Void
    /// Groups shown whole from the start (the harness and the performance test).
    var expandedAtStart: Set<SummaryGroup> = []

    @State private var expanded: Set<SummaryGroup> = []
    @State private var searching = false
    @State private var query = ""
    @FocusState private var searchFocused: Bool
    @Environment(\.frozenNow) private var frozenNow

    private var items: [SidebarItem] {
        board.map { SidebarLayout.items($0, expanded: expanded, selected: selected, query: query) } ?? []
    }

    var body: some View {
        VStack(spacing: 0) {
            titlebar
            if searching { searchField }
            list
            Rectangle().fill(Palette.border).frame(height: 1)
            footer
        }
        .frame(width: width)
        .frame(maxHeight: .infinity)
        .background(Palette.bgSidebar)
        .overlay(alignment: .trailing) { Rectangle().fill(Palette.border).frame(width: 1) }
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Runs")
        .onAppear { expanded.formUnion(expandedAtStart) }
    }

    /// The traffic lights sit here (the window's own); the search button on the right.
    private var titlebar: some View {
        HStack {
            Spacer()
            IconButton(systemImage: "magnifyingglass", name: searching ? "Close search" : "Search runs") {
                searching.toggle()
                if searching { searchFocused = true } else { query = "" }
            }
        }
        .padding(.leading, Gap.x16 + 4)
        .padding(.trailing, Gap.x12)
        .frame(height: Metrics.toolbarHeight)
    }

    private var searchField: some View {
        TextField("Search runs", text: $query)
            .textFieldStyle(.plain)
            .textStyle(.body)
            .focused($searchFocused)
            .padding(.horizontal, Gap.x8)
            .frame(height: Metrics.buttonHeight)
            .background(RoundedRectangle(cornerRadius: Corner.control).fill(Palette.bgRaised))
            .overlay(RoundedRectangle(cornerRadius: Corner.control).strokeBorder(searchFocused ? Palette.focusRing : Palette.border, lineWidth: searchFocused ? 2 : 1))
            .padding(.horizontal, Gap.x8)
            .padding(.bottom, Gap.x4)
            .onSubmit {
                if let first = items.first(where: { if case .run = $0 { true } else { false } }), case .run(let id) = first { select(id) }
            }
    }

    private var list: some View {
        RunsTable(items: items, summaries: summaries, selected: selected, select: select,
                  expand: { expanded.insert($0) }, frozenNow: frozenNow)
    }

    private var summaries: [String: Summary] {
        Dictionary(board?.runs.map { ($0.runId, $0) } ?? [], uniquingKeysWith: { first, _ in first })
    }

    private var footer: some View {
        HStack(spacing: Gap.x8) {
            switch connection {
            case .offline, .refused:
                StatusGlyph(kind: .stopped, color: .secondary, size: 14)
                Text("Not connected").textStyle(.caption).foregroundStyle(Palette.textSecondary)
            case .connecting:
                StatusGlyph(kind: .checking, color: .accent, size: 14)
                Text("Connecting").textStyle(.caption).foregroundStyle(Palette.textSecondary)
            case .online:
                Image(systemName: "desktopcomputer").font(.system(size: 11)).foregroundStyle(Palette.textSecondary).frame(width: 14, height: 14)
                Text(board?.macs.text ?? "").textStyle(.caption).foregroundStyle(Palette.textSecondary)
            }
            Spacer(minLength: 0)
            IconButton(systemImage: "slider.horizontal.3", name: "Settings", action: openSettings)
        }
        .padding(.leading, Gap.x16)
        .padding(.trailing, Gap.x8)
        .frame(height: Metrics.footerHeight)
    }
}

/// A clock held still, for snapshots of runs recorded in the past; nil follows the real clock.
struct FrozenNowKey: EnvironmentKey {
    static let defaultValue: Date? = nil
}

extension EnvironmentValues {
    var frozenNow: Date? {
        get { self[FrozenNowKey.self] }
        set { self[FrozenNowKey.self] = newValue }
    }
}
