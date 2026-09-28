package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/tools-without-toolkit.json from the server")

// Without -desktop-toolkit the tool surface is exactly what it was before wave 1 (daemon ADR
// 0006 point 9): the same tools, descriptions and schemas, byte for byte. The golden file was
// written from the server before the toolkit's tools existed; regenerate it with -update only
// for a change meant for every daemon, toolkit or not.
func TestTheToolListWithoutTheToolkitIsUnchanged(t *testing.T) {
	h := newHarness(t)
	res, err := h.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.MarshalIndent(res.Tools, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "tools-without-toolkit.json")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the tool list without the toolkit changed; it must stay what it was before wave 1.\ngot:\n%s", got)
	}
}
