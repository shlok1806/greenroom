package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// Issue #285: with greenroom-lean-a and greenroom-base gone, image-status said only "not on this
// host" although greenroom-base-v10-r4 was there and current. It now lists the other local
// greenroom images with their state, and never offers to rebuild one of them.
func TestImageStatusListsOtherLocalImages(t *testing.T) {
	home := t.TempDir()
	current := fmt.Sprintf("greenroom-base-v%d-r%d", machine.InputHelperVersion(), machine.ImageRecipeVersion())
	trees := map[string]string{}
	for name, tree := range map[string]struct{ helper, manifest string }{
		current:                {"greenroom-input-" + strconv.Itoa(machine.InputHelperVersion()), fmt.Sprintf(`{"known":true,"imageRecipe":%d}`, machine.ImageRecipeVersion())},
		"greenroom-base-v7-r2": {"greenroom-input-7", `{"known":true,"imageRecipe":2}`},
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
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, tree.helper), nil, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(root, machine.ToolchainPath)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(tree.manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		trees[disk] = root
	}
	orig := mountImage
	mountImage = func(_ context.Context, disk string) (string, func(), error) { return trees[disk], func() {}, nil }
	t.Cleanup(func() { mountImage = orig })

	vms := []tart.VM{
		{Source: "local", Name: "greenroom-base-v7-r2", State: "stopped"},
		{Source: "local", Name: current, State: "stopped"},
		{Source: "local", Name: "greenroom-base", State: "stopped"}, // asked about, so not "other"
		{Source: "local", Name: "greenroom-20261002-000722-ff76250f706f68f6", State: "running"},
		{Source: "local", Name: "gr249-probe", State: "stopped"},
	}
	others := otherImages([]string{"greenroom-lean-a", "greenroom-base"}, vms)
	if got, want := strings.Join(others, ","), current+",greenroom-base-v7-r2"; got != want {
		t.Fatalf("otherImages = %s, want %s", got, want)
	}
	var out strings.Builder
	printOtherImages(&out, checkImages(context.Background(), others, vms, home))
	for _, want := range []string{
		"Other local greenroom images (serve one with greenroom serve -image <name>, or GREENROOM_IMAGE=<name> scripts/install.sh):\n",
		"  " + current + ": current (input helper " + strconv.Itoa(machine.InputHelperVersion()),
		"  greenroom-base-v7-r2: stale: its input helper is 7",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "build-image.sh") || strings.Contains(out.String(), "-rebuild") {
		t.Errorf("an image that is not a default name is offered a rebuild:\n%s", out.String())
	}

	out.Reset()
	printOtherImages(&out, nil)
	if out.Len() != 0 {
		t.Errorf("no other images printed %q", out.String())
	}
}

// Issue #285: serve started cleanly and advertised a default image tart did not have. It now
// warns at start, naming the local images and the build command, and says nothing when the
// image is there or is an OCI reference tart pulls on clone.
func TestServeWarnsWhenTheDefaultImageIsMissing(t *testing.T) {
	for _, tc := range []struct {
		name, image string
		warns       bool
	}{
		{"missing", "greenroom-lean-a", true},
		{"present", "greenroom-base-v10-r4", false},
		{"OCI reference", defaultImage, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin, control := testsupport.FakeTart(t)
			list := `[{"Source":"local","Name":"greenroom-base-v10-r4","State":"stopped"},{"Source":"local","Name":"gr249-probe","State":"stopped"}]`
			if err := os.WriteFile(filepath.Join(control, "list.json"), []byte(list), 0o644); err != nil {
				t.Fatal(err)
			}
			quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
			mgr, err := machine.NewManager(t.TempDir(), quiet, machine.WithTartBin(bin))
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			warnMissingImage(slog.New(slog.NewTextHandler(&buf, nil)), mgr.MissingImage(context.Background(), tc.image))
			if !tc.warns {
				if buf.Len() != 0 {
					t.Errorf("warned about an image that is fine: %s", buf.String())
				}
				return
			}
			for _, want := range []string{
				"level=WARN", "the default image is not on this host", "image=greenroom-lean-a",
				`localImages="greenroom-base-v10-r4`, `build="cd apps/daemon && scripts/build-image.sh -lean -name greenroom-lean-a"`,
			} {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("the warning lacks %q:\n%s", want, buf.String())
				}
			}
			if strings.Contains(buf.String(), "gr249-probe") {
				t.Errorf("the warning offers a VM that is not an image:\n%s", buf.String())
			}
		})
	}
}
