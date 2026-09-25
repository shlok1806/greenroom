package machine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A bare `greenroom serve` used the upstream Cirrus image while install.sh picked greenroom-lean-a,
// so the two daemons behaved differently. The daemon now makes install.sh's choice.
func TestPreferredImageIsTheInstalledDaemonsChoice(t *testing.T) {
	for _, tc := range []struct {
		local []string
		want  string
	}{
		{[]string{"greenroom-base", "greenroom-lean-a"}, "greenroom-lean-a"},
		{[]string{"other", "greenroom-base"}, "greenroom-base"},
		{[]string{"other"}, "upstream"},
	} {
		mgr, _, control := newTestManager(t)
		names := ""
		for _, n := range tc.local {
			names += n + "\n"
		}
		if err := os.WriteFile(filepath.Join(control, "vmnames"), []byte(names), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := mgr.PreferredImage(context.Background(), "upstream"); got != tc.want {
			t.Errorf("local %v: PreferredImage = %q, want %q", tc.local, got, tc.want)
		}
	}
}
