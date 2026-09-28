import AppKit
import SwiftUI

// Parts, for proving the clone (docs/22 section 6.1). A view marks a piece of itself with
// `.clonePart("Name")` or opens a scope with `.cloneScope("Run row[2]")`; the harness and the
// tests read every part's frame in the window and compare it with the Figma layer of the same
// name path ("Sidebar/Runs/Run row[2]/Name"). Off unless asked for: nothing is added to the
// view tree in the running app.

enum CloneParts {
    /// Whether parts report their frames: set by the harness and the tests before any view
    /// is built (`GREENROOM_CLONE` in the environment does it for the harness).
    nonisolated(unsafe) static var enabled = ProcessInfo.processInfo.environment["GREENROOM_CLONE"] != nil

    /// Every part in `window`, by its path, in the content view's coordinates (origin top
    /// left, points). A path met twice keeps the first.
    @MainActor
    static func frames(in window: NSWindow) -> [String: CGRect] {
        guard let content = window.contentView else { return [:] }
        var out: [String: CGRect] = [:]
        func walk(_ view: NSView) {
            if let probe = view as? ClonePartProbe.ProbeView, !probe.path.isEmpty, !probe.isHiddenOrHasHiddenAncestor {
                var rect = content.convert(probe.bounds, from: probe)
                if !content.isFlipped { rect.origin.y = content.bounds.height - rect.maxY }
                if out[probe.path] == nil { out[probe.path] = rect }
            }
            view.subviews.forEach(walk)
        }
        walk(content)
        return out
    }
}

struct ClonePathKey: EnvironmentKey {
    static let defaultValue = ""
}

extension EnvironmentValues {
    /// The path of the scope a view sits in: "Sidebar/Runs".
    var clonePath: String {
        get { self[ClonePathKey.self] }
        set { self[ClonePathKey.self] = newValue }
    }
}

/// An invisible view the size of the part, that knows the part's path.
struct ClonePartProbe: NSViewRepresentable {
    var name: String

    final class ProbeView: NSView {
        var path = ""
        override func hitTest(_ point: NSPoint) -> NSView? { nil }
    }

    func makeNSView(context: Context) -> ProbeView { ProbeView() }

    func updateNSView(_ view: ProbeView, context: Context) {
        let scope = context.environment.clonePath
        view.path = scope.isEmpty ? name : scope + "/" + name
    }
}

private struct CloneScope: ViewModifier {
    var name: String
    @Environment(\.clonePath) private var path

    func body(content: Content) -> some View {
        content
            .environment(\.clonePath, path.isEmpty ? name : path + "/" + name)
            .background(ClonePartProbe(name: name).accessibilityHidden(true))
    }
}

extension View {
    /// Marks this view as the part `name` of the scope it sits in.
    @ViewBuilder
    func clonePart(_ name: String) -> some View {
        if CloneParts.enabled {
            background(ClonePartProbe(name: name).accessibilityHidden(true))
        } else {
            self
        }
    }

    /// Marks this view as the part `name` and makes it the scope of the parts inside it.
    @ViewBuilder
    func cloneScope(_ name: String) -> some View {
        if CloneParts.enabled {
            modifier(CloneScope(name: name))
        } else {
            self
        }
    }
}
