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
}

/// A picture of the Mac's screen, framed, with at most one mark on it.
struct EvidenceFrame: View {
    var content: FrameContent
    /// Where the value sits, as fractions of the picture.
    var mark: SummaryBox?
    var markColor: ToneColor = .fail
    /// Dims the picture: the screen stopped answering, or the Mac is restarting.
    var dimmed = false
    /// Offered on a missing frame.
    var openRecording: (() -> Void)?
    var aspect: CGFloat = 4 / 3

    var body: some View {
        ZStack {
            switch content {
            case .image(let image):
                Image(nsImage: image)
                    .resizable()
                    .interpolation(.high)
                    .aspectRatio(contentMode: .fit)
                    .overlay {
                        if let mark {
                            GeometryReader { geo in
                                EvidenceMarkView(color: markColor)
                                    .frame(width: max(12, mark.w * geo.size.width) + 8, height: max(12, mark.h * geo.size.height) + 8)
                                    .position(x: (mark.x + mark.w / 2) * geo.size.width, y: (mark.y + mark.h / 2) * geo.size.height)
                            }
                            .accessibilityHidden(true)
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

/// The ring around a cited value: a 2 pt ring and a soft 4 pt halo outside it.
struct EvidenceMarkView: View {
    var color: ToneColor = .fail

    var body: some View {
        RoundedRectangle(cornerRadius: Corner.control)
            .strokeBorder(color.token.color, lineWidth: 2)
            .background {
                RoundedRectangle(cornerRadius: Corner.control + 3)
                    .strokeBorder(color.token.color.opacity(0.25), lineWidth: 4)
                    .padding(-4)
            }
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

    var body: some View {
        VStack(alignment: .leading, spacing: Gap.x4) {
            ZStack {
                Palette.bgSelected
                if let image {
                    Image(nsImage: image).resizable().interpolation(.medium).aspectRatio(contentMode: .fill)
                }
            }
            .frame(width: width, height: width * 3 / 4)
            .clipShape(RoundedRectangle(cornerRadius: Corner.keycap))
            .overlay(RoundedRectangle(cornerRadius: Corner.keycap)
                .strokeBorder(selected ? Palette.text : Palette.border, lineWidth: selected ? 2 : 1))
            .opacity(selected ? 1 : 0.85)
            RoundedRectangle(cornerRadius: 1.5)
                .fill(markColor)
                .frame(width: width, height: 3)
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
