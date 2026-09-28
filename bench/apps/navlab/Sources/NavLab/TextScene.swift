import AppKit
import SwiftUI

// The control catalog's wave 1 checks (docs/21a rows I2, I15, M15): a paragraph a triple click
// selects whole, a secure field whose value only its length reveals, and the main window's
// toolbar, which holds more items than fit so some sit behind the overflow chevron.

/// A paragraph in a text view: a triple click selects it, and a line reports how many characters
/// are selected. A password field, and a line that shows only its length.
struct TextScene: View {
    @State private var selected = 0
    @State private var password = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            SceneTitle("Text and password")
            Text("Triple-click the paragraph to select all of it.")
            ParagraphView(selected: $selected)
                .frame(width: 520, height: 110)
                .overlay(RoundedRectangle(cornerRadius: 4).stroke(Color.secondary.opacity(0.4)))
            Text("Selected: \(selected) characters")
                .accessibilityIdentifier("selectedCount")
            HStack(spacing: 8) {
                Text("Password")
                SecureField("Password", text: $password)
                    .textFieldStyle(.roundedBorder)
                    .frame(width: 220)
                    .accessibilityLabel("Password")
            }
            Text("Password length: \(password.count)")
                .accessibilityIdentifier("passwordLength")
            Spacer()
        }
        .padding(24)
    }
}

/// A read-only text view holding two paragraphs, so a triple click selects one, not all.
struct ParagraphView: NSViewRepresentable {
    @Binding var selected: Int

    let text = "The first paragraph is short.\nThe second paragraph is the one to select: a triple click selects it whole, while a double click selects only one word of it.\n"

    func makeCoordinator() -> Coordinator {
        Coordinator(selected: $selected)
    }

    func makeNSView(context: Context) -> NSScrollView {
        let scroll = NSTextView.scrollableTextView()
        let view = scroll.documentView as! NSTextView
        view.string = text
        view.isEditable = false
        view.isSelectable = true
        view.font = NSFont.systemFont(ofSize: 14)
        view.delegate = context.coordinator
        view.setAccessibilityLabel("Paragraphs")
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        context.coordinator.selected = $selected
    }

    final class Coordinator: NSObject, NSTextViewDelegate {
        var selected: Binding<Int>

        init(selected: Binding<Int>) {
            self.selected = selected
        }

        func textViewDidChangeSelection(_ notification: Notification) {
            guard let view = notification.object as? NSTextView else {
                return
            }
            let length = view.selectedRanges.reduce(0) { $0 + $1.rangeValue.length }
            DispatchQueue.main.async {
                self.selected.wrappedValue = length
            }
        }
    }
}

/// The main window's toolbar: twelve labelled items in a 900-point window, so the last ones sit
/// behind the overflow chevron. Each sets the toolbar line of the Text and password scene.
final class LabToolbar: NSObject, NSToolbarDelegate {
    let model: LabModel
    let names = ["Back", "Forward", "Refresh", "Share", "Tag", "Flag", "Archive", "Print", "Export", "Duplicate", "Rename", "Inspect"]

    init(model: LabModel) {
        self.model = model
    }

    func toolbarAllowedItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        names.map { NSToolbarItem.Identifier($0) }
    }

    func toolbarDefaultItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        names.map { NSToolbarItem.Identifier($0) }
    }

    func toolbar(_ toolbar: NSToolbar, itemForItemIdentifier id: NSToolbarItem.Identifier, willBeInsertedIntoToolbar flag: Bool) -> NSToolbarItem? {
        let item = NSToolbarItem(itemIdentifier: id)
        item.label = id.rawValue
        item.paletteLabel = id.rawValue
        item.toolTip = id.rawValue
        item.image = NSImage(systemSymbolName: "circle", accessibilityDescription: id.rawValue)
        item.isBordered = true
        item.target = self
        item.action = #selector(pressed(_:))
        return item
    }

    @MainActor @objc func pressed(_ item: NSToolbarItem) {
        model.toolbarPressed = item.itemIdentifier.rawValue
    }
}
