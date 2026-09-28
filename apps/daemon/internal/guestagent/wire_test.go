package guestagent

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"testing"
)

func TestFramesRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	payloads := [][]byte{nil, []byte(`{"t":1}`), bytes.Repeat([]byte{7}, MaxPayload)}
	for i, p := range payloads {
		if err := WriteFrame(&buf, byte(0x20+i), p); err != nil {
			t.Fatal(err)
		}
	}
	for i, want := range payloads {
		typ, got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if typ != byte(0x20+i) || !bytes.Equal(got, want) {
			t.Fatalf("frame %d: got type 0x%02x and %d bytes, want 0x%02x and %d", i, typ, len(got), 0x20+i, len(want))
		}
	}
	if _, _, err := ReadFrame(&buf); !errors.Is(err, io.EOF) {
		t.Fatalf("a clean end between frames: got %v, want io.EOF", err)
	}
}

func TestFramesOverTheLimitAreRefusedBothWays(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, TypeResponse, make([]byte, MaxPayload+1)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("writing: got %v, want ErrFrameTooLarge", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("a refused frame wrote %d bytes", buf.Len())
	}
	// A header claiming 4 GiB is refused before anything is allocated or read.
	head := []byte{TypeResponse, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(head[1:], 0xFFFFFFFF)
	if _, _, err := ReadFrame(bytes.NewReader(head)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("reading: got %v, want ErrFrameTooLarge", err)
	}
}

func TestTruncatedFramesAreUnexpectedEOF(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, TypeEvent, []byte(`{"kind":"log"}`)); err != nil {
		t.Fatal(err)
	}
	whole := buf.Bytes()
	for _, n := range []int{3, 5, len(whole) - 1} {
		if _, _, err := ReadFrame(bytes.NewReader(whole[:n])); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("a frame cut at %d of %d bytes: got %v, want io.ErrUnexpectedEOF", n, len(whole), err)
		}
	}
}

func TestBlobChunksCarryDataInMiBPieces(t *testing.T) {
	data := make([]byte, 2*MaxBlobChunk+12345)
	for i := range data {
		data[i] = byte(i * 7)
	}
	chunks := BlobChunks(9, data)
	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3", len(chunks))
	}
	var joined []byte
	for i, c := range chunks {
		id, seq, last, part, err := DecodeBlobChunk(c)
		if err != nil {
			t.Fatal(err)
		}
		if id != 9 || int(seq) != i || last != (i == 2) || len(part) > MaxBlobChunk {
			t.Fatalf("chunk %d: id %d seq %d last %v, %d bytes", i, id, seq, last, len(part))
		}
		joined = append(joined, part...)
	}
	if !bytes.Equal(joined, data) {
		t.Fatal("the chunks do not rebuild the data")
	}
	if chunks := BlobChunks(1, nil); len(chunks) != 1 || len(chunks[0]) != blobHeader || chunks[0][6] != 1 {
		t.Fatalf("empty data: got %d chunks %v, want one empty last chunk", len(chunks), chunks)
	}
	if _, _, _, _, err := DecodeBlobChunk([]byte{0, 0, 1}); err == nil {
		t.Fatal("a BLOB without its header was accepted")
	}
}

func TestScreenTakesWholeNumbersWrittenAsFloats(t *testing.T) {
	var h Hello
	if err := json.Unmarshal([]byte(`{"screen":{"width":1024.0,"height":768,"scale":2}}`), &h); err != nil {
		t.Fatal(err)
	}
	if h.Screen != (Screen{Width: 1024, Height: 768, Scale: 2}) {
		t.Fatalf("got %+v", h.Screen)
	}
}

func TestAgentErrorsReadAsTheirMessage(t *testing.T) {
	e := &Error{Code: CodeRefused, Message: "e41 is covered by e70"}
	if e.Error() != "e41 is covered by e70 (refused)" {
		t.Fatalf("got %q", e.Error())
	}
}
