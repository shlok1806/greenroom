import SwiftUI

// Rows (Figma Components: Run row 4:40, Check row 4:59, Palette row 4:72), with the file's
// numbers. Selection is a filled rounded rectangle (bg-selected), never a coloured side stripe.

/// A run in the sidebar: one glyph, a name of five words or fewer, one short meta. 32 tall,
/// radius 6, 8 at the sides, 8 between the glyph, the name and the meta.
struct RunRowView: View {
    var model: RunRowModel
    var selected: Bool
    var hovered = false

    var body: some View {
        HStack(spacing: Gap.x8) {
            StatusGlyph(kind: model.glyph, color: model.glyphColor)
                .clonePart("Glyph")
            Text(model.name)
                .textStyle(selected ? .bodyEmphasis : .body)
                .foregroundStyle(Palette.text)
                .lineLimit(1)
                .truncationMode(.tail)
                .frame(maxWidth: .infinity, alignment: .leading)
                .clonePart("Name")
            Text(model.meta)
                .textStyle(.caption)
                .foregroundStyle(model.metaColor.token.color)
                .lineLimit(1)
                .fixedSize()
                .clonePart("Meta")
        }
        .padding(.horizontal, Gap.x8)
        .frame(height: Metrics.runRowHeight)
        .background(RoundedRectangle(cornerRadius: Corner.control)
            .fill(selected ? Palette.bgSelected : (hovered ? Palette.bgHover : .clear)))
        .contentShape(Rectangle())
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(model.accessibilityLabel)
        .accessibilityAddTraits(selected ? [.isSelected, .isButton] : .isButton)
    }
}

/// A run row still loading: a 16 pt circle and a 10 pt bar, still (Figma: motion only on a
/// state change, so no pulse).
struct RunRowPlaceholder: View {
    var body: some View {
        HStack(spacing: Gap.x8) {
            Circle().fill(Palette.bgSelected).frame(width: 16, height: 16)
            Capsule().fill(Palette.bgSelected).frame(width: 116, height: 10)
            Spacer(minLength: 0)
        }
        .padding(.horizontal, Gap.x8)
        .frame(height: Metrics.runRowHeight)
        .accessibilityLabel("Loading runs")
    }
}

/// A group heading in the sidebar, sentence case: Caption Emphasis, 12 above, 4 below, 8 at
/// the sides (30 tall).
struct GroupHeading: View {
    var title: String

    var body: some View {
        Text(title)
            .textStyle(.captionEmphasis)
            .foregroundStyle(Palette.textSecondary)
            .clonePart("Text")
            .padding(.horizontal, Gap.x8)
            .padding(.top, Gap.x12)
            .padding(.bottom, Gap.x4)
            .frame(maxWidth: .infinity, alignment: .leading)
            .accessibilityAddTraits(.isHeader)
    }
}

/// A check: the claim, its glyph, and the one fact that differs ("saw $10.00") or "checking".
/// Selected shows one line of what the evidence showed. 10 above and below, 12 at the sides,
/// radius 8; 10 between the glyph, the claim and the fact; the second line 4 below, in line
/// with the claim.
struct CheckRowView: View {
    var check: SummaryCheck
    var selected: Bool
    /// Drawn as the check being worked on now: the spinner and "checking".
    var checking = false
    /// Overrides the meta: "waiting", "paused".
    var metaOverride: String?
    /// Whether a passed check shows the value it read: in a run that passed (Figma 04), not in
    /// one that failed, where the eye goes to what failed (Figma 03).
    var showsPassValue = true
    var hovered = false

    var body: some View {
        VStack(alignment: .leading, spacing: Gap.x4) {
            HStack(alignment: .top, spacing: 10) {
                StatusGlyph(kind: checking ? .checking : check.state.glyph, color: checking ? .accent : check.state.color)
                    .frame(width: 16, height: 18)
                    .clonePart("GlyphBox")
                Text(check.text)
                    .textStyle(selected ? .bodyEmphasis : .body)
                    .foregroundStyle(check.state == .pending && !checking && !selected ? Palette.textSecondary : Palette.text)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .clonePart("Label")
                if let meta {
                    Text(meta.text)
                        .textStyle(.body)
                        .foregroundStyle(meta.color.token.color)
                        .lineLimit(1)
                        .fixedSize()
                        .clonePart("Meta")
                }
            }
            .cloneScope("Top")
            if selected, let observed = check.observed {
                Text(observed)
                    .textStyle(.body)
                    .foregroundStyle(Palette.textSecondary)
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .clonePart("Detail")
                    .padding(.leading, 26)
                    .cloneScope("DetailWrap")
            }
        }
        .padding(.horizontal, Gap.x12)
        .padding(.vertical, 10)
        .background(RoundedRectangle(cornerRadius: Corner.row)
            .fill(selected ? Palette.bgSelected : (hovered ? Palette.bgHover : .clear)))
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
        case .pass: return showsPassValue ? check.saw.map { ($0, .secondary) } : nil
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

/// A row of the Cmd-K palette: icon, a verb-first label, its key. 36 tall, radius 6, 12 at
/// the sides and between the pieces.
struct PaletteRowView: View {
    var icon: Icon?
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
                } else if let icon {
                    IconView(icon: icon).foregroundStyle(enabled ? Palette.textSecondary : Palette.textTertiary)
                }
            }
            .frame(width: 16, height: 16)
            .clonePart("Icon")
            Text(label)
                .textStyle(.body)
                .foregroundStyle(enabled ? Palette.text : Palette.textTertiary)
                .lineLimit(1)
                .frame(maxWidth: .infinity, alignment: .leading)
                .clonePart("Label")
            if let keys { Keycap(keys: keys).clonePart("Shortcut") }
        }
        .padding(.horizontal, Gap.x12)
        .frame(height: Metrics.paletteRowHeight)
        .background(RoundedRectangle(cornerRadius: Corner.control).fill(selected ? Palette.bgSelected : .clear))
        .contentShape(Rectangle())
    }
}
