// Package desktop is the meaning layer of the desktop toolkit (daemon ADR 0006 point 1).
//
// The guest agent does the mechanics: it walks AX, keeps the refs, hit-tests, runs the
// actionability checks, posts input and returns structured trees. This package turns those
// results into what a model reads: the snapshot outline, the diff between two trees, the effect
// of an action, the refusal text, and the scroll, wait and expect lines. It also parses and
// validates the tool arguments that MCP and the verifier share, so both surfaces say the same
// thing about the same mistake.
//
// It does no I/O and imports nothing of the daemon's, so its wording can change without a helper
// version bump and every sentence is unit-tested here. internal/machine owns the calls;
// internal/mcpserver and internal/verifier only map arguments and results through this package.
//
// Guest text is data (ADR 0006 point 13): every string that came from the guest is rendered with
// %q, so a label holding a newline or a fake "effect:" line cannot pose as a line of ours.
package desktop
