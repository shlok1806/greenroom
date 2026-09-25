package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// entry is one tar member for tarball; typ defaults to a regular file.
type entry struct {
	name, body, link string
	typ              byte
	mode             int64
	mtime            time.Time
}

func tarball(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Linkname: e.link, Typeflag: e.typ, Mode: e.mode, ModTime: e.mtime}
		if hdr.Typeflag == 0 {
			hdr.Typeflag = tar.TypeReg
		}
		if hdr.Mode == 0 {
			hdr.Mode = 0o644
		}
		if hdr.ModTime.IsZero() {
			hdr.ModTime = time.Now()
		}
		if hdr.Typeflag == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

var roomy = untarLimits{bytes: 1 << 20, entries: 100}

func TestUntarUnpacksFilesDirsAndLinksWithModesAndTimes(t *testing.T) {
	dir := t.TempDir()
	fileTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	dirTime := time.Date(2025, 6, 7, 8, 9, 10, 0, time.UTC)
	data := tarball(t,
		entry{name: "./", typ: tar.TypeDir, mode: 0o755},
		entry{name: "src/", typ: tar.TypeDir, mode: 0o755, mtime: dirTime},
		entry{name: "src/main.swift", body: "print(1)\n", mtime: fileTime},
		entry{name: "run.sh", body: "#!/bin/sh\n", mode: 0o755},
		entry{name: "deep/er/file.txt", body: "no dir entries before me"},
		entry{name: "latest", typ: tar.TypeSymlink, link: "src/main.swift"},
		entry{name: "src/up", typ: tar.TypeSymlink, link: "../run.sh"},
		entry{name: "self", typ: tar.TypeSymlink, link: "."},
	)
	if err := untar(bytes.NewReader(data), dir, roomy); err != nil {
		t.Fatalf("untar: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "src", "main.swift"))
	if err != nil || string(b) != "print(1)\n" {
		t.Fatalf("src/main.swift = %q, %v", b, err)
	}
	st, err := os.Stat(filepath.Join(dir, "src", "main.swift"))
	if err != nil {
		t.Fatal(err)
	}
	if !st.ModTime().Equal(fileTime) {
		t.Errorf("file mtime %v, want %v: a repeat rsync would copy it again", st.ModTime(), fileTime)
	}
	if st.Mode().Perm() != 0o644 {
		t.Errorf("file mode %v, want 0644", st.Mode().Perm())
	}
	if st, err := os.Stat(filepath.Join(dir, "run.sh")); err != nil || st.Mode().Perm() != 0o755 {
		t.Errorf("run.sh mode = %v, %v; want 0755", st.Mode().Perm(), err)
	}
	// Written after its children, or creating them would have moved it.
	if st, err := os.Stat(filepath.Join(dir, "src")); err != nil || !st.ModTime().Equal(dirTime) {
		t.Errorf("src mtime = %v, %v; want %v", st.ModTime(), err, dirTime)
	}
	if _, err := os.ReadFile(filepath.Join(dir, "deep", "er", "file.txt")); err != nil {
		t.Errorf("missing parents were not made: %v", err)
	}
	if link, err := os.Readlink(filepath.Join(dir, "latest")); err != nil || link != "src/main.swift" {
		t.Errorf("latest -> %q, %v", link, err)
	}
	if link, err := os.Readlink(filepath.Join(dir, "src", "up")); err != nil || link != "../run.sh" {
		t.Errorf("src/up -> %q, %v", link, err)
	}
}

func TestUntarRefusesWhatWouldEscape(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []entry
		want    string
	}{
		{"parent traversal", []entry{{name: "../evil", body: "x"}}, ".."},
		{"traversal inside a name", []entry{{name: "a/../../evil", body: "x"}}, ".."},
		{"traversal that cleans inside", []entry{{name: "a/../b", body: "x"}}, ".."},
		{"absolute name", []entry{{name: "/tmp/evil", body: "x"}}, "relative"},
		{"absolute symlink", []entry{{name: "l", typ: tar.TypeSymlink, link: "/etc"}}, "relative"},
		{"escaping symlink", []entry{{name: "l", typ: tar.TypeSymlink, link: ".."}}, "leaves"},
		{"escaping symlink from a subdir", []entry{{name: "a/l", typ: tar.TypeSymlink, link: "../../x"}}, "leaves"},
		{"write through a link to .", []entry{{name: "l", typ: tar.TypeSymlink, link: "."}, {name: "l/x", body: "x"}}, "symlink"},
		{"write through a link to a dir", []entry{{name: "d/", typ: tar.TypeDir}, {name: "l", typ: tar.TypeSymlink, link: "d"}, {name: "l/x", body: "x"}}, "symlink"},
		{"hard link", []entry{{name: "f", body: "x"}, {name: "h", typ: tar.TypeLink, link: "f"}}, "hard link"},
		{"fifo", []entry{{name: "p", typ: tar.TypeFifo}}, "fifo"},
		{"device", []entry{{name: "d", typ: tar.TypeChar}}, "device"},
		{"file over a directory", []entry{{name: "d/", typ: tar.TypeDir}, {name: "d", body: "x"}}, "directory"},
		{"file as a parent", []entry{{name: "f", body: "x"}, {name: "f/g", body: "y"}}, "not a directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outer := t.TempDir()
			dir := filepath.Join(outer, "staging")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			err := untar(bytes.NewReader(tarball(t, tc.entries...)), dir, roomy)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("untar = %v, want an error about %q", err, tc.want)
			}
			if errors.Is(err, errTooLarge) {
				t.Errorf("a bad archive is not a large one: %v", err)
			}
			for _, name := range []string{"evil", "x"} {
				if _, err := os.Lstat(filepath.Join(outer, name)); err == nil {
					t.Errorf("%s was written outside the staging directory", name)
				}
			}
		})
	}
}

func TestUntarReplacesAnEarlierLinkWithoutFollowingIt(t *testing.T) {
	outer := t.TempDir()
	dir := filepath.Join(outer, "staging")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "target"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	data := tarball(t, entry{name: "l", typ: tar.TypeSymlink, link: "target"}, entry{name: "l", body: "new"})
	if err := untar(bytes.NewReader(data), dir, roomy); err != nil {
		t.Fatalf("untar: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "target")); string(b) != "keep" {
		t.Errorf("the link's target was written through: %q", b)
	}
	if st, err := os.Lstat(filepath.Join(dir, "l")); err != nil || !st.Mode().IsRegular() {
		t.Errorf("l is not the later regular file: %v", err)
	}
}

func TestUntarLimits(t *testing.T) {
	big := tarball(t, entry{name: "a", body: strings.Repeat("x", 600)}, entry{name: "b", body: strings.Repeat("y", 600)})
	err := untar(bytes.NewReader(big), t.TempDir(), untarLimits{bytes: 1000, entries: 10})
	if !errors.Is(err, errTooLarge) {
		t.Errorf("1200 bytes under a 1000 byte cap: %v, want errTooLarge", err)
	}
	if err := untar(bytes.NewReader(big), t.TempDir(), untarLimits{bytes: 1200, entries: 10}); err != nil {
		t.Errorf("1200 bytes at a 1200 byte cap: %v", err)
	}
	many := tarball(t, entry{name: "a"}, entry{name: "b"}, entry{name: "c"})
	if err := untar(bytes.NewReader(many), t.TempDir(), untarLimits{bytes: 1000, entries: 2}); !errors.Is(err, errTooLarge) {
		t.Errorf("3 entries under a cap of 2: %v, want errTooLarge", err)
	}
}

func TestUntarRefusesAMalformedArchive(t *testing.T) {
	if err := untar(strings.NewReader("not gzip"), t.TempDir(), roomy); err == nil || errors.Is(err, errTooLarge) {
		t.Errorf("plain text: %v, want a bad-archive error", err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte(strings.Repeat("garbage!", 100)))
	_ = gz.Close()
	if err := untar(&buf, t.TempDir(), roomy); err == nil || errors.Is(err, errTooLarge) {
		t.Errorf("gzip of garbage: %v, want a bad-archive error", err)
	}
	whole := tarball(t, entry{name: "a", body: strings.Repeat("x", 4096)})
	cut := whole[:len(whole)/2]
	if err := untar(bytes.NewReader(cut), t.TempDir(), roomy); err == nil {
		t.Error("a truncated archive unpacked without an error")
	}
}
