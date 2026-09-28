// Package guestagent is the daemon's side of the guest agent channel (daemon ADR 0005): the
// frames, one connection to a running `greenroom-input --agent` (Conn), and the loop that keeps
// one connected per machine (Supervisor). It knows the wire and nothing of machines, tart or
// tools, and imports only the standard library; internal/machine decides when it runs and what
// goes over it.
package guestagent

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

// Frame types (daemon ADR 0005, "Frames"). Every message either way is
// [type u8][length u32 big-endian][payload], the `--serve` format.
const (
	TypeHello    byte = 0x01 // agent to host, first frame
	TypeRequest  byte = 0x20 // host to agent
	TypeResponse byte = 0x21 // agent to host
	TypeBlob     byte = 0x22 // agent to host, binary, before its request's RESPONSE
	TypeCancel   byte = 0x23 // host to agent
	TypeEvent    byte = 0x24 // agent to host
	TypePing     byte = 0x25 // host to agent
	TypePong     byte = 0x26 // agent to host, the PING's payload echoed
	TypePause    byte = 0x27 // host to agent
	TypeResume   byte = 0x28 // host to agent
	TypeStream   byte = 0x29 // agent to host; reserved for wave 3's live video
)

// MaxPayload is the largest payload a frame may carry. A frame that claims more is a broken
// channel: the reader closes it rather than allocate.
const MaxPayload = 4 << 20

// Protocol is the wire version this package speaks. It bumps only on a breaking change; what an
// agent can do is its HELLO's caps, never guessed from a version.
const Protocol = 1

// MaxBlobChunk is the most bytes one BLOB frame carries after its header.
const MaxBlobChunk = 1 << 20

// MaxDeadline is the longest deadline a request may carry: every call stays under the MCP
// client's 50 s cap (root ADR 0015).
const MaxDeadline = 45 * time.Second

// blobHeader is a BLOB payload's [id u32][seq u16][last u8].
const blobHeader = 7

// ErrFrameTooLarge is a frame over MaxPayload, written or read.
var ErrFrameTooLarge = errors.New("a guest agent frame is larger than 4 MiB")

// WriteFrame writes one frame in a single Write, so frames from one writer never interleave.
func WriteFrame(w io.Writer, typ byte, payload []byte) error {
	b, err := encodeFrame(typ, payload)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func encodeFrame(typ byte, payload []byte) ([]byte, error) {
	if len(payload) > MaxPayload {
		return nil, fmt.Errorf("%w: type 0x%02x, %d bytes", ErrFrameTooLarge, typ, len(payload))
	}
	b := make([]byte, 5+len(payload))
	b[0] = typ
	binary.BigEndian.PutUint32(b[1:], uint32(len(payload)))
	copy(b[5:], payload)
	return b, nil
}

// ReadFrame reads one frame. A length over MaxPayload is ErrFrameTooLarge before anything is
// allocated; a frame cut short is io.ErrUnexpectedEOF; a clean end between frames is io.EOF.
func ReadFrame(r io.Reader) (typ byte, payload []byte, err error) {
	var head [5]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(head[1:])
	if n > MaxPayload {
		return 0, nil, fmt.Errorf("%w: type 0x%02x claims %d bytes", ErrFrameTooLarge, head[0], n)
	}
	payload = make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return 0, nil, err
	}
	return head[0], payload, nil
}

// EncodeBlobChunk is one BLOB payload: [id u32][seq u16][last u8][bytes].
func EncodeBlobChunk(id uint32, seq uint16, last bool, data []byte) []byte {
	b := make([]byte, blobHeader+len(data))
	binary.BigEndian.PutUint32(b, id)
	binary.BigEndian.PutUint16(b[4:], seq)
	if last {
		b[6] = 1
	}
	copy(b[blobHeader:], data)
	return b
}

// DecodeBlobChunk splits a BLOB payload into its header and bytes.
func DecodeBlobChunk(p []byte) (id uint32, seq uint16, last bool, data []byte, err error) {
	if len(p) < blobHeader {
		return 0, 0, false, nil, fmt.Errorf("a BLOB frame of %d bytes has no header", len(p))
	}
	if len(p)-blobHeader > MaxBlobChunk {
		return 0, 0, false, nil, fmt.Errorf("a BLOB chunk of %d bytes is over 1 MiB", len(p)-blobHeader)
	}
	return binary.BigEndian.Uint32(p), binary.BigEndian.Uint16(p[4:]), p[6] != 0, p[blobHeader:], nil
}

// BlobChunks splits data into the BLOB payloads that carry it for request id, in order, each at
// most MaxBlobChunk bytes; empty data is one empty last chunk. The fake agents use it.
func BlobChunks(id uint32, data []byte) [][]byte {
	var out [][]byte
	for seq := 0; ; seq++ {
		n := min(len(data), MaxBlobChunk)
		out = append(out, EncodeBlobChunk(id, uint16(seq), n == len(data), data[:n]))
		data = data[n:]
		if len(data) == 0 {
			return out
		}
	}
}

// Trust is what the agent's process is granted: Accessibility, screen capture and posting events.
type Trust struct {
	Accessibility bool `json:"accessibility"`
	Screen        bool `json:"screen"`
	PostEvent     bool `json:"postEvent"`
}

// Screen is the guest display in points, and its pixels per point.
type Screen struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Scale  float64 `json:"scale"`
}

// UnmarshalJSON takes whole numbers written either way (1024 or 1024.0): the agent's
// serializer decides, not the protocol.
func (s *Screen) UnmarshalJSON(b []byte) error {
	var raw struct {
		Width, Height, Scale float64
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*s = Screen{Width: int(math.Round(raw.Width)), Height: int(math.Round(raw.Height)), Scale: raw.Scale}
	return nil
}

// Hello is the agent's first frame.
type Hello struct {
	Version  int      `json:"version"`  // the helper version, 9 and up
	Source   string   `json:"source"`   // the hash of the helper's sources (daemon ADR 0005 point 2)
	Protocol int      `json:"protocol"` // Protocol when it speaks this package's wire
	PID      int      `json:"pid"`
	Trusted  Trust    `json:"trusted"`
	Screen   Screen   `json:"screen"`
	Caps     []string `json:"caps"` // the ops it answers
}

// Request is one call to the agent.
type Request struct {
	Op   string
	Args any // encoded as JSON; nil sends {}
	// Deadline bounds the agent's work; zero or more than MaxDeadline means MaxDeadline. The
	// call waits Deadline plus Options.CallGrace for the answer.
	Deadline time.Duration
	// Reader is the seat whose refs the request uses: coder, verifier or human.
	Reader string
	// Input marks an op that posts events or changes a value, which a PAUSE refuses.
	Input bool
}

// Response is a successful answer.
type Response struct {
	Result   json.RawMessage
	Blob     []byte // the request's BLOB chunks, assembled; nil when it sent none
	BlobMIME string
	Elapsed  time.Duration // what the agent says the op took
}

// Error codes an agent answers with (daemon ADR 0005, "Frames").
const (
	CodeBadRequest    = "bad_request"
	CodeUnknownOp     = "unknown_op"
	CodeStaleRef      = "stale_ref"
	CodeNotFound      = "not_found"
	CodeAmbiguous     = "ambiguous"
	CodeRefused       = "refused" // detail.reason names the actionability check
	CodePaused        = "paused"
	CodeCancelled     = "cancelled"
	CodeDeadline      = "deadline"
	CodeNotTrusted    = "not_trusted"
	CodeNotResponding = "not_responding"
	CodeAXError       = "ax_error"
	CodeCaptureFailed = "capture_failed"
	CodeInternal      = "internal"
)

// Error is the agent's own error answer to a request. Retryable is true only where the same
// request may work again (deadline, not_responding, capture_failed); an input is never retried
// by the daemon.
type Error struct {
	Code      string          `json:"code"`
	Message   string          `json:"message"`
	Retryable bool            `json:"retryable"`
	Detail    json.RawMessage `json:"detail,omitempty"`
}

func (e *Error) Error() string {
	if e.Message == "" {
		return "the guest agent answered " + e.Code
	}
	return e.Message + " (" + e.Code + ")"
}

// Event is one EVENT frame: its kind and the whole payload, which carries the kind's fields.
type Event struct {
	Kind    string
	Payload json.RawMessage
}

// Event kinds the daemon acts on (daemon ADR 0005 point 9).
const (
	EventLog       = "log"       // {message}
	EventStalled   = "stalled"   // {in: "ax"|"capture", seconds}
	EventRecovered = "recovered" // {in}
)

// The errors a call ends with besides an agent's *Error. Each is wrapped with what happened, and
// their texts are what a model reads, so they say what to do next.
var (
	// ErrLost is a channel that ended while the call was outstanding. The call's error goes on
	// to say what that means for it (lostAdvice): an input may or may not have been posted, a
	// read changed nothing.
	ErrLost = errors.New("the guest agent's channel was lost during the call")
	// ErrDeadline is a call the agent did not answer within its deadline plus the grace.
	ErrDeadline = errors.New("the guest agent did not answer in time")
	// ErrUnavailable is no live connection: nothing was sent.
	ErrUnavailable = errors.New("the guest agent is not connected (it is reconnecting); try again in a few seconds")
	// ErrMissingOp is an op the connected agent does not list in its caps.
	ErrMissingOp = errors.New("the guest agent does not offer this operation")
)

// wireRequest is REQUEST's payload.
type wireRequest struct {
	ID         uint32 `json:"id"`
	Op         string `json:"op"`
	Args       any    `json:"args"`
	DeadlineMS int64  `json:"deadlineMs"`
	Reader     string `json:"reader"`
	Input      bool   `json:"input"`
}

// wireResponse is RESPONSE's payload.
type wireResponse struct {
	ID     uint32          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Blob   *struct {
		Bytes int    `json:"bytes"`
		MIME  string `json:"mime"`
	} `json:"blob"`
	MS    float64 `json:"ms"`
	Error *Error  `json:"error"`
}
