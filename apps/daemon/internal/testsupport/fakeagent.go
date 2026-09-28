package testsupport

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/guestagent"
)

// fakeAgentEnv makes a test binary act as `greenroom-input --agent` (daemon ADR 0005) instead of
// running tests. The fake tart re-executes the test binary with it set to the control directory,
// as it does for the live screen (fakescreen.go).
const fakeAgentEnv = "GREENROOM_FAKE_AGENT"

// FakeAgentCaps is what the fake agent's HELLO lists: the ops of daemon ADR 0005 point 12 and
// the toolkit ops of daemon ADR 0006.
var FakeAgentCaps = []string{"screen", "capture", "ui", "desktop", "input", "sh",
	"refs", "snapshot", "find", "press", "type", "setValue", "key", "scroll", "waitFor", "expect"}

func init() {
	if dir := os.Getenv(fakeAgentEnv); dir != "" {
		os.Exit(serveFakeAgent(dir, os.Stdin, os.Stdout))
	}
}

// serveFakeAgent speaks the agent's side of the channel. It says HELLO (version 9, protocol 1,
// every permission granted, the screen from `screen`, caps FakeAgentCaps), answers PING with
// PONG, and answers each REQUEST on its own goroutine (inputs one at a time). Control files:
//
//	agent-<op>.json     the answer to every <op> request, canned: {"result": ...} or
//	                    {"error": {"code", "message", "retryable", "detail"}}. A canned capture
//	                    result still sends shot.b64 as its BLOB when that file exists. Toolkit ops
//	                    (snapshot, find, press, ...) have no built-in answer: without their file
//	                    they answer an internal error saying so.
//	agent-<op>-sleep    <op> takes this many seconds (a float) before it answers; a CANCEL
//	                    during the sleep answers cancelled
//	agent-hang          no request is ever answered (a CANCEL still answers cancelled)
//	agent-exit          exit 3, without answering, on the next REQUEST
//	agent-nopong        PING is never answered
//	agent-slow-hello    HELLO waits this many seconds (default 1 when empty)
//	agent-stalled       while it exists, EVENT stalled {in: <its content, default capture>}
//	                    is sent once; removing it sends recovered
//
// Built-in answers, from the files the fake tart uses too: screen from `screen`; capture sends
// shot.b64's PNG as a BLOB with {width, height, scale, rect} (capture_failed without it); ui is
// ui.json, desktop desktop.json (the fake tart's defaults without them); input runs the batch's
// sleeps, checking between actions for a CANCEL or a PAUSE by another holder, then answers
// {ok, actions, screen} (input-down refuses it, and ui, like the helper); sh answers the
// capture-approval script as the fake tart does (fail-capture-approval exits 1,
// capture-approval-stale makes `check` exit 3), and any other script with exit 0 and no output;
// refs answers {reader, next} from its args, as the agent does for a counter it raised.
// A PAUSE refuses new inputs of other holders with `paused`, as the real agent does: any request
// with input true (the toolkit's actions too) before it starts and during its agent-<op>-sleep.
//
// It appends every REQUEST as a JSON line to agent-requests, every PAUSE, RESUME and CANCEL as
// "<kind> <payload>" to agent-control, each input batch's args to agent-input, a line to
// agent-starts when it starts and to agent-exits when it ends. It exits on stdin EOF, and when
// the control directory is gone.
func serveFakeAgent(control string, in io.Reader, out io.Writer) (code int) {
	a := &fakeAgentProc{control: control, out: bufio.NewWriter(out), cancelled: map[uint32]chan struct{}{}}
	a.appendLine("agent-starts", "start")
	defer func() { a.appendLine("agent-exits", fmt.Sprintf("exit %d", code)) }()

	if a.exists("agent-slow-hello") {
		secs := a.seconds("agent-slow-hello")
		if secs == 0 {
			secs = 1
		}
		time.Sleep(time.Duration(secs * float64(time.Second)))
	}
	w, h := a.screen()
	a.sendJSON(guestagent.TypeHello, map[string]any{"version": 9, "source": "fake", "protocol": guestagent.Protocol,
		"pid": os.Getpid(), "trusted": map[string]bool{"accessibility": true, "screen": true, "postEvent": true},
		"screen": map[string]any{"width": w, "height": h, "scale": 1}, "caps": FakeAgentCaps})

	type frame struct {
		typ     byte
		payload []byte
	}
	frames := make(chan frame)
	go func() {
		defer close(frames)
		r := bufio.NewReader(in)
		for {
			typ, payload, err := guestagent.ReadFrame(r)
			if err != nil {
				return
			}
			frames <- frame{typ, payload}
		}
	}()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	stalled := ""
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				return 0
			}
			if exit := a.onFrame(f.typ, f.payload); exit {
				fmt.Fprintln(os.Stderr, "fake agent: exiting on agent-exit")
				return 3
			}
		case <-tick.C:
			if _, err := os.Stat(control); err != nil {
				return 0
			}
			now := ""
			if a.exists("agent-stalled") {
				now = strings.TrimSpace(a.read("agent-stalled"))
				if now == "" {
					now = "capture"
				}
			}
			switch {
			case now != "" && stalled == "":
				a.sendJSON(guestagent.TypeEvent, map[string]any{"kind": guestagent.EventStalled, "in": now, "seconds": 10})
			case now == "" && stalled != "":
				a.sendJSON(guestagent.TypeEvent, map[string]any{"kind": guestagent.EventRecovered, "in": stalled})
			}
			stalled = now
		}
	}
}

type fakeAgentProc struct {
	control string
	wmu     sync.Mutex
	out     *bufio.Writer
	inputMu sync.Mutex // inputs run one at a time, as on the agent's serial input queue

	mu        sync.Mutex
	paused    string
	cancelled map[uint32]chan struct{}
}

type fakeAgentRequest struct {
	ID     uint32          `json:"id"`
	Op     string          `json:"op"`
	Args   json.RawMessage `json:"args"`
	Reader string          `json:"reader"`
	Input  bool            `json:"input"`
}

// onFrame handles one frame from the host; exit says to exit at once (agent-exit).
func (a *fakeAgentProc) onFrame(typ byte, payload []byte) (exit bool) {
	switch typ {
	case guestagent.TypePing:
		if !a.exists("agent-nopong") {
			a.send(guestagent.TypePong, payload)
		}
	case guestagent.TypePause, guestagent.TypeResume:
		var p struct {
			Holder string `json:"holder"`
		}
		_ = json.Unmarshal(payload, &p)
		kind := "resume"
		if typ == guestagent.TypePause {
			kind = "pause"
		}
		a.appendLine("agent-control", kind+" "+string(payload))
		a.mu.Lock()
		a.paused = p.Holder
		a.mu.Unlock()
	case guestagent.TypeCancel:
		a.appendLine("agent-control", "cancel "+string(payload))
		var c struct {
			ID uint32 `json:"id"`
		}
		_ = json.Unmarshal(payload, &c)
		a.mu.Lock()
		if ch := a.cancelled[c.ID]; ch != nil {
			close(ch)
			delete(a.cancelled, c.ID)
		}
		a.mu.Unlock()
	case guestagent.TypeRequest:
		a.appendLine("agent-requests", string(payload))
		if a.exists("agent-exit") {
			return true
		}
		var r fakeAgentRequest
		if err := json.Unmarshal(payload, &r); err != nil {
			fmt.Fprintln(os.Stderr, "fake agent: unreadable request:", err)
			return true
		}
		cancel := make(chan struct{})
		a.mu.Lock()
		a.cancelled[r.ID] = cancel
		a.mu.Unlock()
		go a.answer(r, cancel)
	}
	return false
}

// answer runs one request and sends its RESPONSE (and BLOB).
func (a *fakeAgentProc) answer(r fakeAgentRequest, cancel <-chan struct{}) {
	started := time.Now()
	defer func() {
		a.mu.Lock()
		delete(a.cancelled, r.ID)
		a.mu.Unlock()
	}()
	fail := func(code, msg string) {
		a.sendJSON(guestagent.TypeResponse, map[string]any{"id": r.ID, "ok": false, "ms": ms(started),
			"error": map[string]any{"code": code, "message": msg, "retryable": false}})
	}
	if a.exists("agent-hang") {
		<-cancel
		fail(guestagent.CodeCancelled, "cancelled")
		return
	}
	// An input of another holder than the one the screen is paused for stops, before it starts
	// and at every point it waits, as on the agent's input queue.
	pausedBy := func() string {
		a.mu.Lock()
		defer a.mu.Unlock()
		if r.Input && a.paused != "" && a.paused != r.Reader {
			return a.paused
		}
		return ""
	}
	if holder := pausedBy(); holder != "" {
		fail(guestagent.CodePaused, "the "+holder+" took the screen; this input stopped")
		return
	}
	if secs := a.seconds("agent-" + r.Op + "-sleep"); secs > 0 {
		end := time.After(time.Duration(secs * float64(time.Second)))
		poll := time.NewTicker(10 * time.Millisecond)
		defer poll.Stop()
	sleep:
		for {
			select {
			case <-end:
				break sleep
			case <-cancel:
				fail(guestagent.CodeCancelled, "cancelled")
				return
			case <-poll.C:
				if holder := pausedBy(); holder != "" {
					fail(guestagent.CodePaused, "the "+holder+" took the screen; this input stopped")
					return
				}
			}
		}
	}
	if !slices.Contains(FakeAgentCaps, r.Op) {
		fail(guestagent.CodeUnknownOp, "unknown op "+r.Op)
		return
	}
	var blob []byte
	var canned struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if data, err := os.ReadFile(filepath.Join(a.control, "agent-"+r.Op+".json")); err == nil {
		if err := json.Unmarshal(data, &canned); err != nil {
			fail(guestagent.CodeInternal, "the fake agent's agent-"+r.Op+".json is not JSON: "+err.Error())
			return
		}
		if len(canned.Error) > 0 {
			a.sendJSON(guestagent.TypeResponse, map[string]any{"id": r.ID, "ok": false, "ms": ms(started), "error": canned.Error})
			return
		}
		if r.Op == "capture" {
			blob, _ = a.shot()
		}
		a.reply(r.ID, started, canned.Result, blob)
		return
	}
	var result any
	var err *fakeAgentError
	switch r.Op {
	case "screen":
		w, h := a.screen()
		result = map[string]any{"width": w, "height": h, "scale": 1}
	case "capture":
		result, blob, err = a.capture()
	case "ui":
		if a.exists("input-down") {
			err = &fakeAgentError{guestagent.CodeNotTrusted, "this machine has not granted Accessibility"}
			break
		}
		result = a.jsonFile("ui.json", `{"app":{"name":"Finder","bundleId":"com.apple.finder","pid":1},"apps":["Finder"],"screen":{"width":1024,"height":768},"elements":[],"truncated":false}`)
	case "desktop":
		result = a.jsonFile("desktop.json", `{"windows":[{"owner":"Window Server","name":"Menubar","layer":24,"alpha":1,"x":0,"y":0,"width":1024,"height":30},{"owner":"Dock","name":"Dock","layer":20,"alpha":1,"x":0,"y":0,"width":1024,"height":768}],"apps":[{"name":"Finder","bundleId":"com.apple.finder","pid":391}]}`)
	case "input":
		result, err = a.input(r, cancel)
	case "sh":
		result = a.sh(r.Args)
	case "refs":
		var args struct {
			Reader string `json:"reader"`
			Next   int    `json:"next"`
		}
		_ = json.Unmarshal(r.Args, &args)
		result = map[string]any{"reader": args.Reader, "next": args.Next}
	default:
		err = &fakeAgentError{guestagent.CodeInternal, "the fake agent has no agent-" + r.Op + ".json to answer " + r.Op + " with"}
	}
	if err != nil {
		fail(err.code, err.msg)
		return
	}
	data, _ := json.Marshal(result)
	a.reply(r.ID, started, data, blob)
}

type fakeAgentError struct{ code, msg string }

func (a *fakeAgentProc) reply(id uint32, started time.Time, result json.RawMessage, blob []byte) {
	a.wmu.Lock() // the BLOBs and their RESPONSE go out together
	defer a.wmu.Unlock()
	msg := map[string]any{"id": id, "ok": true, "result": result, "ms": ms(started)}
	if blob != nil {
		for _, chunk := range guestagent.BlobChunks(id, blob) {
			a.sendLocked(guestagent.TypeBlob, chunk)
		}
		msg["blob"] = map[string]any{"bytes": len(blob), "mime": "image/png"}
	}
	data, _ := json.Marshal(msg)
	a.sendLocked(guestagent.TypeResponse, data)
}

func (a *fakeAgentProc) capture() (any, []byte, *fakeAgentError) {
	data, err := a.shot()
	if err != nil {
		return nil, nil, &fakeAgentError{guestagent.CodeCaptureFailed, err.Error()}
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, nil, &fakeAgentError{guestagent.CodeCaptureFailed, "shot.b64 is not a PNG: " + err.Error()}
	}
	w, h := a.screen()
	return map[string]any{"width": cfg.Width, "height": cfg.Height, "scale": float64(cfg.Width) / float64(w),
		"rect": []int{0, 0, w, h}}, data, nil
}

func (a *fakeAgentProc) shot() ([]byte, error) {
	text, err := os.ReadFile(filepath.Join(a.control, "shot.b64"))
	if err != nil {
		return nil, fmt.Errorf("could not create image from display (the fake agent has no shot.b64)")
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(string(text)))
}

// input runs a batch the way the agent's input queue does: one at a time, the pause and cancel
// checked before each action.
func (a *fakeAgentProc) input(r fakeAgentRequest, cancel <-chan struct{}) (any, *fakeAgentError) {
	a.inputMu.Lock()
	defer a.inputMu.Unlock()
	var batch struct {
		Actions []struct {
			Type string `json:"type"`
			MS   int    `json:"ms"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(r.Args, &batch); err != nil {
		return nil, &fakeAgentError{guestagent.CodeBadRequest, err.Error()}
	}
	a.appendLine("agent-input", string(r.Args))
	if a.exists("input-down") {
		return nil, &fakeAgentError{guestagent.CodeNotTrusted, "this machine refused the event"}
	}
	check := func() *fakeAgentError {
		select {
		case <-cancel:
			return &fakeAgentError{guestagent.CodeCancelled, "cancelled"}
		default:
		}
		a.mu.Lock()
		paused := a.paused
		a.mu.Unlock()
		if paused != "" && paused != r.Reader {
			return &fakeAgentError{guestagent.CodePaused, "the " + paused + " took the screen; this input stopped"}
		}
		return nil
	}
	for _, act := range batch.Actions {
		if err := check(); err != nil {
			return nil, err
		}
		if act.Type == "sleep" {
			end := time.Now().Add(time.Duration(act.MS) * time.Millisecond)
			for time.Now().Before(end) {
				time.Sleep(min(10*time.Millisecond, time.Until(end)))
				if err := check(); err != nil {
					return nil, err
				}
			}
		}
	}
	w, h := a.screen()
	return map[string]any{"ok": true, "actions": len(batch.Actions), "screen": map[string]int{"width": w, "height": h}}, nil
}

// sh answers the daemon's own scripts as the fake tart would.
func (a *fakeAgentProc) sh(raw json.RawMessage) any {
	var args struct {
		Script string   `json:"script"`
		Args   []string `json:"args"`
	}
	_ = json.Unmarshal(raw, &args)
	res := map[string]any{"stdout": "", "stderr": "", "exit": 0, "timedOut": false}
	if strings.Contains(args.Script, "ScreenCaptureApprovals") {
		switch {
		case a.exists("fail-capture-approval"):
			res["exit"], res["stderr"] = 1, "defaults: cannot write\n"
		case len(args.Args) > 0 && args.Args[0] == "check" && a.exists("capture-approval-stale"):
			res["exit"] = 3
		}
	}
	return res
}

func (a *fakeAgentProc) screen() (int, int) {
	w, h := 1024, 768
	if data, err := os.ReadFile(filepath.Join(a.control, "screen")); err == nil {
		_, _ = fmt.Sscanf(strings.TrimSpace(string(data)), "%dx%d", &w, &h)
	}
	return w, h
}

func (a *fakeAgentProc) jsonFile(name, def string) json.RawMessage {
	if data, err := os.ReadFile(filepath.Join(a.control, name)); err == nil {
		return json.RawMessage(bytes.TrimSpace(data))
	}
	return json.RawMessage(def)
}

func (a *fakeAgentProc) exists(name string) bool {
	_, err := os.Stat(filepath.Join(a.control, name))
	return err == nil
}

func (a *fakeAgentProc) read(name string) string {
	data, _ := os.ReadFile(filepath.Join(a.control, name))
	return string(data)
}

func (a *fakeAgentProc) seconds(name string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(a.read(name)), 64)
	return f
}

func (a *fakeAgentProc) appendLine(name, line string) {
	f, err := os.OpenFile(filepath.Join(a.control, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		_, _ = f.WriteString(line + "\n")
		_ = f.Close()
	}
}

func (a *fakeAgentProc) send(typ byte, payload []byte) {
	a.wmu.Lock()
	defer a.wmu.Unlock()
	a.sendLocked(typ, payload)
}

func (a *fakeAgentProc) sendLocked(typ byte, payload []byte) {
	_ = guestagent.WriteFrame(a.out, typ, payload)
	_ = a.out.Flush()
}

func (a *fakeAgentProc) sendJSON(typ byte, v any) {
	data, _ := json.Marshal(v)
	a.send(typ, data)
}

func ms(since time.Time) float64 { return float64(time.Since(since).Microseconds()) / 1000 }

// AgentStarts counts how many times the guest agent (`exec -i ... --agent`) was started.
func AgentStarts(t *testing.T, control string) int {
	t.Helper()
	n := 0
	for _, line := range strings.Split(Calls(t, control), "\n") {
		if IsAgentStart(line) {
			n++
		}
	}
	return n
}

// IsAgentStart reports whether a calls.log line is a start of the guest agent.
func IsAgentStart(line string) bool {
	return strings.HasPrefix(line, "exec -i ") && strings.HasSuffix(line, " --agent")
}

// ControlLines returns the lines the fake agent (or tart) appended to the control file name.
func ControlLines(t *testing.T, control, name string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(control, name))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}
