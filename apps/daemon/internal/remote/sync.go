package remote

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// SyncArgs is machine_sync's input, as the daemon defines it.
type SyncArgs struct {
	RunID   string   `json:"runId"`
	Source  string   `json:"source"`
	Dest    string   `json:"dest,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
	Mirror  bool     `json:"mirror,omitempty"`
}

// RefusedError is the daemon refusing a request connect made itself (an upload, a pull, a
// download): what it was, its status and the daemon's message.
type RefusedError struct {
	Op      string
	Status  int
	Message string
}

func (e *RefusedError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = "no message"
	}
	return fmt.Sprintf("the daemon refused the %s (HTTP %d %s): %s", e.Op, e.Status, http.StatusText(e.Status), msg)
}

// errUploadDone stops the tar writer once the request is over.
var errUploadDone = errors.New("upload finished")

// maxResponse caps what is read of the daemon's answer to an upload.
const maxResponse = 1 << 20

// Upload tars and gzips args.Source as it walks it, streaming, and PUTs it to
// <base>/api/runs/{runId}/sync. It returns the daemon's JSON result (machine.SyncResult) as is.
func Upload(ctx context.Context, client *http.Client, base string, args SyncArgs) (json.RawMessage, error) {
	if args.RunID == "" {
		return nil, errors.New("runId is required")
	}
	if !filepath.IsAbs(args.Source) {
		return nil, fmt.Errorf("source %q must be an absolute path on this computer", args.Source)
	}
	source := filepath.Clean(args.Source)
	// Only the root is resolved: a project reached through a symlink is still that project.
	root, err := filepath.EvalSymlinks(source)
	if err != nil {
		return nil, fmt.Errorf("source %q: %w", args.Source, err)
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("source %q is not a directory", args.Source)
	}
	excludes := ParseExcludes(args.Exclude)

	q := url.Values{"name": {filepath.Base(source)}}
	if args.Dest != "" {
		q.Set("dest", args.Dest)
	}
	// The archive leaves excluded paths out; the daemon needs the patterns too, so a mirror
	// keeps the guest's copies of them and the stray count skips them (daemon ADR 0001).
	for _, ex := range args.Exclude {
		if ex = strings.TrimSpace(ex); ex != "" {
			q.Add("exclude", ex)
		}
	}
	if args.Mirror {
		q.Set("mirror", "true")
	}
	target := base + "/api/runs/" + url.PathEscape(args.RunID) + "/sync?" + q.Encode()

	pr, pw := io.Pipe()
	tarDone := make(chan error, 1)
	go func() {
		err := WriteTarGz(pw, root, excludes)
		_ = pw.CloseWithError(err)
		tarDone <- err
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, pr)
	if err != nil {
		_ = pr.CloseWithError(err)
		<-tarDone
		return nil, err
	}
	req.Header.Set("Content-Type", "application/gzip")
	resp, err := client.Do(req)
	// The daemon may answer before reading everything (a 413); stop the writer either way.
	_ = pr.CloseWithError(errUploadDone)
	tarErr := <-tarDone
	if errors.Is(tarErr, errUploadDone) {
		tarErr = nil
	}
	if err != nil {
		if tarErr != nil {
			return nil, fmt.Errorf("pack %s: %w", source, tarErr)
		}
		return nil, describeTransportError(base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if resp.StatusCode/100 != 2 {
		return nil, &RefusedError{Op: "upload", Status: resp.StatusCode, Message: daemonMessage(body)}
	}
	if tarErr != nil {
		return nil, fmt.Errorf("pack %s: %w", source, tarErr)
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("the daemon answered the upload with something that is not JSON: %.200s", body)
	}
	return body, nil
}

// daemonMessage is the "error" of a JSON body, or the body's text.
func daemonMessage(body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return e.Error
	}
	return strings.TrimSpace(string(body))
}

// WriteTarGz writes root's contents as a gzipped tar: names relative to root, directories
// included, symlinks as symlinks (never followed), regular files with their mode and
// mtime. Sockets, devices and named pipes are skipped. Owners are not recorded.
func WriteTarGz(w io.Writer, root string, excludes Excludes) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if excludes.Match(rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return addEntry(tw, p, rel, info)
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func addEntry(tw *tar.Writer, p, rel string, info fs.FileInfo) error {
	var link string
	switch mode := info.Mode(); {
	case mode.IsDir(), mode.IsRegular():
	case mode&fs.ModeSymlink != 0:
		target, err := os.Readlink(p)
		if err != nil {
			return err
		}
		link = target
	default: // socket, device, named pipe
		return nil
	}
	hdr, err := tar.FileInfoHeader(info, link)
	if err != nil {
		return err
	}
	hdr.Name = rel
	if info.IsDir() {
		hdr.Name += "/"
	}
	hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname = 0, 0, "", ""
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := io.CopyN(tw, f, hdr.Size); err != nil {
		return fmt.Errorf("%s changed while it was read: %w", rel, err)
	}
	return nil
}

// Excludes are rsync-style exclude patterns, the simple forms:
//   - no slash: matches the base name of any path component ("node_modules", "*.log")
//   - a slash inside: matches the path relative to source, at a component boundary ("docs/tmp")
//   - a leading slash: anchored at the source root ("/dist")
//   - a trailing slash: directories only ("build/")
//
// Glob syntax is path.Match's; rsync's "**" and include rules are not supported.
type Excludes []exclude

type exclude struct {
	glob     string
	anchored bool
	hasSlash bool
	dirOnly  bool
}

// ParseExcludes reads machine_sync's exclude list. Empty patterns are ignored.
func ParseExcludes(patterns []string) Excludes {
	var out Excludes
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		e := exclude{}
		if strings.HasSuffix(p, "/") {
			e.dirOnly = true
			p = strings.TrimRight(p, "/")
		}
		if strings.HasPrefix(p, "/") {
			e.anchored = true
			p = strings.TrimLeft(p, "/")
		}
		if p == "" {
			continue
		}
		e.hasSlash = e.anchored || strings.Contains(p, "/")
		e.glob = p
		out = append(out, e)
	}
	return out
}

// Covers reports whether rel or a directory above it is excluded. An archive is a flat list
// of paths, with nothing to skip a directory's contents the way the walk does.
func (x Excludes) Covers(rel string, isDir bool) bool {
	for i := 1; i < len(rel); i++ {
		if rel[i] == '/' && x.Match(rel[:i], true) {
			return true
		}
	}
	return x.Match(rel, isDir)
}

// Match reports whether rel (slash-separated, relative to source) is excluded. The walk
// asks for every component on the way down and skips an excluded directory whole, so a
// base-name pattern excludes a match at any depth.
func (x Excludes) Match(rel string, isDir bool) bool {
	for _, e := range x {
		if e.dirOnly && !isDir {
			continue
		}
		switch {
		case !e.hasSlash:
			if ok, _ := path.Match(e.glob, path.Base(rel)); ok {
				return true
			}
		case e.anchored:
			if ok, _ := path.Match(e.glob, rel); ok {
				return true
			}
		default:
			for i := 0; i < len(rel); i++ {
				if i > 0 && rel[i-1] != '/' {
					continue
				}
				if ok, _ := path.Match(e.glob, rel[i:]); ok {
					return true
				}
			}
		}
	}
	return false
}
