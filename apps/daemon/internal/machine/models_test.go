package machine

import (
	"testing"
)

// Issue #154: a run's manifest and machine record who verifies it, as the manager was told
// before the run was created; a run created before SetModels records nothing.
func TestARunRecordsItsModels(t *testing.T) {
	unset, _, _ := newTestManager(t)
	before := readyMachine(t, unset)
	mgr, _, _ := newTestManager(t)
	models := Models{Brain: BrainNIM, Model: "nvidia/ultra", Vision: "meta/muse-glimmer-30b",
		VisionOptions: map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": false}}}
	mgr.SetModels(models)
	mc := readyMachine(t, mgr)

	man, err := ReadManifest(mc.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if man.Models == nil || man.Models.Vision != "meta/muse-glimmer-30b" || man.Models.Model != "nvidia/ultra" {
		t.Fatalf("manifest models = %+v, want the manager's", man.Models)
	}
	if mc.Models == nil || mc.Models.Label() != `nim nvidia/ultra, describer meta/muse-glimmer-30b {"enable_thinking":false}` {
		t.Errorf("machine models = %+v", mc.Models)
	}
	if old, err := ReadManifest(before.Dir); err != nil || old.Models != nil {
		t.Errorf("a run created before SetModels records %+v (%v), want nothing", old.Models, err)
	}
	for brain, want := range map[string]string{BrainManual: "manual", BrainNone: "none"} {
		if got := (Models{Brain: brain}).Label(); got != want {
			t.Errorf("Label of %s = %q", brain, got)
		}
	}
	if got := (Models{Brain: BrainNIM, Model: "m"}).Label(); got != "nim m, describer none" {
		t.Errorf("Label with no describer = %q", got)
	}
}
