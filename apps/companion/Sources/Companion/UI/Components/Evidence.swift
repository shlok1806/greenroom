import AppKit
import SwiftUI

// Evidence (Figma Components: Evidence frame 6:86, Evidence mark 6:83, Filmstrip thumb 6:82).
// Every claim sits next to its proof: the frame a check cites, with a mark around the value it
// read (companion ADR 0014). The mark never covers the value.

/// What an evidence frame shows.
enum FrameContent {
    case image(NSImage)
    case loading
    /// The frame could not be read; the recording may still have it.
    case missing

    var image: NSImage? {
        if case .image(let image) = self { return image }
        return nil
    }

    /// A blank picture the size of the real one, for counting the words the app itself shows.
    static func redacted(_ content: FrameContent) -> FrameContent {
        guard case .image(let image) = content else { return content }
        let blank = NSImage(size: image.size)
        blank.lockFocus()
        NSColor.gray.setFill()
        NSRect(origin: .zero, size: image.size).fill()
        blank.unlockFocus()
        return .image(blank)
    }
}

/// Draws every picture of the guest's screen blank: the word count reads only the app's own
/// words (docs/20 counts the run pane without the guest's screen).
struct RedactsGuestScreenKey: EnvironmentKey {
    static let defaultValue = false
}

extension EnvironmentValues {
    var redactsGuestScreen: Bool {
        get { self[RedactsGuestScreenKey.self] }
        set { self[RedactsGuestScreenKey.self] = newValue }
    }
}

/// A picture of the Mac's screen, framed, with at most one mark on it.
struct EvidenceFrame: View {
    var content: FrameContent
    /// Where the value sits, as fractions of the picture.
    var mark: SummaryBox?
    var markColor: ToneColor = .fail
    /// Text the check rests on that a person cannot see on this picture (companion ADR
    /// 0014): each outlined, dashed, with why in words.
    var unseen: [UnseenText] = []
    /// Dims the picture: the screen stopped answering, or the Mac is restarting.
    var dimmed = false
    /// Offered on a missing frame.
    var openRecording: (() -> Void)?
    var aspect: CGFloat = 4 / 3
    @Environment(\.redactsGuestScreen) private var redacted
    @Environment(\.displayScale) private var displayScale
    /// The picture's size on screen, for its filter.
    @State private var drawn = CGSize.zero

    var body: some View {
        ZStack {
            switch redacted ? FrameContent.redacted(content) : content {
            case .image(let image):
                Image(nsImage: image)
                    .resizable()
                    // At a whole multiple of its pixels a filter only blurs (root ADR 0046).
                    .interpolation(PicturePlacement.isWholeMultiple(image.pixelSize, drawn: drawn, scale: displayScale) ? .none : .high)
                    .aspectRatio(contentMode: .fill)
                    .onGeometryChange(for: CGSize.self) { $0.size } action: { drawn = $0 }
                    .overlay {
                        if let mark, !redacted {
                            GeometryReader { geo in
                                // The value's own box; the mark draws outside it.
                                EvidenceMarkView(color: markColor)
                                    .frame(width: max(12, mark.w * geo.size.width), height: max(12, mark.h * geo.size.height))
                                    .clonePart("Evidence mark")
                                    .position(x: (mark.x + mark.w / 2) * geo.size.width, y: (mark.y + mark.h / 2) * geo.size.height)
                            }
                            .accessibilityHidden(true)
                        }
                    }
                    .overlay {
                        if !unseen.isEmpty, !redacted {
                            UnseenMarks(marks: unseen)
                        }
                    }
                    .overlay { if dimmed { Palette.bg.opacity(0.55) } }
                    .clipShape(RoundedRectangle(cornerRadius: Corner.row))
                    .overlay(RoundedRectangle(cornerRadius: Corner.row).strokeBorder(Palette.border, lineWidth: 1))
            case .loading:
                placeholder {
                    VStack(spacing: Gap.x8) {
                        StatusGlyph(kind: .checking, color: .accent)
                        Text("Loading frame").textStyle(.caption).foregroundStyle(Palette.textSecondary)
                    }
                }
            case .missing:
                placeholder {
                    VStack(spacing: Gap.x4) {
                        Text("This frame is missing").textStyle(.body).foregroundStyle(Palette.text)
                        Text("The recording still has it").textStyle(.caption).foregroundStyle(Palette.textSecondary)
                        if let openRecording {
                            Button("Open recording", action: openRecording)
                                .buttonStyle(ActionButtonStyle(kind: .secondary))
                                .padding(.top, Gap.x8)
                        }
                    }
                }
            }
        }
    }

    private func placeholder(@ViewBuilder _ inner: () -> some View) -> some View {
        RoundedRectangle(cornerRadius: Corner.row)
            .fill(Palette.bgSelected)
            .aspectRatio(aspect, contentMode: .fit)
            .overlay(inner())
            .overlay(RoundedRectangle(cornerRadius: Corner.row).strokeBorder(Palette.border, lineWidth: 1))
    }
}

/// Where a UI read reports text a person cannot see: the element's frame dashed in the
/// waiting colour, with what is wrong ("not drawn") above it (the old window's evidence
/// marks, companion ADR 0014). Takes no clicks; the check's row says the same in words.
struct UnseenMarks: View {
    var marks: [UnseenText]

    var body: some View {
        GeometryReader { geo in
            ForEach(Array(marks.enumerated()), id: \.offset) { _, mark in
                if let frame = mark.frame {
                    let box = CGRect(x: frame.minX * geo.size.width, y: frame.minY * geo.size.height,
                                     width: max(12, frame.width * geo.size.width), height: max(12, frame.height * geo.size.height))
                        .insetBy(dx: -Gap.x4, dy: -Gap.x4)
                    RoundedRectangle(cornerRadius: Corner.control)
                        .stroke(Palette.wait, style: StrokeStyle(lineWidth: 2, dash: [5, 3]))
                        .frame(width: box.width, height: box.height)
                        .position(x: box.midX, y: box.midY)
                    Text(mark.why.words)
                        .textStyle(.captionEmphasis)
                        .foregroundStyle(Palette.text)
                        .padding(.horizontal, Gap.x4)
                        .background(RoundedRectangle(cornerRadius: Corner.control).fill(Palette.bgRaised))
                        .overlay(RoundedRectangle(cornerRadius: Corner.control).strokeBorder(Palette.wait, lineWidth: 1))
                        .fixedSize()
                        .position(x: box.minX + 40, y: box.minY >= 18 ? box.minY - 10 : box.maxY + 10)
                }
            }
        }
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }
}

/// The mark around a cited value (Figma: Evidence mark 6:83): a 4 pt band outside the value's
/// box, its outer corners radius 9, so it never covers the value.
struct EvidenceMarkView: View {
    var color: ToneColor = .fail

    var body: some View {
        RoundedRectangle(cornerRadius: 9)
            .strokeBorder(color.token.color, lineWidth: 4)
            .padding(-4)
            .allowsHitTesting(false)
    }
}

/// One key frame of the filmstrip. A red bar marks a frame a failed check cites; the selected
/// frame has a 2 pt border in the text colour.
struct FilmstripThumb: View {
    enum Mark { case none, failed, passed }

    var image: NSImage?
    var selected: Bool
    var mark: Mark = .none
    /// Under the thumb, for key frames that carry one ("$10.00 at 25%").
    var caption: String?
    var width: CGFloat = Metrics.thumbWidth
    @Environment(\.redactsGuestScreen) private var redacted

    var body: some View {
        VStack(alignment: .leading, spacing: Gap.x4) {
            ZStack {
                Palette.bgSelected
                if let image, !redacted {
                    Image(nsImage: image).resizable().interpolation(.medium).aspectRatio(contentMode: .fill)
                }
            }
            .frame(width: width, height: width * 3 / 4)
            .clipShape(RoundedRectangle(cornerRadius: Corner.keycap))
            .overlay {
                // Figma: 1 pt inside at 85% by default; selected, 2 pt outside in the text colour.
                if selected {
                    RoundedRectangle(cornerRadius: Corner.keycap + 2).strokeBorder(Palette.text, lineWidth: 2).padding(-2)
                } else {
                    RoundedRectangle(cornerRadius: Corner.keycap).strokeBorder(Palette.border, lineWidth: 1)
                }
            }
            .opacity(selected ? 1 : 0.85)
            .clonePart("Frame")
            RoundedRectangle(cornerRadius: 1.5)
                .fill(markColor)
                .frame(width: width, height: 3)
                .clonePart("Mark")
            if let caption {
                Text(caption).textStyle(.caption).foregroundStyle(selected ? Palette.text : Palette.textSecondary).lineLimit(1)
                    .frame(width: width, alignment: .leading)
            }
        }
        .contentShape(Rectangle())
    }

    private var markColor: Color {
        switch mark {
        case .none: .clear
        case .failed: Palette.fail
        case .passed: Palette.pass
        }
    }
}
