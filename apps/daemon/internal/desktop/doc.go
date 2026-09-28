// Package desktop is the meaning layer of the desktop toolkit (daemon ADR 0006 point 1).
//
// The guest agent does the mechanics: it walks AX, keeps the refs, hit-tests, runs the
// actionability checks, posts input and returns structured trees. This package turns those
// results into what a model reads: the snapshot outline, the diff between two trees, the effect
// of an action, the refusal text, and the find, scroll, wait and expect lines. It also parses and
// validates the tool arguments that MCP and the verifier share, so both surfaces say the same
// thing about the same mistake.
//
// It does no I/O and imports nothing of the daemon's, so its wording can change without a helper
// version bump and every sentence is unit-tested here. internal/machine owns the calls;
// internal/mcpserver and internal/verifier only map arguments and results through this package.
//
// Where things are:
//
//   - types.go: the wire types of the ops, decoded leniently (unknown fields ignored, absent ones
//     zero).
//   - text.go: the small renderers every line is built from (Label, the flags, durations).
//   - outline.go: Outline, a snapshot as the model reads it.
//   - diff.go: Diff and Change, two trees compared by ref.
//   - effect.go: EffectOf, ActionText and its lead lines, RefusalText.
//   - findtext.go, waittext.go: FindText, ScrollText, WaitText, ExpectText.
//   - args.go: the argument types with Normalize, and DecodeArgs.
//
// Two rules hold for every text here, and a new renderer must keep them:
//
// Guest text is data (ADR 0006 point 13). Every string that came from the guest is rendered
// with quote (%q), and every token a model reads bare (a role, a state, a ref) with word, which
// quotes anything that is not a plain identifier. So a label holding a newline or a fake
// "effect:" line cannot pose as a line of ours.
//
// A secure text field's value is never shown (catalog I15): not in an outline, a diff, a lead
// line, a wait or an expectation, and not what was typed into it. Texts say SecretText's
// `<secret, N chars>` instead, the decoders drop a value an agent should not have sent, and
// Action.Redacted gives the form a step may record.
package desktop
