import SwiftUI

// The three containers that place the window's panes where `PaneLayout` says. Custom
// layouts rather than stacks, so every pane keeps one place in the view tree whatever
// the width class or zoom: a change moves and hides panes, never rebuilds them. That is
// what keeps the live screen, the control lease and a half-typed draft through a resize
// or a zoom, and what lets the settle spring animate the frames.

/// The runs, the line after them, and the run (or the empty state).
struct WindowPanesLayout: Layout {
    let layout: PaneLayout
    let runsWidth: Double

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        proposal.replacingUnspecifiedDimensions()
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let frames = layout.window(in: bounds.size, runsWidth: runsWidth)
        place(subviews, at: [frames.runs, frames.divider, frames.detail], in: bounds)
    }
}

/// The run's stage column, the line before the conversation, and the conversation.
struct RunPanesLayout: Layout {
    let layout: PaneLayout
    let conversationWidth: Double

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        proposal.replacingUnspecifiedDimensions()
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let frames = layout.run(in: bounds.size, conversationWidth: conversationWidth)
        place(subviews, at: [frames.stage, frames.divider, frames.conversation], in: bounds)
    }
}

/// The screen, then the steps under it. The screen is asked how tall it is at this width
/// (its picture fitted, its player), so it never takes more than it draws.
struct StageBodyLayout: Layout {
    let layout: PaneLayout

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        proposal.replacingUnspecifiedDimensions()
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        guard let screen = subviews.first else { return }
        let natural = screen.sizeThatFits(ProposedViewSize(width: bounds.width, height: nil)).height
        let frames = layout.stage(in: bounds.size, screenNatural: natural)
        place(subviews, at: [frames.screen, frames.steps], in: bounds)
    }
}

private func place(_ subviews: LayoutSubviews, at frames: [CGRect], in bounds: CGRect) {
    for (subview, frame) in zip(subviews, frames) {
        subview.place(
            at: CGPoint(x: bounds.minX + frame.minX, y: bounds.minY + frame.minY),
            anchor: .topLeading,
            proposal: ProposedViewSize(width: frame.width, height: frame.height)
        )
    }
}

extension View {
    /// A pane the layout is not showing stays in the tree, under what shows: it draws
    /// nothing, takes no clicks and is not read out.
    func paneShown(_ shown: Bool) -> some View {
        opacity(shown ? 1 : 0)
            .allowsHitTesting(shown)
            .accessibilityHidden(!shown)
            .zIndex(shown ? 1 : 0)
    }
}
