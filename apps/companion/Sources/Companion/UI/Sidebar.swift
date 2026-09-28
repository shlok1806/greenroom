import SwiftUI

/// The runs sidebar (Figma Mockups 01 to 04, companion ADR 0019): runs grouped Needs you,
/// Running and Done, one glyph, a name and one short meta each; "Show N more" under Done; the
/// footer says how many Macs are free. The rows are an `NSTableView` (`RunsTable`), which builds
/// only the rows on screen, so 2,000 runs scroll as smoothly as 20. 248 wide (208 compact, 272
/// wide), its 1 pt right border inside that width.
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
    @State private var scrollMetrics = ScrollMetrics()
    @State private var scrollPosition = ""
    @State private var scrollDriver = ScrollDriver()
    @Environment(\.frozenNow) private var frozenNow

    private var items: [SidebarItem] {
        let doneShown = (WindowClass.allCases.first { $0.sidebar == width } ?? .regular).doneShown
        return board.map { SidebarLayout.items($0, expanded: expanded, selected: selected, query: query, doneShown: doneShown) } ?? []
    }

    var body: some View {
        HStack(spacing: 0) {
            VStack(spacing: 0) {
                titlebar
                if searching { searchField }
                list.cloneScope("Runs")
                footer
            }
            // The 1 pt border is inside the sidebar's width, as the design draws it.
            Rectangle().fill(Palette.border).frame(width: 1)
        }
        .frame(width: width)
        .frame(maxHeight: .infinity)
        .background(Palette.bgSidebar)
        .cloneScope("Sidebar")
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Runs")
        .onAppear { expanded.formUnion(expandedAtStart) }
    }

    /// 52 tall. The traffic lights sit here (the window's own), 20 from the left; the search
    /// button 12 from the right.
    private var titlebar: some View {
        HStack {
            Spacer()
            IconButton(icon: .search, name: searching ? "Close search" : "Search runs") {
                searching.toggle()
                if searching { searchFocused = true } else { query = "" }
            }
            .cloneScope("Icon button")
        }
        .padding(.leading, 20)
        .padding(.trailing, Gap.x12)
        .frame(height: Metrics.toolbarHeight)
        .cloneScope("Titlebar")
    }

    private var searchField: some View {
        TextField("", text: $query, prompt: Text("Search runs").foregroundStyle(Palette.textSecondary))
            .textFieldStyle(.plain)
            .textStyle(.body)
            .tint(Palette.accent)
            .focused($searchFocused)
            .padding(.horizontal, Gap.x8)
            .frame(height: Metrics.buttonHeight)
            .background(RoundedRectangle(cornerRadius: Corner.control).fill(Palette.bgRaised))
            .overlay(RoundedRectangle(cornerRadius: Corner.control).strokeBorder(Palette.border, lineWidth: 1))
            .focusRing(searchFocused, radius: Corner.control)
            .padding(.horizontal, Gap.x8)
            .padding(.bottom, Gap.x4)
            .onSubmit {
                if let first = items.first(where: { if case .run = $0 { true } else { false } }), case .run(let id) = first { select(id) }
            }
            .onExitCommand {
                searching = false
                query = ""
            }
    }

    private var list: some View {
        RunsTable(items: items, summaries: summaries, selected: selected, select: select,
                  expand: { expanded.insert($0) }, frozenNow: frozenNow, driver: scrollDriver,
                  onScroll: { metrics, position in
                      if scrollMetrics != metrics { scrollMetrics = metrics }
                      if scrollPosition != position { scrollPosition = position }
                  })
            .overlay(alignment: .trailing) {
                VisibleScroller(metrics: scrollMetrics, position: scrollPosition) { scrollDriver.scroll(to: $0) }
            }
    }

    private var summaries: [String: Summary] {
        Dictionary(board?.runs.map { ($0.runId, $0) } ?? [], uniquingKeysWith: { first, _ in first })
    }

    /// 44 tall with its 1 pt top border inside: the machine icon (14) 16 from the left, the
    /// words 8 after it, the settings button 8 from the right.
    private var footer: some View {
        VStack(spacing: 0) {
            Rectangle().fill(Palette.border).frame(height: 1)
            HStack(spacing: Gap.x8) {
                switch connection {
                case .offline, .refused:
                    StatusGlyph(kind: .stopped, color: .secondary, size: 14)
                    Text("Not connected").textStyle(.caption).foregroundStyle(Palette.textSecondary).clonePart("Text")
                case .connecting:
                    StatusGlyph(kind: .checking, color: .accent, size: 14)
                    Text("Connecting").textStyle(.caption).foregroundStyle(Palette.textSecondary).clonePart("Text")
                case .online:
                    IconView(icon: .machine, size: 14).foregroundStyle(Palette.textSecondary).clonePart("Icon/machine")
                    Text(board?.macs.text ?? "").textStyle(.caption).foregroundStyle(Palette.textSecondary)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .clonePart("Text")
                }
                Spacer(minLength: 0)
                IconButton(icon: .settings, name: "Settings", action: openSettings).cloneScope("Icon button")
            }
            .padding(.leading, Gap.x16)
            .padding(.trailing, Gap.x8)
            .frame(maxHeight: .infinity)
        }
        .frame(height: Metrics.footerHeight)
        .cloneScope("Footer")
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
