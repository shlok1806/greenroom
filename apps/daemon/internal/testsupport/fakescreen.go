package testsupport

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// fakeScreenEnv makes a test binary act as `greenroom-input --serve` (ADR 0011)
// instead of running tests. The fake tart re-executes the test binary with it
// set to the control directory, so no helper has to be built.
const fakeScreenEnv = "GREENROOM_FAKE_SCREEN"

// FakeScreenInterval is how often the fake helper sends a frame; every
// FakeScreenGOP-th frame is a keyframe.
const (
	FakeScreenInterval = 2 * time.Millisecond
	FakeScreenGOP      = 25
)

// FakeScreenFormat is the avcC the fake helper announces.
var FakeScreenFormat = []byte("fake-avcC")

func init() {
	if dir := os.Getenv(fakeScreenEnv); dir != "" {
		os.Exit(serveFakeScreen(dir, os.Stdin, os.Stdout))
	}
}

// serveFakeScreen speaks the helper's side of the protocol. Control files:
//
//	screen        "<width>x<height>" in points, as for the one-shot helper
//	serve-still   no periodic frames, only the first keyframe and those KEYFRAME asks for
//	serve-exit    exit 3 with a message on the next tick
//	input-down    ACK every INPUT with an error
//
// Like the real helper it runs an INPUT's sleeps before its ACK.
// It appends each INPUT payload to serve-input and a line per KEYFRAME to serve-keyframes.
func serveFakeScreen(control string, in io.Reader, out io.Writer) int {
	w, h := 1024, 768
	if data, err := os.ReadFile(filepath.Join(control, "screen")); err == nil {
		_, _ = fmt.Sscanf(strings.TrimSpace(string(data)), "%dx%d", &w, &h)
	}
	var mu sync.Mutex
	bw := bufio.NewWriter(out)
	started := time.Now()
	seq := uint32(0)
	send := func(typ byte, payload []byte) {
		mu.Lock()
		defer mu.Unlock()
		var head [5]byte
		head[0] = typ
		binary.BigEndian.PutUint32(head[1:], uint32(len(payload)))
		_, _ = bw.Write(head[:])
		_, _ = bw.Write(payload)
		_ = bw.Flush()
	}
	video := func(key bool) {
		p := make([]byte, 13)
		if key {
			p[0] = 1
		}
		binary.BigEndian.PutUint64(p[1:], uint64(time.Since(started).Microseconds()))
		mu.Lock()
		seq++
		binary.BigEndian.PutUint32(p[9:], seq)
		mu.Unlock()
		send(0x03, p)
	}
	appendLine := func(name string, line []byte) {
		f, err := os.OpenFile(filepath.Join(control, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			_, _ = f.Write(append(line, '\n'))
			_ = f.Close()
		}
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(control, name))
		return err == nil
	}

	hello, _ := json.Marshal(map[string]any{
		"version": "greenroom-input 7",
		"screen":  map[string]int{"width": w, "height": h},
		"pixels":  map[string]int{"width": 2 * w, "height": 2 * h},
	})
	send(0x01, hello)
	send(0x02, FakeScreenFormat)
	video(true)

	eof := make(chan struct{})
	go func() {
		defer close(eof)
		r := bufio.NewReader(in)
		for {
			var head [5]byte
			if _, err := io.ReadFull(r, head[:]); err != nil {
				return
			}
			payload := make([]byte, binary.BigEndian.Uint32(head[1:]))
			if _, err := io.ReadFull(r, payload); err != nil {
				return
			}
			switch head[0] {
			case 0x10:
				appendLine("serve-input", payload)
				var input struct {
					ID      int64 `json:"id"`
					Actions []struct {
						Type string `json:"type"`
						MS   int    `json:"ms"`
					} `json:"actions"`
				}
				_ = json.Unmarshal(payload, &input)
				for _, a := range input.Actions {
					if a.Type == "sleep" {
						time.Sleep(time.Duration(a.MS) * time.Millisecond)
					}
				}
				ack := map[string]any{"id": input.ID}
				if exists("input-down") {
					ack["error"] = "this machine refused the event"
				}
				data, _ := json.Marshal(ack)
				send(0x04, data)
			case 0x11:
				appendLine("serve-keyframes", []byte("keyframe"))
				send(0x02, FakeScreenFormat)
				video(true)
			}
		}
	}()

	still := exists("serve-still")
	tick := time.NewTicker(FakeScreenInterval)
	defer tick.Stop()
	for n := 1; ; n++ {
		select {
		case <-eof:
			return 0
		case <-tick.C:
		}
		if exists("serve-exit") {
			fmt.Fprintln(os.Stderr, "fake helper crashed")
			return 3
		}
		if _, err := os.Stat(control); err != nil {
			return 0
		}
		if !still {
			video(n%FakeScreenGOP == 0)
		}
	}
}
