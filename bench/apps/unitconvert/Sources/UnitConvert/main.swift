import SwiftUI

// UnitConvert: convert a value between two units of a category, either way round.
// A verifier bench fixture (bench/README.md). Mutant patches edit this file, so keep it
// plain: one statement a line, no clever helpers.

enum Category: String, CaseIterable, Identifiable {
    case length = "Length"
    case temperature = "Temperature"
    case weight = "Weight"

    var id: String { rawValue }

    var units: (String, String) {
        switch self {
        case .length: return ("km", "mi")
        case .temperature: return ("°C", "°F")
        case .weight: return ("kg", "lb")
        }
    }

    func convert(_ v: Double, forward: Bool) -> Double {
        switch self {
        case .length:
            return forward ? v * 0.621371 : v / 0.621371
        case .temperature:
            return forward ? v * 9 / 5 + 32 : (v - 32) * 5 / 9
        case .weight:
            return forward ? v * 2.20462 : v / 2.20462
        }
    }
}

struct ContentView: View {
    @AppStorage("category") private var category = Category.length
    @AppStorage("decimals") private var decimals = 2
    @State private var input = "10"
    @State private var forward = true

    var value: Double? {
        Double(input.trimmingCharacters(in: .whitespaces))
    }

    var from: String { forward ? category.units.0 : category.units.1 }
    var to: String { forward ? category.units.1 : category.units.0 }

    var result: String {
        guard let v = value else { return "Enter a number" }
        let out = category.convert(v, forward: forward)
        return "\(shown(v)) \(from) = \(String(format: "%.\(decimals)f", out)) \(to)"
    }

    // shown writes the input as typed, without a trailing ".0" for whole numbers.
    func shown(_ v: Double) -> String {
        v == v.rounded() ? String(format: "%.0f", v) : String(v)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("UnitConvert").font(.system(size: 34, weight: .bold))
            Picker("Category", selection: $category) {
                ForEach(Category.allCases) { Text($0.rawValue).tag($0) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .fixedSize()
            HStack {
                Text("Value").frame(width: 70, alignment: .leading)
                TextField("0", text: $input)
                    .textFieldStyle(.roundedBorder)
                    .frame(width: 140)
                    .accessibilityLabel("Value")
                    .accessibilityIdentifier("value")
                Text(from).frame(width: 40, alignment: .leading)
            }
            HStack {
                Button("Swap units") {
                    forward.toggle()
                }
                Stepper("Decimals: \(decimals)", value: $decimals, in: 0...4)
                    .frame(width: 180, alignment: .leading)
            }
            Divider()
            Text(result)
                .font(.system(size: 26, weight: .semibold))
                .accessibilityIdentifier("result")
        }
        .padding(32)
        .frame(width: 460)
    }
}

@main
struct UnitConvertApp: App {
    init() {
        DispatchQueue.main.async {
            NSApp.setActivationPolicy(.regular)
            NSApp.activate(ignoringOtherApps: true)
        }
    }

    var body: some Scene {
        WindowGroup("UnitConvert") { ContentView() }
            .windowResizability(.contentSize)
    }
}
