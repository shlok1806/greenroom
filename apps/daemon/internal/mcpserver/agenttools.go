package mcpserver

import (
	"context"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// addAgentTools exposes the run's conversation, the coder's only channel to the verifier (ADR 0006).
func addAgentTools(s *mcp.Server, reg *session.Registry) {
	type transcriptOut struct {
		Messages []session.Message    `json:"messages"`
		Last     int                  `json:"last"`
		Verdict  session.VerdictState `json:"verdict"`
	}
	transcript := func(store *session.Store, msgs []session.Message, after int) transcriptOut {
		return transcriptOut{Messages: msgs, Last: lastSeq(msgs, after, store), Verdict: store.Verdict()}
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
		Description: "Post into the run's conversation, which is how you reach greenroom's verifier. Send a task " +
			"to set it working, then call agent_wait in a loop until it replies. A note adds context it reads on " +
			"its next turn; your note never starts a turn, so send a task when you want an answer. Answer a question with kind answer and replyTo set to the question's seq. A verdict " +
			"is a proposal: accept it, or dispute it with replyTo and the reason, and the verifier takes another " +
			"turn. After two disputes the verdict is contested and only a human can close it. A reply is a plain " +
			"answer, not a verdict, so keep waiting if your task is not done. Returns your message's seq.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in sendIn) (*mcp.CallToolResult, sendOut, error) {
		store, err := reg.Get(in.RunID)
		if err != nil {
			return nil, sendOut{}, err
		}
		// Validation errors and ErrContested go back verbatim: their text is what the coder needs.
		m, err := store.Append(session.Message{From: session.Coder, Kind: session.Kind(in.Kind), Text: in.Text, ReplyTo: in.ReplyTo})
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
		Description: "Block until the conversation has something newer than after, then return it. The verifier's " +
			"progress lines (one per tool call it makes) do not end the wait: it returns when its turn ends with a " +
			"reply, question or verdict, when anyone else posts, or at the timeout, with everything gathered so " +
			"far. After sending a task, call this repeatedly with after set to last from the previous call until a " +
			"verdict arrives. An empty messages list means nothing happened before the timeout; call again. A question needs an " +
			"agent_send of kind answer before the verifier continues; a reply is the verifier answering in words " +
			"with no verdict, so it does not end your task. A reply with stop set (steps or time) means the " +
			"verifier's turn hit its tool-call or time limit before it gave a verdict and it is waiting: send a " +
			"task or note to let it continue, or check the result yourself. A watching human's actions show up " +
			"here too, and the verifier answers a human's note.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in waitIn) (*mcp.CallToolResult, transcriptOut, error) {
		store, err := reg.Get(in.RunID)
		if err != nil {
			return nil, transcriptOut{}, err
		}
		ctx, cancel := context.WithTimeout(ctx, waitTimeout(in.TimeoutSeconds))
		defer cancel()
		return nil, transcript(store, waitPastProgress(ctx, store, in.After), in.After), nil
	})

	type transcriptIn struct {
		RunID string `json:"runId" jsonschema:"runId from machine_create"`
		After int    `json:"after,omitempty" jsonschema:"Return messages with a seq above this. Omit for the whole transcript."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "agent_transcript",
		Description: "Read the run's conversation without waiting: every message from you, greenroom's verifier, " +
			"a watching human and the daemon, plus the current verdict and its status. It includes what the " +
			"verifier told a watching human. Use it to catch up, for example after picking a run back up with " +
			"machine_list.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in transcriptIn) (*mcp.CallToolResult, transcriptOut, error) {
		store, err := reg.Get(in.RunID)
		if err != nil {
			return nil, transcriptOut{}, err
		}
		return nil, transcript(store, store.After(in.After), in.After), nil
	})
}

// waitPastProgress waits for messages after seq and keeps waiting while all of them are verifier
// progress, so waiting for one verdict is one call rather than one per verifier step (issue #46).
// It returns what it gathered at the timeout or when the store closes.
func waitPastProgress(ctx context.Context, store *session.Store, seq int) []session.Message {
	var out []session.Message
	for {
		msgs := store.Wait(ctx, seq)
		out = append(out, msgs...)
		if len(msgs) == 0 || ctx.Err() != nil {
			return out
		}
		seq = msgs[len(msgs)-1].Seq
		if slices.ContainsFunc(msgs, func(m session.Message) bool {
			return m.From != session.Verifier || m.Kind != session.Progress
		}) {
			return out
		}
	}
}

// lastSeq is the caller's next after. It comes from the messages returned, so a reader never skips what arrived meanwhile.
func lastSeq(msgs []session.Message, after int, store *session.Store) int {
	if n := len(msgs); n > 0 {
		return msgs[n-1].Seq
	}
	if after == 0 {
		return store.Len()
	}
	return after
}
