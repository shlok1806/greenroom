import AppKit
import SwiftUI
@testable import Companion

/// Renders the native redesign (companion ADR 0019) for review beside the Figma exports.
/// `GREENROOM_REDESIGN_SNAPSHOTS=<dir>` writes, per scenario and appearance,
/// `<scenario>-<light|dark>-<size>.png` at 2x and `<scenario>-<light|dark>-<size>.words.txt`
/// (what Apple's text recogniser reads in the run pane, and the count), and prints one line
/// per render with its words against the budget. `GREENROOM_SNAPSHOTS_ONLY` filters by name.
/// Like the rest of the harness it never shows a window on a display and never becomes key.
@MainActor
final class RedesignHarness {
    struct Size {
        var name: String
        var width: CGFloat
        var height: CGFloat

        static let regular = Size(name: "1280x800", width: 1280, height: 800)
        static let compact = Size(name: "1024x680", width: 1024, height: 680)
        static let wide = Size(name: "1600x1000", width: 1600, height: 1000)
    }

    struct Scenario {
        var name: String
        var sizes: [Size] = [.regular]
        var dark = false
        /// The budget the run pane's words are held to, if the scenario is a whole state.
        var budget: WordBudget.Screen?
        /// Everything left of this x (the sidebar) is not counted.
        var countFrom: CGFloat = 0
        var view: @MainActor (Size) async -> AnyView
    }

    private var environment: [String: String] { ProcessInfo.processInfo.environment }

    func run(output: String, scenarios: [Scenario]) async throws {
        let directory = URL(fileURLWithPath: output, isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let only = environment["GREENROOM_SNAPSHOTS_ONLY"] ?? ""
        var over: [String] = []
        for scenario in scenarios where only.isEmpty || scenario.name.contains(only) {
            for dark in scenario.dark ? [false, true] : [false] {
                for size in scenario.sizes {
                    let base = "\(scenario.name)-\(dark ? "dark" : "light")-\(size.name)"
                    let view = await scenario.view(size)
                    let image = try await render(view, size: size, dark: dark, to: directory.appending(path: "\(base).png"))
                    guard let image else { continue }
                    let exclude = [CGRect(x: 0, y: 0, width: scenario.countFrom * 2, height: CGFloat(image.height))]
                    let reading = try VisibleWords.read(image, excluding: exclude)
                    let verdict = scenario.budget.map { "\(reading.words) of \($0.budget)" } ?? "\(reading.words)"
                    let text = reading.lines.joined(separator: "\n") + "\n\nwords: \(verdict)\n"
                    try text.write(to: directory.appending(path: "\(base).words.txt"), atomically: true, encoding: .utf8)
                    print("rendered \(base).png, words \(verdict)")
                    if let budget = scenario.budget, reading.words > budget.budget { over.append("\(base): \(reading.words) > \(budget.budget)") }
                }
            }
        }
        if !over.isEmpty { print("OVER BUDGET:\n" + over.joined(separator: "\n")) }
    }

    /// Hosts `view` in an off-display window of `size` points and writes it at 2x.
    func render(_ view: AnyView, size: Size, dark: Bool, to file: URL) async throws -> CGImage? {
        let window = OffscreenWindow(
            contentRect: NSRect(x: OffscreenWindow.origin.x, y: OffscreenWindow.origin.y, width: size.width, height: size.height),
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.titleVisibility = .hidden
        window.titlebarAppearsTransparent = true
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.contentViewController = NSHostingController(rootView: view.environment(\.colorScheme, dark ? .dark : .light))
        window.setContentSize(NSSize(width: size.width, height: size.height))
        window.setFrameOrigin(OffscreenWindow.origin)
        window.orderFrontRegardless()
        try await Task.sleep(for: .milliseconds(700))
        window.setContentSize(NSSize(width: size.width, height: size.height))
        try await Task.sleep(for: .milliseconds(500))
        defer {
            window.orderOut(nil)
            window.close()
        }
        guard let content = window.contentView else { return nil }
        let bounds = content.bounds
        guard let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int(bounds.width * 2), pixelsHigh: Int(bounds.height * 2),
                                         bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                         colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0) else { return nil }
        rep.size = bounds.size
        (window.appearance ?? NSAppearance.currentDrawing()).performAsCurrentDrawingAppearance {
            content.cacheDisplay(in: bounds, to: rep)
        }
        guard let png = rep.representation(using: .png, properties: [:]) else { return nil }
        try png.write(to: file)
        return rep.cgImage
    }
}

extension RedesignHarness {
    /// A real guest frame for the pictures: `GREENROOM_SNAPSHOTS_FRAME`, else the TipSplit
    /// run's evidence screenshot under `~/.greenroom/runs`, else none.
    static func guestFrame() -> NSImage? {
        let env = ProcessInfo.processInfo.environment
        if let path = env["GREENROOM_SNAPSHOTS_FRAME"], let image = NSImage(contentsOfFile: path) { return image }
        let home = FileManager.default.homeDirectoryForCurrentUser
        let run = home.appending(path: ".greenroom/runs/20260923-044138-de31017819a86d84")
        for name in ["017-screenshot.png", "012-screenshot.png"] {
            if let image = NSImage(contentsOf: run.appending(path: name)) { return image }
        }
        return nil
    }

    /// The daemon's golden board (root ADR 0036), the runs the Figma screens show.
    static func goldenBoard() -> SummaryBoard? {
        let url = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
            .appendingPathComponent("daemon/internal/summary/testdata/board.golden.json")
        return (try? Data(contentsOf: url)).flatMap { try? JSONDecoder.daemon().decode(SummaryBoard.self, from: $0) }
    }

    /// The scenarios of this phase.
    static func scenarios() -> [Scenario] {
        let frame = guestFrame()
        return [
            Scenario(name: "c01-component-states", sizes: [RedesignHarness.Size(name: "board", width: 2000, height: 1320)], dark: true) { _ in
                AnyView(ComponentGallery(frame: frame))
            },
            Scenario(name: "s01-runs-sidebar", sizes: [.regular, .compact, .wide], dark: true) { size in
                AnyView(SidebarStage(board: goldenBoard(), selected: "20260923-044138-de31017819a86d84", size: size))
            },
            Scenario(name: "s02-runs-sidebar-2000", sizes: [.regular], dark: true) { size in
                AnyView(SidebarStage(board: SyntheticBoard.board(runs: 2000), selected: nil, size: size))
            },
            Scenario(name: "s03-runs-sidebar-offline", sizes: [.regular]) { size in
                AnyView(SidebarStage(board: nil, selected: nil, size: size, connection: .offline(hasData: false)))
            },
        ]
    }
}

/// The sidebar beside an empty run pane, at a window size.
struct SidebarStage: View {
    var board: SummaryBoard?
    var selected: String?
    var size: RedesignHarness.Size
    var connection: ConnectionState = .online

    var body: some View {
        HStack(spacing: 0) {
            RunsSidebar(board: board, connection: connection, selected: selected,
                        width: WindowClass.of(width: size.width).sidebar, select: { _ in }, openSettings: {})
            Palette.bg
        }
        .frame(width: size.width, height: size.height)
        .ignoresSafeArea()
        .environment(\.frozenNow, board?.updatedAt ?? Date())
    }
}
