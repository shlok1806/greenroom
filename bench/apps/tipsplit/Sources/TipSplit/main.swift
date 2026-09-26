import SwiftUI

// TipSplit: enter a bill, pick a tip, split it between people.
// A verifier bench fixture (bench/README.md). Mutant patches edit this file, so keep it
// plain: one statement a line, no clever helpers.

struct ContentView: View {
    @State private var bill = "84.00"
    @AppStorage("tipPercent") private var tipPercent = 18
    @AppStorage("people") private var people = 2
    @State private var roundUp = false

    var billValue: Double {
        Double(bill.trimmingCharacters(in: .whitespaces)) ?? 0
    }

    var tip: Double {
        billValue * Double(tipPercent) / 100
    }

    var perPerson: Double {
        let share = (billValue + tip) / Double(people)
        return roundUp ? share.rounded(.up) : share
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("TipSplit").font(.system(size: 34, weight: .bold))
            HStack {
                Text("Bill").frame(width: 70, alignment: .leading)
                TextField("0.00", text: $bill)
                    .textFieldStyle(.roundedBorder)
                    .frame(width: 140)
                    .accessibilityLabel("Bill")
                    .accessibilityIdentifier("bill")
            }
            HStack {
                Text("Tip").frame(width: 70, alignment: .leading)
                Picker("Tip", selection: $tipPercent) {
                    ForEach([15, 18, 20, 25], id: \.self) { Text("\($0)%").tag($0) }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .fixedSize()
            }
            Stepper("People: \(people)", value: $people, in: 1...12)
                .frame(width: 200, alignment: .leading)
            Toggle("Round up each share", isOn: $roundUp)
            Divider()
            Text(String(format: "Tip: $%.2f", tip))
                .font(.title2)
                .accessibilityIdentifier("tip")
            Text(String(format: "Each pays: $%.2f", perPerson))
                .font(.system(size: 28, weight: .semibold))
                .accessibilityIdentifier("perPerson")
            Button("Reset") {
                reset()
            }
        }
        .padding(32)
        .frame(width: 440)
    }

    func reset() {
        bill = "84.00"
        tipPercent = 18
        people = 2
        roundUp = false
    }
}

@main
struct TipSplitApp: App {
    init() {
        DispatchQueue.main.async {
            NSApp.setActivationPolicy(.regular)
            NSApp.activate(ignoringOtherApps: true)
        }
    }

    var body: some Scene {
        WindowGroup("TipSplit") { ContentView() }
            .windowResizability(.contentSize)
    }
}
