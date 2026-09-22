package machine

import (
	"strings"
	"testing"
)

// The window is what keeps a runaway build out of the daemon's memory. When
// it overflows the oldest bytes go, and a caller that was behind them is told
// how many, because a gap it cannot see is worse than no output at all.
func TestTheOutputWindowDropsTheOldestAndSaysSo(t *testing.T) {
	s := newStream(10)
	if _, err := s.Write([]byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write([]byte("abcde")); err != nil {
		t.Fatal(err)
	}

	// 15 bytes written into a 10 byte window: the first 5 are gone.
	data, next, pending, dropped := s.read(0, 1024)
	if string(data) != "56789abcde" {
		t.Errorf("read %q, want the last ten bytes", data)
	}
	if dropped != 5 {
		t.Errorf("dropped %d, want 5", dropped)
	}
	if next != 15 {
		t.Errorf("next is %d, want 15", next)
	}
	if pending != 0 {
		t.Errorf("pending is %d, want 0", pending)
	}
}

// Reads are by position, so two in a row never hand back the same bytes.
func TestReadingContinuesWhereTheLastReadStopped(t *testing.T) {
	s := newStream(1024)
	if _, err := s.Write([]byte("hello ")); err != nil {
		t.Fatal(err)
	}
	first, next, _, _ := s.read(0, 1024)
	if string(first) != "hello " {
		t.Fatalf("first read gave %q", first)
	}
	if _, err := s.Write([]byte("world")); err != nil {
		t.Fatal(err)
	}
	second, _, pending, dropped := s.read(next, 1024)
	if string(second) != "world" {
		t.Errorf("second read gave %q, want only the new bytes", second)
	}
	if pending != 0 || dropped != 0 {
		t.Errorf("pending %d dropped %d, want 0 and 0", pending, dropped)
	}
}

// A read is capped so one tool result cannot carry the whole window, and what
// is left over is reported rather than quietly dropped.
func TestAReadIsCappedAndReportsWhatIsLeft(t *testing.T) {
	s := newStream(1024)
	if _, err := s.Write([]byte(strings.Repeat("x", 100))); err != nil {
		t.Fatal(err)
	}
	data, next, pending, _ := s.read(0, 30)
	if len(data) != 30 {
		t.Errorf("read %d bytes, want the 30 byte cap", len(data))
	}
	if next != 30 {
		t.Errorf("next is %d, want 30", next)
	}
	if pending != 70 {
		t.Errorf("pending is %d, want 70", pending)
	}
}

// A pty carries what a terminal would act on. A model reading build output
// gets nothing from it but noise, so it goes before the text is handed on.
func TestCleanTTYLeavesTheTextAndDropsTheTerminalCodes(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"colour", "\x1b[32mPASS\x1b[0m tests", "PASS tests"},
		{"window title", "\x1b]0;admin@guest\x07ready", "ready"},
		{"clear line", "building\x1b[2K\x1b[1Gdone", "buildingdone"},
		{"pty line endings", "one\r\ntwo\r\n", "one\ntwo\n"},
		{"progress redraw", "50%\r100%", "50%\n100%"},
		{"bracketed paste", "\x1b[?2004hls\x1b[?2004l", "ls"},
		{"plain text is untouched", "swift build\n", "swift build\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := cleanTTY(c.in); got != c.want {
				t.Errorf("cleanTTY(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// Output has to be valid UTF-8 to travel in a tool result, and a build can
// print anything at all.
func TestCleanTTYRepairsBrokenBytes(t *testing.T) {
	got := cleanTTY("ok \xff\xfe done")
	if !strings.HasPrefix(got, "ok ") || !strings.HasSuffix(got, " done") {
		t.Errorf("cleanTTY mangled the readable text: %q", got)
	}
	for i, r := range got {
		if r == '�' {
			return
		}
		_ = i
	}
	t.Errorf("the broken bytes were not replaced: %q", got)
}
