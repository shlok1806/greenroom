import SwiftUI

// Rows (Figma Components: Run row 4:40, Check row 4:59, Palette row 4:72). Selection is a
// filled rounded rectangle (bg-selected), never a coloured side stripe.

/// A run in the sidebar: one glyph, a name of five words or fewer, one short meta. 32 pt.
struct RunRowView: View {
    var model: RunRowModel
    var selected: Bool

    var body: some View {
        HStack(spacing: Gap.x8) {
            StatusGlyph(kind: model.glyph, color: model.glyphColor)
            Text(model.name)
                .textStyle(selected ? .bodyEmphasis : .body)
                .foregroundStyle(Palette.text)
                .lineLimit(1)
                .truncationMode(.tail)
                .frame(maxWidth: .infinity, alignment: .leading)
            Text(model.meta)
                .textStyle(.caption)
                .foregroundStyle(model.metaColor.token.color)
                .lineLimit(1)
                .fixedSize()
        }
        .padding(.horizontal, Gap.x8)
        .frame(height: Metrics.runRowHeight)
        .background(RoundedRectangle(cornerRadius: Corner.control).fill(selected ? Palette.bgSelected : .clear))
        .contentShape(Rectangle())
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(model.accessibilityLabel)
        .accessibilityAddTraits(selected ? [.isSelected, .isButton] : .isButton)
    }
}

/// A group heading in the sidebar, sentence case.
struct GroupHeading: View {
    var title: String

    var body: some View {
        Text(title)
            .textStyle(.captionEmphasis)
            .foregroundStyle(Palette.textSecondary)
            .padding(.horizontal, Gap.x8)
            .padding(.top, Gap.x12)
            .padding(.bottom, Gap.x4)
            .frame(maxWidth: .infinity, alignment: .leading)
            .accessibilityAddTraits(.isHeader)
    }
}

/// A check: the claim, its glyph, and the one fact that differs ("saw $10.00") or "checking".
/// Selected shows one line of what the evidence showed. 40 pt or more.
struct CheckRowView: View {
    var check: SummaryCheck
    var selected: Bool
    /// Drawn as the check being worked on now: the spinner and "checking".
    var checking = false
    /// Overrides the meta: "waiting", "paused".
    var metaOverride: String?

    var body: some View {
        VStack(alignment: .leading, spacing: Gap.x4) {
            HStack(alignment: .top, spacing: 10) {
                StatusGlyph(kind: checking ? .checking : check.state.glyph, color: checking ? .accent : check.state.color)
                    .frame(height: 18)
                Text(check.text)
                    .textStyle(selected ? .bodyEmphasis : .body)
                    .foregroundStyle(check.state == .pending && !checking && !selected ? Palette.textSecondary : Palette.text)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .fixedSize(horizontal: false, vertical: true)
                if let meta {
                    Text(meta.text)
                        .textStyle(.body)
                        .foregroundStyle(meta.color.token.color)
                        .lineLimit(1)
                        .fixedSize()
                }
            }
            if selected, let observed = check.observed {
                Text(observed)
                    .textStyle(.body)
                    .foregroundStyle(Palette.textSecondary)
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .padding(.leading, 26)
            }
        }
        .padding(.horizontal, Gap.x12)
        .padding(.vertical, 10)
        .frame(minHeight: Metrics.checkRowMinHeight, alignment: .top)
        .background(RoundedRectangle(cornerRadius: Corner.row).fill(selected ? Palette.bgSelected : .clear))
        .contentShape(Rectangle())
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accessibilityLabel)
        .accessibilityAddTraits(selected ? [.isSelected, .isButton] : .isButton)
    }

    private var meta: (text: String, color: ToneColor)? {
        if let metaOverride { return (metaOverride, .secondary) }
        if checking { return ("checking", .accent) }
        switch check.state {
        case .fail: return check.saw.map { ("saw \($0)", .fail) }
        case .pass: return check.saw.map { ($0, .secondary) }
        case .pending: return nil
        }
    }

    private var accessibilityLabel: String { Self.label(check, checking: checking, meta: meta?.text) }

    /// "Each pays becomes $50.00 at 25%, failed, saw $10.00".
    static func label(_ check: SummaryCheck, checking: Bool, meta: String?) -> String {
        let state = checking ? "checking" : (check.state == .pass ? "passed" : check.state == .fail ? "failed" : "not checked yet")
        return [check.text, state, meta].compactMap { $0 }.joined(separator: ", ")
    }
}

/// A row of the Cmd-K palette: icon, a verb-first label, its key.
struct PaletteRowView: View {
    var systemImage: String?
    var glyph: (GlyphKind, ToneColor)?
    var label: String
    var keys: String?
    var selected: Bool
    var enabled = true

    var body: some View {
        HStack(spacing: Gap.x12) {
            Group {
                if let glyph {
                    StatusGlyph(kind: glyph.0, color: glyph.1)
                } else if let systemImage {
                    Image(systemName: systemImage).font(.system(size: 13)).foregroundStyle(Palette.textSecondary)
                }
            }
            .frame(width: 16, height: 16)
            Text(label)
                .textStyle(.body)
                .foregroundStyle(enabled ? Palette.text : Palette.textTertiary)
                .lineLimit(1)
                .frame(maxWidth: .infinity, alignment: .leading)
            if let keys { Keycap(keys: keys) }
        }
        .padding(.horizontal, Gap.x12)
        .frame(height: Metrics.paletteRowHeight)
        .background(RoundedRectangle(cornerRadius: Corner.control).fill(selected ? Palette.bgSelected : .clear))
        .contentShape(Rectangle())
    }
}
