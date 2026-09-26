package verifier

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// Check kinds (ADR 0027). A declared check is value, visual or timing, and the kind says what
// evidence it needs. The daemon reads each criterion's words and upgrades the kind the model
// declared when they claim appearance or speed; it never downgrades one. The applied kind is
// what the declaration posts and what the verdict review checks.

// visualWords claim appearance on screen: a check that says them needs a screenshot.
var visualWords = []string{
	"visible", "invisible", "visibly", "shown", "displayed", "readable", "unreadable", "legible",
	"see", "seen", "hidden", "colour", "color", "coloured", "colored", "bold", "large",
}

// timingWords claim speed: a check that says them is timed from its last action. "within N
// s" is read by withinRE, which also gives the window.
var timingWords = []string{
	"at once", "immediately", "instantly", "instant", "right away", "straight away", "straightaway",
	"the moment", "as you type", "without delay",
}

var (
	visualRE = wordsRE(visualWords)
	timingRE = wordsRE(timingWords)
	// withinRE reads "within 3 s", "within 1.5 seconds", "within a second".
	withinRE = regexp.MustCompile(`(?i)\bwithin\s+(\d+(?:\.\d+)?|an?|one)\s*(?:s|secs?|seconds?)\b`)
)

// wordsRE matches any of words (phrases may span any whitespace) as whole words, ignoring case.
// A phrase that starts or ends with a symbol ("$49.56") is bounded there by anything.
func wordsRE(words []string) *regexp.Regexp {
	alts := make([]string, len(words))
	for i, w := range words {
		alt := strings.Join(strings.Fields(regexp.QuoteMeta(w)), `\s+`)
		if isWordByte(w[0]) {
			alt = `\b` + alt
		}
		if isWordByte(w[len(w)-1]) {
			alt += `\b`
		}
		alts[i] = alt
	}
	return regexp.MustCompile(`(?i)(?:` + strings.Join(alts, "|") + `)`)
}

func isWordByte(b byte) bool {
	return b == '_' || '0' <= b && b <= '9' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z'
}

// wordClaim is one kind a criterion's words claim, and the words that claimed it.
type wordClaim struct{ kind, words string }

// wordsKinds is every kind a criterion's words claim (visual before timing), and for a timing
// claim with a number, its window in seconds (0 when none is given).
func wordsKinds(criterion string) (claims []wordClaim, within float64) {
	if w := visualRE.FindString(criterion); w != "" {
		claims = append(claims, wordClaim{session.CheckVisual, w})
	}
	if m := withinRE.FindStringSubmatch(criterion); m != nil {
		n, err := strconv.ParseFloat(m[1], 64)
		if err != nil { // "a", "an" or "one" second
			n = 1
		}
		return append(claims, wordClaim{session.CheckTiming, m[0]}), n
	}
	if w := timingRE.FindString(criterion); w != "" {
		claims = append(claims, wordClaim{session.CheckTiming, w})
	}
	return claims, 0
}

// applyKinds is the kinds and window a check gets from its declared kinds and within and its
// criterion's words: every kind either claims, so a kind is added and never taken away, with a
// note for the model when the daemon changed what was declared. declared holds no value kind;
// within is 0 when none was given, and a within alone declares timing.
func applyKinds(id, criterion string, declared []string, within float64) (kinds []string, window float64, note string) {
	has := map[string]bool{}
	for _, k := range declared {
		has[k] = true
	}
	if within > 0 {
		has[session.CheckTiming] = true
	}
	var notes []string
	claims, wwithin := wordsKinds(criterion)
	for _, c := range claims {
		if !has[c.kind] {
			has[c.kind] = true
			notes = append(notes, fmt.Sprintf("%q is %s: its criterion says %q", id, c.kind, strings.ToLower(c.words)))
		}
	}
	for _, k := range []string{session.CheckVisual, session.CheckTiming} {
		if has[k] {
			kinds = append(kinds, k)
		}
	}
	if len(kinds) == 0 {
		return []string{session.CheckValue}, 0, ""
	}
	if !has[session.CheckTiming] {
		return kinds, 0, strings.Join(notes, "; ")
	}
	window = within
	if wwithin > 0 && (window == 0 || wwithin < window) {
		window = wwithin
	}
	if window == 0 {
		window = session.MinWithin
	}
	if window < session.MinWithin {
		notes = append(notes, fmt.Sprintf("%q is timed within %g s, not %g s: the UI read after an input comes 1 to 2 s "+
			"after it, so a shorter window cannot be met", id, session.MinWithin, window))
		window = session.MinWithin
	}
	return kinds, min(window, session.MaxWithin), strings.Join(notes, "; ")
}

// kindLabel is how the declaration result names a check's kinds and what each needs.
func kindLabel(c session.Check) string {
	var needs []string
	if c.Is(session.CheckVisual) {
		needs = append(needs, "visual: cite a machine_screenshot taken after its actions")
	}
	if c.Is(session.CheckTiming) {
		needs = append(needs, fmt.Sprintf("timing within %g s: cite its action and an observation that started within %g s "+
			"after it ended, such as the UI read right after the input; a later one cannot pass it", c.Within, c.Within))
	}
	if c.Is(session.CheckVisual) && c.Is(session.CheckTiming) {
		needs = append(needs, "a later screenshot may show how it looks, not when")
	}
	if len(needs) == 0 {
		return c.ID + " (value)"
	}
	return c.ID + " (" + strings.Join(needs, "; ") + ")"
}
