package nim

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
