package main

import (
	"flag"
	"testing"
)

// The CLI must preserve the unspecified cap: passing its former default 40 to
// New would disable checklist sizing for every installed daemon and bench run.
func TestVerifierStepFlagDefaultsToAutomaticAndPreservesExplicitCaps(t *testing.T) {
	for _, command := range []string{"serve", "bench"} {
		for _, tc := range []struct {
			args []string
			want int
		}{{nil, 0}, {[]string{"-verifier-max-steps", "40"}, 40}, {[]string{"-verifier-max-steps", "6"}, 6}} {
			t.Run(command, func(t *testing.T) {
				var fs *flag.FlagSet
				var got *int
				if command == "serve" {
					var o *serveOpts
					fs, o = serveFlags()
					got = &o.verifierMaxSteps
				} else {
					var o *benchRunOpts
					fs, o = benchRunFlags()
					got = &o.verifierMaxSteps
				}
				if err := fs.Parse(tc.args); err != nil {
					t.Fatal(err)
				}
				if *got != tc.want {
					t.Errorf("cap=%d want %d", *got, tc.want)
				}
			})
		}
	}
}
