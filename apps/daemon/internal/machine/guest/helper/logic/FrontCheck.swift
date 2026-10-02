// Whether an app is in front, from what NSWorkspace says (`focus` in Focus.swift and the
// toolkit's `bringToFront`). Pure, so the host tests pin it.

/// In the long-lived agent `NSWorkspace.frontmostApplication` can be stale: measured on
/// greenroom-base-v10-r4, it still named an accessory app after Finder was activated over it,
/// while that app's own `isActive` said false, and a click by element then hit Finder's window.
/// So the frontmost pid counts only with `isActive` not saying otherwise, or once it changed
/// to the app since the check began (`frontAtStart`); `isActive` true always counts.
func isInFront(pid: Int32, isActive: Bool?, frontNow: Int32?, frontAtStart: Int32?) -> Bool {
    if isActive == true { return true }
    guard frontNow == pid else { return false }
    return isActive == nil || frontAtStart != pid
}
