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
    /// Each run's task text, for the search (read only while there is a query).
    var tasks: () -> [String: String] = { [:] }
    /// Bumped (`/`) to open the search and give it the keyboard.
    var searchRequest = 0
    /// News about Greenroom's builds, badged on the settings button.
    var updateBadge: MoreBadge?

    @State private var expanded: Set<SummaryGroup> = []
    @State private var searching = false
    @State private var query = ""
    @FocusState private var searchFocused: Bool
    @State private var scroll = ScrollTracker()
    @State private var twinCache = TwinCache()
    @Environment(\.frozenNow) private var frozenNow

    private var items: [SidebarItem] {
        let doneShown = (WindowClass.allCases.first { $0.sidebar == width } ?? .regular).doneShown
        return board.map {
            SidebarLayout.items($0, expanded: expanded, selected: selected, query: query, doneShown: doneShown, tasks: tasks)
        } ?? []
    }

    var body: some View {
        let shown = items
        HStack(spacing: 0) {
            VStack(spacing: 0) {
                titlebar
                if searching { searchField(shown) }
                list(shown).cloneScope("Runs")
                    .overlay(alignment: .topLeading) {
                        if board != nil, !query.trimmingCharacters(in: .whitespaces).isEmpty, shown.isEmpty {
                            Text(SidebarSearch.empty(query)).textStyle(.caption).foregroundStyle(Palette.textSecondary)
                                .padding(.horizontal, Gap.x16)
                                .padding(.top, Gap.x12)
                        }
                    }
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
        .onChange(of: searchRequest) { _, _ in
            searching = true
            searchFocused = true
        }
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

    private func searchField(_ items: [SidebarItem]) -> some View {
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

    private func list(_ items: [SidebarItem]) -> some View {
        RunsTable(items: items, summaries: summaries, selected: selected, select: select,
                  expand: { expanded.insert($0) }, frozenNow: frozenNow, tracker: scroll,
                  twins: twinCache.marks(board))
            .overlay(alignment: .trailing) { TrackedScroller(tracker: scroll) }
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
                IconButton(icon: .settings, name: updateBadge.map { "Settings, \($0.spoken)" } ?? "Settings", action: openSettings)
                    .overlay(alignment: .topTrailing) {
                        if updateBadge != nil {
                            // News about the builds (the old More menu's badge): one accent dot.
                            Circle().fill(Palette.accent).frame(width: Gap.x8, height: Gap.x8)
                                .overlay(Circle().strokeBorder(Palette.bgSidebar, lineWidth: 1))
                                .offset(x: -Gap.x4, y: Gap.x4)
                                .allowsHitTesting(false)
                                .accessibilityHidden(true)
                        }
                    }
                    .cloneScope("Icon button")
            }
            .padding(.leading, Gap.x16)
            .padding(.trailing, Gap.x8)
            .frame(maxHeight: .infinity)
        }
        .frame(height: Metrics.footerHeight)
        .cloneScope("Footer")
    }
}

/// The sidebar's twin marks, worked out again only when a run's id, name or start changes:
/// the board changes on every summary event and the sidebar redraws on every selection.
@MainActor
final class TwinCache {
    private var key: Int?
    private var held: [String: TwinMark] = [:]

    func marks(_ board: SummaryBoard?) -> [String: TwinMark] {
        var hasher = Hasher()
        for run in board?.runs ?? [] {
            hasher.combine(run.runId)
            hasher.combine(run.name)
            hasher.combine(run.startedAt)
        }
        let key = hasher.finalize()
        guard key != self.key else { return held }
        self.key = key
        held = board.map { RunRowModel.twinMarks($0.runs) } ?? [:]
        return held
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
