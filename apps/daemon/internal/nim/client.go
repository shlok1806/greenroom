// Package nim talks to an OpenAI-compatible chat endpoint. greenroom uses
// NVIDIA NIM (ADR 0005), but nothing here is specific to that host.
package nim

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// DefaultBaseURL is NVIDIA's hosted endpoint.
const DefaultBaseURL = "https://integrate.api.nvidia.com/v1"

// Client calls one chat endpoint. It holds no conversation state.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// New returns a Client with a timeout long enough for a slow model.
func New(baseURL, apiKey string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 3 * time.Minute},
	}
}

// Tool is one function the model may call.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any // JSON Schema for the arguments
}

// ToolCall is the model asking for one function.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // raw JSON, because only the caller knows the shape
}

// Message is one turn. A tool result carries ToolCallID.
type Message struct {
	Role       string // system, user, assistant, tool
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

// Usage reports what one call cost.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type wireMessage struct {
	Role       string `json:"role"`
	Content    any    `json:"content"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolCalls  []struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls,omitempty"`
}

type wireResponse struct {
	Choices []struct {
		Message      wireMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Chat sends one turn and returns the assistant message. A model that asks
// for a function returns ToolCalls; otherwise Content holds its answer.
func (c *Client) Chat(ctx context.Context, model string, msgs []Message, tools []Tool) (Message, Usage, error) {
	body := map[string]any{
		"model":       model,
		"messages":    encodeMessages(msgs),
		"max_tokens":  1200,
		"temperature": 0.2,
	}
	if len(tools) > 0 {
		body["tools"] = encodeTools(tools)
		body["tool_choice"] = "auto"
	}
	wm, usage, err := c.complete(ctx, model, body)
	if err != nil {
		return Message{}, usage, err
	}
	msg := Message{Role: "assistant", Content: textOf(wm.Content)}
	for _, tc := range wm.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	return msg, usage, nil
}

// Describe asks a vision model what is in an image (ADR 0005: the reasoning
// model cannot take images).
func (c *Client) Describe(ctx context.Context, model string, jpeg []byte, prompt string) (string, error) {
	body := map[string]any{
		"model":       model,
		"max_tokens":  700,
		"temperature": 0.2,
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "text", "text": prompt},
				map[string]any{"type": "image_url", "image_url": map[string]any{
					"url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpeg),
				}},
			},
		}},
	}
	wm, _, err := c.complete(ctx, model, body)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(textOf(wm.Content)), nil
}

// complete posts body and returns the first choice's message.
func (c *Client) complete(ctx context.Context, model string, body map[string]any) (wireMessage, Usage, error) {
	var out wireResponse
	if err := c.post(ctx, body, &out); err != nil {
		return wireMessage{}, Usage{}, err
	}
	if out.Error != nil && out.Error.Message != "" {
		return wireMessage{}, out.Usage, fmt.Errorf("%s: %s", model, out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return wireMessage{}, out.Usage, fmt.Errorf("%s: the endpoint returned no choices", model)
	}
	return out.Choices[0].Message, out.Usage, nil
}

// RetryBackoff is the wait before each retry of a failed request, so the
// client makes len(RetryBackoff)+1 attempts. Tests zero it.
var RetryBackoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}

func (c *Client) post(ctx context.Context, body map[string]any, out *wireResponse) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	attempts := len(RetryBackoff) + 1
	for attempt := 1; ; attempt++ {
		raw, status, retryAfter, err := c.attempt(ctx, data)
		var last string
		switch {
		case err != nil:
			if attempt >= attempts || !retryableTransport(ctx, err) {
				return fmt.Errorf("call %s failed after %s: %w", c.BaseURL, plural(attempt), err)
			}
			last = err.Error()
		case status == http.StatusOK:
			if err := json.Unmarshal(raw, out); err != nil {
				return fmt.Errorf("decode response: %w: %s", err, truncate(string(raw), 200))
			}
			return nil
		default:
			if attempt >= attempts || !retryableStatus(status) {
				return fmt.Errorf("%s returned %d after %s: %s", c.BaseURL, status, plural(attempt), strings.TrimSpace(truncate(string(raw), 300)))
			}
			last = "status " + strconv.Itoa(status)
		}
		wait := RetryBackoff[attempt-1]
		if retryAfter >= 0 {
			wait = retryAfter
		}
		if err := sleep(ctx, wait); err != nil {
			return fmt.Errorf("gave up on %s after %s (last: %s): %w", c.BaseURL, plural(attempt), last, err)
		}
	}
}

// attempt makes one request. A non-nil error is a transport failure; status
// and body are the endpoint's answer. retryAfter is the Retry-After header in
// seconds, or -1 when the endpoint did not send a usable one.
func (c *Client) attempt(ctx context.Context, data []byte) ([]byte, int, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, 0, -1, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, -1, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, 0, -1, err
	}
	return raw, resp.StatusCode, retryAfterOf(resp), nil
}

// retryableStatus is true for "not now" statuses; any other 4xx is final.
func retryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

// retryableTransport is true for a network failure while ctx is still live.
// A timeout is final: the client's 3-minute limit, retried, would outlast the
// verifier's whole turn.
func retryableTransport(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && !ne.Timeout()
}

// retryAfterOf reads a 429/503 Retry-After in seconds (the date form is
// ignored), or returns -1.
func retryAfterOf(resp *http.Response) time.Duration {
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
	default:
		return -1
	}
	v := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if v == "" {
		return -1
	}
	secs, err := strconv.Atoi(v)
	if err != nil || secs < 0 {
		return -1
	}
	return time.Duration(secs) * time.Second
}

// sleep waits d, or returns as soon as the caller gives up.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func plural(attempts int) string {
	if attempts == 1 {
		return "1 attempt"
	}
	return fmt.Sprintf("%d attempts", attempts)
}

func encodeTools(tools []Tool) []wireTool {
	out := make([]wireTool, 0, len(tools))
	for _, t := range tools {
		wt := wireTool{Type: "function"}
		wt.Function.Name, wt.Function.Description, wt.Function.Parameters = t.Name, t.Description, t.Schema
		out = append(out, wt)
	}
	return out
}

func encodeMessages(msgs []Message) []any {
	out := make([]any, 0, len(msgs))
	for _, m := range msgs {
		// content is always sent: some endpoints reject a tool-call-only
		// assistant turn without it.
		wm := map[string]any{"role": m.Role, "content": m.Content}
		if m.ToolCallID != "" {
			wm["tool_call_id"] = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			calls := make([]any, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				calls = append(calls, map[string]any{
					"id": tc.ID, "type": "function",
					"function": map[string]any{"name": tc.Name, "arguments": tc.Arguments},
				})
			}
			wm["tool_calls"] = calls
		}
		out = append(out, wm)
	}
	return out
}

// textOf accepts both a plain string and the content-parts form.
func textOf(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, part := range v {
			if m, ok := part.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					b.WriteString(s)
				}
			}
		}
		return b.String()
	default:
		return ""
	}
}

// truncate cuts s to at most n bytes on a rune boundary.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "..."
}
