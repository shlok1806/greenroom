package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

type rebootResult struct {
	machine.Machine
	Step int `json:"step"`
}

// The description is how an agent learns when to reboot, what it keeps and loses, and that
// machine_wait finishes the wait (daemon ADR 0004).
func TestRebootToolSaysWhenToUseItAndWhatItKeeps(t *testing.T) {
	h := newHarness(t)
	res, err := h.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name != "machine_reboot" {
			continue
		}
		for _, want := range []string{"WindowServer", "Guest Agent", "~/work", "ssh key", "approvals",
			"machine_session_", "machine_exec", "/tmp", "live screen", "machine_ui", "IP address may change",
			"max 50", "status rebooting", "machine_wait", "5 minutes", "disk is kept", "machine_destroy"} {
			if !strings.Contains(tool.Description, want) {
				t.Errorf("machine_reboot's description does not mention %q", want)
			}
		}
		return
	}
	t.Fatal("no machine_reboot tool")
}

func TestRebootToolRebootsAndRecordsAStep(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	var out rebootResult
	h.call("machine_reboot", map[string]any{"runId": runID}, &out)
	if out.Status != machine.Ready || out.RunID != runID || out.Step == 0 {
		t.Fatalf("machine_reboot returned %+v, want the machine ready again and its step", out)
	}
	steps, err := h.mgr.Steps(runID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range steps {
		found = found || s.Seq == out.Step && s.Tool == "machine_reboot" && s.Error == ""
	}
	if !found {
		t.Errorf("no machine_reboot step %d in %+v", out.Step, steps)
	}
	if calls := testsupport.Calls(t, h.control); strings.Contains(calls, "delete greenroom-"+runID) {
		t.Errorf("the reboot deleted the VM\ncalls:\n%s", calls)
	}
}

// A reboot longer than the call's wait returns rebooting; machine_wait finishes it, and every
// other machine call is refused at once meanwhile.
func TestRebootToolReturnsRebootingAndMachineWaitFinishes(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	testsupport.Flag(t, h.control, "agent-down")

	var out rebootResult
	h.call("machine_reboot", map[string]any{"runId": runID, "waitSeconds": 1}, &out)
	if out.Status != machine.Rebooting {
		t.Fatalf("machine_reboot returned %s, want rebooting while the guest is not back", out.Status)
	}
	if res := h.raw("machine_reboot", map[string]any{"runId": runID}); !res.IsError || !strings.Contains(text(res), "already") {
		t.Errorf("a second machine_reboot: %q, want a refusal", text(res))
	}
	res := h.raw("machine_exec", map[string]any{"runId": runID, "command": "true"})
	if !res.IsError || !strings.Contains(text(res), "rebooting") || !strings.Contains(text(res), "machine_wait") {
		t.Errorf("machine_exec during a reboot: %q, want the rebooting error", text(res))
	}
	var list struct {
		Machines []machine.Machine `json:"machines"`
	}
	h.call("machine_list", nil, &list)
	if len(list.Machines) != 1 || list.Machines[0].Status != machine.Rebooting {
		t.Errorf("machine_list shows %+v, want the machine rebooting", list.Machines)
	}

	if err := os.Remove(filepath.Join(h.control, "agent-down")); err != nil {
		t.Fatal(err)
	}
	var mc machine.Machine
	for i := 0; i < 10 && mc.Status != machine.Ready; i++ {
		h.call("machine_wait", map[string]any{"runId": runID, "timeoutSeconds": 5}, &mc)
	}
	if mc.Status != machine.Ready {
		t.Fatalf("machine_wait ended at %s (%s), want ready", mc.Status, mc.Error)
	}
}
