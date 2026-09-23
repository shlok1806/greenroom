package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// openLive starts GET /screen/live and returns the response; cancel ends the request.
func (h *harness) openLive(runID string) (*http.Response, context.CancelFunc) {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.url+"/api/runs/"+runID+"/screen/live", nil)
	if err != nil {
		h.t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		h.t.Fatalf("GET screen/live: %v", err)
	}
	h.t.Cleanup(func() { cancel(); _ = res.Body.Close() })
	return res, cancel
}

func readFrame(t *testing.T, r io.Reader) (byte, []byte) {
	t.Helper()
	var head [5]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		t.Fatalf("read a frame header: %v", err)
	}
	payload := make([]byte, binary.BigEndian.Uint32(head[1:]))
	if _, err := io.ReadFull(r, payload); err != nil {
		t.Fatalf("read a frame payload: %v", err)
	}
	return head[0], payload
}

// expectLiveOpening reads HELLO, FORMAT, then frames until the first VIDEO, which must be a keyframe.
func expectLiveOpening(t *testing.T, body io.Reader) {
	t.Helper()
	typ, payload := readFrame(t, body)
	var hello struct {
		Screen machine.Screen `json:"screen"`
	}
	if typ != machine.ScreenHello || json.Unmarshal(payload, &hello) != nil || hello.Screen.Width != 1024 {
		t.Fatalf("first frame is 0x%02x %q, want HELLO", typ, payload)
	}
	if typ, payload := readFrame(t, body); typ != machine.ScreenFormat || string(payload) != string(testsupport.FakeScreenFormat) {
		t.Fatalf("second frame is 0x%02x %q, want FORMAT", typ, payload)
	}
	for {
		typ, payload := readFrame(t, body)
		if typ == machine.ScreenFormat {
			continue
		}
		if typ != machine.ScreenVideo || payload[0]&1 == 0 {
			t.Fatalf("first picture is 0x%02x flags %d, want a keyframe", typ, payload[0])
		}
		return
	}
}

func TestLiveScreenStreamsFrames(t *testing.T) {
	// The live screen arms its idle stop when it starts, so a short idle can stop it on a loaded
	// host before the viewer subscribes. A second still stops it promptly once the client leaves.
	h := newHarness(t, machine.WithScreenIdle(time.Second))
	runID := h.ready()

	res, cancel := h.openLive(runID)
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/x-greenroom-screen" {
		t.Errorf("Content-Type = %q", ct)
	}
	expectLiveOpening(t, res.Body)
	for range 5 {
		if typ, _ := readFrame(t, res.Body); typ != machine.ScreenVideo && typ != machine.ScreenFormat {
			t.Fatalf("the stream carried type 0x%02x; only HELLO, FORMAT and VIDEO belong on it", typ)
		}
	}

	// A client that goes away is a viewer that left: the helper stops, and the next client starts a new one.
	cancel()
	deadline := time.Now().Add(10 * time.Second)
	for {
		time.Sleep(1500 * time.Millisecond) // longer than the idle time, so a stream with no viewer has stopped
		again, leave := h.openLive(runID)
		expectLiveOpening(t, again.Body)
		leave()
		if testsupport.ServeStarts(t, h.control) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the helper kept running after its only client went away")
		}
	}
}

func TestLiveScreenRefusesAMachineThatIsNotReady(t *testing.T) {
	h := newHarness(t)
	testsupport.Flag(t, h.control, "agent-down")
	booting := h.create()
	if code, body := h.status(http.MethodGet, "/api/runs/"+booting+"/screen/live", nil); code != http.StatusConflict {
		t.Errorf("a booting machine answered %d: %s", code, body)
	}
}

func TestLiveScreenRefusesADestroyedMachine(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	if err := h.mgr.Destroy(context.Background(), runID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if code, body := h.status(http.MethodGet, "/api/runs/"+runID+"/screen/live", nil); code != http.StatusConflict {
		t.Errorf("a destroyed machine answered %d: %s", code, body)
	}
	if code, _ := h.status(http.MethodGet, "/api/runs/no-such-run/screen/live", nil); code != http.StatusNotFound {
		t.Errorf("an unknown run answered %d, want 404", code)
	}
}
