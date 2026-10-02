package machine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// current is the VM suite's name for an image of this daemon's helper and recipe.
func current() string {
	return fmt.Sprintf("greenroom-base-v%d-r%d", inputHelperVersion, imageRecipeVersion)
}

func writeList(t *testing.T, control string, vms ...string) {
	t.Helper()
	var parts []string
	for _, v := range vms {
		parts = append(parts, `{"Source":"local","Name":"`+v+`","State":"stopped"}`)
	}
	if err := os.WriteFile(filepath.Join(control, "list.json"), []byte("["+strings.Join(parts, ",")+"]"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Issue #285: the configured default image was gone and machine_create failed with only tart's
// "does not exist". Now the failure names the local images it could use and the build command,
// and is still recorded in the run like any failed clone.
func TestCreateFromAMissingLocalImageNamesWhatIsThere(t *testing.T) {
	mgr, root, control := newTestManager(t)
	testsupport.Flag(t, control, "fail-clone")
	writeList(t, control, "greenroom-base-v7-r2", current(), "greenroom-20261001-165657-dbf50fe1589037b9", "gr249-probe")

	_, err := mgr.Create(context.Background(), "greenroom-lean-a")
	var missing *MissingImageError
	if !errors.As(err, &missing) {
		t.Fatalf("Create = %v, want a MissingImageError", err)
	}
	for _, want := range []string{
		`image "greenroom-lean-a" is not on this host`,
		"Local greenroom images: " + current() + " (named for this daemon's input helper",
		", greenroom-base-v7-r2. Call machine_create again with one of them as image",
		"scripts/build-image.sh -lean -name greenroom-lean-a",
		"greenroom image-status",
		"image not found", // tart's own message stays
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error lacks %q:\n%v", want, err)
		}
	}
	for _, not := range []string{"greenroom-20261001", "gr249-probe"} {
		if strings.Contains(err.Error(), not) {
			t.Errorf("the error offers %s, which is not an image:\n%v", not, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(root, "runs"))
	if len(entries) != 1 {
		t.Fatalf("the failed create wrote %d run directories, want 1", len(entries))
	}
	if steps := readSteps(t, filepath.Join(root, "runs", entries[0].Name())); len(steps) != 1 || !strings.Contains(steps[0].Error, "is not on this host") {
		t.Errorf("the run's step does not carry the guidance: %+v", steps)
	}
}

// A clone that fails for another reason keeps tart's error alone: the image is there.
func TestCreateFromAPresentImageKeepsTartsError(t *testing.T) {
	mgr, _, control := newTestManager(t)
	testsupport.Flag(t, control, "fail-clone")
	writeList(t, control, "greenroom-lean-a")

	_, err := mgr.Create(context.Background(), "greenroom-lean-a")
	if err == nil || strings.Contains(err.Error(), "not on this host") || !strings.Contains(err.Error(), "image not found") {
		t.Errorf("Create = %v, want tart's error alone", err)
	}
}

func TestMissingImage(t *testing.T) {
	for _, tc := range []struct {
		name, image, list string
		missing           bool
	}{
		{"absent", "greenroom-lean-a", `[{"Source":"local","Name":"greenroom-base","State":"stopped"}]`, true},
		{"present", "greenroom-lean-a", `[{"Source":"local","Name":"greenroom-lean-a","State":"stopped"}]`, false},
		{"only an OCI copy", "greenroom-lean-a", `[{"Source":"OCI","Name":"greenroom-lean-a","State":"stopped"}]`, true},
		// tart pulls an OCI reference on clone, so it is never missing here.
		{"OCI reference", "ghcr.io/cirruslabs/macos-tahoe-base:latest", `[]`, false},
		// A tart that cannot list leaves the clone's own error to speak.
		{"tart cannot list", "greenroom-lean-a", `not json`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr, _, control := newTestManager(t)
			if err := os.WriteFile(filepath.Join(control, "list.json"), []byte(tc.list), 0o644); err != nil {
				t.Fatal(err)
			}
			err := mgr.MissingImage(context.Background(), tc.image)
			if (err != nil) != tc.missing {
				t.Errorf("MissingImage(%q) = %v, want missing %v", tc.image, err, tc.missing)
			}
		})
	}
}

func TestMissingImageWithNoLocalImageSaysSo(t *testing.T) {
	err := CheckImageOnHost("greenroom-base", []tart.VM{{Source: "local", Name: "gr249-probe"}})
	if err == nil {
		t.Fatal("no error for a missing image")
	}
	msg := err.Error()
	if !strings.Contains(msg, "This host has no local greenroom image at all") || strings.Contains(msg, "Call machine_create again") {
		t.Errorf("the error offers images there are none of:\n%s", msg)
	}
	if !strings.Contains(msg, "(from apps/daemon: scripts/build-image.sh, about 20 GB free)") {
		t.Errorf("the error lacks the bare build command for greenroom-base:\n%s", msg)
	}
}

func TestLocalImagesAreGreenroomImagesCurrentFirst(t *testing.T) {
	vms := []tart.VM{
		{Source: "local", Name: "greenroom-lean-a"},
		{Source: "local", Name: "greenroom-base-v7-r2"},
		{Source: "local", Name: current(), State: "running"},
		{Source: "local", Name: "greenroom-20261002-000722-ff76250f706f68f6", State: "running"}, // a run's clone
		{Source: "local", Name: "greenroom-lean-a-building"},
		{Source: "local", Name: "greenroom-lean-a-check-0a1b2c3d"},
		{Source: "local", Name: "gr282-norow"},
		{Source: "OCI", Name: "ghcr.io/cirruslabs/macos-tahoe-base:latest"},
	}
	got := strings.Join(LocalImages(vms), ",")
	want := current() + ",greenroom-base-v7-r2,greenroom-lean-a"
	if got != want {
		t.Errorf("LocalImages = %s, want %s", got, want)
	}
	for name, want := range map[string]bool{
		current():               true,
		"greenroom-base-v7-r2":  false,
		"greenroom-lean-a":      false,
		current() + "-building": false,
		"greenroom-base-vX-r4":  false,
		"greenroom-base-v10":    false,
	} {
		if NamedForThisDaemon(name) != want {
			t.Errorf("NamedForThisDaemon(%q) = %v, want %v", name, !want, want)
		}
	}
}

func TestBuildArgs(t *testing.T) {
	for image, want := range map[string][2]string{
		"greenroom-base":        {"", "-force"},
		"greenroom-lean-a":      {"-lean -name greenroom-lean-a", "-lean -name greenroom-lean-a -force"},
		"greenroom-base-v10-r4": {"-name greenroom-base-v10-r4", "-name greenroom-base-v10-r4 -force"},
	} {
		if got := BuildArgs(image); got != want[0] {
			t.Errorf("BuildArgs(%q) = %q, want %q", image, got, want[0])
		}
		if got := RebuildArgs(image); got != want[1] {
			t.Errorf("RebuildArgs(%q) = %q, want %q", image, got, want[1])
		}
	}
}
