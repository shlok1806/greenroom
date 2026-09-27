package machine

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// guestRoot is a data volume's tree with the given helpers and manifest ("" for none).
func guestRoot(t *testing.T, helpers []string, manifest string) string {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "Users", "admin", ".greenroom", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, h := range helpers {
		if err := os.WriteFile(filepath.Join(bin, h), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if manifest != "" {
		path := filepath.Join(root, ToolchainPath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// Issue #159: greenroom-lean-a was built before helper 7 and before image recipes, so every
// machine from it compiled the helper at boot and had no Xcode. Its disk says so, and the
// status names the command that rebuilds it.
func TestAnImageFromBeforeTheHelperAndRecipeIsStale(t *testing.T) {
	root := guestRoot(t, []string{"greenroom-input-6"}, "")
	helpers, manifest, err := ReadImageFacts(root)
	if err != nil {
		t.Fatal(err)
	}
	st := JudgeImage("greenroom-lean-a", helpers, manifest)
	if st.State != ImageStale || st.Rebuild != "-lean -name greenroom-lean-a -force" || len(st.Reasons) != 2 {
		t.Fatalf("status = %+v, want stale with both reasons and the lean rebuild", st)
	}
	if !strings.Contains(st.Reasons[0], "its input helper is 6, this daemon's is "+strconv.Itoa(inputHelperVersion)) ||
		!strings.Contains(st.Reasons[1], "it records no image recipe, this daemon's is 2") {
		t.Errorf("reasons = %q", st.Reasons)
	}
}

func TestAnImageWithThisDaemonsHelperAndRecipeIsCurrent(t *testing.T) {
	root := guestRoot(t, []string{"greenroom-input-6", "greenroom-input-" + strconv.Itoa(inputHelperVersion)}, `{"known":true,"imageRecipe":2}`)
	helpers, manifest, err := ReadImageFacts(root)
	if err != nil {
		t.Fatal(err)
	}
	if st := JudgeImage("greenroom-base", helpers, manifest); st.State != ImageCurrent || st.Recipe != 2 || st.Rebuild != "" {
		t.Errorf("status = %+v, want current", st)
	}
	// An older recipe alone is stale too.
	st := JudgeImage("greenroom-base", []int{inputHelperVersion}, map[string]any{"known": true, "imageRecipe": float64(1)})
	if st.State != ImageStale || st.Rebuild != "-force" || !strings.Contains(st.Reasons[0], "it records image recipe 1") {
		t.Errorf("status = %+v", st)
	}
}

func TestRebuildArgsKeepTheImagesProfileAndName(t *testing.T) {
	for image, want := range map[string]string{
		"greenroom-lean-a": "-lean -name greenroom-lean-a -force",
		"greenroom-base":   "-force",
		"greenroom-mine":   "-name greenroom-mine -force",
	} {
		if got := RebuildArgs(image); got != want {
			t.Errorf("RebuildArgs(%s) = %q, want %q", image, got, want)
		}
	}
}
