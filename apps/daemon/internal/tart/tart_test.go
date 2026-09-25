package tart

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// argsBin is a stand-in tart that appends its arguments to the returned log
// and then runs body.
func argsBin(t *testing.T, body string) (*Client, string) {
	t.Helper()
	log := filepath.Join(t.TempDir(), "args")
	return sessionBin(t, `printf '%s\n' "$*" >> '`+log+"'\n"+body), log
}

func TestSimpleCommandsPassTheirArguments(t *testing.T) {
	c, log := argsBin(t, `[ "$1" = ip ] && echo '  192.168.64.3  '; exit 0`)
	ctx := context.Background()
	if err := c.Clone(ctx, "base", "vm"); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if err := c.Stop(ctx, "vm"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := c.Delete(ctx, "vm"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	ip, err := c.IP(ctx, "vm")
	if err != nil || ip != "192.168.64.3" {
		t.Fatalf("IP = %q, %v; want the trimmed address", ip, err)
	}
	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if want := "clone base vm\nstop vm\ndelete vm\nip vm\n"; string(got) != want {
		t.Errorf("tart was called with\n%s\nwant\n%s", got, want)
	}
}

func TestSimpleCommandsReportWhatTartPrinted(t *testing.T) {
	c, _ := argsBin(t, `echo 'the specified VM "vm" does not exist' >&2; exit 2`)
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"clone":  func() error { return c.Clone(ctx, "base", "vm") },
		"stop":   func() error { return c.Stop(ctx, "vm") },
		"delete": func() error { return c.Delete(ctx, "vm") },
		"ip":     func() error { _, err := c.IP(ctx, "vm"); return err },
		"list":   func() error { _, err := c.List(ctx); return err },
	} {
		err := call()
		if err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Errorf("%s: err = %v, want tart's message", name, err)
		}
	}
}

func TestListParsesTartsJSON(t *testing.T) {
	c, _ := argsBin(t, `echo '[{"Source":"local","Name":"vm","State":"running","Size":34}]'`)
	vms, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(vms) != 1 || vms[0] != (VM{Source: "local", Name: "vm", State: "running"}) {
		t.Errorf("List = %+v", vms)
	}
}

func TestListRejectsOutputThatIsNotJSON(t *testing.T) {
	c, _ := argsBin(t, `echo 'NAME STATE'`)
	if _, err := c.List(context.Background()); err == nil || !strings.Contains(err.Error(), "parse tart list") {
		t.Errorf("List err = %v, want a parse error", err)
	}
}

// tart 2.37 forwards a guest's exit code and prints nothing of its own, so a
// guest that complains in tart-like words is still a result, not an error.
func TestExecTellsTartFailuresFromGuestExits(t *testing.T) {
	for _, c := range []struct {
		name, body string
		tartErr    bool
		code       int
	}{
		{"guest docker down", `echo 'docker daemon is not running' >&2; exit 1`, false, 1},
		{"guest mentions the agent", `echo 'guest agent: cannot reach' >&2; exit 2`, false, 2},
		{"guest exits 0", `echo ok`, false, 0},
		{"tart line then more guest output", `printf 'VM "x" is not running\nmore\n' >&2; exit 1`, false, 1},
		{"tart words with a guest exit code", `echo 'VM "vm" is not running' >&2; exit 3`, false, 3},
		{"vm not running", `echo 'VM "vm" is not running' >&2; exit 2`, true, 0},
		{"vm missing", `echo 'the specified VM "vm" does not exist' >&2; exit 2`, true, 0},
		{"agent unreachable", `echo 'Failed to connect to the VM using its control socket: unavailable, is the Tart Guest Agent running?' >&2; exit 1`, true, 0},
		{"usage", `echo "Error: Missing expected argument '<name>'" >&2; exit 64`, true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			bin, _ := argsBin(t, c.body)
			res, err := bin.Exec(context.Background(), "vm", "true")
			if c.tartErr {
				if err == nil {
					t.Fatalf("a tart failure was reported as exit %d", res.ExitCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("a guest exit was reported as a tart failure: %v", err)
			}
			if res.ExitCode != c.code {
				t.Errorf("ExitCode = %d, want %d", res.ExitCode, c.code)
			}
		})
	}
}

func TestExecPassesTheCommandAfterTheVMName(t *testing.T) {
	c, log := argsBin(t, `echo out; echo err >&2`)
	res, err := c.Exec(context.Background(), "vm", "sh", "-c", "echo hi")
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "out\n" || res.Stderr != "err\n" {
		t.Errorf("Exec = %+v", res)
	}
	if got, _ := os.ReadFile(log); string(got) != "exec vm sh -c echo hi\n" {
		t.Errorf("tart was called with %q", got)
	}
}

// ExecInputTo puts -i before the VM name and hands the guest command stdin to its end.
func TestExecInputToAttachesStdin(t *testing.T) {
	c, log := argsBin(t, `cat; echo err >&2; exit 3`)
	var stdout, stderr strings.Builder
	code, err := c.ExecInputTo(context.Background(), strings.NewReader("line 1\nline 2\n"), &stdout, &stderr, "vm", "/bin/sh", "-s", "x")
	if err != nil || code != 3 {
		t.Fatalf("ExecInputTo = %d, %v; want the guest's exit 3", code, err)
	}
	if stdout.String() != "line 1\nline 2\n" || stderr.String() != "err\n" {
		t.Errorf("stdout %q, stderr %q", stdout.String(), stderr.String())
	}
	if got, _ := os.ReadFile(log); string(got) != "exec -i vm /bin/sh -s x\n" {
		t.Errorf("tart was called with %q", got)
	}
}

func TestExecStopsWhenTheContextEnds(t *testing.T) {
	c, _ := argsBin(t, `exec sleep 30`)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Exec(ctx, "vm", "true")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the context's error", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("Exec took %s to notice the context ended", time.Since(start))
	}
}

func TestExecReportsAMissingBinary(t *testing.T) {
	c := &Client{Bin: filepath.Join(t.TempDir(), "no-tart")}
	if _, err := c.Exec(context.Background(), "vm", "true"); err == nil {
		t.Error("Exec with no tart binary returned no error")
	}
}
