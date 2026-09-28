// What a ref remembers about its element besides the element itself (daemon ADR 0006 point 3),
// and the decision that finds the element again once its AXUIElement is dead. Pure: the guest
// side (Refs.swift) reads the candidates, this file only compares them.

import Foundation

/// An element's identity in words. A dead ref is matched against the fingerprints of the
/// elements now in its window, so every field is something a fresh read of an element gives.
struct Fingerprint: Equatable {
    /// The process, as pid plus start time: a recycled pid has another start.
    var pid: Int32
    var started: String
    /// The window server's number of the element's window, 0 when it has none or it is unknown.
    var window: Int
    /// Where the element sits under its window: `AXGroup[0]/AXButton[2]`, each step the role and
    /// the index among its parent's children. Empty for the window itself.
    var path: String
    /// `AXIdentifier`, empty when the element has none or only AppKit's generated one.
    var identifier: String
    var role: String
    var name: String
}

/// A role path one step deeper.
func pathAppending(_ path: String, role: String, index: Int) -> String {
    let step = "\(role)[\(index)]"
    return path.isEmpty ? step : path + "/" + step
}

/// The identifier a fingerprint and a node carry: AppKit names every view it archives `_NS:12`,
/// which says nothing about the element and changes between builds, so that one is dropped.
func stableIdentifier(_ identifier: String?) -> String {
    guard let identifier, !identifier.isEmpty, !identifier.hasPrefix("_NS:") else { return "" }
    return identifier
}

enum Rematch<Candidate> {
    case unique(Candidate)
    case none
    /// More than one element fits, so none of them is taken: a ref never guesses.
    case ambiguous(Int)
}

/// Finds the one candidate that is the element `wanted` described, among the elements of its
/// window (or of its app, for an element with no window). With an identifier the identifier and
/// role decide, narrowed by path and name only when several elements share them (rows of one
/// list); without one the role path, role and name must all be the same.
func rematch<Candidate>(_ wanted: Fingerprint, among candidates: [(candidate: Candidate, fingerprint: Fingerprint)]) -> Rematch<Candidate> {
    let sameRole = candidates.filter { $0.fingerprint.role == wanted.role }
    var fits: [(candidate: Candidate, fingerprint: Fingerprint)]
    if !wanted.identifier.isEmpty {
        fits = sameRole.filter { $0.fingerprint.identifier == wanted.identifier }
        if fits.count > 1 {
            let exact = fits.filter { $0.fingerprint.path == wanted.path && $0.fingerprint.name == wanted.name }
            if !exact.isEmpty { fits = exact }
        }
    } else {
        fits = sameRole.filter {
            $0.fingerprint.identifier.isEmpty && $0.fingerprint.path == wanted.path && $0.fingerprint.name == wanted.name
        }
    }
    switch fits.count {
    case 0: return .none
    case 1: return .unique(fits[0].candidate)
    default: return .ambiguous(fits.count)
    }
}
