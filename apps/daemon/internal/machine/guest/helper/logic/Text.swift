// Pure logic shared by the agent's ops. Everything under logic/ uses Foundation
// and CoreGraphics only, so it also compiles on the host, where
// guest/helper/tests runs it (daemon ADR 0005).

import Foundation

/// The longest string a snapshot sends before it is cut and marked (daemon ADR 0006).
let textLimit = 240

/// cut returns s whole, or its first `limit` characters and true when it is longer.
/// The caller marks the cut (the element's `cut` and `chars`), so a cut string can
/// never pass for the app's own ellipsis (#190).
func cut(_ s: String, limit: Int = textLimit) -> (text: String, cut: Bool) {
    if s.count <= limit { return (s, false) }
    return (String(s.prefix(limit)), true)
}
