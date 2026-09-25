// Package tarball unpacks a gzipped tar that came from somewhere untrusted without letting it
// write outside its directory. The daemon's sync upload route (a client's project) and
// greenroom connect's machine_pull (a guest's files) both use it, so both refuse the same
// things. It imports nothing of the daemon's, so internal/remote may use it (ADR 0021).
package tarball

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ErrTooLarge marks an archive past a Limits bound; the upload route answers 413 for it.
var ErrTooLarge = errors.New("the archive is too large")

// Limits bounds what one archive may unpack to.
type Limits struct {
	Bytes   int64 // total size of the regular files
	Entries int
}

// Stats is what an archive unpacked to: regular files and their total size.
type Stats struct {
	Files int
	Bytes int64
}

// Skip reports whether the entry at rel (a clean slash path under the archive root) is left
// out. It is asked once per entry and must answer for an entry under a skipped directory too.
type Skip func(rel string, isDir bool) bool

// Untar unpacks the gzipped tar r into dir, which must exist. It takes only regular files,
// directories and symlinks, never writes outside dir and never follows a symlink while
// creating an entry: every parent under dir, including one that was there before, is checked
// with Lstat. A symlink must be relative and point inside dir. A file or link replaces one
// already there; a directory merges with one. Permission bits and modification times are
// kept, because rsync -a's quick check (size and mtime) is what keeps a repeat sync from the
// staging directory incremental. skip, when not nil, leaves entries out. A limit breach wraps
// ErrTooLarge; anything else wrong with the archive is an error for the caller to report as a
// bad request.
func Untar(r io.Reader, dir string, lim Limits, skip Skip) (Stats, error) {
	var stats Stats
	gz, err := gzip.NewReader(r)
	if err != nil {
		return stats, fmt.Errorf("not a gzip stream: %w", err)
	}
	tr := tar.NewReader(gz)
	type dirMeta struct {
		path  string
		mode  fs.FileMode
		mtime time.Time
	}
	var dirs []dirMeta
	for n := 0; ; n++ {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return stats, fmt.Errorf("read archive: %w", err)
		}
		if n >= lim.Entries {
			return stats, fmt.Errorf("%w: more than %d entries", ErrTooLarge, lim.Entries)
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue // metadata for the whole archive (git archive writes one), not a file
		}
		rel, err := entryPath(hdr.Name)
		if err != nil {
			return stats, err
		}
		if rel == "." {
			continue // the archive's own root, which is dir
		}
		if skip != nil && skip(rel, hdr.Typeflag == tar.TypeDir) {
			continue
		}
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if err := makeParents(dir, rel); err != nil {
			return stats, err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			st, err := os.Lstat(target)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				if err := os.Mkdir(target, 0o700); err != nil {
					return stats, err
				}
			case err != nil:
				return stats, err
			case !st.IsDir():
				return stats, fmt.Errorf("%q is a directory and an earlier entry", hdr.Name)
			}
			// Applied once every entry is in: creating a child changes its directory's mtime.
			dirs = append(dirs, dirMeta{target, hdr.FileInfo().Mode().Perm(), hdr.ModTime})
		case tar.TypeReg:
			if hdr.Size < 0 || hdr.Size > lim.Bytes-stats.Bytes {
				return stats, fmt.Errorf("%w: more than %d bytes unpacked", ErrTooLarge, lim.Bytes)
			}
			stats.Files++
			stats.Bytes += hdr.Size
			if err := replaceable(target, hdr.Name); err != nil {
				return stats, err
			}
			if err := writeFile(target, tr, hdr); err != nil {
				return stats, err
			}
		case tar.TypeSymlink:
			if err := checkLink(dir, rel, hdr.Linkname); err != nil {
				return stats, fmt.Errorf("symlink %q: %w", hdr.Name, err)
			}
			if err := replaceable(target, hdr.Name); err != nil {
				return stats, err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return stats, err
			}
		default:
			return stats, fmt.Errorf("%q is a %s; only regular files, directories and symlinks are accepted", hdr.Name, typeName(hdr.Typeflag))
		}
	}
	for _, d := range dirs {
		if err := os.Chmod(d.path, d.mode|0o700); err != nil {
			return stats, err
		}
		if err := os.Chtimes(d.path, d.mtime, d.mtime); err != nil {
			return stats, err
		}
	}
	return stats, nil
}

// entryPath is an entry name as a clean slash path relative to the archive root, or an error
// when it is absolute or has a ".." element anywhere.
func entryPath(name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("entry %q: names must be relative", name)
	}
	for _, el := range strings.Split(name, "/") {
		if el == ".." {
			return "", fmt.Errorf("entry %q: names must not have a .. element", name)
		}
	}
	return path.Clean(name), nil
}

// makeParents creates rel's missing parent directories under dir and refuses a parent that is
// a symlink or not a directory, so no write lands through a link an earlier entry made.
func makeParents(dir, rel string) error {
	p := dir
	parts := strings.Split(rel, "/")
	for _, el := range parts[:len(parts)-1] {
		p = filepath.Join(p, el)
		st, err := os.Lstat(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := os.Mkdir(p, 0o700); err != nil {
				return err
			}
		case err != nil:
			return err
		case st.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("entry %q: its parent %q is a symlink", rel, el)
		case !st.IsDir():
			return fmt.Errorf("entry %q: its parent %q is not a directory", rel, el)
		}
	}
	return nil
}

// replaceable clears target for a file or symlink entry. A later entry replaces an earlier
// file or link of the same name, as tar does; removing a link never touches what it points at.
func replaceable(target, name string) error {
	st, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("%q is a file and an earlier directory", name)
	}
	return os.Remove(target)
}

func writeFile(target string, r io.Reader, hdr *tar.Header) error {
	// O_EXCL and O_NOFOLLOW: replaceable just cleared the name, so anything there now is a race to refuse.
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	_, err = io.CopyN(f, r, hdr.Size)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("unpack %q: %w", hdr.Name, err)
	}
	// Owner read at least, or rsync could not copy the file on.
	if err := os.Chmod(target, hdr.FileInfo().Mode().Perm()|0o400); err != nil {
		return err
	}
	return os.Chtimes(target, hdr.ModTime, hdr.ModTime)
}

// checkLink refuses a symlink at rel whose target is absolute or, read as a path from the
// link's own directory, leaves dir.
func checkLink(dir, rel, link string) error {
	if link == "" || strings.HasPrefix(link, "/") {
		return fmt.Errorf("target %q must be a relative path", link)
	}
	resolved := path.Join(path.Dir(rel), link)
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("target %q leaves %s", link, dir)
	}
	return nil
}

func typeName(flag byte) string {
	switch flag {
	case tar.TypeLink:
		return "hard link"
	case tar.TypeChar, tar.TypeBlock:
		return "device"
	case tar.TypeFifo:
		return "fifo"
	}
	return fmt.Sprintf("tar entry of type %q", flag)
}
