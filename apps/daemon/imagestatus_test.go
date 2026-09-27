package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// Issue #159: install.sh prints, for each local default image, whether its helper and recipe are
// this daemon's, and the command that rebuilds a stale one. A running image is not read, and a
// missing one is said.
func TestImageStatusNamesStaleImagesAndTheirRebuild(t *testing.T) {
	home := t.TempDir()
	trees := map[string]string{}
	for name, tree := range map[string]struct{ helper, manifest string }{
		"greenroom-lean-a": {"greenroom-input-6", ""},
		"greenroom-base":   {"greenroom-input-7", `{"known":true,"imageRecipe":2}`},
	} {
		disk := filepath.Join(home, "vms", name, "disk.img")
		if err := os.MkdirAll(filepath.Dir(disk), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(disk, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		root := t.TempDir()
		bin := filepath.Join(root, "Users", "admin", ".greenroom", "bin")
		_ = os.MkdirAll(bin, 0o755)
		_ = os.WriteFile(filepath.Join(bin, tree.helper), nil, 0o755)
		if tree.manifest != "" {
			p := filepath.Join(root, machine.ToolchainPath)
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			_ = os.WriteFile(p, []byte(tree.manifest), 0o644)
		}
		trees[disk] = root
	}
	mounted := 0
	orig := mountImage
	mountImage = func(_ context.Context, disk string) (string, func(), error) {
		mounted++
		return trees[disk], func() { mounted-- }, nil
	}
	t.Cleanup(func() { mountImage = orig })

	vms := []tart.VM{{Source: "local", Name: "greenroom-lean-a", State: "stopped"}, {Source: "local", Name: "greenroom-base", State: "stopped"},
		{Source: "local", Name: "greenroom-busy", State: "running"}}
	statuses := checkImages(context.Background(), []string{"greenroom-lean-a", "greenroom-base", "greenroom-busy", "greenroom-gone"}, vms, home)
	if mounted != 0 {
		t.Errorf("%d disks left mounted", mounted)
	}
	var out strings.Builder
	printImageStatuses(&out, statuses)
	for _, want := range []string{
		"greenroom-lean-a: stale: its input helper is 6, this daemon's is 7",
		"it records no image recipe, this daemon's is 2",
		"  Rebuild it (needs about 20 GB free): scripts/build-image.sh -lean -name greenroom-lean-a -force\n",
		"greenroom-base: current (input helper 7, image recipe 2)\n",
		"greenroom-busy: running, so not checked",
		"greenroom-gone: not on this host\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}
