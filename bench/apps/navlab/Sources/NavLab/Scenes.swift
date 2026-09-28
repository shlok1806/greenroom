import AppKit
import SwiftUI

// The wave 1 scenes: each is one hazard of docs/21 section 1 with a result on screen that says
// whether the verifier got through it.

/// A floating Runs list drawn over the Open run 42 button in the same window (#189). While the
/// list is up, a click at the button's center lands on Run 9 and opens the wrong run.
struct OverlayScene: View {
    @State private var openRun = "none"
    @State private var showRuns = true
    let runs = [7, 8, 9, 10, 11, 12]

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            SceneTitle("Overlay")
            Text("Open: \(openRun)")
                .font(.title2)
                .accessibilityIdentifier("openRun")
            HStack(spacing: 12) {
                Button("Show runs") {
                    showRuns = true
                }
                Button("Hide runs") {
                    showRuns = false
                }
            }
            Spacer()
                .frame(height: 90)
            Button("Open run 42") {
                openRun = "Run 42"
            }
            .buttonStyle(DrawnButtonStyle())
            .overlay(alignment: .topLeading) {
                if showRuns {
                    RunsList(runs: runs) { run in
                        openRun = "Run \(run)"
                    }
                    .offset(x: -16, y: -86)
                }
            }
            Spacer()
        }
        .padding(24)
    }
}

/// A button SwiftUI draws itself. A system push button can be an AppKit view, and AppKit hands a
/// click to a view before any SwiftUI content drawn over it, so the list would not cover it.
struct DrawnButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .padding(.vertical, 6)
            .padding(.horizontal, 14)
            .foregroundColor(.white)
            .background(RoundedRectangle(cornerRadius: 6).fill(Color.accentColor.opacity(configuration.isPressed ? 0.7 : 1)))
    }
}

struct RunsList: View {
    let runs: [Int]
    let open: (Int) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("Runs")
                .font(.headline)
                .frame(height: 24)
                .padding(.horizontal, 10)
            ForEach(runs, id: \.self) { run in
                Button {
                    open(run)
                } label: {
                    Text("Run \(run)")
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .frame(height: 28)
                        .padding(.horizontal, 10)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Run \(run)")
            }
        }
        .padding(.vertical, 6)
        .frame(width: 200)
        .fixedSize()
        .background(RoundedRectangle(cornerRadius: 8).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(Color.secondary.opacity(0.4)))
        .shadow(radius: 6)
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Runs")
    }
}

/// An outer scroll area whose Summary line is below the fold, around an inner fixed-height scroll
/// area of 40 rows that ends with a Details button (#190). The inner area sits under the window's
/// center, so a wheel there moves only the inner rows.
struct NestedScrollScene: View {
    @State private var details = "Details: not opened"
    let rowCount = 40
    let flaggedRows = [5, 17, 33]
    let detailItems = 17

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                SceneTitle("Nested scroll")
                Text("The rows below scroll on their own. Details is at the end of the rows; the summary is at the bottom of this page.")
                    .fixedSize(horizontal: false, vertical: true)
                Text(details)
                    .font(.title3)
                    .accessibilityIdentifier("details")
                ScrollView {
                    VStack(alignment: .leading, spacing: 0) {
                        ForEach(1...rowCount, id: \.self) { row in
                            Text(rowTitle(row))
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .frame(height: 26)
                                .padding(.horizontal, 10)
                        }
                        Button("Details") {
                            details = "Details: \(detailItems) items"
                        }
                        .padding(10)
                    }
                }
                .frame(width: 420, height: 250)
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color.secondary.opacity(0.5)))
                .accessibilityLabel("Rows")
                Text("Flagged rows are counted in the summary.")
                Color.clear
                    .frame(height: 360)
                Text("Summary: \(rowCount) rows, \(flaggedRows.count) flagged")
                    .font(.title3)
                    .accessibilityIdentifier("summary")
            }
            .padding(24)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .accessibilityLabel("Page")
    }

    func rowTitle(_ row: Int) -> String {
        flaggedRows.contains(row) ? "Row \(row) (flagged)" : "Row \(row)"
    }
}

/// A release note of about 400 characters on one line that the window truncates (#190). Its last
/// sentence carries the fact a task asks about.
struct LongLabelScene: View {
    let note = "Release 4.2 notes: the importer now accepts CSV files with a header row, the settings window remembers its size between launches, the sidebar hides empty folders, search matches accented letters, exports keep the original file dates, the progress bar no longer jumps back at 99 percent, and crash reports include the last three actions. The nightly export now starts at 02:30 UTC."

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            SceneTitle("Long label")
            Text("The release note below is a single line.")
            Text(note)
                .lineLimit(1)
                .truncationMode(.tail)
                .accessibilityIdentifier("releaseNote")
            Spacer()
        }
        .padding(24)
    }
}

/// An Amount field that drops a key arriving within 15 ms of the one before it, as a busy app
/// does (docs/14 case 5), and a line echoing the field with tax added.
struct AmountScene: View {
    @State private var amount = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            SceneTitle("Amount field")
            Text("Type an amount; the lines below follow the field.")
            HStack(spacing: 8) {
                Text("Amount")
                AmountField(text: $amount)
                    .frame(width: 160, height: 24)
            }
            Text("Amount: \(amount)")
                .font(.title3)
                .accessibilityIdentifier("amountEcho")
            Text("With 8% tax: \(withTax)")
                .font(.title3)
                .accessibilityIdentifier("withTax")
            Spacer()
        }
        .padding(24)
    }

    var withTax: String {
        let value = Double(amount) ?? 0
        return String(format: "%.2f", value * 1.08)
    }
}

/// The Amount field: an NSTextField whose field editor drops keys typed too fast.
struct AmountField: NSViewRepresentable {
    @Binding var text: String

    func makeCoordinator() -> Coordinator {
        Coordinator(text: $text)
    }

    func makeNSView(context: Context) -> DroppingField {
        let field = DroppingField(frame: .zero)
        field.isEditable = true
        field.isSelectable = true
        field.isBezeled = true
        field.bezelStyle = .roundedBezel
        field.drawsBackground = true
        field.placeholderString = "0"
        field.setAccessibilityLabel("Amount")
        field.delegate = context.coordinator
        field.onValueSet = context.coordinator.set
        return field
    }

    func updateNSView(_ field: DroppingField, context: Context) {
        context.coordinator.text = $text
        if field.stringValue != text {
            field.stringValue = text
        }
    }

    final class Coordinator: NSObject, NSTextFieldDelegate {
        var text: Binding<String>

        init(text: Binding<String>) {
            self.text = text
        }

        func controlTextDidChange(_ notification: Notification) {
            guard let field = notification.object as? NSTextField else {
                return
            }
            set(field.stringValue)
        }

        func set(_ value: String) {
            text.wrappedValue = value
        }
    }
}

final class DroppingField: NSTextField {
    var onValueSet: ((String) -> Void)?

    override class var cellClass: AnyClass? {
        get { DroppingFieldCell.self }
        set {}
    }

    // A value set through accessibility (not typed) reaches the echo too.
    override func setAccessibilityValue(_ value: Any?) {
        guard let string = value as? String else {
            super.setAccessibilityValue(value)
            return
        }
        stringValue = string
        onValueSet?(string)
    }
}

final class DroppingFieldCell: NSTextFieldCell {
    lazy var editor: DroppingEditor = {
        let editor = DroppingEditor(frame: .zero)
        editor.isFieldEditor = true
        return editor
    }()

    override func fieldEditor(for controlView: NSView) -> NSTextView? {
        editor
    }
}

/// Drops a typed character that arrives within 15 ms of the previous key, unless the previous
/// one was dropped: typing "120" as fast as a script can leaves "10". Keys that type nothing
/// (Tab, Return, arrows, Delete) always pass and do not count.
final class DroppingEditor: NSTextView {
    var lastKey: TimeInterval = 0
    var droppedLast = false

    override func keyDown(with event: NSEvent) {
        guard typesText(event) else {
            super.keyDown(with: event)
            return
        }
        let tooSoon = event.timestamp - lastKey < 0.015
        lastKey = event.timestamp
        if tooSoon && !droppedLast {
            droppedLast = true
            return
        }
        droppedLast = false
        super.keyDown(with: event)
    }

    func typesText(_ event: NSEvent) -> Bool {
        if !event.modifierFlags.intersection([.command, .control]).isEmpty {
            return false
        }
        guard let characters = event.characters, !characters.isEmpty else {
            return false
        }
        return characters.unicodeScalars.allSatisfy { scalar in
            scalar.value >= 0x20 && scalar.value != 0x7F && !(0xF700...0xF8FF).contains(scalar.value)
        }
    }
}

/// Compute shows its result 3 seconds after it is pressed: a wait, not a look loop.
struct DelayedScene: View {
    @State private var result = "Result: none"
    @State private var runs = 0

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            SceneTitle("Delayed result")
            Text("Compute takes about 3 seconds.")
            Button("Compute") {
                compute()
            }
            Text(result)
                .font(.title2)
                .accessibilityIdentifier("result")
            Spacer()
        }
        .padding(24)
    }

    func compute() {
        runs += 1
        let run = runs
        result = "Computing..."
        DispatchQueue.main.asyncAfter(deadline: .now() + 3) {
            if run == runs {
                result = "Result: 42"
            }
        }
    }
}

/// Continue is disabled until 2 seconds after Prepare is pressed.
struct PrepareScene: View {
    @State private var status = "not prepared"
    @State private var ready = false
    @State private var step = 1

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            SceneTitle("Prepare and continue")
            Text("Continue is enabled once Prepare finishes, about 2 seconds after it is pressed.")
            HStack(spacing: 12) {
                Button("Prepare") {
                    prepare()
                }
                Button("Continue") {
                    step = min(step + 1, 3)
                }
                .disabled(!ready)
            }
            Text("Status: \(status)")
                .accessibilityIdentifier("status")
            Text("Stage: Step \(step) of 3")
                .font(.title2)
                .accessibilityIdentifier("stage")
            Spacer()
        }
        .padding(24)
    }

    func prepare() {
        status = "preparing"
        DispatchQueue.main.asyncAfter(deadline: .now() + 2) {
            ready = true
            status = "ready"
        }
    }
}
