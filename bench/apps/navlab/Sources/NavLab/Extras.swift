import AppKit
import SwiftUI
import WebKit

// The scenes built now for later waves (docs/21 section 8, waves 2 and 3), so those waves need
// no app change: a thin splitter, dialogs and menus, and a second window over the first.

/// A split view with a 9-point divider grip and a label showing the left pane's width
/// (docs/21 section 1.4 case 3).
struct SplitterScene: View {
    @State private var leftWidth = 240.0

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            SceneTitle("Splitter")
            Text("Drag the divider between the panes.")
            Text("Left pane: \(Int(leftWidth.rounded())) pt")
                .font(.title3)
                .accessibilityIdentifier("leftWidth")
            SplitPanes(leftWidth: $leftWidth)
                .frame(width: 600, height: 300)
            Spacer()
        }
        .padding(24)
    }
}

struct SplitPanes: NSViewRepresentable {
    @Binding var leftWidth: Double

    func makeCoordinator() -> Coordinator {
        Coordinator(leftWidth: $leftWidth)
    }

    func makeNSView(context: Context) -> GripSplitView {
        let split = GripSplitView(frame: NSRect(x: 0, y: 0, width: 600, height: 300))
        split.isVertical = true
        split.dividerStyle = .thin
        split.addArrangedSubview(pane("Left"))
        split.addArrangedSubview(pane("Right"))
        split.setHoldingPriority(.defaultHigh, forSubviewAt: 0)
        split.delegate = context.coordinator
        split.setAccessibilityLabel("Panes")
        return split
    }

    func updateNSView(_ split: GripSplitView, context: Context) {
        context.coordinator.leftWidth = $leftWidth
    }

    func pane(_ title: String) -> NSView {
        let view = NSView(frame: NSRect(x: 0, y: 0, width: 300, height: 300))
        let label = NSTextField(labelWithString: title)
        label.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(label)
        label.centerXAnchor.constraint(equalTo: view.centerXAnchor).isActive = true
        label.centerYAnchor.constraint(equalTo: view.centerYAnchor).isActive = true
        view.setAccessibilityElement(true)
        view.setAccessibilityRole(.group)
        view.setAccessibilityLabel("\(title) pane")
        return view
    }

    final class Coordinator: NSObject, NSSplitViewDelegate {
        var leftWidth: Binding<Double>

        init(leftWidth: Binding<Double>) {
            self.leftWidth = leftWidth
        }

        func splitView(_ splitView: NSSplitView, constrainMinCoordinate proposedMinimumPosition: CGFloat, ofSubviewAt dividerIndex: Int) -> CGFloat {
            100
        }

        func splitView(_ splitView: NSSplitView, constrainMaxCoordinate proposedMaximumPosition: CGFloat, ofSubviewAt dividerIndex: Int) -> CGFloat {
            500
        }

        // Layout calls this while SwiftUI may be updating; the label follows on the next turn.
        func splitViewDidResizeSubviews(_ notification: Notification) {
            guard let split = notification.object as? NSSplitView, let left = split.arrangedSubviews.first else {
                return
            }
            let width = Double(left.frame.width)
            DispatchQueue.main.async {
                self.leftWidth.wrappedValue = width
            }
        }
    }
}

/// A split view whose divider is 9 points wide with a thin line and a small grip drawn in it:
/// thin enough that a drag has to aim for it.
final class GripSplitView: NSSplitView {
    var positioned = false

    override var dividerThickness: CGFloat {
        9
    }

    // SwiftUI sizes the view after it is made; the divider starts at 240 once it has its width.
    override func layout() {
        super.layout()
        if !positioned && bounds.width > 400 {
            positioned = true
            setPosition(240, ofDividerAt: 0)
        }
    }

    override func drawDivider(in rect: NSRect) {
        NSColor.separatorColor.setFill()
        NSRect(x: rect.midX - 0.5, y: rect.minY, width: 1, height: rect.height).fill()
        NSColor.tertiaryLabelColor.setFill()
        for offset in [-6.0, 0.0, 6.0] {
            NSBezierPath(ovalIn: NSRect(x: rect.midX - 1.5, y: rect.midY + offset - 1.5, width: 3, height: 3)).fill()
        }
    }
}

/// A sheet, an alert, a save panel, a context menu, a pop-up button, the Tools menu's result, a
/// web view with its own button and a canvas-drawn button with no accessibility element.
struct DialogsScene: View {
    @ObservedObject var model: LabModel
    let lab: NavLabApp
    @State private var showSheet = false
    @State private var showAlert = false
    @State private var sheetResult = "none"
    @State private var alertResult = "none"
    @State private var savedTo = "nothing"
    @State private var fileAction = "none"
    @State private var size = "Medium"
    @State private var taps = 0

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            SceneTitle("Dialogs and menus")
            HStack(spacing: 16) {
                Button("Close document") {
                    showSheet = true
                }
                Text("Sheet: \(sheetResult)")
                    .accessibilityIdentifier("sheetResult")
            }
            HStack(spacing: 16) {
                Button("Delete draft") {
                    showAlert = true
                }
                Text("Alert: \(alertResult)")
                    .accessibilityIdentifier("alertResult")
            }
            HStack(spacing: 16) {
                Button("Export...") {
                    lab.export { path in
                        savedTo = path
                    }
                }
                Text("Saved to: \(savedTo)")
                    .accessibilityIdentifier("savedTo")
            }
            HStack(spacing: 16) {
                Text("report.txt")
                    .padding(.vertical, 4)
                    .padding(.horizontal, 10)
                    .background(RoundedRectangle(cornerRadius: 4).fill(Color.secondary.opacity(0.15)))
                    .contextMenu {
                        Button("Rename") {
                            fileAction = "Rename"
                        }
                        Button("Duplicate") {
                            fileAction = "Duplicate"
                        }
                        Button("Move to Trash") {
                            fileAction = "Move to Trash"
                        }
                    }
                    .accessibilityLabel("File report.txt")
                Text("File: \(fileAction)")
                    .accessibilityIdentifier("fileAction")
            }
            HStack(spacing: 16) {
                Picker("Size", selection: $size) {
                    Text("Small").tag("Small")
                    Text("Medium").tag("Medium")
                    Text("Large").tag("Large")
                }
                .pickerStyle(.menu)
                .frame(width: 180)
                Text("Size: \(size)")
                    .accessibilityIdentifier("size")
            }
            Text("Reviewed: \(model.reviewed ? "yes" : "no") (Tools menu, Mark Reviewed)")
                .accessibilityIdentifier("reviewed")
            WebPane()
                .frame(width: 420, height: 80)
                .overlay(RoundedRectangle(cornerRadius: 4).stroke(Color.secondary.opacity(0.4)))
            HStack(spacing: 16) {
                TapCanvas {
                    taps += 1
                }
                Text("Taps: \(taps)")
                    .accessibilityIdentifier("taps")
            }
            Spacer()
        }
        .padding(24)
        .sheet(isPresented: $showSheet) {
            SaveChangesSheet { result in
                sheetResult = result
                showSheet = false
            }
        }
        .alert("Delete draft?", isPresented: $showAlert) {
            Button("Delete", role: .destructive) {
                alertResult = "deleted"
            }
            Button("Cancel", role: .cancel) {
                alertResult = "kept"
            }
        } message: {
            Text("The draft cannot be recovered.")
        }
    }
}

struct SaveChangesSheet: View {
    let done: (String) -> Void

    var body: some View {
        VStack(spacing: 16) {
            Text("Save changes?")
                .font(.headline)
            Text("Your edits to report.txt are not saved yet.")
            HStack(spacing: 12) {
                Button("Cancel") {
                    done("cancelled")
                }
                .keyboardShortcut(.cancelAction)
                Button("Save") {
                    done("saved")
                }
                .keyboardShortcut(.defaultAction)
            }
        }
        .padding(24)
        .frame(width: 320)
    }
}

/// A local page with its own button, which lives in the web view's accessibility tree.
struct WebPane: NSViewRepresentable {
    let html = """
    <!doctype html>
    <html><body style="font: 14px -apple-system, sans-serif; margin: 10px">
    <button onclick="n += 1; document.getElementById('out').textContent = 'Web: clicked ' + n">Web button</button>
    <p id="out">Web: not clicked</p>
    <script>var n = 0;</script>
    </body></html>
    """

    func makeNSView(context: Context) -> WKWebView {
        let web = WKWebView(frame: .zero)
        web.setAccessibilityLabel("Web page")
        web.loadHTMLString(html, baseURL: nil)
        return web
    }

    func updateNSView(_ web: WKWebView, context: Context) {}
}

/// A "Tap" button drawn on a canvas, with no accessibility element: only pixels say it is there.
struct TapCanvas: View {
    let tap: () -> Void

    var body: some View {
        Canvas { context, size in
            let rect = CGRect(origin: .zero, size: size).insetBy(dx: 2, dy: 2)
            context.fill(Path(roundedRect: rect, cornerRadius: 8), with: .color(.accentColor))
            context.draw(Text("Tap").font(.headline).foregroundColor(.white), at: CGPoint(x: size.width / 2, y: size.height / 2))
        }
        .frame(width: 90, height: 36)
        .contentShape(Rectangle())
        .onTapGesture {
            tap()
        }
        .accessibilityHidden(true)
    }
}

/// The main window's side of the Inspector: a Project header the Inspector's Apply sets, and a
/// Note field. The Inspector opens over both and takes the keyboard.
struct SecondWindowScene: View {
    @ObservedObject var model: LabModel
    let lab: NavLabApp
    @State private var note = ""
    @State private var savedNote = "none"

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            SceneTitle("Second window")
            Text("Project: \(model.projectName)")
                .font(.title2)
                .accessibilityIdentifier("project")
            HStack(spacing: 8) {
                Text("Note")
                TextField("Note", text: $note)
                    .textFieldStyle(.roundedBorder)
                    .frame(width: 220)
                    .accessibilityLabel("Note")
                    .onSubmit {
                        savedNote = note
                    }
            }
            Text("Note: \(savedNote)")
                .accessibilityIdentifier("savedNote")
            Spacer()
                .frame(height: 150)
            HStack(spacing: 12) {
                Button("Open Inspector") {
                    lab.openInspector()
                }
                Button("Close Inspector") {
                    lab.closeInspector()
                }
            }
            Spacer()
        }
        .padding(24)
    }
}

struct InspectorView: View {
    @ObservedObject var model: LabModel
    @State private var name = ""
    @FocusState private var nameFocused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Project name")
                .font(.headline)
            TextField("Name", text: $name)
                .textFieldStyle(.roundedBorder)
                .accessibilityLabel("Name")
                .focused($nameFocused)
            Button("Apply") {
                model.projectName = name
            }
            Spacer()
        }
        .padding(20)
        .frame(width: 340, height: 240)
        .onAppear {
            nameFocused = true
        }
    }
}
