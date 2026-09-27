package verifier

import (
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
)

// Issue #154: the verifier names its models and the options its requests carry, for the record.
func TestTheVerifierNamesItsModelsAndRequestOptions(t *testing.T) {
	mgr, _, _ := ready(t)
	m := newVerifier(t, mgr, "http://127.0.0.1:1").Models()
	if m.Brain != machine.BrainNIM || m.Model != "reasoner" || m.Vision != "eyes" {
		t.Errorf("models = %+v, want nim, reasoner and eyes", m)
	}
	if m.ModelOptions["max_tokens"] != nim.ChatMaxTokens || m.VisionOptions["max_tokens"] != 700 {
		t.Errorf("options = %v and %v, want the requests'", m.ModelOptions, m.VisionOptions)
	}
	v, err := New(mgr, Config{APIKey: "k", Model: "reasoner"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m := v.Models(); m.Vision != "" || m.VisionOptions != nil {
		t.Errorf("with no describer: %+v, want none", m)
	}
}
