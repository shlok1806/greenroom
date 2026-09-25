package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

var wholeBoot = []string{"clone", "start", "agent", "ip", "key", "settings", "helper", "checks", "ssh"}

func TestRunDetailShowsTheMachinesBootPhases(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()

	// Decoded as the companion reads it, not through our own types.
	var d struct {
		Machine struct {
			Status string `json:"status"`
			IP     string `json:"ip"`
			Boot   []struct {
				Phase   string   `json:"phase"`
				At      string   `json:"at"`
				Seconds *float64 `json:"seconds"`
				Detail  string   `json:"detail"`
			} `json:"boot"`
		} `json:"machine"`
	}
	h.get("/api/runs/"+runID, &d)
	var got []string
	for _, p := range d.Machine.Boot {
		got = append(got, p.Phase)
		if p.Seconds == nil || p.At == "" {
			t.Errorf("phase %+v, want it ended with a start time", p)
		}
	}
	if !slices.Equal(got, wholeBoot) {
		t.Fatalf("machine.boot = %v, want %v", got, wholeBoot)
	}
	if d.Machine.Boot[3].Detail != d.Machine.IP || d.Machine.IP == "" {
		t.Errorf("ip phase detail = %q, want the machine's address %q", d.Machine.Boot[3].Detail, d.Machine.IP)
	}
}

func TestAFailedBootShowsWhereItStoppedInTheRunDetail(t *testing.T) {
	h := newHarness(t, machine.WithReadyTimeout(2*time.Second))
	testsupport.Flag(t, h.control, "fail-keyinstall")
	runID := h.create()
	if _, err := h.mgr.Wait(context.Background(), runID, 30*time.Second); err != nil {
		t.Fatalf("wait: %v", err)
	}
	var d RunDetail
	h.get("/api/runs/"+runID, &d)
	if d.Machine == nil || d.Machine.Status != machine.Failed {
		t.Fatalf("machine = %+v, want failed", d.Machine)
	}
	last := d.Machine.Boot[len(d.Machine.Boot)-1]
	if last.Phase != "key" || last.Error == "" {
		t.Errorf("last phase = %+v, want key with its error", last)
	}
}

func TestEventStreamSendsEachBootPhase(t *testing.T) {
	h := newHarness(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.url+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open the stream: %v", err)
	}
	defer func() { _ = res.Body.Close() }()

	type frame struct{ name, data string }
	frames := make(chan frame, 128)
	go func() {
		sc := bufio.NewScanner(res.Body)
		var name string
		for sc.Scan() {
			line := sc.Text()
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				name = v
			} else if v, ok := strings.CutPrefix(line, "data: "); ok {
				frames <- frame{name, v}
			}
		}
	}()

	runID := h.create()
	var ended []string
	var createdBoot int
	deadline := time.After(10 * time.Second)
	for !slices.Contains(ended, "ssh") {
		select {
		case f := <-frames:
			switch f.name {
			case "boot":
				var ev struct {
					RunID string            `json:"runId"`
					Phase machine.BootPhase `json:"phase"`
				}
				if err := json.Unmarshal([]byte(f.data), &ev); err != nil {
					t.Fatalf("boot event %s: %v", f.data, err)
				}
				if ev.RunID != runID {
					t.Errorf("boot event for %q, want %q", ev.RunID, runID)
				}
				if ev.Phase.Seconds != nil {
					ended = append(ended, ev.Phase.Phase)
				}
			case "run":
				var ev struct {
					Kind    string       `json:"kind"`
					Machine *LiveMachine `json:"machine"`
				}
				if err := json.Unmarshal([]byte(f.data), &ev); err != nil {
					t.Fatalf("run event %s: %v", f.data, err)
				}
				if ev.Kind == "created" && ev.Machine != nil {
					createdBoot = len(ev.Machine.Boot)
				}
			}
		case <-deadline:
			t.Fatalf("the stream sent ended phases %v, want every phase through ssh", ended)
		}
	}
	if !slices.Equal(ended, wholeBoot) {
		t.Errorf("ended phases = %v, want %v", ended, wholeBoot)
	}
	// A run event's machine carries its phases too, like the run detail.
	if createdBoot != 2 {
		t.Errorf("the created event's machine has %d phases, want clone and start", createdBoot)
	}
}
