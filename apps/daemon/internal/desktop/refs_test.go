package desktop

import "testing"

func TestHighestRefReadsOnlyTheFieldsThatHoldRefs(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want int
	}{
		{"nothing", ``, 0},
		{"not JSON", `{"ref":`, 0},
		{"no refs", `{"satisfied":true,"elapsedMs":40}`, 0},
		{"a snapshot", `{"focused":"e4","windows":["e1","e61"],"nodes":[{"ref":"e39","role":"Button","name":"Inspect","window":"e1","scroller":"e20",
			"covered":{"by":"e70","role":"List"},"clipped":"window"}],"attention":[{"ref":"e80","kind":"sheet"}]}`, 80},
		{"an action's trees", `{"target":{"ref":"e41"},"before":{"nodes":[{"ref":"e41"}]},"after":{"nodes":[{"ref":"e143"}]},"reResolved":["e41"]}`, 143},
		{"an error's candidates", `{"candidates":[{"ref":"e12"},{"ref":"e17"}],"ref":"e9"}`, 17},
		// Guest text that looks like a ref is not one.
		{"a name or a value like a ref", `{"nodes":[{"ref":"e3","name":"e999","value":"e1000","desc":"e5000"}]}`, 3},
		{"clipped by the window is no ref", `{"nodes":[{"ref":"e2","clipped":"window"},{"ref":"e1","clipped":"screen"}]}`, 2},
		{"a number past the pattern", `{"ref":"e12345678901"}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HighestRef([]byte(tc.raw)); got != tc.want {
				t.Errorf("HighestRef = %d, want %d", got, tc.want)
			}
		})
	}
}
