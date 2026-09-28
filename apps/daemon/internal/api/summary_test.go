package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
	"github.com/shlok1806/greenroom/apps/daemon/internal/summary"
)

func (h *harness) summary(runID string) summary.Summary {
	h.t.Helper()
	var s summary.Summary
	h.get("/api/runs/"+runID+"/summary", &s)
	return s
}

func (h *harness) board() summary.Board {
	h.t.Helper()
	var b summary.Board
	h.get("/api/summary", &b)
	return b
}

func groupOf(t *testing.T, b summary.Board, runID string) summary.Group {
	t.Helper()
	for _, g := range b.Groups {
		for _, r := range g.Runs {
			if r.RunID == runID {
				return g.ID
			}
		}
	}
	t.Fatalf("run %s is on no group of the board", runID)
	return ""
}

func TestARunsSummaryFollowsItFromBootToVerdictToDone(t *testing.T) {
	h := newHarness(t)
	runID := h.create()
	if s := h.summary(runID); s.State != summary.Starting || s.Group != summary.Running {
		t.Fatalf("booting: %s in %s, want Starting in Running", s.Status, s.Group)
	}
	if _, err := h.mgr.Wait(context.Background(), runID, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	store := h.store(runID)
	if _, err := store.Append(session.Message{From: session.Coder, Kind: session.Task,
		Text: "TipSplit, a tip calculator, is on screen. Check Each pays at 25%."}); err != nil {
		t.Fatal(err)
	}
	s := h.summary(runID)
	if s.State != summary.Checking || s.Name != "TipSplit" || s.PrimaryAction == nil || s.PrimaryAction.Label != "Take control" {
		t.Fatalf("with a task: %+v", s)
	}

	if _, err := store.Append(session.Message{From: session.Verifier, Kind: session.Verdict, Verdict: "fail", Text: "Each pays is wrong.",
		Checks: []session.Check{
			{ID: "tip", Criterion: "Tip is $30.00", Status: session.CheckPass, Evidence: []int{1}, Observed: "Tip reads $30.00."},
			{ID: "each", Criterion: "Each pays becomes $50.00 at 25%", Status: session.CheckFail, Evidence: []int{1}, Observed: "Each pays reads $10.00."},
		}}); err != nil {
		t.Fatal(err)
	}
	s = h.summary(runID)
	if s.State != summary.Failed || s.Group != summary.NeedsYou || s.Checks.Text != "1 of 2 checks" {
		t.Fatalf("proposed fail: %s in %s, %q", s.Status, s.Group, s.Checks.Text)
	}
	if s.Failing == nil || s.Failing.Expected != "$50.00" || s.Failing.Saw != "$10.00" {
		t.Errorf("failing = %+v", s.Failing)
	}
	if s.PrimaryAction == nil || s.PrimaryAction.Label != "Accept fail" {
		t.Errorf("primary = %+v", s.PrimaryAction)
	}

	if code, body := h.status(http.MethodPost, "/api/runs/"+runID+"/destroy", nil); code != http.StatusOK {
		t.Fatalf("destroy: %d %s", code, body)
	}
	s = h.summary(runID)
	if s.State != summary.Failed || s.Group != summary.Done || s.EndedAt == nil {
		t.Errorf("destroyed with the fail still proposed: %s in %s (ended %v), want Failed in Done", s.Status, s.Group, s.EndedAt)
	}
	if s.Machine.Status != "off" {
		t.Errorf("machine = %+v, want off", s.Machine)
	}
}

// A finished run's summary is kept, but a person accepting its verdict later changes it.
func TestAFinishedRunsSummaryChangesWhenItsConversationDoes(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	store := h.store(runID)
	v, err := store.Append(session.Message{From: session.Verifier, Kind: session.Verdict, Verdict: "pass", Text: "It works.",
		Checks: []session.Check{{ID: "a", Criterion: "Tip is $24.00", Status: session.CheckPass, Evidence: []int{1}}}})
	if err != nil {
		t.Fatal(err)
	}
	if code, body := h.status(http.MethodPost, "/api/runs/"+runID+"/destroy", nil); code != http.StatusOK {
		t.Fatalf("destroy: %d %s", code, body)
	}
	if s := h.summary(runID); s.Tone != summary.TonePass || s.PrimaryAction == nil {
		t.Fatalf("before review: tone %s primary %+v", s.Tone, s.PrimaryAction)
	}
	if code, body := h.status(http.MethodPost, "/api/runs/"+runID+"/messages", map[string]any{"kind": "accept", "replyTo": v.Seq}); code != http.StatusCreated {
		t.Fatalf("accept: %d %s", code, body)
	}
	if s := h.summary(runID); s.Detail != "You accepted it." || s.PrimaryAction != nil {
		t.Errorf("after review: detail %q primary %+v", s.Detail, s.PrimaryAction)
	}
}

func TestTheBoardGroupsEveryRunAndCountsFreeMacs(t *testing.T) {
	// The fake tart lists a VM of someone else's, which takes one of the three places.
	h := newHarness(t, machine.WithMaxMachines(3))
	done := h.ready()
	finish := session.Finish{Outcome: session.OutcomeUnverified, Summary: "Shipped without a check."}
	if _, err := h.store(done).Append(session.Message{From: session.System, Kind: session.Event, Text: session.FinishText(finish), Finish: &finish}); err != nil {
		t.Fatal(err)
	}
	waiting := h.ready()
	if _, err := h.store(waiting).Append(session.Message{From: session.Verifier, Kind: session.Question, Text: "Which tip?"}); err != nil {
		t.Fatal(err)
	}

	b := h.board()
	if len(b.Groups) != 3 {
		t.Fatalf("groups = %+v", b.Groups)
	}
	if g := groupOf(t, b, waiting); g != summary.NeedsYou {
		t.Errorf("a verifier's question: group %s, want needs-you", g)
	}
	if g := groupOf(t, b, done); g != summary.Done {
		t.Errorf("a destroyed run: group %s, want done", g)
	}
	if b.Groups[0].Count != 1 || b.Groups[1].Count != 0 || b.Groups[2].Count != 1 {
		t.Errorf("counts = %d, %d, %d; want 1, 0, 1", b.Groups[0].Count, b.Groups[1].Count, b.Groups[2].Count)
	}
	// Greenroom's own two machines count; the host's other VMs do not (ADR 0036).
	if b.Macs.Text != "1 of 3 Macs free" {
		t.Errorf("macs = %+v, want 1 of 3 free", b.Macs)
	}
}

func TestTheSummaryUsesTheNameAndClientTheRunWasCreatedWith(t *testing.T) {
	h := newHarness(t)
	runID := h.create()
	if err := h.mgr.RecordLabel(runID, "TipSplit: split the bill", "claude-code"); err != nil {
		t.Fatal(err)
	}
	if s := h.summary(runID); s.Name != "TipSplit: split the bill" || s.Source != "Claude Code" {
		t.Errorf("name %q source %q", s.Name, s.Source)
	}
}

// Issue #186's files warning reaches the summary in words, and the run needs you.
func TestAMachineNearItsFileLimitWarnsInWords(t *testing.T) {
	h := newHarness(t, machine.WithFileCheck(machine.FileCheck{
		Interval: 10 * time.Millisecond,
		Count:    func(context.Context, int) (int, error) { return 250, nil },
		Limit:    func() (uint64, bool) { return 256, true },
	}))
	runID := h.ready()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s := h.summary(runID)
		if s.Machine.Warning != "" {
			if s.Machine.Warning != summary.LowOnFilesWarning || s.Group != summary.NeedsYou {
				t.Errorf("warning %q in %s, want the words in needs-you", s.Machine.Warning, s.Group)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no warning in %+v", s.Machine)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAnUnknownRunHasNoSummary(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.status(http.MethodGet, "/api/runs/nope/summary", nil); code != http.StatusNotFound {
		t.Errorf("status %d, want 404", code)
	}
}

func TestTheEventStreamSendsASummaryWhenARunChanges(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	store := h.store(runID)

	old := SummaryEvery
	SummaryEvery = 20 * time.Millisecond
	t.Cleanup(func() { SummaryEvery = old })

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

	events := make(chan summaryEvent, 64)
	go func() {
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		name := ""
		for sc.Scan() {
			line := sc.Text()
			if n, ok := strings.CutPrefix(line, "event: "); ok {
				name = n
				continue
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok && name == "summary" {
				var ev summaryEvent
				if json.Unmarshal([]byte(data), &ev) == nil {
					select {
					case events <- ev:
					default:
					}
				}
			}
		}
	}()

	if _, err := store.Append(session.Message{From: session.Verifier, Kind: session.Question, Text: "Which tip?"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.RunID != runID {
				t.Fatalf("event for %s, want %s", ev.RunID, runID)
			}
			if ev.Summary.State == summary.Paused {
				if ev.Macs.Total != 2 {
					t.Errorf("macs = %+v", ev.Macs)
				}
				// The same summary is not sent twice: a note from the coder changes nothing.
				if _, err := store.Append(session.Message{From: session.Coder, Kind: session.Note, Text: "fyi"}); err != nil {
					t.Fatal(err)
				}
				select {
				case again := <-events:
					t.Errorf("a summary that did not change was sent again: %+v", again.Summary)
				case <-time.After(300 * time.Millisecond):
				}
				return
			}
		case <-deadline:
			t.Fatal("no summary event with the run Paused")
		}
	}
}

func TestASummaryWarnsWhenTheMacRunsLowOnFiles(t *testing.T) {
	h := newHarness(t, machine.WithFileCheck(machine.FileCheck{
		Interval: 20 * time.Millisecond,
		Count:    func(context.Context, int) (int, error) { return 250, nil },
		Limit:    func() (uint64, bool) { return 256, true },
	}))
	runID := h.create()
	if _, err := h.mgr.Wait(context.Background(), runID, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		s := h.summary(runID)
		if s.Machine.Warning == summary.LowOnFilesWarning {
			if s.Group != summary.NeedsYou {
				t.Fatalf("a Mac low on resources should need you, got %s", s.Group)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no low-resources warning: %+v", s.Machine)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
