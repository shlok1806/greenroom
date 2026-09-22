package machine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Sync through the host's real rsync, with ssh replaced by a local shell, so
// the quoting is tested by the programs that parse it: a state root with a
// space and a dest with shell syntax must both arrive intact.
func TestSyncSurvivesSpacesAndShellSyntax(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("no rsync on this host")
	}
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)

	bin := t.TempDir()
	argsFile := filepath.Join(bin, "ssh-args")
	// Like ssh: drop the options and host, join the rest, hand it to a shell.
	ssh := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argsFile + "'\n" +
		"while [ $# -gt 0 ]; do case \"$1\" in -i|-o) shift 2 ;; *) break ;; esac; done\n" +
		"shift\ncd \"$HOME\" && exec sh -c \"$*\"\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(ssh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	home := t.TempDir()
	t.Setenv("HOME", home)
	mgr.sshKey = filepath.Join(t.TempDir(), "state root", "id_ed25519")

	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "f"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := "my app $(touch pwned)"
	if _, err := mgr.Sync(context.Background(), mc.RunID, source, dest, nil); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if got, err := os.ReadFile(filepath.Join(home, dest, "f")); err != nil || string(got) != "hi" {
		t.Errorf("the file did not arrive at %q: %q, %v", dest, got, err)
	}
	if _, err := os.Stat(filepath.Join(home, "pwned")); err == nil {
		t.Error("the dest was run as shell syntax on the guest")
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ssh never ran: %v", err)
	}
	if !strings.Contains(string(args), "-i\n"+mgr.sshKey+"\n") {
		t.Errorf("ssh did not get the key path whole:\n%s", args)
	}
}
