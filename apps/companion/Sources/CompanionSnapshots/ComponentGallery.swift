import AppKit
import SwiftUI
@testable import Companion

/// Every component of the redesign in every state it can be in, laid out like the Figma
/// file's "Component states" board (Components page, 19:2882), so a render sits beside
/// `docs/20-companion-ux-research/figma/component-states.png` for review. Needs no daemon.
struct ComponentGallery: View {
    var frame: NSImage?

    var body: some View {
        VStack(alignment: .leading, spacing: Gap.x24) {
            Text("Component states").textStyle(.display).foregroundStyle(Palette.text)
            section("Status glyph") {
                HStack(spacing: Gap.x16) {
                    ForEach(GlyphKind.allCases, id: \.self) { kind in
                        VStack(spacing: Gap.x4) {
                            StatusGlyph(kind: kind, color: color(for: kind))
                            caption(kind.rawValue.capitalized)
                        }
                    }
                }
            }
            section("Run row") {
                HStack(alignment: .top, spacing: Gap.x16) {
                    labeled("Default", width: 248) { RunRowView(model: row("WordCount: longest word", .passed, .pass, "2h"), selected: false) }
                    labeled("Selected", width: 248) { RunRowView(model: row("TipSplit: split the bill", .failed, .fail, "2 failed", .fail), selected: true) }
                    labeled("Running", width: 248) { RunRowView(model: row("UnitConvert: Temperature", .checking, .accent, "1:12"), selected: false) }
                    labeled("Starting", width: 248) { RunRowView(model: row("TodoList: summary line", .starting, .accent, "0:24"), selected: false) }
                    labeled("Empty group", width: 248) {
                        Text("Nothing needs you").textStyle(.caption).foregroundStyle(Palette.textSecondary)
                            .frame(width: 232, height: Metrics.runRowHeight, alignment: .leading).padding(.leading, Gap.x8)
                    }
                    labeled("Stopped", width: 248) { RunRowView(model: row("UnitConvert: result size", .stopped, .secondary, "1d"), selected: false) }
                }
            }
            section("Check row") {
                HStack(alignment: .top, spacing: Gap.x16) {
                    labeled("Pending") { CheckRowView(check: .init(id: "1", text: "Each pays becomes $50.00 at 25%", state: .pending), selected: false) }
                    labeled("Checking") { CheckRowView(check: .init(id: "2", text: "Each pays is $48.00 for 3 people", state: .pending), selected: false, checking: true) }
                    labeled("Passed") { CheckRowView(check: .init(id: "3", text: "Tip is $24.00 for $120 at 20%", state: .pass, saw: "$24.00"), selected: false) }
                    labeled("Failed") { CheckRowView(check: .init(id: "4", text: "Each pays is $48.00 for 3 people", state: .fail, saw: "$8.00"), selected: false) }
                    labeled("Selected, with its evidence line") {
                        CheckRowView(check: .init(id: "5", text: "Each pays becomes $50.00 at 25%", state: .fail, saw: "$10.00",
                                                  observed: "Bill 120, 3 people, then 25%."), selected: true)
                    }
                }
            }
            section("Button") {
                Grid(horizontalSpacing: Gap.x24, verticalSpacing: Gap.x12) {
                    GridRow {
                        caption("")
                        ForEach(["Default", "Disabled", "Loading"], id: \.self) { caption($0) }
                    }
                    ForEach([("Primary", ActionKind.primary), ("Secondary", .secondary), ("Plain", .plain)], id: \.0) { name, kind in
                        GridRow {
                            caption(name)
                            Button("Accept fail") {}.buttonStyle(ActionButtonStyle(kind: kind))
                            Button("Accept fail") {}.buttonStyle(ActionButtonStyle(kind: kind)).disabled(true)
                            Button("Accept fail") {}.buttonStyle(ActionButtonStyle(kind: kind, loading: true))
                        }
                    }
                }
            }
            section("Toolbar button, icon button, keycap") {
                HStack(spacing: Gap.x16) {
                    ToolbarButton(icon: .activity, title: "Activity") {}
                    ToolbarButton(icon: .activity, title: "Activity", on: true) {}
                    ToolbarButton(icon: .message, title: "Message") {}
                    ToolbarButton(icon: .pointer, title: "Take control") {}.disabled(true)
                    IconButton(icon: .search, name: "Search runs") {}
                    IconButton(icon: .more, name: "More") {}
                    Keycap(keys: "⌘K")
                    Keycap(keys: "A")
                }
            }
            section("Palette row") {
                HStack(spacing: Gap.x16) {
                    labeled("Default") { PaletteRowView(icon: .activity, label: "Open activity", keys: "A", selected: false).frame(width: 300) }
                    labeled("Selected") { PaletteRowView(icon: .check, label: "Accept fail", keys: "⌘↩", selected: true).frame(width: 300) }
                    labeled("Disabled") { PaletteRowView(icon: .pointer, label: "Take control", selected: false, enabled: false).frame(width: 300) }
                }
            }
            section("Tool chip, task row, thinking") {
                HStack(alignment: .top, spacing: Gap.x16) {
                    VStack(alignment: .leading, spacing: Gap.x8) {
                        ToolChip(chip: .init(id: 1, icon: .camera, label: "Screenshot", meta: "now", state: .running))
                        ToolChip(chip: .init(id: 2, icon: .pointer, label: "Click 25%", meta: "0.4s", state: .done))
                        ToolChip(chip: .init(id: 3, icon: .eye, label: "Read \"Summary\"", meta: "", state: .error("timed out")))
                    }
                    TaskRowView(row: .init(id: "a", title: "Opened TipSplit", glyph: .passed, color: .pass, meta: "0:18",
                                           chips: [.init(id: 1, icon: .pointer, label: "Click", meta: "0.2s", state: .done)], opensItself: false),
                                expanded: .constant(false)).frame(width: 360)
                    TaskRowView(row: .init(id: "b", title: "Each pays showed $10.00 at 25%", glyph: .failed, color: .fail, meta: "0:09",
                                           chips: [.init(id: 1, icon: .pointer, label: "Click 25%", meta: "0.4s", state: .done),
                                                   .init(id: 2, icon: .camera, label: "Screenshot", meta: "0.9s", state: .done),
                                                   .init(id: 3, icon: .eye, label: "Read \"Each pays\"", meta: "1.2s", state: .done)],
                                           opensItself: true),
                                expanded: .constant(true)).frame(width: 360)
                    VStack(alignment: .leading, spacing: Gap.x8) {
                        ThinkingView(live: true)
                        ThinkingView(live: false, doneText: "Thought for 4s")
                    }
                }
            }
            section("Composer") {
                HStack(spacing: Gap.x16) {
                    ComposerView(text: .constant(""), send: {}).frame(width: 300).focusable(false)
                    ComposerView(text: .constant("Also check 15% and 18%"), send: {}).frame(width: 300)
                    ComposerView(text: .constant("Also check 15% and 18%"), sending: true, send: {}).frame(width: 300)
                    ComposerView(text: .constant(""), disabledReason: "The verifier stopped. Continue to message it.", send: {}).frame(width: 380)
                }
            }
            section("Evidence frame, mark, filmstrip") {
                HStack(alignment: .top, spacing: Gap.x16) {
                    EvidenceFrame(content: frame.map(FrameContent.image) ?? .loading,
                                  mark: SummaryBox(x: 0.515, y: 0.635, w: 0.23, h: 0.05)).frame(width: 240)
                    EvidenceFrame(content: .loading).frame(width: 200)
                    EvidenceFrame(content: .missing, openRecording: {}).frame(width: 200)
                    HStack(spacing: Gap.x8) {
                        FilmstripThumb(image: frame, selected: false)
                        FilmstripThumb(image: frame, selected: false, mark: .failed)
                        FilmstripThumb(image: frame, selected: true)
                    }
                }
            }
        }
        .padding(Gap.x32)
        .frame(width: 2000, alignment: .topLeading)
        .background(Palette.bg)
    }

    private func color(for kind: GlyphKind) -> ToneColor {
        switch kind {
        case .passed: .pass
        case .failed: .fail
        case .checking, .starting: .accent
        case .paused, .warning: .wait
        case .stopped: .secondary
        case .pending: .tertiary
        }
    }

    private func row(_ name: String, _ glyph: GlyphKind, _ color: ToneColor, _ meta: String, _ metaColor: ToneColor = .secondary) -> RunRowModel {
        var s = Summary(runId: name, name: name)
        s.state = .checking
        var model = RunRowModel(s, now: Date())
        model.glyph = glyph
        model.glyphColor = color
        model.meta = meta
        model.metaColor = metaColor
        return model
    }

    private func section(_ title: String, @ViewBuilder _ content: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: Gap.x12) {
            Text(title).textStyle(.title).foregroundStyle(Palette.text)
            content()
        }
    }

    private func labeled(_ title: String, width: CGFloat = 300, @ViewBuilder _ content: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: Gap.x8) {
            content().frame(width: width, alignment: .leading)
            caption(title)
        }
    }

    private func caption(_ text: String) -> some View {
        Text(text).textStyle(.caption).foregroundStyle(Palette.textSecondary)
    }
}
