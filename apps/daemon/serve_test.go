package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// pendingRoot is a root holding one undestroyed run whose last message, a human note, has
// no reply yet: any verifier that starts on this root answers it at once.
func pendingRoot(t *testing.T) (root, conversation string) {
	t.Helper()
	root = t.TempDir()
	run := filepath.Join(root, "runs", "20260923-000000-qa00000000000063")
	if err := os.MkdirAll(run, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"runId":"20260923-000000-qa00000000000063","image":"x","createdAt":"2026-09-23T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(run, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	conversation = filepath.Join(run, "conversation.jsonl")
	note := `{"seq":1,"at":"2026-09-23T00:00:01Z","from":"human","kind":"note","text":"screenshot"}` + "\n"
	if err := os.WriteFile(conversation, []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, conversation
}

// serveAsync runs serve and returns its error, or fails the test if it is still serving after d.
func serveAsync(t *testing.T, d time.Duration, args ...string) error {
	t.Helper()
	args = append(args, "-tart", "/usr/bin/false", "-verifier", "manual",
		"-env-file", filepath.Join(t.TempDir(), "none.env"))
	errCh := make(chan error, 1)
	go func() { errCh <- serve(args) }()
	select {
	case err := <-errCh:
		return err
	case <-time.After(d):
		t.Fatalf("serve %v is still running after %s; it should have refused to start", args, d)
		return nil
	}
}

func assertUntouched(t *testing.T, conversation string) {
	t.Helper()
	time.Sleep(300 * time.Millisecond) // an actor started by mistake answers within milliseconds
	b, err := os.ReadFile(conversation)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "\n"); n != 1 {
		t.Fatalf("a daemon that never served wrote into the transcript (%d lines):\n%s", n, b)
	}
}

// Issue #63: a daemon that cannot bind its address must fail before it starts a verifier, or
// it answers live runs behind the running daemon's back and duplicates seq numbers.
func TestServeThatCannotBindTouchesNothing(t *testing.T) {
	root, conversation := pendingRoot(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = busy.Close() })

	err = serveAsync(t, 5*time.Second, "-addr", busy.Addr().String(), "-root", root)
	if err == nil || !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("serve on a taken address = %v, want a bind error", err)
	}
	assertUntouched(t, conversation)
	if _, err := os.Stat(filepath.Join(root, "state.json")); err == nil {
		t.Fatal("a daemon that never served wrote state.json")
	}
}

// Issue #63: two daemons on one root, even on different ports, would share its machines and transcripts.
func TestServeRefusesARootAnotherDaemonHolds(t *testing.T) {
	root, conversation := pendingRoot(t)
	release, err := lockRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)

	err = serveAsync(t, 5*time.Second, "-addr", "127.0.0.1:0", "-root", root)
	if err == nil || !strings.Contains(err.Error(), "another greenroom daemon") || !strings.Contains(err.Error(), root) {
		t.Fatalf("serve on a locked root = %v, want it refused naming the root", err)
	}
	assertUntouched(t, conversation)
}

func TestLockRootIsReleasedForTheNextDaemon(t *testing.T) {
	root := t.TempDir()
	release, err := lockRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockRoot(root); err == nil {
		t.Fatal("a second lock on the same root succeeded")
	}
	release()
	again, err := lockRoot(root)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	again()
}

// unsetEnv clears keys for the test and restores them after, including anything loadEnvFile set meanwhile.
func unsetEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		old, had := os.LookupEnv(k)
		_ = os.Unsetenv(k)
		t.Cleanup(func() {
			if had {
				_ = os.Setenv(k, old)
			} else {
				_ = os.Unsetenv(k)
			}
		})
	}
}

// ADR 0021: a public host with no token, or a guessable one, would open every machine to the tunnel.
func TestServeRefusesAPublicHostWithoutALongToken(t *testing.T) {
	unsetEnv(t, "GREENROOM_PUBLIC_HOST", "GREENROOM_TOKEN")
	for _, token := range []string{"", "short", "   " + strings.Repeat("x", 31) + "   "} {
		root, conversation := pendingRoot(t)
		if token != "" {
			t.Setenv("GREENROOM_TOKEN", token)
		}
		err := serveAsync(t, 5*time.Second, "-addr", "127.0.0.1:0", "-root", root, "-public-host", "gr.example.com")
		if err == nil || !strings.Contains(err.Error(), "GREENROOM_TOKEN") || !strings.Contains(err.Error(), "openssl rand -hex 32") {
			t.Fatalf("token %q: serve = %v, want a refusal saying how to make a token", token, err)
		}
		if strings.TrimSpace(token) != "" && strings.Contains(err.Error(), strings.TrimSpace(token)) {
			t.Errorf("the refusal repeats the token: %v", err)
		}
		assertUntouched(t, conversation)
		if _, err := os.Stat(filepath.Join(root, "daemon.lock")); err == nil {
			t.Error("the refused daemon locked the root")
		}
	}
	root, _ := pendingRoot(t)
	err := serveAsync(t, 5*time.Second, "-addr", "127.0.0.1:0", "-root", root, "-public-host", "https://gr.example.com/")
	if err == nil || !strings.Contains(err.Error(), "bare hostname") {
		t.Errorf("a URL as public host: %v, want it refused", err)
	}
}

// The env file names the public host and token when the flags do not, as it does GREENROOM_IMAGE.
func TestServeTakesThePublicHostAndTokenFromTheEnvFile(t *testing.T) {
	unsetEnv(t, "GREENROOM_PUBLIC_HOST", "GREENROOM_TOKEN")
	const token = "0123456789abcdef0123456789abcdef"
	envFile := filepath.Join(t.TempDir(), "remote.env")
	if err := os.WriteFile(envFile, []byte("GREENROOM_PUBLIC_HOST=gr.example.com\nGREENROOM_TOKEN="+token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveUntil(ctx, []string{"-addr", addr, "-root", t.TempDir(), "-tart", "/usr/bin/false", "-verifier", "manual", "-env-file", envFile})
	}()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	})

	get := func(auth string) int {
		req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/healthz", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "gr.example.com"
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	deadline := time.Now().Add(5 * time.Second)
	for get("Bearer "+token) != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("the public host with the env file's token was never served")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if code := get(""); code != http.StatusUnauthorized {
		t.Errorf("the public host without a token: %d, want 401", code)
	}
}

// Issue #103: serve sweeps orphaned run clones at start, in the background, and says so;
// -sweep-orphans-after 0 keeps them.
func TestServeSweepsOrphanedRunClonesAtStart(t *testing.T) {
	for _, tc := range []struct {
		after string
		swept bool
	}{{"6h", true}, {"0", false}} {
		t.Run(tc.after, func(t *testing.T) {
			bin, control := testsupport.FakeTart(t)
			t.Setenv("TART_HOME", t.TempDir())
			const orphan = "greenroom-20260923-215022-10d218794d3d8da9"
			list := `[{"Source":"local","Name":"` + orphan + `","State":"stopped"},{"Source":"local","Name":"greenroom-lean-a","State":"stopped"}]`
			if err := os.WriteFile(filepath.Join(control, "list.json"), []byte(list), 0o644); err != nil {
				t.Fatal(err)
			}
			probe, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := probe.Addr().String()
			_ = probe.Close()
			ctx, stop := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				done <- serveUntil(ctx, []string{"-addr", addr, "-root", t.TempDir(), "-tart", bin, "-verifier", "manual",
					"-env-file", filepath.Join(t.TempDir(), "none.env"), "-sweep-orphans-after", tc.after})
			}()
			deleted := func() bool { return strings.Contains(testsupport.Calls(t, control), "delete "+orphan) }
			wait := 500 * time.Millisecond // the sweep runs at start: long enough to see one that should not happen
			if tc.swept {
				wait = 5 * time.Second
			}
			for deadline := time.Now().Add(wait); !deleted() && time.Now().Before(deadline); {
				time.Sleep(20 * time.Millisecond)
			}
			stop()
			if err := <-done; err != nil {
				t.Errorf("serve: %v", err)
			}
			if deleted() != tc.swept {
				t.Errorf("deleted %s = %v, want %v\ncalls:\n%s", orphan, deleted(), tc.swept, testsupport.Calls(t, control))
			}
			if strings.Contains(testsupport.Calls(t, control), "delete greenroom-lean-a") {
				t.Error("the sweep deleted an image")
			}
		})
	}
}
