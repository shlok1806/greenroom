// Package nim talks to an OpenAI-compatible chat endpoint. greenroom uses
// NVIDIA NIM (ADR 0005), but nothing here is specific to that host.
package nim

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
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
	var out wireResponse
	if err := c.post(ctx, body, &out); err != nil {
		return Message{}, Usage{}, err
	}
	if out.Error != nil && out.Error.Message != "" {
		return Message{}, out.Usage, fmt.Errorf("%s: %s", model, out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return Message{}, out.Usage, fmt.Errorf("%s: the endpoint returned no choices", model)
	}
	wm := out.Choices[0].Message
	msg := Message{Role: "assistant", Content: textOf(wm.Content)}
	for _, tc := range wm.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	return msg, out.Usage, nil
}

// Describe asks a vision model what is in an image. The reasoning model
// cannot accept images (ADR 0005), so a screenshot reaches it as this text.
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
	var out wireResponse
	if err := c.post(ctx, body, &out); err != nil {
		return "", err
	}
	if out.Error != nil && out.Error.Message != "" {
		return "", fmt.Errorf("%s: %s", model, out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("%s: the endpoint returned no choices", model)
	}
	return strings.TrimSpace(textOf(out.Choices[0].Message.Content)), nil
}

func (c *Client) post(ctx context.Context, body map[string]any, out *wireResponse) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("call %s: %w", c.BaseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %d: %s", c.BaseURL, resp.StatusCode, strings.TrimSpace(truncate(string(raw), 300)))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w: %s", err, truncate(string(raw), 200))
	}
	return nil
}

func encodeTools(tools []Tool) []wireTool {
	out := make([]wireTool, 0, len(tools))
	for _, t := range tools {
		var wt wireTool
		wt.Type = "function"
		wt.Function.Name = t.Name
		wt.Function.Description = t.Description
		wt.Function.Parameters = t.Schema
		out = append(out, wt)
	}
	return out
}

func encodeMessages(msgs []Message) []any {
	out := make([]any, 0, len(msgs))
	for _, m := range msgs {
		wm := map[string]any{"role": m.Role}
		// An assistant turn that only calls tools must still carry content,
		// because some endpoints reject a missing field.
		wm["content"] = m.Content
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
