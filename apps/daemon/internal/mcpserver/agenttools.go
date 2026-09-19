package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// addAgentTools exposes the run's conversation (ADR 0006). These three tools
// are the only way the coding agent reaches greenroom's verifier, and the
// same store carries everything a watching human does, so the coder sees it.
func addAgentTools(s *mcp.Server, reg *session.Registry) {
	type transcriptOut struct {
		Messages []session.Message    `json:"messages"`
		Last     int                  `json:"last"`
		Verdict  session.VerdictState `json:"verdict"`
	}

	type sendIn struct {
		RunID   string `json:"runId" jsonschema:"runId from machine_create"`
		Kind    string `json:"kind" jsonschema:"One of: task (work for the verifier), note (context you expect no reply to; a human's note in the companion app is answered by the verifier), answer (reply to a question), accept (the verdict in replyTo is final), dispute (why the verdict in replyTo is wrong)."`
		Text    string `json:"text,omitempty" jsonschema:"What you are saying. Required for every kind except accept."`
		ReplyTo int    `json:"replyTo,omitempty" jsonschema:"The seq of the message this replies to. Required for answer, accept and dispute."`
	}
	type sendOut struct {
		Seq int       `json:"seq"`
		At  time.Time `json:"at"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "agent_send",
		Description: "Say something into the run's conversation, which is how you reach greenroom's verifier. " +
			"Send a task to set it working, then call agent_wait in a loop until it replies. A note adds context " +
			"it reads on its next turn. If it sends you a question, reply with kind answer and replyTo set to the " +
			"question's seq. A verdict is a proposal, not an outcome: accept it, or dispute it with replyTo and " +
			"the reason, and it takes another turn. After two disputes the verdict is contested and only a human " +
			"can close it. The verifier may also answer with a reply, which is a plain answer and not a verdict, " +
			"so keep waiting if your task is not done. Returns the seq the conversation gave your message.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in sendIn) (*mcp.CallToolResult, sendOut, error) {
		store, err := reg.Get(in.RunID)
		if err != nil {
			return nil, sendOut{}, err
		}
		// Validation errors, and session.ErrContested, go straight back to the
		// coder: their text is the whole message it needs.
		m, err := store.Append(session.Message{
			From:    session.Coder,
			Kind:    session.Kind(in.Kind),
			Text:    in.Text,
			ReplyTo: in.ReplyTo,
		})
		if err != nil {
			return nil, sendOut{}, err
		}
		return nil, sendOut{Seq: m.Seq, At: m.At}, nil
	})

	type waitIn struct {
		RunID          string `json:"runId" jsonschema:"runId from machine_create"`
		After          int    `json:"after" jsonschema:"Return messages with a seq above this. Pass the last you have seen, or 0 for the whole transcript."`
		TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"How long to block before returning. Default 45, max 50."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "agent_wait",
		Description: "Block until the conversation has something newer than after, then return it. This is how you " +
			"hear the verifier: send a task, then call agent_wait with after set to last from the previous call, " +
			"over and over, until a verdict arrives. An empty messages list means nothing happened in the timeout, " +
			"which is normal; call again. A question needs an agent_send of kind answer before the verifier moves; " +
			"a reply is the verifier answering in words with no verdict, so it does not end your task. " +
			"Anything a watching human does shows up here too, and a human's note is answered by the verifier.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in waitIn) (*mcp.CallToolResult, transcriptOut, error) {
		store, err := reg.Get(in.RunID)
		if err != nil {
			return nil, transcriptOut{}, err
		}
		timeout := defaultWait
		if in.TimeoutSeconds > 0 {
			timeout = min(time.Duration(in.TimeoutSeconds)*time.Second, maxWait)
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		msgs := store.Wait(ctx, in.After)
		return nil, transcriptOut{Messages: msgs, Last: lastSeq(msgs, in.After, store), Verdict: store.Verdict()}, nil
	})

	type transcriptIn struct {
		RunID string `json:"runId" jsonschema:"runId from machine_create"`
		After int    `json:"after,omitempty" jsonschema:"Return messages with a seq above this. Omit for the whole transcript."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "agent_transcript",
		Description: "Read the run's conversation without waiting: every message from you, greenroom's verifier, " +
			"a watching human and the daemon, plus the current verdict and its status. The verifier's replies to a " +
			"watching human are in here too, so this is where you see what it told them. Use it to catch up, for " +
			"example after picking a run back up with machine_list.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in transcriptIn) (*mcp.CallToolResult, transcriptOut, error) {
		store, err := reg.Get(in.RunID)
		if err != nil {
			return nil, transcriptOut{}, err
		}
		msgs := store.After(in.After)
		return nil, transcriptOut{Messages: msgs, Last: lastSeq(msgs, in.After, store), Verdict: store.Verdict()}, nil
	})
}

// lastSeq is what the caller should pass as `after` next time. It comes from
// the messages the caller just got, so a reader that is behind the
// conversation does not skip whatever arrived while it was reading.
func lastSeq(msgs []session.Message, after int, store *session.Store) int {
	if n := len(msgs); n > 0 {
		return msgs[n-1].Seq
	}
	if after == 0 {
		return store.Len()
	}
	return after
}
