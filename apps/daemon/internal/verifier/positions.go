package verifier

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// positionPair is a parenthesised or bracketed pair of numbers, as a description gives a
// control's center: "(0.60, 0.47)", "[180, 872]".
var positionPair = regexp.MustCompile(`[(\[]\s*(-?\d+(?:\.\d+)?)\s*,\s*(-?\d+(?:\.\d+)?)\s*[)\]]`)

// unknownPosition replaces a position that is not a fraction of the image.
const unknownPosition = "(position unknown)"

// checkPositions keeps every position in a screenshot description that is a pair of fractions
// of the image, 0 to 1, and replaces any other with unknownPosition (daemon ADR 0010, issue
// #248): describers also answer in points or on a 0 to 1000 grid, and which one a pair above 1
// is in cannot be told from the pair, so none is rescaled. A pair inside quotes on its line is
// window text the describer copied and stays. It returns the description, ending with a line
// that says how many positions went when any did, and that count.
func checkPositions(desc string) (string, int) {
	removed := 0
	lines := strings.Split(desc, "\n")
	for i, line := range lines {
		matches := positionPair.FindAllStringSubmatchIndex(line, -1)
		if matches == nil {
			continue
		}
		var b strings.Builder
		last := 0
		for _, m := range matches {
			b.WriteString(line[last:m[0]])
			last = m[1]
			if quoted(line[:m[0]]) || fraction(line[m[2]:m[3]]) && fraction(line[m[4]:m[5]]) {
				b.WriteString(line[m[0]:m[1]])
				continue
			}
			b.WriteString(unknownPosition)
			removed++
		}
		b.WriteString(line[last:])
		lines[i] = b.String()
	}
	out := strings.Join(lines, "\n")
	switch {
	case removed == 1:
		out += "\n(1 position was not a fraction of the image, 0 to 1, and was removed: take positions from machine_ui.)"
	case removed > 1:
		out += fmt.Sprintf("\n(%d positions were not fractions of the image, 0 to 1, and were removed: take positions "+
			"from machine_ui.)", removed)
	}
	return out, removed
}

// fraction reports whether s is a number from 0 to 1.
func fraction(s string) bool {
	v, err := strconv.ParseFloat(s, 64)
	return err == nil && v >= 0 && v <= 1
}

// quoted reports whether the text before a point on a line leaves a quote open: straight double
// quotes counted in pairs, and curly ones by which came last.
func quoted(before string) bool {
	if strings.Count(before, `"`)%2 == 1 {
		return true
	}
	return strings.LastIndex(before, "“") > strings.LastIndex(before, "”")
}
