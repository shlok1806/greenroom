package machine

import (
	"strings"
	"testing"
)

// An overflowing window drops the oldest bytes and reports how many.
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

func TestCleanTTYRepairsBrokenBytes(t *testing.T) {
	got := cleanTTY("ok \xff\xfe done")
	if !strings.HasPrefix(got, "ok ") || !strings.HasSuffix(got, " done") {
		t.Errorf("cleanTTY mangled the readable text: %q", got)
	}
	if !strings.ContainsRune(got, '�') {
		t.Errorf("the broken bytes were not replaced: %q", got)
	}
}

// Text read in arbitrary pieces must equal the text read whole.
func TestAReadNeverSplitsASequenceOrACharacter(t *testing.T) {
	whole := "a\x1b[31mred\x1b[0m €uro\r\nend\x1b]0;title\x07!"
	pieces := []string{"a\x1b[3", "1mred\x1b[0m \xe2\x82", "\xacuro\r", "\nend\x1b]0;ti", "tle\x07!"}

	s := newStream(1024)
	var off int64
	var got strings.Builder
	for i, p := range pieces {
		if _, err := s.Write([]byte(p)); err != nil {
			t.Fatal(err)
		}
		text, next, _, _ := s.readText(off, 1024, true)
		if strings.ContainsRune(text, '�') {
			t.Errorf("read %d split a character: %q", i, text)
		}
		got.WriteString(text)
		off = next
	}
	if want := cleanTTY(whole); got.String() != want {
		t.Errorf("read in pieces gave %q, want %q", got.String(), want)
	}
}

// A capped read is split the same way, since the cap can land anywhere.
func TestACappedReadHoldsBackItsIncompleteTail(t *testing.T) {
	s := newStream(1024)
	if _, err := s.Write([]byte("ab€cd")); err != nil {
		t.Fatal(err)
	}
	text, next, pending, _ := s.readText(0, 3, false)
	if text != "ab" || next != 2 || pending != 5 {
		t.Errorf("capped read gave %q next %d pending %d, want \"ab\" 2 5", text, next, pending)
	}
	text, _, _, _ = s.readText(next, 1024, false)
	if text != "€cd" {
		t.Errorf("the next read gave %q, want \"€cd\"", text)
	}
}

// Once nothing more can arrive, an incomplete tail is all there is, and it is
// handed over rather than held forever.
func TestAFinishedStreamReturnsItsTail(t *testing.T) {
	s := newStream(1024)
	if _, err := s.Write([]byte("done\r")); err != nil {
		t.Fatal(err)
	}
	if text, _, _, _ := s.readText(0, 1024, false); text != "done\n" {
		t.Errorf("a finished stream gave %q, want its tail too", text)
	}
}
