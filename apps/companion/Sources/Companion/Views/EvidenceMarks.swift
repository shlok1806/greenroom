import SwiftUI

/// Over a verdict's evidence, where a UI read reports text a person cannot see (companion
/// ADR 0014, root ADR 0027): the element's frame, dashed, with what is wrong in words
/// ("not drawn"). The claim the check makes is in the bar above the picture; this shows the
/// place on the picture it rests on. Takes no clicks and is hidden from VoiceOver: the
/// warning under the check and in the bar says the same in words.
struct EvidenceMarkLayer: View {
    let picture: CGSize
    let marks: [UnseenText]

    @Environment(\.theme) private var theme

    var body: some View {
        GeometryReader { geometry in
            let fitted = ScreenGeometry.fitted(image: picture, in: geometry.size)
            ZStack(alignment: .topLeading) {
                ForEach(Array(marks.enumerated()), id: \.offset) { _, mark in
                    if let frame = mark.frame,
                       let rect = ScreenGeometry.rect(atFraction: frame, image: picture, view: geometry.size) {
                        let box = rect.insetBy(dx: -Space.xs, dy: -Space.xs)
                        // A dark rim under the ink, so the mark reads on light and dark pictures.
                        RoundedRectangle(cornerRadius: Radius.sm, style: .continuous)
                            .stroke(Color.black.opacity(0.45), lineWidth: 4)
                            .frame(width: box.width, height: box.height)
                            .offset(x: box.minX, y: box.minY)
                        RoundedRectangle(cornerRadius: Radius.sm, style: .continuous)
                            .stroke(theme.color(.attention), style: StrokeStyle(lineWidth: 2, dash: [5, 3]))
                            .frame(width: box.width, height: box.height)
                            .offset(x: box.minX, y: box.minY)
                        label(mark)
                            .offset(x: box.minX, y: labelY(box: box, fitted: fitted))
                    }
                }
            }
            .frame(width: geometry.size.width, height: geometry.size.height, alignment: .topLeading)
            .clipShape(Rectangle().path(in: fitted))
        }
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }

    /// Above the box, or under it when the box is at the picture's top edge.
    private func labelY(box: CGRect, fitted: CGRect) -> CGFloat {
        let height: CGFloat = 20
        return box.minY - height - 2 >= fitted.minY ? box.minY - height - 2 : box.maxY + 2
    }

    private func label(_ mark: UnseenText) -> some View {
        Text(mark.why.words)
            .monoStyle(.monoBold, size: TypeScale.monoSmall)
            // The ground as ink, as on the driving bar: it clears text contrast on the
            // attention role in every theme.
            .foregroundStyle(theme.background)
            .padding(.horizontal, Space.xs)
            .frame(height: 20)
            .background(theme.color(.attention), in: RoundedRectangle(cornerRadius: Radius.sm, style: .continuous))
            .fixedSize()
    }
}
