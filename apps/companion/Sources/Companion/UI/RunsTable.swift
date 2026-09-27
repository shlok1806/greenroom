import AppKit
import SwiftUI

/// The runs list as an `NSTableView` with fixed row heights and reused cells, the approach
/// NetNewsWire's timeline takes (a pattern, no code copied): the table asks for the rows on
/// screen only and never measures the others, so 2,000 runs lay out and scroll at the
/// cost of the thirty that show. Each cell hosts the SwiftUI row. Up and Down move the
/// selection natively while the list has the keyboard.
struct RunsTable: NSViewRepresentable {
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
        scroll.contentInsets = NSEdgeInsets(top: 0, left: 0, bottom: Gap.x8, right: 0)
        scroll.automaticallyAdjustsContentInsets = false
        context.coordinator.table = table
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
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
            switch items[row] {
            case .heading: 30
            case .run, .empty: Metrics.runRowHeight + 2
            case .more: 26
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
            cell.rootView = AnyView(content(for: items[row]).padding(.horizontal, Gap.x8).environment(\.frozenNow, frozenNow))
            return cell
        }

        @ViewBuilder
        private func content(for item: SidebarItem) -> some View {
            switch item {
            case .heading(let group):
                GroupHeading(title: group.title).frame(maxHeight: .infinity, alignment: .bottom)
            case .run(let id):
                if let summary = summaries[id] {
                    SidebarRunRow(summary: summary, selected: id == selected).padding(.vertical, 1)
                }
            case .more(_, let hidden):
                Text("Show \(hidden) more").textStyle(.caption).foregroundStyle(Palette.textSecondary)
                    .padding(.leading, Gap.x24)
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .leading)
                    .accessibilityAddTraits(.isButton)
            case .empty:
                Text("Nothing needs you").textStyle(.caption).foregroundStyle(Palette.textSecondary)
                    .padding(.horizontal, Gap.x8)
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .leading)
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
}

/// A run row whose time counts up while the run is open; a done run's age holds still.
struct SidebarRunRow: View {
    var summary: Summary
    var selected: Bool
    @Environment(\.frozenNow) private var frozenNow

    var body: some View {
        if let frozenNow {
            RunRowView(model: RunRowModel(summary, now: frozenNow), selected: selected)
        } else if summary.group == .done {
            RunRowView(model: RunRowModel(summary, now: Date()), selected: selected)
        } else {
            TimelineView(.periodic(from: .now, by: 1)) { context in
                RunRowView(model: RunRowModel(summary, now: context.date), selected: selected)
            }
        }
    }
}
