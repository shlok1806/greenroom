package machine

import (
	"strings"
	"testing"
)

func TestShellQuote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "work/app", `'work/app'`},
		{"empty", "", `''`},
		{"space", "my dir", `'my dir'`},
		{"single quote", "it's", `'it'"'"'s'`},
		{"two quotes", "'a'", `''"'"'a'"'"''`},
		{"newline", "a\nb", "'a\nb'"},
		{"semicolon and backtick", "a; `id`", "'a; `id`'"},
		{"double quote", `say "hi"`, `'say "hi"'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shellQuote(tt.in); got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// A quoted string must survive a round trip through sh -c, including the
// characters that would otherwise end the command.
func TestShellQuoteIsSafeInSh(t *testing.T) {
	for _, in := range []string{"plain", "my dir", "it's", "a; echo pwned", "$(id)", "`id`", `back\slash`} {
		out := runSh(t, "printf %s "+shellQuote(in))
		if out != in {
			t.Errorf("sh round trip of %q gave %q", in, out)
		}
	}
}

func TestRsyncSummary(t *testing.T) {
	tests := []struct {
		name  string
		stats string
		want  string
	}{
		{"empty", "", ""},
		{"unrelated lines only", "sending incremental file list\n./\nfoo.txt\n", ""},
		{
			name: "real rsync stats",
			stats: "Number of files: 64 (reg: 48, dir: 16)\n" +
				"Number of created files: 48\n" +
				"Number of regular files transferred: 48\n" +
				"Total file size: 190,164 bytes\n" +
				"Total transferred file size: 190,164 bytes\n" +
				"sent 50,000 bytes  received 900 bytes\n",
			want: "Number of files: 64 (reg: 48, dir: 16); " +
				"Number of regular files transferred: 48; Total transferred file size: 190,164 bytes",
		},
		{"trailing whitespace is trimmed", "Number of files: 2   \n", "Number of files: 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rsyncSummary(tt.stats); got != tt.want {
				t.Errorf("rsyncSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTruncatedForLog(t *testing.T) {
	const max = 64 * 1024
	t.Run("short output is unchanged", func(t *testing.T) {
		in := ExecResult{Stdout: "hi", Stderr: "boom", ExitCode: 2}
		got := truncatedForLog(in)
		if got != in {
			t.Errorf("short result changed: %+v", got)
		}
	})
	t.Run("output at the limit is unchanged", func(t *testing.T) {
		in := ExecResult{Stdout: strings.Repeat("a", max)}
		if got := truncatedForLog(in); got.Stdout != in.Stdout {
			t.Errorf("output at the limit was truncated: %d bytes", len(got.Stdout))
		}
	})
	t.Run("long output is truncated on both streams", func(t *testing.T) {
		in := ExecResult{Stdout: strings.Repeat("a", max+1), Stderr: strings.Repeat("b", max+100)}
		got := truncatedForLog(in)
		for name, s := range map[string]string{"stdout": got.Stdout, "stderr": got.Stderr} {
			if !strings.HasSuffix(s, "\n...[truncated]") {
				t.Errorf("%s has no truncation marker", name)
			}
			if len(s) != max+len("\n...[truncated]") {
				t.Errorf("%s is %d bytes, want %d", name, len(s), max+len("\n...[truncated]"))
			}
		}
	})
	t.Run("the caller's result is not modified", func(t *testing.T) {
		in := ExecResult{Stdout: strings.Repeat("a", max+1)}
		_ = truncatedForLog(in)
		if len(in.Stdout) != max+1 {
			t.Error("truncatedForLog modified its argument")
		}
	})
}

func TestNewRunID(t *testing.T) {
	id := newRunID()
	// 20060102-150405 plus a dash plus sixteen hex characters.
	const suffix = 16
	if len(id) != len("20060102-150405")+1+suffix {
		t.Fatalf("runId %q has length %d, want %d", id, len(id), len("20060102-150405")+1+suffix)
	}
	parts := strings.Split(id, "-")
	if len(parts) != 3 {
		t.Fatalf("runId %q does not have three parts", id)
	}
	for _, r := range parts[2] {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("runId %q has a non-hex suffix", id)
		}
	}
	// Many ids in the same second must still be distinct.
	seen := map[string]bool{}
	for i := 0; i < 20000; i++ {
		got := newRunID()
		if seen[got] {
			t.Fatalf("newRunID returned %q twice in %d calls", got, i+1)
		}
		seen[got] = true
	}
}

func TestGuestDest(t *testing.T) {
	ok := map[string]string{
		"work/app":        "work/app",
		"work/./app":      "work/app",
		"work/sub/../app": "work/app",
		"app":             "app",
	}
	for in, want := range ok {
		got, err := guestDest(in)
		if err != nil {
			t.Errorf("guestDest(%q) refused a path inside the home: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("guestDest(%q) = %q, want %q", in, got, want)
		}
	}
	bad := []string{"/tmp/escaped", "..", "../sibling", "../../../tmp/escaped", "work/../../etc", ".", ""}
	for _, in := range bad {
		if got, err := guestDest(in); err == nil {
			t.Errorf("guestDest(%q) = %q, want a refusal", in, got)
		}
	}
}
