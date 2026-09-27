package bench

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
	"github.com/shlok1806/greenroom/apps/daemon/internal/verifier"
)

// scriptedBrain answers each task by a word in it, as a verifier would end its turn:
// PASS and FAIL post two progress steps and that verdict, TAKEOVER posts one step and asks
// once a person has taken the screen, BOOT asks at once, and ERROR fails every attempt.
type scriptedBrain struct{}

func (scriptedBrain) Turn(ctx context.Context, _ string, store *session.Store) (verifier.TurnResult, error) {
	task := ""
	for _, m := range store.After(0) {
		if m.Kind == session.Task {
			task = m.Text
		}
	}
	post := func(m session.Message) {
		m.From = session.Verifier
		if _, err := store.Append(m); err != nil {
			panic(err)
		}
	}
	progress := func(n int) {
		for i := 1; i <= n; i++ {
			post(session.Message{Kind: session.Progress, Text: fmt.Sprintf("machine_ui {}\nstep %d", i), Step: i})
		}
	}
	switch {
	case strings.Contains(task, "ERROR"):
		return verifier.TurnResult{Tokens: 7}, errors.New("model endpoint down")
	case strings.Contains(task, "TAKEOVER"):
		progress(1)
		deadline := time.Now().Add(10 * time.Second)
		for !hasControlEvent(store, session.ControlTaken) {
			if time.Now().After(deadline) || ctx.Err() != nil {
				post(session.Message{Kind: session.Reply, Text: "nobody took the screen"})
				return verifier.TurnResult{Steps: 1}, nil
			}
			time.Sleep(10 * time.Millisecond)
		}
		post(session.Message{Kind: session.Question, Text: "A person has the screen. Press Give Back in the Companion and I will continue from here."})
		return verifier.TurnResult{Ended: session.Question, Steps: 1, Tokens: 300}, nil
	case strings.Contains(task, "DISKFULL"):
		// The disk filled mid-trial: the daemon says the machine stopped, and the verifier,
		// finding it gone, reports a fail that says nothing about the app.
		if _, err := store.Append(session.Message{From: session.System, Kind: session.Event,
			Text: "machine stopped: tart no longer lists the VM as running"}); err != nil {
			panic(err)
		}
		post(session.Message{Kind: session.Verdict, Verdict: "fail", Text: "The app is gone."})
		return verifier.TurnResult{Ended: session.Verdict, Steps: 1, Tokens: 10}, nil
	case strings.Contains(task, "BOOT"):
		post(session.Message{Kind: session.Question, Text: "The machine is still booting; can you wait and resend?"})
		return verifier.TurnResult{Ended: session.Question, Tokens: 100}, nil
	case strings.Contains(task, "PASS"), strings.Contains(task, "FAIL"):
		verdict := "pass"
		if strings.Contains(task, "FAIL") {
			verdict = "fail"
		}
		progress(2)
		post(session.Message{Kind: session.Verdict, Verdict: verdict, Text: "It " + verdict + "s: step 2 shows it."})
		return verifier.TurnResult{Ended: session.Verdict, Steps: 2, Tokens: 1234}, nil
	}
	post(session.Message{Kind: session.Reply, Text: "I do not know this task."})
	return verifier.TurnResult{Ended: session.Reply}, nil
}

func hasControlEvent(store *session.Store, control string) bool {
	for _, m := range store.After(0) {
		if m.Kind == session.Event && m.Control == control {
			return true
		}
	}
	return false
}

// fakeTartMachines is the real manager on the fake tart. The fake's VMs all stop on one
// "stopped" file, so each Create clears it: trials run one at a time here. failBuild makes
// build.sh exit 1.
type fakeTartMachines struct {
	*machine.Manager
	control   string
	failBuild atomic.Bool
	noSpace   atomic.Bool // build.sh fails as a full disk makes it fail
}

func (f *fakeTartMachines) Create(ctx context.Context, image string) (*machine.Machine, error) {
	_ = os.Remove(filepath.Join(f.control, "stopped"))
	return f.Manager.Create(ctx, image)
}

func (f *fakeTartMachines) Exec(ctx context.Context, runID, command, cwd string, timeout time.Duration) (machine.ExecResult, error) {
	if command == "./build.sh" && f.noSpace.Load() {
		return machine.ExecResult{ExitCode: 1, Stderr: "error: unable to write build/Demo.app: No space left on device"}, nil
	}
	if command == "./build.sh" && f.failBuild.Load() {
		return machine.ExecResult{ExitCode: 1, Stderr: "main.swift:3: error: cannot find 'x' in scope"}, nil
	}
	return f.Manager.Exec(ctx, runID, command, cwd, timeout)
}

type rig struct {
	bench, root, control, rsyncLog, synced string
	mgr                                    *machine.Manager
	machines                               *fakeTartMachines
	reg                                    *session.Registry
}

// newRig is a bench with the demo app, a manager on the fake tart, and a fake rsync that
// copies what it is given, so a test can see the patched app that reached the "guest".
func newRig(t *testing.T) *rig {
	t.Helper()
	delays := verifier.TurnRetryDelays
	verifier.TurnRetryDelays = []time.Duration{0, 0, 0}
	t.Cleanup(func() { verifier.TurnRetryDelays = delays })

	r := &rig{bench: fixtureBench(t), root: t.TempDir()}
	bin, control := testsupport.FakeTart(t)
	r.control = control
	fake := t.TempDir()
	r.rsyncLog, r.synced = filepath.Join(fake, "rsync.log"), filepath.Join(fake, "synced")
	script := "#!/bin/sh\necho \"$*\" >> '" + r.rsyncLog + "'\nsrc=\nfor a; do case \"$a\" in */) [ -z \"$src\" ] && src=\"$a\" ;; esac; done\n" +
		"rm -rf '" + r.synced + "' && cp -R \"$src\" '" + r.synced + "'\n"
	if err := os.WriteFile(filepath.Join(fake, "rsync"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))

	mgr, err := machine.NewManager(r.root, slog.New(slog.NewTextHandler(io.Discard, nil)),
		machine.WithTartBin(bin), machine.WithReadyTimeout(10*time.Second), machine.WithFrameInterval(0),
		machine.WithMaxMachines(0), machine.WithSSHProbe(func(context.Context, string, string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, mc := range mgr.List() {
			_ = mgr.Destroy(context.Background(), mc.RunID)
		}
	})
	r.mgr, r.machines = mgr, &fakeTartMachines{Manager: mgr, control: control}
	r.reg = session.NewRegistry(r.root, session.DefaultMaxDisputes)
	return r
}

func (r *rig) addCase(t *testing.T, c string) Case {
	t.Helper()
	id := between(c, `"id":"`, `"`)
	path := filepath.Join(r.bench, "cases", id+".json")
	if err := os.WriteFile(path, []byte(c), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCase(r.bench, path)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func between(s, a, b string) string {
	_, rest, _ := strings.Cut(s, a)
	v, _, _ := strings.Cut(rest, b)
	return v
}

func (r *rig) runner(cases []Case, trials int, out string) *Runner {
	return New(Config{BenchDir: r.bench, Cases: cases, Trials: trials, Out: out, Image: "img", Model: "scripted",
		Parallel: 1, TurnTimeout: 20 * time.Second}, r.mgr, r.reg, scriptedBrain{}, WithMachines(r.machines))
}

func byKey(t *testing.T, path string) map[string]Result {
	t.Helper()
	rs, err := ReadResults(path)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Result{}
	for _, res := range Latest(rs) {
		out[res.key()] = res
	}
	return out
}

func TestRunnerRunsEachCaseOnAFreshMachineAndResumes(t *testing.T) {
	r := newRig(t)
	if err := os.WriteFile(filepath.Join(r.bench, "cases", "patches", "demo-broken.diff"),
		[]byte("--- a/main.swift\n+++ b/main.swift\n@@ -1,3 +1,3 @@\n let a = 1\n-let b = 2\n+let b = 3\n print(a + b)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	good := r.addCase(t, `{"id":"demo-good","app":"demo","kind":"correct","split":"dev","patch":null,"task":"Check it. PASS","expected":"pass","must_check":["x"]}`)
	broken := r.addCase(t, `{"id":"demo-broken","app":"demo","kind":"mutant","family":"wrong_computation","split":"holdout","patch":"patches/demo-broken.diff","task":"Check it. FAIL","expected":"fail","must_check":["x"]}`)
	out := filepath.Join(t.TempDir(), "results", "r.jsonl")

	sum, err := r.runner([]Case{good, broken}, 2, out).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sum.Ran != 4 || sum.Right != 4 || sum.Skipped != 0 {
		t.Errorf("summary = %+v, want 4 ran, 4 right", sum)
	}
	got := byKey(t, out)
	runs := map[string]bool{}
	for _, key := range []string{"demo-good#1", "demo-good#2", "demo-broken#1", "demo-broken#2"} {
		res, ok := got[key]
		if !ok {
			t.Fatalf("no result for %s: %v", key, got)
		}
		if res.Ending != EndVerdict || !res.Right() || res.Steps != 2 || res.Tokens != 1234 || res.Turns != 1 {
			t.Errorf("%s = %+v, want a right verdict in 2 steps and 1234 tokens", key, res)
		}
		if res.Image != "img" || res.Model != "scripted" || res.Split == "" || res.Kind == "" || len(res.MustCheck) == 0 {
			t.Errorf("%s does not carry the case and run facts: %+v", key, res)
		}
		if _, err := os.Stat(filepath.Join(res.RunDir, "conversation.jsonl")); err != nil {
			t.Errorf("%s: run directory kept no conversation: %v", key, err)
		}
		runs[res.RunID] = true
	}
	if len(runs) != 4 {
		t.Errorf("%d distinct machines for 4 trials, want one each", len(runs))
	}
	if got["demo-good#1"].Verdict != "pass" || got["demo-broken#1"].Verdict != "fail" {
		t.Errorf("verdicts not recorded: %+v", got)
	}
	for _, mc := range r.mgr.List() {
		t.Errorf("machine %s outlived its trial (%s)", mc.RunID, mc.Status)
	}

	// Trial by trial: every case once before any case twice.
	all, _ := ReadResults(out)
	if all[0].Trial != 1 || all[1].Trial != 1 || all[2].Trial != 2 {
		t.Errorf("order = %v", []int{all[0].Trial, all[1].Trial, all[2].Trial, all[3].Trial})
	}
	// The last sync carried the broken case's patched app into ~/work/demo.
	if data, err := os.ReadFile(filepath.Join(r.synced, "main.swift")); err != nil || !strings.Contains(string(data), "let b = 3") {
		t.Errorf("synced main.swift = %q, %v; want the patched source", data, err)
	}
	if log, _ := os.ReadFile(r.rsyncLog); !strings.Contains(string(log), ":'work/demo'/") {
		t.Errorf("rsync did not target ~/work/demo: %s", log)
	}
	stdin := testsupport.ExecStdin(t, r.control)
	for _, want := range []string{"./build.sh", "open 'build/Demo.app'", "pgrep -x 'Demo'"} {
		if !strings.Contains(stdin, want) {
			t.Errorf("the guest never ran %q", want)
		}
	}

	// A second run with the same results file has nothing left to do. One runner per manager,
	// as in the command: two would start two actors on every run.
	r2 := newRig(t)
	r2.bench = r.bench
	sum, err = r2.runner([]Case{good, broken}, 2, out).Run(context.Background())
	if err != nil || sum.Ran != 0 || sum.Skipped != 4 {
		t.Errorf("resume = %+v, %v; want everything skipped", sum, err)
	}
}

func TestRunnerPlaysATakeoverAndATaskWhileBooting(t *testing.T) {
	r := newRig(t)
	takeover := r.addCase(t, `{"id":"demo-takeover","app":"demo","kind":"infra","split":"dev","patch":null,"task":"Check it. TAKEOVER","expected":"ask_or_inconclusive","must_check":[],"infra":{"type":"human_takeover","afterSteps":1}}`)
	booting := r.addCase(t, `{"id":"demo-booting","app":"demo","kind":"infra","split":"dev","patch":null,"task":"Check it. BOOT","expected":"ask_or_inconclusive","must_check":[],"infra":{"type":"booting"}}`)
	out := filepath.Join(t.TempDir(), "r.jsonl")

	if _, err := r.runner([]Case{takeover, booting}, 1, out).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := byKey(t, out)
	tk := got["demo-takeover#1"]
	if tk.Ending != EndQuestion || !tk.Right() || tk.Infra != InfraTakeover {
		t.Errorf("takeover = %+v, want a right question", tk)
	}
	conv, err := os.ReadFile(filepath.Join(tk.RunDir, "conversation.jsonl"))
	if err != nil || !strings.Contains(string(conv), `"control":"taken"`) || !strings.Contains(string(conv), "human took control of the screen") {
		t.Errorf("the takeover never reached the conversation: %s %v", conv, err)
	}
	bt := got["demo-booting#1"]
	if bt.Ending != EndQuestion || !bt.Right() {
		t.Errorf("booting = %+v, want a right question", bt)
	}
	if builds := strings.Count(testsupport.ExecStdin(t, r.control), "./build.sh"); builds != 1 {
		t.Errorf("%d builds for a takeover and a booting case, want 1: the booting case's task arrives before anything is set up", builds)
	}
}

func TestRunnerRecordsModelErrorsAndRetriesSetupErrorsOnResume(t *testing.T) {
	r := newRig(t)
	flaky := r.addCase(t, `{"id":"demo-flaky","app":"demo","kind":"correct","split":"dev","patch":null,"task":"Check it. ERROR","expected":"pass","must_check":["x"]}`)
	good := r.addCase(t, `{"id":"demo-good","app":"demo","kind":"correct","split":"dev","patch":null,"task":"Check it. PASS","expected":"pass","must_check":["x"]}`)
	out := filepath.Join(t.TempDir(), "r.jsonl")

	r.machines.failBuild.Store(true)
	sum, err := r.runner([]Case{good}, 1, out).Run(context.Background())
	if err != nil || sum.SetupErrors != 1 {
		t.Fatalf("summary = %+v, %v; want one setup error", sum, err)
	}
	res := byKey(t, out)["demo-good#1"]
	if res.Ending != EndSetupError || !strings.Contains(res.Error, "build: exit 1") || !strings.Contains(res.Error, "cannot find 'x'") {
		t.Errorf("setup error = %+v", res)
	}
	if res.Answered() || res.Right() {
		t.Error("a setup error is not an answer")
	}

	r2 := newRig(t) // one runner per manager
	r2.bench = r.bench
	sum, err = r2.runner([]Case{good, flaky}, 1, out).Run(context.Background())
	if err != nil || sum.Ran != 2 || sum.Skipped != 0 {
		t.Fatalf("resume = %+v, %v; want the setup error rerun and the new case run", sum, err)
	}
	got := byKey(t, out)
	if got["demo-good#1"].Ending != EndVerdict {
		t.Errorf("rerun = %+v, want a verdict", got["demo-good#1"])
	}
	fl := got["demo-flaky#1"]
	if fl.Ending != EndModelError || !strings.Contains(fl.Text, "gave up") || fl.Turns != len(verifier.TurnRetryDelays)+1 || fl.Tokens != 7*fl.Turns {
		t.Errorf("model error = %+v, want every attempt's cost and the give-up text", fl)
	}
}

// countingMachines never boots anything: it counts how many machines are out at once, and
// refuses the first creates as a host at its limit would.
type countingMachines struct {
	root              string
	mu                sync.Mutex
	live, most, total int
	refuse            int
}

func (c *countingMachines) Create(context.Context, string) (*machine.Machine, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.refuse > 0 {
		c.refuse--
		return nil, errors.New("host is at its limit of 2 machines, none of them this daemon's")
	}
	c.total++
	c.live++
	c.most = max(c.most, c.live)
	id := fmt.Sprintf("run-%d", c.total)
	if err := os.MkdirAll(c.RunDir(id), 0o755); err != nil {
		return nil, err
	}
	return &machine.Machine{RunID: id, Status: machine.Ready}, nil
}

func (c *countingMachines) Wait(_ context.Context, runID string, _ time.Duration) (*machine.Machine, error) {
	return &machine.Machine{RunID: runID, Status: machine.Ready}, nil
}

func (c *countingMachines) Sync(context.Context, string, string, machine.SyncOptions) (machine.SyncResult, error) {
	time.Sleep(20 * time.Millisecond)
	return machine.SyncResult{}, nil
}

func (c *countingMachines) Exec(context.Context, string, string, string, time.Duration) (machine.ExecResult, error) {
	return machine.ExecResult{}, nil
}

func (c *countingMachines) Destroy(context.Context, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.live--
	return nil
}

func (c *countingMachines) TakeControl(string, string, time.Duration) (machine.Control, bool, error) {
	return machine.Control{}, true, nil
}

func (c *countingMachines) Input(context.Context, string, string, []machine.InputAction) (machine.InputResult, error) {
	return machine.InputResult{}, nil
}

func (c *countingMachines) RunDir(runID string) string { return filepath.Join(c.root, "runs", runID) }

// No verifier answers here, so every trial times out; what is measured is the machines.
func TestRunnerKeepsToItsMachineLimitAndWaitsForAFreeSlot(t *testing.T) {
	r := newRig(t)
	cases := []Case{
		r.addCase(t, `{"id":"demo-a","app":"demo","kind":"correct","split":"dev","patch":null,"task":"A","expected":"pass","must_check":["x"]}`),
		r.addCase(t, `{"id":"demo-b","app":"demo","kind":"correct","split":"dev","patch":null,"task":"B","expected":"pass","must_check":["x"]}`),
		r.addCase(t, `{"id":"demo-c","app":"demo","kind":"correct","split":"dev","patch":null,"task":"C","expected":"pass","must_check":["x"]}`),
	}
	counting := &countingMachines{root: r.root, refuse: 2}
	out := filepath.Join(t.TempDir(), "r.jsonl")
	sum, err := New(Config{BenchDir: r.bench, Cases: cases, Trials: 2, Out: out, Parallel: 2,
		TurnTimeout: 200 * time.Millisecond, CapacityPoll: 10 * time.Millisecond}, r.mgr, r.reg, scriptedBrain{}, WithMachines(counting)).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sum.Ran != 6 || counting.total != 6 {
		t.Errorf("ran %d trials on %d machines, want 6 and 6", sum.Ran, counting.total)
	}
	if counting.most != 2 {
		t.Errorf("at most %d machines at once, want 2", counting.most)
	}
	if counting.live != 0 {
		t.Errorf("%d machines left", counting.live)
	}
	for key, res := range byKey(t, out) {
		if res.Ending != EndTimeout || res.Answered() {
			t.Errorf("%s = %+v, want a timeout that is not an answer", key, res)
		}
	}
}

func TestRunnerRecordsNothingForATrialItWasStoppedIn(t *testing.T) {
	r := newRig(t)
	c := r.addCase(t, `{"id":"demo-a","app":"demo","kind":"correct","split":"dev","patch":null,"task":"A","expected":"pass","must_check":["x"]}`)
	counting := &countingMachines{root: r.root}
	out := filepath.Join(t.TempDir(), "r.jsonl")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := New(Config{BenchDir: r.bench, Cases: []Case{c}, Trials: 1, Out: out, TurnTimeout: time.Minute},
		r.mgr, r.reg, scriptedBrain{}, WithMachines(counting)).Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the cancellation", err)
	}
	if rs, _ := ReadResults(out); len(rs) != 0 {
		t.Errorf("an interrupted trial was recorded: %+v", rs)
	}
	if counting.live != 0 {
		t.Error("the interrupted trial's machine was not destroyed")
	}
}

func TestReadChecksCopiesTheVerdictsAndTheDeclaredChecks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.jsonl")
	lines := strings.Join([]string{
		`{"seq":1,"from":"coder","kind":"task","text":"old task","checks":[{"id":"z"}]}`,
		`{"seq":2,"from":"verifier","kind":"verdict","verdict":"pass","text":"old","checks":[{"id":"old"}]}`,
		`{"seq":3,"from":"coder","kind":"task","text":"t"}`,
		`{"seq":4,"from":"verifier","kind":"progress","text":"declare_checks","checks":[{"id":"c1","criterion":"Each pays reads $48.00"}]}`,
		`{"seq":5,"from":"verifier","kind":"progress","text":"report_verdict {}\nerror: check c1 cites no observation"}`,
		`{"seq":6,"from":"verifier","kind":"verdict","verdict":"fail","text":"f","checks":[{"id":"c1","status":"fail","observed":"$8.00"}]}`,
		`{"seq":7,"from":"verifier","kind":"progress","text":"later","checks":[{"id":"late"}]}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	var res Result
	readChecks(path, 3, &res)
	if !strings.Contains(string(res.DeclaredChecks), "Each pays reads $48.00") || !strings.Contains(string(res.Checks), `"observed":"$8.00"`) {
		t.Errorf("checks = %s, declared = %s", res.Checks, res.DeclaredChecks)
	}
	res = Result{}
	readChecks(path+".missing", 0, &res)
	if res.Checks != nil || res.DeclaredChecks != nil {
		t.Error("checks from a missing conversation")
	}
}

// freeDisk is a scripted free-space probe: each call returns the next value (bytes), and the
// last one forever after.
type freeDisk struct {
	mu     sync.Mutex
	values []uint64
	calls  int
}

func (f *freeDisk) probe() (uint64, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	v := f.values[min(f.calls, len(f.values))-1]
	return v, "/fake/.tart", nil
}

const gb = 1 << 30

// Issue #155: under the free-disk threshold the runner waits, then stops cleanly. Nothing is
// cut short, the trial that could not start is recorded as a setup error of cause disk, it
// says so where the run's progress goes, and a rerun with the same results file finishes.
func TestRunnerStopsCleanlyOnLowDisk(t *testing.T) {
	r := newRig(t)
	cases := []Case{
		r.addCase(t, `{"id":"demo-a","app":"demo","kind":"correct","split":"dev","patch":null,"task":"A","expected":"pass","must_check":["x"]}`),
		r.addCase(t, `{"id":"demo-b","app":"demo","kind":"correct","split":"dev","patch":null,"task":"B","expected":"pass","must_check":["x"]}`),
		r.addCase(t, `{"id":"demo-c","app":"demo","kind":"correct","split":"dev","patch":null,"task":"C","expected":"pass","must_check":["x"]}`),
	}
	counting := &countingMachines{root: r.root}
	out := filepath.Join(t.TempDir(), "r.jsonl")
	disk := &freeDisk{values: []uint64{10 * gb, 1 * gb}} // enough for demo-a, then low
	var progress strings.Builder
	cfg := Config{BenchDir: r.bench, Cases: cases, Trials: 1, Out: out, Parallel: 1, TurnTimeout: 200 * time.Millisecond,
		MinFreeDisk: 5 * gb, DiskWait: 50 * time.Millisecond, DiskPoll: 10 * time.Millisecond, FreeDisk: disk.probe,
		Progress: &progress}
	sum, err := New(cfg, r.mgr, r.reg, scriptedBrain{}, WithMachines(counting)).Run(context.Background())
	if !errors.Is(err, ErrLowDisk) {
		t.Fatalf("err = %v, want ErrLowDisk", err)
	}
	if counting.total != 1 || sum.Ran != 2 || sum.SetupErrors != 1 {
		t.Errorf("summary %+v on %d machines, want demo-a run and demo-b refused before a machine", sum, counting.total)
	}
	got := byKey(t, out)
	if got["demo-a#1"].Ending == EndSetupError {
		t.Errorf("demo-a = %+v, want it run", got["demo-a#1"])
	}
	b := got["demo-b#1"]
	if b.Ending != EndSetupError || b.Cause != CauseDisk || !strings.Contains(b.Error, "1.0 GB free on /fake/.tart, under 5.0 GB") {
		t.Errorf("demo-b = %+v, want a setup error of cause disk saying how much was free", b)
	}
	if _, ok := got["demo-c#1"]; ok {
		t.Error("demo-c was recorded after the stop")
	}
	for _, want := range []string{"1.0 GB free on /fake/.tart, under 5.0 GB; waiting up to", "stopping: no trial starts under 5.0 GB free"} {
		if !strings.Contains(progress.String(), want) {
			t.Errorf("progress lacks %q:\n%s", want, progress.String())
		}
	}

	// Space is back: the rerun retries demo-b and runs demo-c.
	r2 := newRig(t)
	r2.bench = r.bench
	cfg.FreeDisk = (&freeDisk{values: []uint64{10 * gb}}).probe
	sum, err = New(cfg, r2.mgr, r2.reg, scriptedBrain{}, WithMachines(&countingMachines{root: r2.root})).Run(context.Background())
	if err != nil || sum.Ran != 2 || sum.Skipped != 1 {
		t.Errorf("resume = %+v, %v; want demo-b and demo-c run", sum, err)
	}
}

// A low disk that recovers while the runner waits costs a wait, not the run.
func TestRunnerWaitsForDiskToComeBack(t *testing.T) {
	r := newRig(t)
	c := r.addCase(t, `{"id":"demo-a","app":"demo","kind":"correct","split":"dev","patch":null,"task":"A","expected":"pass","must_check":["x"]}`)
	out := filepath.Join(t.TempDir(), "r.jsonl")
	disk := &freeDisk{values: []uint64{2 * gb, 3 * gb, 8 * gb}}
	var progress strings.Builder
	sum, err := New(Config{BenchDir: r.bench, Cases: []Case{c}, Trials: 1, Out: out, Parallel: 1, TurnTimeout: 200 * time.Millisecond,
		MinFreeDisk: 5 * gb, DiskWait: 10 * time.Second, DiskPoll: 10 * time.Millisecond, FreeDisk: disk.probe, Progress: &progress},
		r.mgr, r.reg, scriptedBrain{}, WithMachines(&countingMachines{root: r.root})).Run(context.Background())
	if err != nil || sum.Ran != 1 || sum.SetupErrors != 0 {
		t.Fatalf("summary = %+v, %v; want the trial run after the wait", sum, err)
	}
	if !strings.Contains(progress.String(), "8.0 GB free again; going on") {
		t.Errorf("progress = %q, want the recovery said", progress.String())
	}
}

// A trial whose machine stopped while the disk was low is a setup error of cause disk, not
// the wrong verdict the verifier gave about a machine that was gone; so is a build that ran
// out of space.
func TestATrialTheDiskKilledIsASetupError(t *testing.T) {
	r := newRig(t)
	full := r.addCase(t, `{"id":"demo-full","app":"demo","kind":"correct","split":"dev","patch":null,"task":"Check it. DISKFULL","expected":"pass","must_check":["x"]}`)
	good := r.addCase(t, `{"id":"demo-good","app":"demo","kind":"correct","split":"dev","patch":null,"task":"Check it. PASS","expected":"pass","must_check":["x"]}`)
	out := filepath.Join(t.TempDir(), "r.jsonl")
	// Enough to start each trial; low when demo-full ends. demo-good ends with space.
	disk := &freeDisk{values: []uint64{6 * gb, 2 * gb, 6 * gb}}
	cfg := Config{BenchDir: r.bench, Cases: []Case{full, good}, Trials: 1, Out: out, Image: "img", Parallel: 1,
		TurnTimeout: 20 * time.Second, MinFreeDisk: 5 * gb, DiskWait: time.Second, DiskPoll: 10 * time.Millisecond, FreeDisk: disk.probe}
	if _, err := New(cfg, r.mgr, r.reg, scriptedBrain{}, WithMachines(r.machines)).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := byKey(t, out)
	f := got["demo-full#1"]
	if f.Ending != EndSetupError || f.Cause != CauseDisk || f.Verdict != "" ||
		!strings.Contains(f.Error, "machine stopped during the trial with 2.0 GB free") || !strings.Contains(f.Error, "its fail is not counted") {
		t.Errorf("demo-full = %+v, want a setup error of cause disk", f)
	}
	if g := got["demo-good#1"]; g.Ending != EndVerdict || g.Cause != "" {
		t.Errorf("demo-good = %+v, want its verdict", g)
	}

	// Out of space in the build, whatever the probe says.
	r2 := newRig(t)
	r2.bench = r.bench
	r2.machines.noSpace.Store(true)
	cfg.Cases, cfg.Out, cfg.FreeDisk = []Case{good}, filepath.Join(t.TempDir(), "r2.jsonl"), (&freeDisk{values: []uint64{50 * gb}}).probe
	if _, err := New(cfg, r2.mgr, r2.reg, scriptedBrain{}, WithMachines(r2.machines)).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b := byKey(t, cfg.Out)["demo-good#1"]; b.Ending != EndSetupError || b.Cause != CauseDisk {
		t.Errorf("a build out of space = %+v, want cause disk", b)
	}
}
