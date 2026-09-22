package nim

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

// noBackoff makes the retry schedule instant for the length of a test, so a
// test of five attempts costs no seconds.
func noBackoff(t *testing.T) {
	t.Helper()
	old := RetryBackoff
	RetryBackoff = []time.Duration{0, 0, 0, 0}
	t.Cleanup(func() { RetryBackoff = old })
}

const okBody = `{"choices":[{"message":{"role":"assistant","content":"hello"}}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`

// endpoint counts requests and answers each one from fn.
func endpoint(t *testing.T, fn func(w http.ResponseWriter, n int)) (*Client, *int64) {
	t.Helper()
	var calls int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fn(w, int(atomic.AddInt64(&calls, 1)))
	}))
	t.Cleanup(ts.Close)
	return New(ts.URL, "k"), &calls
}

func TestChatRetriesAServerError(t *testing.T) {
	noBackoff(t)
	c, calls := endpoint(t, func(w http.ResponseWriter, n int) {
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"Internal server error"}}`)
			return
		}
		_, _ = io.WriteString(w, okBody)
	})

	msg, _, err := c.Chat(context.Background(), "m", []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if msg.Content != "hello" {
		t.Errorf("content = %q, want the answer from the third attempt", msg.Content)
	}
	if got := atomic.LoadInt64(calls); got != 3 {
		t.Errorf("the endpoint saw %d requests, want 3", got)
	}
}

func TestChatDoesNotRetryABadRequest(t *testing.T) {
	noBackoff(t)
	c, calls := endpoint(t, func(w http.ResponseWriter, _ int) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"bad model"}}`)
	})

	_, _, err := c.Chat(context.Background(), "m", []Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("Chat returned no error although the endpoint refused the request")
	}
	if !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "1 attempt") {
		t.Errorf("error = %v, want the status and the attempt count", err)
	}
	if got := atomic.LoadInt64(calls); got != 1 {
		t.Errorf("the endpoint saw %d requests, want 1: a 400 is the request's own fault", got)
	}
}

func TestChatHonorsRetryAfter(t *testing.T) {
	noBackoff(t)
	c, calls := endpoint(t, func(w http.ResponseWriter, n int) {
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"message":"busy"}}`)
			return
		}
		_, _ = io.WriteString(w, okBody)
	})

	if _, _, err := c.Chat(context.Background(), "m", []Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got := atomic.LoadInt64(calls); got != 2 {
		t.Errorf("the endpoint saw %d requests, want 2", got)
	}
}

func TestChatGivesUpAfterEveryAttempt(t *testing.T) {
	noBackoff(t)
	c, calls := endpoint(t, func(w http.ResponseWriter, _ int) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"Internal server error"}}`)
	})

	_, _, err := c.Chat(context.Background(), "m", []Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("Chat returned no error although every attempt failed")
	}
	if !strings.Contains(err.Error(), "returned 500 after 5 attempts") {
		t.Errorf("error = %v, want it to say how many attempts it made", err)
	}
	if got := atomic.LoadInt64(calls); got != 5 {
		t.Errorf("the endpoint saw %d requests, want 5", got)
	}
}

func TestChatRetriesATransportFailure(t *testing.T) {
	noBackoff(t)
	var calls int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt64(&calls, 1) == 1 {
			// Hang up mid-response: the client sees a broken connection.
			hj, ok := w.(http.Hijacker)
			if !ok {
				return
			}
			conn, _, err := hj.Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		_, _ = io.WriteString(w, okBody)
	}))
	defer ts.Close()

	c := New(ts.URL, "k")
	if _, _, err := c.Chat(context.Background(), "m", []Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Errorf("the endpoint saw %d requests, want 2", got)
	}
}

// A retried client timeout would outlast the verifier's turn budget.
func TestChatDoesNotRetryAClientTimeout(t *testing.T) {
	noBackoff(t)
	for _, c := range []struct {
		name       string
		sendHeader bool
	}{{"awaiting headers", false}, {"reading the body", true}} {
		t.Run(c.name, func(t *testing.T) {
			var calls int64
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt64(&calls, 1)
				// Draining the body lets the server notice the client hang up.
				_, _ = io.Copy(io.Discard, r.Body)
				if c.sendHeader {
					w.WriteHeader(http.StatusOK)
					_, _ = io.WriteString(w, `{"choices":`)
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
				case <-time.After(5 * time.Second):
				}
			}))
			defer ts.Close()
			cl := New(ts.URL, "k")
			cl.HTTP.Timeout = 100 * time.Millisecond

			if _, _, err := cl.Chat(context.Background(), "m", []Message{{Role: "user", Content: "hi"}}, nil); err == nil {
				t.Fatal("Chat returned no error although the request timed out")
			}
			if got := atomic.LoadInt64(&calls); got != 1 {
				t.Errorf("the endpoint saw %d requests, want 1", got)
			}
		})
	}
}

func TestChatExplainsGivingUpDuringBackoff(t *testing.T) {
	old := RetryBackoff
	RetryBackoff = []time.Duration{time.Hour}
	t.Cleanup(func() { RetryBackoff = old })
	c, _ := endpoint(t, func(w http.ResponseWriter, _ int) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, _, err := c.Chat(ctx, "m", []Message{{Role: "user", Content: "hi"}}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want it to wrap the context's error", err)
	}
	if !strings.Contains(err.Error(), "1 attempt") || !strings.Contains(err.Error(), "503") {
		t.Errorf("error = %v, want the attempt count and the last status", err)
	}
}

func TestTruncateKeepsWholeRunes(t *testing.T) {
	for n := 0; n <= 6; n++ {
		got := truncate("héllo wörld", n)
		if !utf8.ValidString(got) {
			t.Errorf("truncate(_, %d) = %q, split a rune", n, got)
		}
		if len(strings.TrimSuffix(got, "...")) > n {
			t.Errorf("truncate(_, %d) = %q, longer than %d bytes", n, got, n)
		}
	}
	if got := truncate("héllo", 2); got != "h..." {
		t.Errorf("truncate(héllo, 2) = %q, want h...", got)
	}
}

func TestChatDoesNotRetryACancelledCall(t *testing.T) {
	noBackoff(t)
	c, calls := endpoint(t, func(w http.ResponseWriter, _ int) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := c.Chat(ctx, "m", []Message{{Role: "user", Content: "hi"}}, nil); err == nil {
		t.Fatal("Chat returned no error although the caller gave up")
	}
	if got := atomic.LoadInt64(calls); got > 1 {
		t.Errorf("the endpoint saw %d requests after the caller gave up, want at most 1", got)
	}
}
