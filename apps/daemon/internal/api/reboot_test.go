package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// POST /reboot starts machine_reboot for a person (daemon ADR 0004): 202 with the machine
// rebooting, an event in the conversation, a "run" event on the stream, and 409 for a
// second reboot or a guest call while it runs.
func TestRebootRouteRebootsAndSaysSo(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	store := h.store(runID)
	testsupport.Flag(t, h.control, "agent-down") // hold the reboot until the test lets it finish

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.url+"/api/events?runId="+runID, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	kinds := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var ev struct {
					Kind string `json:"kind"`
				}
				if json.Unmarshal([]byte(data), &ev) == nil && ev.Kind != "" {
					kinds <- ev.Kind
				}
			}
		}
	}()

	code, body := h.status(http.MethodPost, "/api/runs/"+runID+"/reboot", nil)
	if code != http.StatusAccepted {
		t.Fatalf("status %d: %s", code, body)
	}
	var out struct {
		Machine machine.Machine `json:"machine"`
		Step    int             `json:"step"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if out.Machine.Status != machine.Rebooting || out.Step == 0 {
		t.Errorf("reboot answered %+v, want the machine rebooting and its step", out)
	}
	if msgs := store.After(0); len(msgs) != 1 || !strings.HasPrefix(msgs[0].Text, "human rebooted the machine (step ") {
		t.Errorf("conversation = %+v, want the reboot event", msgs)
	}
	deadline := time.After(10 * time.Second)
	for seen := false; !seen; {
		select {
		case k := <-kinds:
			seen = k == "rebooting"
		case <-deadline:
			t.Fatal("the event stream never said the machine is rebooting")
		}
	}

	if code, body := h.status(http.MethodPost, "/api/runs/"+runID+"/reboot", nil); code != http.StatusConflict {
		t.Errorf("a second reboot: status %d: %s, want 409", code, body)
	}
	if code, body := h.status(http.MethodPost, "/api/runs/"+runID+"/screenshot", nil); code != http.StatusConflict || !strings.Contains(body, "rebooting") {
		t.Errorf("a screenshot during a reboot: status %d: %s, want 409 saying it is rebooting", code, body)
	}

	if err := os.Remove(filepath.Join(h.control, "agent-down")); err != nil {
		t.Fatal(err)
	}
	if mc, err := h.mgr.Wait(context.Background(), runID, 20*time.Second); err != nil || mc.Status != machine.Ready {
		t.Fatalf("after the reboot: %+v, %v; want ready", mc, err)
	}
}
