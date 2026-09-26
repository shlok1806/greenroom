import SwiftUI

// WordCount: type or paste text, see its words and characters, change its case.
// The draft is saved between launches.
// A verifier bench fixture (bench/README.md). Mutant patches edit this file, so keep it
// plain: one statement a line, no clever helpers.

struct ContentView: View {
    @AppStorage("draft") private var text = "The quick brown fox"
    @AppStorage("countSpaces") private var countSpaces = true

    var words: [Substring] {
        text.split(whereSeparator: \.isWhitespace)
    }

    var characters: Int {
        countSpaces ? text.count : text.filter { !$0.isWhitespace }.count
    }

    var longest: String {
        guard let word = words.max(by: { $0.count < $1.count }) else { return "none" }
        return String(word)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("WordCount").font(.system(size: 34, weight: .bold))
            TextField("Text", text: $text, axis: .vertical)
                .textFieldStyle(.roundedBorder)
                .lineLimit(3...6)
                .frame(width: 360)
                .accessibilityLabel("Text")
                .accessibilityIdentifier("text")
            HStack {
                Button("UPPERCASE") {
                    text = text.uppercased()
                }
                Button("lowercase") {
                    text = text.lowercased()
                }
                Button("Clear") {
                    text = ""
                }
            }
            Toggle("Count spaces", isOn: $countSpaces)
            Divider()
            Text("Words: \(words.count)")
                .font(.title2)
                .accessibilityIdentifier("words")
            Text("Characters: \(characters)")
                .font(.title2)
                .accessibilityIdentifier("characters")
            Text("Longest word: \(longest)")
                .accessibilityIdentifier("longest")
        }
        .padding(32)
        .frame(width: 440, alignment: .leading)
    }
}

@main
struct WordCountApp: App {
    init() {
        DispatchQueue.main.async {
            NSApp.setActivationPolicy(.regular)
            NSApp.activate(ignoringOtherApps: true)
        }
    }

    var body: some Scene {
        WindowGroup("WordCount") { ContentView() }
            .windowResizability(.contentSize)
    }
}
