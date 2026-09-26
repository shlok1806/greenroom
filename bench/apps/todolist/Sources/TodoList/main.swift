import SwiftUI

// TodoList: add items, tick them off, delete them, clear the done ones. Saved between launches.
// A verifier bench fixture (bench/README.md). Mutant patches edit this file, so keep it
// plain: one statement a line, no clever helpers.

struct Item: Codable, Identifiable, Equatable {
    var id = UUID()
    var title: String
    var done = false
}

final class Store: ObservableObject {
    @Published var items: [Item] = []
    @Published var focus: UUID?

    init() {
        load()
    }

    func load() {
        guard let data = UserDefaults.standard.data(forKey: "items") else { return }
        items = (try? JSONDecoder().decode([Item].self, from: data)) ?? []
    }

    func save() {
        UserDefaults.standard.set(try? JSONEncoder().encode(items), forKey: "items")
    }

    func add(_ title: String) {
        let trimmed = title.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty else { return }
        items.append(Item(title: trimmed))
        save()
    }

    func toggle(_ id: UUID) {
        guard let i = items.firstIndex(where: { $0.id == id }) else { return }
        items[i].done.toggle()
        save()
    }

    func delete(_ id: UUID) {
        guard let i = items.firstIndex(where: { $0.id == id }) else { return }
        items.remove(at: i)
        focus = i < items.count ? items[i].id : items.last?.id
        save()
    }

    func clearDone() {
        items.removeAll { $0.done }
        save()
    }

    var summary: String {
        let n = items.count
        let done = items.filter(\.done).count
        return "\(n) \(n == 1 ? "item" : "items"), \(done) done"
    }
}

struct ContentView: View {
    @StateObject private var store = Store()
    @State private var newTitle = ""

    var canAdd: Bool {
        !newTitle.trimmingCharacters(in: .whitespaces).isEmpty
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("TodoList").font(.system(size: 34, weight: .bold))
            HStack {
                TextField("New item", text: $newTitle)
                    .textFieldStyle(.roundedBorder)
                    .frame(width: 220)
                    .accessibilityLabel("New item")
                    .accessibilityIdentifier("newItem")
                    .onSubmit { add() }
                Button("Add") {
                    add()
                }
                .disabled(!canAdd)
            }
            VStack(alignment: .leading, spacing: 6) {
                ForEach(store.items) { item in
                    HStack {
                        Toggle(item.title, isOn: Binding(
                            get: { item.done },
                            set: { _ in store.toggle(item.id) }
                        ))
                        Spacer()
                        Button("Delete") {
                            store.delete(item.id)
                        }
                        .accessibilityLabel("Delete \(item.title)")
                    }
                    .frame(width: 320)
                }
            }
            Divider()
            Text(store.summary)
                .accessibilityIdentifier("summary")
            Button("Clear done") {
                store.clearDone()
            }
        }
        .padding(32)
        .frame(width: 400, alignment: .leading)
    }

    func add() {
        store.add(newTitle)
        newTitle = ""
    }
}

@main
struct TodoListApp: App {
    init() {
        DispatchQueue.main.async {
            NSApp.setActivationPolicy(.regular)
            NSApp.activate(ignoringOtherApps: true)
        }
    }

    var body: some Scene {
        WindowGroup("TodoList") { ContentView() }
            .windowResizability(.contentSize)
    }
}
