import AppKit
import SwiftUI

/// The runs list as an `NSTableView` with fixed row heights and reused cells, the approach
/// NetNewsWire's timeline takes (a pattern, no code copied): the table asks for the rows on
/// screen only and never measures the others, so 2,000 runs lay out and scroll at the
/// cost of the thirty that show. Each cell hosts the SwiftUI row. Up and Down move the
/// selection natively while the list has the keyboard.
struct RunsTable: NSViewRepresentable {
    /// The gap between the rows of the list.
    static let gap: CGFloat = 2

    var items: [SidebarItem]
    var summaries: [String: Summary]
    var selected: String?
    var select: (String) -> Void
    var expand: (SummaryGroup) -> Void
    var frozenNow: Date?

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeNSView(context: Context) -> NSScrollView {
        let table = KeyTable()
        table.headerView = nil
        table.backgroundColor = .clear
        table.selectionHighlightStyle = .none
        table.intercellSpacing = .zero
        table.usesAutomaticRowHeights = false
        table.style = .plain
        table.focusRingType = .none
        table.allowsEmptySelection = true
        table.setAccessibilityLabel("Runs")
        let column = NSTableColumn(identifier: .init("run"))
        column.resizingMask = .autoresizingMask
        table.addTableColumn(column)
        table.columnAutoresizingStyle = .uniformColumnAutoresizingStyle
        table.dataSource = context.coordinator
        table.delegate = context.coordinator
        table.target = context.coordinator
        table.action = #selector(Coordinator.clicked(_:))

        let scroll = NSScrollView()
        scroll.documentView = table
        scroll.drawsBackground = false
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        // The design draws no scroll bar beside the rows: an overlay scroller, which shows only
        // while scrolling, even with a mouse attached (seen as a 17 pt bar in a Greenroom VM).
        scroll.scrollerStyle = .overlay
        scroll.contentInsets = NSEdgeInsets(top: 0, left: 0, bottom: Gap.x8, right: 0)
        scroll.automaticallyAdjustsContentInsets = false
        context.coordinator.table = table
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        if scroll.scrollerStyle != .overlay { scroll.scrollerStyle = .overlay }
        let c = context.coordinator
        c.parent = self
        let changed = c.items != items || c.summaries != summaries || c.frozenNow != frozenNow
        let selectionChanged = c.selected != selected
        c.items = items
        c.summaries = summaries
        c.selected = selected
        c.frozenNow = frozenNow
        guard let table = c.table else { return }
        if changed {
            table.reloadData()
        } else if selectionChanged {
            // Only the rows whose selection changed redraw.
            table.reloadData(forRowIndexes: IndexSet(integersIn: 0..<items.count).filteredIndexSet { index in
                if case .run = items[index] { return true }
                return false
            }, columnIndexes: [0])
        }
        if let selected, let row = items.firstIndex(of: .run(selected)) {
            c.syncingSelection = true
            table.selectRowIndexes([row], byExtendingSelection: false)
            c.syncingSelection = false
            if selectionChanged { table.scrollRowToVisible(row) }
        } else {
            table.deselectAll(nil)
        }
    }

    @MainActor
    final class Coordinator: NSObject, NSTableViewDataSource, NSTableViewDelegate {
        var parent: RunsTable?
        var items: [SidebarItem] = []
        var summaries: [String: Summary] = [:]
        var selected: String?
        var frozenNow: Date?
        weak var table: NSTableView?
        var syncingSelection = false

        func numberOfRows(in tableView: NSTableView) -> Int { items.count }

        func tableView(_ tableView: NSTableView, heightOfRow row: Int) -> CGFloat {
            // The design stacks the list 2 apart: each row is its own height and the gap after it.
            switch items[row] {
            case .heading: 30 + RunsTable.gap
            case .run, .empty: Metrics.runRowHeight + RunsTable.gap
            case .more: 26 + RunsTable.gap
            }
        }

        func tableView(_ tableView: NSTableView, shouldSelectRow row: Int) -> Bool {
            if case .run = items[row] { return true }
            return false
        }

        func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
            let id = NSUserInterfaceItemIdentifier("cell")
            let cell = (tableView.makeView(withIdentifier: id, owner: nil) as? NSHostingView<AnyView>) ?? {
                let view = NSHostingView(rootView: AnyView(EmptyView()))
                view.identifier = id
                return view
            }()
            cell.rootView = AnyView(content(for: items[row], at: row)
                .padding(.horizontal, Gap.x8)
                .frame(maxHeight: .infinity, alignment: .top)
                .environment(\.frozenNow, frozenNow)
                .environment(\.clonePath, "Sidebar/Runs"))
            return cell
        }

        /// The part's name as the design's layers are named: "Run row[3]", with its place
        /// among the rows of its kind when there is more than one.
        private func partName(_ base: String, at row: Int, where matches: (SidebarItem) -> Bool) -> String {
            let all = items.indices.filter { matches(items[$0]) }
            guard all.count > 1, let index = all.firstIndex(of: row) else { return base }
            return "\(base)[\(index)]"
        }

        @ViewBuilder
        private func content(for item: SidebarItem, at row: Int) -> some View {
            switch item {
            case .heading(let group):
                GroupHeading(title: group.title)
                    .cloneScope(partName("Group", at: row) { if case .heading = $0 { true } else { false } })
            case .run(let id):
                if let summary = summaries[id] {
                    SidebarRunRow(summary: summary, selected: id == selected)
                        .cloneScope(partName("Run row", at: row) { if case .run = $0 { true } else { false } })
                }
            case .more(_, let hidden):
                // 6 above and below, 32 before: in line with the names.
                Text("Show \(hidden.formatted()) more").textStyle(.caption).foregroundStyle(Palette.textSecondary)
                    .clonePart("Text")
                    .padding(.leading, Gap.x32)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .frame(height: 26)
                    .contentShape(Rectangle())
                    .cloneScope("More")
                    .accessibilityAddTraits(.isButton)
            case .empty:
                Text("Nothing needs you").textStyle(.caption).foregroundStyle(Palette.textSecondary)
                    .padding(.horizontal, Gap.x8)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .frame(height: Metrics.runRowHeight)
            }
        }

        func tableViewSelectionDidChange(_ notification: Notification) {
            guard !syncingSelection, let table, table.selectedRow >= 0, table.selectedRow < items.count,
                  case .run(let id) = items[table.selectedRow], id != selected else { return }
            parent?.select(id)
        }

        @objc func clicked(_ sender: NSTableView) {
            let row = sender.clickedRow
            guard row >= 0, row < items.count else { return }
            if case .more(let group, _) = items[row] { parent?.expand(group) }
        }
    }
}

/// A table that keeps AppKit's arrow-key selection but lets Return and letters reach the window.
private final class KeyTable: NSTableView {
    override var acceptsFirstResponder: Bool { true }

    /// The window opens with the runs list focused, so Up and Down walk it at once and no
    /// titlebar button wears a focus ring nobody asked for (seen in Greenroom run
    /// 20260928-144042-10fc05f5e0a43287: the search button ringed at launch).
    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        guard let window else { return }
        // After SwiftUI has given its first focus (with Full Keyboard Access on, the first
        // button); a field someone is typing in keeps it.
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) { [weak self, weak window] in
            guard let self, let window, !(window.firstResponder is NSTextView) else { return }
            window.makeFirstResponder(self)
        }
    }
}

/// A run row whose time counts up while the run is open; a done run's age holds still.
struct SidebarRunRow: View {
    var summary: Summary
    var selected: Bool
    @Environment(\.frozenNow) private var frozenNow
    @State private var hovering = false

    var body: some View {
        Group {
            if let frozenNow {
                RunRowView(model: RunRowModel(summary, now: frozenNow, selected: selected), selected: selected, hovered: hovering)
            } else if summary.group == .done {
                RunRowView(model: RunRowModel(summary, now: Date(), selected: selected), selected: selected, hovered: hovering)
            } else {
                TimelineView(.periodic(from: .now, by: 1)) { context in
                    RunRowView(model: RunRowModel(summary, now: context.date, selected: selected), selected: selected, hovered: hovering)
                }
            }
        }
        .onHover { hovering = $0 }
    }
}
