import AppKit
import SwiftUI

/// How far a list is scrolled, for the visible scroller. Pure, tested in `TimelineTests`.
struct ScrollMetrics: Equatable, Sendable {
    /// The top of the viewport, from the top of the content.
    var offset: CGFloat = 0
    var content: CGFloat = 0
    var viewport: CGFloat = 0

    /// There is more than fits.
    var scrollable: Bool { content > viewport + 1 }

    /// The knob on a track `track` points tall: its top and height. At least 24 tall, so a
    /// 2,000-row list still has something to grab.
    func knob(track: CGFloat) -> (y: CGFloat, height: CGFloat) {
        guard scrollable, track > 0 else { return (0, track) }
        let height = min(track, max(24, track * viewport / content))
        let progress = min(max(offset / (content - viewport), 0), 1)
        return ((track - height) * progress, height)
    }

    /// The offset that puts the knob's top at `y`.
    func offset(forKnobTop y: CGFloat, track: CGFloat) -> CGFloat {
        let height = knob(track: track).height
        guard track > height else { return 0 }
        return min(max(y / (track - height), 0), 1) * (content - viewport)
    }

    /// "25-48 of 2,000": the rows showing, for rows of equal `rowHeight`.
    func rows(rowHeight: CGFloat, total: Int) -> String {
        guard rowHeight > 0, total > 0 else { return "" }
        let first = min(total, Int((offset / rowHeight).rounded(.down)) + 1)
        let last = min(total, Int(((offset + viewport) / rowHeight).rounded(.down)))
        return "\(first)-\(max(first, last)) of \(total.formatted())"
    }
}

/// An always-visible scroll bar (the system's overlay scroller hides until you scroll, so a
/// long list looked like it ended at the window's edge): a thin track the whole height of the
/// list and a knob that says where you are, which you can drag. Hovering or scrolling shows
/// the position in figures beside the knob.
struct VisibleScroller: View {
    var metrics: ScrollMetrics
    /// "25-48 of 2,000", shown beside the knob while it matters.
    var position: String?
    var scrollTo: (CGFloat) -> Void

    @State private var hovering = false
    @State private var dragStart: CGFloat?
    @State private var recentlyMoved = false
    @State private var fade: Task<Void, Never>?

    var body: some View {
        GeometryReader { geo in
            let track = geo.size.height
            let knob = metrics.knob(track: track)
            let wide = hovering || dragStart != nil
            ZStack(alignment: .topTrailing) {
                Capsule().fill(Palette.border.opacity(0.9))
                    .frame(width: wide ? 7 : 4)
                    .frame(maxHeight: .infinity)
                Capsule().fill(wide ? Palette.textSecondary : Palette.textTertiary)
                    .frame(width: wide ? 7 : 4, height: knob.height)
                    .offset(y: knob.y)
                if let position, !position.isEmpty, wide || recentlyMoved {
                    Text(position)
                        .textStyle(.caption)
                        .monospacedDigit()
                        .foregroundStyle(Palette.text)
                        .padding(.horizontal, 6)
                        .frame(height: 20)
                        .background(Capsule().fill(Palette.bgRaised))
                        .overlay(Capsule().strokeBorder(Palette.border, lineWidth: 1))
                        .fixedSize()
                        .offset(x: -14, y: min(max(0, knob.y + knob.height / 2 - 10), max(0, track - 20)))
                        .allowsHitTesting(false)
                        .transition(.opacity)
                }
            }
            .frame(width: 12, alignment: .trailing)
            .frame(maxWidth: .infinity, alignment: .trailing)
            .contentShape(Rectangle().inset(by: -2))
            .onHover { hovering = $0 }
            .gesture(DragGesture(minimumDistance: 0)
                .onChanged { value in
                    if dragStart == nil {
                        // A press off the knob jumps there first, centred.
                        let onKnob = value.startLocation.y >= knob.y && value.startLocation.y <= knob.y + knob.height
                        dragStart = onKnob ? knob.y : value.startLocation.y - knob.height / 2
                    }
                    scrollTo(metrics.offset(forKnobTop: (dragStart ?? 0) + value.translation.height, track: track))
                }
                .onEnded { _ in dragStart = nil })
        }
        .frame(width: 12)
        .padding(.vertical, 4)
        .padding(.trailing, 2)
        .opacity(metrics.scrollable ? 1 : 0)
        .allowsHitTesting(metrics.scrollable)
        .animation(Motion.easeOut(Motion.press), value: hovering)
        .onChange(of: metrics.offset) { _, _ in
            recentlyMoved = true
            fade?.cancel()
            fade = Task {
                try? await Task.sleep(for: .seconds(1.2))
                if !Task.isCancelled { recentlyMoved = false }
            }
        }
        .accessibilityHidden(true)
    }
}

/// A SwiftUI `ScrollView` with the always-visible scroller at its trailing edge.
struct VisibleScrollerModifier: ViewModifier {
    var position: ((ScrollMetrics) -> String?)?
    @State private var metrics = ScrollMetrics()
    @State private var scroll = ScrollPosition()

    func body(content: Content) -> some View {
        content
            .scrollIndicators(.never)
            .scrollPosition($scroll)
            .onScrollGeometryChange(for: ScrollMetrics.self) { geo in
                ScrollMetrics(offset: geo.contentOffset.y + geo.contentInsets.top, content: geo.contentSize.height,
                              viewport: geo.containerSize.height)
            } action: { _, new in
                metrics = new
            }
            .overlay(alignment: .trailing) {
                VisibleScroller(metrics: metrics, position: position?(metrics)) { offset in
                    scroll.scrollTo(y: offset)
                }
            }
    }
}

extension View {
    /// Draws an always-visible scroller over this `ScrollView`, with the position in figures.
    func visibleScroller(position: ((ScrollMetrics) -> String?)? = nil) -> some View {
        modifier(VisibleScrollerModifier(position: position))
    }
}

/// How far an AppKit list is scrolled, observed only by its scroller, so scrolling redraws
/// the knob and not the list's owner.
@Observable
@MainActor
final class ScrollTracker {
    var metrics = ScrollMetrics()
    var position = ""
    @ObservationIgnored let driver = ScrollDriver()
}

/// The visible scroller over an AppKit list, fed by its `ScrollTracker`.
struct TrackedScroller: View {
    let tracker: ScrollTracker

    var body: some View {
        VisibleScroller(metrics: tracker.metrics, position: tracker.position) { tracker.driver.scroll(to: $0) }
    }
}

/// Lets SwiftUI scroll an AppKit scroll view (the runs table) to an offset.
@MainActor
final class ScrollDriver {
    weak var scrollView: NSScrollView?

    func scroll(to offset: CGFloat) {
        guard let scrollView else { return }
        let clip = scrollView.contentView
        let y = max(0, min(offset, (scrollView.documentView?.frame.height ?? 0) - clip.bounds.height))
        clip.scroll(to: NSPoint(x: clip.bounds.origin.x, y: y))
        scrollView.reflectScrolledClipView(clip)
    }
}
