import Foundation

/// "House lights" (ADR 0006, Take control): while the person drives, everything but the
/// screen's well dims, so the machine is the one lit thing in the window. Pure; the scrim
/// is `Views/HouseLightsScrim.swift`. `HouseLightsTests`.
enum HouseLights {
    /// Down exactly while the hint bar says every key goes to the machine: the lease is
    /// held and the screen has the keyboard. Clicking the conversation while driving
    /// brings them back up (the keys follow the click there); giving back, a zoom that
    /// covers the screen, or losing the lease does too.
    static func down(_ state: ActionState) -> Bool {
        state.drivingFocused
    }

    /// How dark the rest of the window goes, 0 to 1 (`tokens.json` `motion.houseLights`).
    static func dim(down: Bool, tokens: DesignTokens.Motion.HouseLights) -> Double {
        down ? min(max(tokens.dim, 0), 1) : 0
    }
}
