import SwiftUI

/// A part of the window that stays lit while the house lights are down: the screen's
/// well, the driving bar above it, Give Back and the hint bar. Reported as anchors, so
/// the scrim cuts its holes wherever the layout puts them.
struct HouseLightsHole {
    let anchor: Anchor<CGRect>
    let radius: CGFloat
}

struct HouseLightsHolesKey: PreferenceKey {
    static var defaultValue: [HouseLightsHole] { [] }

    static func reduce(value: inout [HouseLightsHole], nextValue: () -> [HouseLightsHole]) {
        value.append(contentsOf: nextValue())
    }
}

extension View {
    /// Keeps this view lit while the rest of the window dims for driving.
    func houseLightsLit(radius: CGFloat = 0) -> some View {
        anchorPreference(key: HouseLightsHolesKey.self, value: .bounds) { [HouseLightsHole(anchor: $0, radius: radius)] }
    }
}

/// The dark over everything but the lit holes while the person drives (ADR 0006, Take
/// control). It takes no clicks, so the dimmed window still works, Give Back above all,
/// and VoiceOver never sees it. It settles in and out on the settle spring, at once
/// under Reduce Motion.
struct HouseLightsScrim: View {
    let keyboard: KeyboardModel
    let holes: [(rect: CGRect, radius: CGFloat)]

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        let down = HouseLights.down(keyboard.state())
        let dim = HouseLights.dim(down: down, tokens: DesignData.shared.tokens.motion.houseLights)
        GeometryReader { proxy in
            Path { path in
                path.addRect(CGRect(origin: .zero, size: proxy.size))
                for hole in holes where hole.rect.width > 0 && hole.rect.height > 0 {
                    path.addRoundedRect(in: hole.rect, cornerSize: CGSize(width: hole.radius, height: hole.radius), style: .continuous)
                }
            }
            .fill(Color.black.opacity(dim), style: FillStyle(eoFill: true))
        }
        .animation(PaneMotion.settle(reduceMotion: reduceMotion), value: down)
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }
}
