package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func must(t *testing.T, s *Store, m Message) Message {
	t.Helper()
	out, err := s.Append(m)
	if err != nil {
		t.Fatalf("append %s from %s: %v", m.Kind, m.From, err)
	}
	return out
}

func TestAppendNumbersAndReadsBack(t *testing.T) {
	s, dir := open(t)
	a := must(t, s, Message{From: Coder, Kind: Task, Text: "build it"})
	b := must(t, s, Message{From: Human, Kind: Note, Text: "use the Release scheme"})
	if a.Seq != 1 || b.Seq != 2 {
		t.Fatalf("seq = %d, %d", a.Seq, b.Seq)
	}
	if a.At.IsZero() {
		t.Fatal("At not set")
	}
	got := s.After(0)
	if len(got) != 2 || got[1].Text != "use the Release scheme" {
		t.Fatalf("After(0) = %+v", got)
	}
	if len(s.After(2)) != 0 {
		t.Fatal("After(2) should be empty")
	}
	data, _ := os.ReadFile(filepath.Join(dir, fileName))
	if strings.Count(string(data), "\n") != 2 {
		t.Fatalf("file has %q", data)
	}
}

func TestReloadFromDisk(t *testing.T) {
	s, dir := open(t)
	must(t, s, Message{From: Coder, Kind: Task, Text: "build"})
	must(t, s, Message{From: Verifier, Kind: Verdict, Verdict: "pass", Text: "it built", Evidence: []string{"step 3"}})

	again, err := Open(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if again.Len() != 2 {
		t.Fatalf("reloaded %d messages", again.Len())
	}
	v := again.Verdict()
	if v.Verdict != "pass" || v.Status != Proposed || v.Evidence[0] != "step 3" {
		t.Fatalf("verdict after reload = %+v", v)
	}
	next := must(t, again, Message{From: Coder, Kind: Accept, ReplyTo: 2})
	if next.Seq != 3 {
		t.Fatalf("seq after reload = %d", next.Seq)
	}
}

func TestWaitWakesOnAppend(t *testing.T) {
	s, _ := open(t)
	got := make(chan []Message, 1)
	go func() { got <- s.Wait(context.Background(), 0) }()
	time.Sleep(20 * time.Millisecond)
	must(t, s, Message{From: Coder, Kind: Task, Text: "go"})
	select {
	case msgs := <-got:
		if len(msgs) != 1 || msgs[0].Text != "go" {
			t.Fatalf("woke with %+v", msgs)
		}
	case <-time.After(time.Second):
		t.Fatal("Wait did not wake")
	}
}

func TestWaitReturnsEmptyOnTimeout(t *testing.T) {
	s, _ := open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if got := s.Wait(ctx, 0); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestWaitReturnsAtOnceWhenNewerExists(t *testing.T) {
	s, _ := open(t)
	must(t, s, Message{From: Coder, Kind: Task, Text: "a"})
	must(t, s, Message{From: Coder, Kind: Note, Text: "b"})
	got := s.Wait(context.Background(), 1)
	if len(got) != 1 || got[0].Seq != 2 {
		t.Fatalf("got %+v", got)
	}
}

func TestSeqIsUniqueUnderConcurrentAppends(t *testing.T) {
	s, _ := open(t)
	var wg sync.WaitGroup
	seen := make([]bool, 101)
	var mu sync.Mutex
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m := must(t, s, Message{From: Coder, Kind: Note, Text: fmt.Sprint(i)})
			mu.Lock()
			if seen[m.Seq] {
				t.Errorf("seq %d handed out twice", m.Seq)
			}
			seen[m.Seq] = true
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if s.Len() != 100 {
		t.Fatalf("len = %d", s.Len())
	}
}

func TestValidationRejectsTheWrongSender(t *testing.T) {
	s, _ := open(t)
	cases := []Message{
		{From: Coder, Kind: Verdict, Verdict: "pass", Text: "x"},
		{From: Verifier, Kind: Task, Text: "x"},
		{From: Human, Kind: Event, Text: "x"},
		{From: Coder, Kind: Kind("shout"), Text: "x"},
		{From: Verifier, Kind: Verdict, Verdict: "maybe", Text: "x"},
		{From: Coder, Kind: Answer, Text: "x"},
		{From: Coder, Kind: Task},
		{From: Human, Kind: Reply, Text: "x"}, // only the verifier replies
		{From: Coder, Kind: Reply, Text: "x"}, //
		{From: Verifier, Kind: Reply},         // a reply with nothing in it says nothing
	}
	for _, m := range cases {
		if _, err := s.Append(m); err == nil {
			t.Errorf("%s %s accepted", m.From, m.Kind)
		}
	}
	if s.Len() != 0 {
		t.Fatal("a rejected message was written")
	}
}

// Only a verifier reply may say it stopped at a limit (issue #127).
func TestOnlyAVerifierReplyCarriesAStop(t *testing.T) {
	s, dir := open(t)
	must(t, s, Message{From: Coder, Kind: Task, Text: "build it"})
	for _, m := range []Message{
		{From: Coder, Kind: Note, Text: "x", Stop: StopSteps},
		{From: Human, Kind: Task, Text: "x", Stop: StopTime},
		{From: Verifier, Kind: Question, Text: "x", Stop: StopSteps},
		{From: Verifier, Kind: Verdict, Verdict: "pass", Text: "x", Stop: StopTime},
		{From: Verifier, Kind: Progress, Stop: StopSteps},
		{From: System, Kind: Event, Text: "x", Stop: StopTime},
		{From: Verifier, Kind: Reply, Text: "x", Stop: "tokens"},
	} {
		if _, err := s.Append(m); err == nil || !strings.Contains(err.Error(), "stop") {
			t.Errorf("append %s from %s with stop %q = %v, want an error naming stop", m.Kind, m.From, m.Stop, err)
		}
	}
	for _, stop := range []string{StopSteps, StopTime} {
		got := must(t, s, Message{From: Verifier, Kind: Reply, Text: "I stopped.", Stop: stop})
		if got.Stop != stop {
			t.Errorf("stop = %q, want %q", got.Stop, stop)
		}
	}
	reopened, err := Open(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if all := reopened.After(0); len(all) != 3 || all[1].Stop != StopSteps || all[2].Stop != StopTime {
		t.Errorf("after reopening = %+v, want both stops kept", all)
	}
}

// Only a system event may say the screen was taken or came back (issue #124).
func TestOnlyASystemEventCarriesControl(t *testing.T) {
	s, dir := open(t)
	must(t, s, Message{From: Coder, Kind: Task, Text: "build it"})
	for _, m := range []Message{
		{From: Human, Kind: Note, Text: "x", Control: ControlReturned},
		{From: Coder, Kind: Task, Text: "x", Control: ControlTaken},
		{From: Verifier, Kind: Reply, Text: "x", Control: ControlReturned},
		{From: Verifier, Kind: Question, Text: "x", Control: ControlReturned},
		{From: System, Kind: Event, Text: "x", Control: "resume"},
	} {
		if _, err := s.Append(m); err == nil || !strings.Contains(err.Error(), "control") {
			t.Errorf("append %s from %s with control %q = %v, want an error naming control", m.Kind, m.From, m.Control, err)
		}
	}
	for _, c := range []string{ControlTaken, ControlReturned} {
		if got := must(t, s, Message{From: System, Kind: Event, Text: "human did something", Control: c}); got.Control != c {
			t.Errorf("control = %q, want %q", got.Control, c)
		}
	}
	reopened, err := Open(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	all := reopened.After(0)
	if len(all) != 3 || all[1].Control != ControlTaken || all[2].Control != ControlReturned {
		t.Errorf("after reopening = %+v, want both controls kept", all)
	}
	if all[2].StartsTurn() {
		t.Error("a returned event starts a turn by itself; the actor decides whether it resumes")
	}
}

func TestRepliesMustPointAtTheRightKind(t *testing.T) {
	s, _ := open(t)
	must(t, s, Message{From: Coder, Kind: Task, Text: "build"})
	must(t, s, Message{From: Verifier, Kind: Question, Text: "which scheme?"})
	if _, err := s.Append(Message{From: Coder, Kind: Answer, ReplyTo: 1, Text: "Release"}); err == nil {
		t.Fatal("answered a task")
	}
	if _, err := s.Append(Message{From: Coder, Kind: Accept, ReplyTo: 2}); err == nil {
		t.Fatal("accepted a question")
	}
	if _, err := s.Append(Message{From: Coder, Kind: Answer, ReplyTo: 9, Text: "x"}); err == nil {
		t.Fatal("replied to a message that does not exist")
	}
	must(t, s, Message{From: Coder, Kind: Answer, ReplyTo: 2, Text: "Release"})
}

// Issue #50: checkReplyLocked only skipped replyTo 0, so a negative one indexed msgs[-n].
func TestANegativeReplyToIsRefusedForEveryKind(t *testing.T) {
	s, _ := open(t)
	must(t, s, Message{From: Coder, Kind: Task, Text: "build it"})
	for _, m := range []Message{
		{From: Coder, Kind: Note, Text: "x", ReplyTo: -5},
		{From: Human, Kind: Task, Text: "x", ReplyTo: -1},
		{From: Coder, Kind: Accept, ReplyTo: -1},
		{From: Verifier, Kind: Progress, ReplyTo: -2},
	} {
		if _, err := s.Append(m); err == nil || !strings.Contains(err.Error(), "replyTo") {
			t.Errorf("append %s from %s with replyTo %d = %v, want an error naming replyTo", m.Kind, m.From, m.ReplyTo, err)
		}
	}
	if s.Len() != 1 {
		t.Fatalf("len = %d, want only the task", s.Len())
	}
}

func TestTurnBoundaries(t *testing.T) {
	// A human is always answered, a note included; the coder's note is
	// context, because its reply channel is its next agent_wait.
	starts := []Message{{From: Coder, Kind: Task}, {From: Human, Kind: Answer}, {From: Coder, Kind: Dispute}, {From: Human, Kind: Note}}
	for _, m := range starts {
		if !m.StartsTurn() {
			t.Errorf("%s %s should start a turn", m.From, m.Kind)
		}
	}
	nots := []Message{{From: Coder, Kind: Note}, {From: Verifier, Kind: Progress}, {From: System, Kind: Event}, {From: Coder, Kind: Accept}}
	for _, m := range nots {
		if m.StartsTurn() {
			t.Errorf("%s %s should not start a turn", m.From, m.Kind)
		}
	}
	ends := []Message{{From: Verifier, Kind: Verdict}, {From: Verifier, Kind: Question}, {From: Verifier, Kind: Reply}}
	for _, m := range ends {
		if !m.EndsTurn() {
			t.Errorf("%s %s should end a turn", m.From, m.Kind)
		}
	}
	if (Message{From: Verifier, Kind: Progress}).EndsTurn() {
		t.Error("progress should not end a turn")
	}
}

// The agreement rules of ADR 0006, walked end to end.
func TestVerdictAcceptedByTheCoder(t *testing.T) {
	s, _ := open(t)
	must(t, s, Message{From: Coder, Kind: Task, Text: "build"})
	v := must(t, s, Message{From: Verifier, Kind: Verdict, Verdict: "pass", Text: "built"})
	if got := s.Verdict(); got.Status != Proposed || got.Seq != v.Seq {
		t.Fatalf("after verdict: %+v", got)
	}
	must(t, s, Message{From: Coder, Kind: Accept, ReplyTo: v.Seq})
	got := s.Verdict()
	if got.Status != Accepted || got.AcceptedBy != Coder {
		t.Fatalf("after accept: %+v", got)
	}
	if _, err := s.Append(Message{From: Human, Kind: Dispute, ReplyTo: v.Seq, Text: "no"}); err == nil {
		t.Fatal("disputed an accepted verdict")
	}
}

func TestCoderDisputesUntilContestedThenAHumanCloses(t *testing.T) {
	s, _ := open(t)
	must(t, s, Message{From: Coder, Kind: Task, Text: "build"})
	v1 := must(t, s, Message{From: Verifier, Kind: Verdict, Verdict: "fail", Text: "wrong scheme"})
	must(t, s, Message{From: Coder, Kind: Dispute, ReplyTo: v1.Seq, Text: "you used Debug"})
	if got := s.Verdict(); got.Status != Proposed || got.Disputes != 1 {
		t.Fatalf("after first dispute: %+v", got)
	}
	v2 := must(t, s, Message{From: Verifier, Kind: Verdict, Verdict: "fail", Text: "still fails"})
	if got := s.Verdict(); got.Status != Proposed || got.Seq != v2.Seq {
		t.Fatalf("second verdict: %+v", got)
	}
	// Disputing the old verdict is refused: only the latest is open.
	if _, err := s.Append(Message{From: Coder, Kind: Dispute, ReplyTo: v1.Seq, Text: "x"}); err == nil {
		t.Fatal("disputed a superseded verdict")
	}
	must(t, s, Message{From: Coder, Kind: Dispute, ReplyTo: v2.Seq, Text: "look at step 4"})
	if got := s.Verdict(); got.Status != Contested || got.Disputes != 2 {
		t.Fatalf("after second dispute: %+v", got)
	}
	v3 := must(t, s, Message{From: Verifier, Kind: Verdict, Verdict: "fail", Text: "step 4 shows the crash"})
	if got := s.Verdict(); got.Status != Contested || got.Seq != v3.Seq {
		t.Fatalf("third verdict should be born contested: %+v", got)
	}
	_, err := s.Append(Message{From: Coder, Kind: Dispute, ReplyTo: v3.Seq, Text: "again"})
	if !errors.Is(err, ErrContested) {
		t.Fatalf("third coder dispute: %v", err)
	}
	// Issue #34: nor may it accept. ADR 0006 keeps a contested verdict open until a human closes
	// it; otherwise the party that lost the argument could skip the escalation it forces.
	if _, err := s.Append(Message{From: Coder, Kind: Accept, ReplyTo: v3.Seq}); !errors.Is(err, ErrContested) {
		t.Fatalf("coder accept of a contested verdict: %v, want ErrContested", err)
	}
	must(t, s, Message{From: Human, Kind: Accept, ReplyTo: v3.Seq})
	if got := s.Verdict(); got.Status != Accepted || got.AcceptedBy != Human {
		t.Fatalf("human accept of contested: %+v", got)
	}
}

func TestHumanDisputeRejectsAndOnlyAHumanCanAcceptAfter(t *testing.T) {
	s, _ := open(t)
	must(t, s, Message{From: Coder, Kind: Task, Text: "build"})
	v1 := must(t, s, Message{From: Verifier, Kind: Verdict, Verdict: "pass", Text: "fine"})
	must(t, s, Message{From: Human, Kind: Dispute, ReplyTo: v1.Seq, Text: "the window never opened"})
	if got := s.Verdict(); got.Status != Rejected {
		t.Fatalf("after human dispute: %+v", got)
	}
	if _, err := s.Append(Message{From: Coder, Kind: Accept, ReplyTo: v1.Seq}); !errors.Is(err, ErrContested) {
		t.Fatalf("coder overrode a human: %v", err)
	}
	v2 := must(t, s, Message{From: Verifier, Kind: Verdict, Verdict: "fail", Text: "you are right"})
	if got := s.Verdict(); got.Status != Contested || got.Seq != v2.Seq {
		t.Fatalf("verdict after human dispute should be contested: %+v", got)
	}
	if _, err := s.Append(Message{From: Coder, Kind: Dispute, ReplyTo: v2.Seq, Text: "x"}); !errors.Is(err, ErrContested) {
		t.Fatalf("coder dispute after human: %v", err)
	}
	must(t, s, Message{From: Human, Kind: Accept, ReplyTo: v2.Seq})
	if got := s.Verdict(); got.Status != Accepted || got.AcceptedBy != Human {
		t.Fatalf("human accept: %+v", got)
	}
}

// A crash mid-append leaves a torn final line; the run must still open.
func TestOpenDropsATornFinalLine(t *testing.T) {
	for _, torn := range []string{`{"seq":3,"from":"cod`, "{\"seq\":3,\n"} {
		s, dir := open(t)
		must(t, s, Message{From: Coder, Kind: Task, Text: "build"})
		must(t, s, Message{From: Human, Kind: Note, Text: "héllo"})
		path := filepath.Join(dir, fileName)
		good, _ := os.ReadFile(path)
		if err := os.WriteFile(path, append(append([]byte{}, good...), torn...), 0o644); err != nil {
			t.Fatal(err)
		}

		s2, err := Open(dir, 2)
		if err != nil {
			t.Fatalf("Open with torn line %q: %v", torn, err)
		}
		if s2.Len() != 2 {
			t.Fatalf("Len = %d, want the 2 whole messages", s2.Len())
		}
		if got, _ := os.ReadFile(path); string(got) != string(good) {
			t.Fatalf("file = %q, want the torn line cut off", got)
		}
		if m := must(t, s2, Message{From: Coder, Kind: Note, Text: "next"}); m.Seq != 3 {
			t.Fatalf("next seq = %d, want 3", m.Seq)
		}
		if s3, err := Open(dir, 2); err != nil || s3.Len() != 3 {
			t.Fatalf("reopen after append: %v, %d messages", err, s3.Len())
		}
	}
}

func TestOpenRefusesABadLineInTheMiddle(t *testing.T) {
	s, dir := open(t)
	must(t, s, Message{From: Coder, Kind: Task, Text: "build"})
	path := filepath.Join(dir, fileName)
	good, _ := os.ReadFile(path)
	bad := string(good) + "not json\n" + string(good)
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, 2); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("Open = %v, want a parse error naming line 2", err)
	}
	if got, _ := os.ReadFile(path); string(got) != bad {
		t.Error("Open changed a file it refused")
	}
}

func TestEvictClosesTheStoreAndGetReopensIt(t *testing.T) {
	root := t.TempDir()
	const id = "20260921-005642-e3b35b"
	if err := os.MkdirAll(filepath.Join(root, "runs", id), 0o755); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(root, 2)
	old, err := r.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	must(t, old, Message{From: Coder, Kind: Task, Text: "build"})

	waited := make(chan []Message)
	go func() { waited <- old.Wait(context.Background(), old.Len()) }()
	r.Evict(id)
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("Evict left a waiter blocked")
	}
	if _, err := old.Append(Message{From: Coder, Kind: Note, Text: "late"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Append to an evicted store = %v, want ErrClosed", err)
	}

	fresh, err := r.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if fresh == old || fresh.Len() != 1 {
		t.Fatalf("Get after Evict returned the old store or lost history (len %d)", fresh.Len())
	}
	var heard int
	r.Listen(func(string, Message) { heard++ })
	if m := must(t, fresh, Message{From: Coder, Kind: Note, Text: "again"}); m.Seq != 2 || heard != 1 {
		t.Fatalf("seq %d, heard %d; want 2 and 1", m.Seq, heard)
	}
	r.Evict("never-opened") // a no-op
}

func TestSubscribeSeesAppendsUntilRemoved(t *testing.T) {
	s, _ := open(t)
	var got []Kind
	off := s.Subscribe(func(m Message) { got = append(got, m.Kind) })
	must(t, s, Message{From: Coder, Kind: Task, Text: "a"})
	off()
	must(t, s, Message{From: Coder, Kind: Note, Text: "b"})
	if len(got) != 1 || got[0] != Task {
		t.Fatalf("got %v", got)
	}
}

func TestRegistrySharesStoresAndFansOut(t *testing.T) {
	root := t.TempDir()
	var verdicts []VerdictState
	r := NewRegistry(root, 2, WithOnVerdict(func(_ string, v VerdictState) { verdicts = append(verdicts, v) }))
	var heard []string
	r.Listen(func(runID string, m Message) { heard = append(heard, runID+":"+string(m.Kind)) })

	if _, err := r.Get("run-a"); err == nil {
		t.Fatal("Get manufactured a run that does not exist")
	}
	if err := os.MkdirAll(filepath.Join(root, "runs", "run-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := r.Get("run-a")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := r.Get("run-a")
	if again != a {
		t.Fatal("registry opened the same run twice")
	}
	must(t, a, Message{From: Coder, Kind: Task, Text: "x"})
	must(t, a, Message{From: Verifier, Kind: Verdict, Verdict: "pass", Text: "y"})
	if len(heard) != 2 || heard[1] != "run-a:verdict" {
		t.Fatalf("heard %v", heard)
	}
	if len(verdicts) != 1 || verdicts[0].Status != Proposed {
		t.Fatalf("verdicts %+v", verdicts)
	}
	ids, err := r.RunIDs()
	if err != nil || len(ids) != 1 || ids[0] != "run-a" {
		t.Fatalf("RunIDs = %v, %v", ids, err)
	}
	if _, err := os.Stat(filepath.Join(root, "runs", "run-a", fileName)); err != nil {
		t.Fatal(err)
	}
}
