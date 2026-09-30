package verifier

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

var (
	screenshotQuotesRE   = regexp.MustCompile("\"([^\"\\n]+)\"|“([^”\\n]+)”|‘([^’\\n]+)’|`([^`\\n]+)`")
	screenshotClausesRE  = regexp.MustCompile(`(?i)[.!?;]\s+|\n+|,\s+|\s+(?:and|but|then)\s+`)
	screenshotNumbersRE  = regexp.MustCompile(`[-+]?(?:[$€£]\s*)?[-+]?\d+(?:[.,]\d+)*`)
	screenshotNegationRE = wordsRE([]string{"no", "not", "never", "without", "missing", "absent", "hidden", "blank", "covered", "obscured"})
)

// screenshotClaimText extracts explicit text assertions from the observation. It excludes
// negated clauses and never guesses appearance from prose. Decimal/separated values are
// distinctive; step IDs are not. Matching a term does not prove visibility or location.
func screenshotClaimText(observed string) []string {
	var terms []string
	for _, clause := range screenshotClauses(observed) {
		outside := screenshotQuotesRE.ReplaceAllString(clause, "")
		if screenshotNegationRE.MatchString(strings.ToLower(outside)) || strings.Contains(strings.ToLower(outside), "n't") {
			continue
		}
		for _, match := range screenshotQuotesRE.FindAllStringSubmatch(clause, -1) {
			for _, quote := range match[1:] {
				if quote = normalizeScreenshotText(quote); quote != "" {
					terms = append(terms, quote)
				}
			}
		}
		for _, number := range screenshotNumbersRE.FindAllString(outside, -1) {
			if strings.ContainsAny(number, ".,") {
				terms = append(terms, number)
			}
		}
	}
	return slices.Compact(slices.Sorted(slices.Values(terms)))
}

func normalizeScreenshotText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// screenshotSupportRule is a negative guard: missing named text cannot support a visual pass.
// Matching it does not waive rendered metadata or certify what is drawn (ADR 0041).
func screenshotSupportRule(c session.Check, fresh []int, steps map[int]stepFact, _ int, handover int) []string {
	var out []string
	var described []int
	terms := screenshotClaimText(c.Criterion + "\n" + c.Observed)
	for _, n := range fresh {
		f := steps[n]
		if f.tool != "machine_screenshot" || n <= handover {
			continue
		}
		switch d := f.description; {
		case d == nil:
			out = append(out, fmt.Sprintf("screenshot support): evidence step %d has no recorded screenshot description; capture the claimed state and cite its described step, or answer unchecked", n))
		case d.Error != "" || strings.TrimSpace(d.Text) == "":
			out = append(out, fmt.Sprintf("screenshot support): evidence step %d could not be described; its image alone cannot support this visual pass. Capture the claimed state again, or answer unchecked", n))
		default:
			described = append(described, n)
		}
	}
	// Each textual claim needs support somewhere in the cited captures: a check can compare
	// several transient states recorded by different screenshots.
	for _, term := range terms {
		if len(described) == 0 {
			break
		}
		if !slices.ContainsFunc(described, func(n int) bool {
			return screenshotAffirmsText(normalizeScreenshotText(steps[n].description.Text), term)
		}) {
			out = append(out, fmt.Sprintf("screenshot support): cited screenshot steps %v do not affirm the claimed text %q (absent or in a negated clause); cite the capture of that state, or answer unchecked. Matching text alone does not prove visibility or location", described, term))
		}
	}
	return out
}

// screenshotClauses splits conjunctions and sentence boundaries outside quoted text. This
// keeps "Save and Close" intact while checking "No error, and the label reads Run B" locally.
func screenshotClauses(text string) []string {
	var out []string
	quotes := screenshotQuotesRE.FindAllStringIndex(text, -1)
	start := 0
	for _, boundary := range screenshotClausesRE.FindAllStringIndex(text, -1) {
		if slices.ContainsFunc(quotes, func(q []int) bool { return boundary[0] >= q[0] && boundary[0] < q[1] }) {
			continue
		}
		out = append(out, text[start:boundary[0]])
		start = boundary[1]
	}
	return append(out, text[start:])
}

func screenshotContainsText(clause, term string) bool {
	// Compare entire numeric tokens, retaining signs and any currency specified by the claim.
	// Word boundaries alone admit 49.56.0 and -$49.56 as support for $49.56.
	if token := screenshotNumbersRE.FindString(term); token == term {
		for _, got := range screenshotNumbersRE.FindAllString(clause, -1) {
			if !strings.ContainsAny(term, "$€£") {
				got = strings.Map(func(r rune) rune {
					if strings.ContainsRune("$€£", r) {
						return -1
					}
					return r
				}, got)
			}
			if normalizeScreenshotText(got) == term {
				return true
			}
		}
		return false
	}
	return wordsRE([]string{term}).MatchString(clause)
}

// screenshotAffirmsText requires an occurrence outside a negated description clause. This is
// intentionally conservative: "not visible" cannot support a pass by naming expected text.
func screenshotAffirmsText(description, term string) bool {
	found := false
	for _, clause := range screenshotClauses(description) {
		if !screenshotContainsText(clause, term) {
			continue
		}
		outside := screenshotQuotesRE.ReplaceAllString(clause, "")
		if screenshotNegationRE.MatchString(outside) || strings.Contains(outside, "n't") {
			return false
		}
		found = true
	}
	return found
}
