package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func finishMsg(outcome string) Message {
	f := Finish{Outcome: outcome, Summary: "Each pays shows the split."}
	return Message{From: System, Kind: Event, Text: FinishText(f), Finish: &f}
}

func TestOnlyASystemEventCarriesFinish(t *testing.T) {
	s, _ := open(t)
	f := &Finish{Outcome: OutcomeUnverified, Summary: "x"}
	for _, m := range []Message{
		{From: Coder, Kind: Note, Text: "x", Finish: f},
		{From: Human, Kind: Task, Text: "x", Finish: f},
		{From: Verifier, Kind: Reply, Text: "x", Finish: f},
	} {
		if _, err := s.Append(m); err == nil || !strings.Contains(err.Error(), "finish") {
			t.Errorf("append %s from %s with finish = %v, want an error naming finish", m.Kind, m.From, err)
		}
	}
}

func TestAFinishIsStampedWithItsMessageTime(t *testing.T) {
	s, dir := open(t)
	in := finishMsg(OutcomeAbandoned)
	m := must(t, s, in)
	if m.Finish.At.IsZero() || !m.Finish.At.Equal(m.At) {
		t.Errorf("finish at %v, message at %v", m.Finish.At, m.At)
	}
	if !in.Finish.At.IsZero() {
		t.Error("Append changed the caller's finish")
	}
	if got := s.Finished(); got == nil || got.Outcome != OutcomeAbandoned {
		t.Errorf("Finished() = %+v", got)
	}
	// It survives a reload, like every field.
	s2, err := Open(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Finished(); got == nil || !got.At.Equal(m.At) {
		t.Errorf("after reload Finished() = %+v", got)
	}
}

func TestAHumansAcceptanceIsEnoughForVerified(t *testing.T) {
	s, _ := open(t)
	must(t, s, Message{From: Coder, Kind: Task, Text: "check"})
	v := must(t, s, Message{From: Verifier, Kind: Verdict, Verdict: "pass", Text: "ok"})
	must(t, s, Message{From: Human, Kind: Accept, ReplyTo: v.Seq})
	must(t, s, finishMsg(OutcomeVerified))
}

func TestARejectedPassIsNotVerified(t *testing.T) {
	s, _ := open(t)
	must(t, s, Message{From: Coder, Kind: Task, Text: "check"})
	v := must(t, s, Message{From: Verifier, Kind: Verdict, Verdict: "pass", Text: "ok"})
	must(t, s, Message{From: Human, Kind: Dispute, ReplyTo: v.Seq, Text: "that is the wrong window"})
	must(t, s, Message{From: Verifier, Kind: Reply, Text: "I see."})
	_, err := s.Append(finishMsg(OutcomeVerified))
	if err == nil || !strings.Contains(err.Error(), "is rejected, not accepted") {
		t.Errorf("finish = %v, want a refusal saying the pass was rejected", err)
	}
	must(t, s, finishMsg(OutcomeUnverified))
}

func TestANoVerifierNoticeOwesNoTurn(t *testing.T) {
	s, _ := open(t)
	must(t, s, Message{From: Coder, Kind: Task, Text: "check"})
	if _, err := s.Append(finishMsg(OutcomeUnverified)); err == nil {
		t.Fatal("finished while a task was owed an answer")
	}
	must(t, s, Message{From: System, Kind: Event, Text: "no verifier is configured on this daemon; nobody will answer this task"})
	must(t, s, finishMsg(OutcomeUnverified))
}

func TestOldTranscriptsLoadWithNoFinish(t *testing.T) {
	var m Message
	if err := json.Unmarshal([]byte(`{"seq":1,"at":"2026-09-18T10:00:00Z","from":"system","kind":"event","text":"machine is ready"}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.Finish != nil {
		t.Errorf("finish = %+v", m.Finish)
	}
	data, _ := json.Marshal(Message{Seq: 1, At: time.Now(), From: System, Kind: Event, Text: "x"})
	if strings.Contains(string(data), "finish") {
		t.Errorf("a message without a finish writes one: %s", data)
	}
}
