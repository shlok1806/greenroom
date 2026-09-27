package diskimage

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// rawDisk makes a small raw APFS disk image, as tart keeps disk.img, whose volume is named Data
// and holds files.
func rawDisk(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("hdiutil"); err != nil {
		t.Skip("needs macOS's hdiutil")
	}
	src := t.TempDir()
	for name, body := range files {
		path := filepath.Join(src, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	out, err := exec.Command("hdiutil", "create", "-quiet", "-srcfolder", src, "-fs", "APFS", "-volname", "Data",
		"-format", "UDTO", "-o", filepath.Join(dir, "disk")).CombinedOutput()
	if err != nil {
		t.Fatalf("hdiutil create: %v: %s", err, out)
	}
	disk := filepath.Join(dir, "disk.img")
	if err := os.Rename(filepath.Join(dir, "disk.cdr"), disk); err != nil {
		t.Fatal(err)
	}
	return disk
}

// Issue #159: a stopped VM's disk is read on the host, read-only, and everything it attached
// is gone after Close.
func TestAStoppedDiskIsReadReadOnly(t *testing.T) {
	disk := rawDisk(t, map[string]string{"usr/local/greenroom/toolchain.json": `{"known":true,"imageRecipe":2}`})
	before, err := os.ReadFile(disk)
	if err != nil {
		t.Fatal(err)
	}
	m, err := MountReadOnly(context.Background(), disk)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(m.Root, "usr/local/greenroom/toolchain.json"))
	if err != nil || string(got) != `{"known":true,"imageRecipe":2}` {
		t.Errorf("read %q, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(m.Root, "x"), []byte("x"), 0o644); err == nil {
		t.Error("the mounted volume took a write")
	}
	root, whole := m.Root, m.whole
	m.Close()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("the mount point is still there: %v", err)
	}
	if out, _ := exec.Command("hdiutil", "info").Output(); len(whole) > 0 && strings.Contains(string(out), whole+"\t") {
		t.Errorf("%s is still attached", whole)
	}
	after, _ := os.ReadFile(disk)
	if string(after) != string(before) {
		t.Error("the disk image changed")
	}
}
