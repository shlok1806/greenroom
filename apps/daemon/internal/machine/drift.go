package machine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Image drift (issue #159). A local image built before this daemon's input helper version or
// image recipe makes every machine from it compile the helper at boot, or lack what the recipe
// now adds (Xcode, ADR 0026); boot only warns, per machine. These read an image's data volume,
// mounted on the host while the image is stopped (internal/diskimage), so install.sh can say it
// once, before any machine, with the command that rebuilds it.

// Image states for ImageStatus.State.
const (
	ImageCurrent = "current" // this daemon's helper and recipe
	ImageStale   = "stale"   // an older helper or recipe: rebuild it
	ImageAbsent  = "absent"  // not on this host
	ImageRunning = "running" // a VM by that name runs; its disk is not read while it does
	ImageUnknown = "unknown" // its disk could not be read
)

// ImageStatus is what one local image holds against this daemon.
type ImageStatus struct {
	Image   string   `json:"image"`
	State   string   `json:"state"`
	Helpers []int    `json:"helpers,omitempty"` // input helper versions baked into it
	Recipe  int      `json:"recipe"`            // its manifest's imageRecipe; 0 when none is recorded
	Reasons []string `json:"reasons,omitempty"` // why it is stale or unknown, one per fact
	Rebuild string   `json:"rebuild,omitempty"` // scripts/build-image.sh's arguments, for a stale image
}

// guestUserGlob finds each guest user's baked helpers, relative to the data volume's root:
// PrepareGuest installs helperName() under the guest user's home.
const guestUserGlob = "Users/*/.greenroom/bin/greenroom-input-*"

// ReadImageFacts reads a data volume mounted at root: the input helper versions baked into any
// user's ~/.greenroom/bin, and the toolchain manifest at ToolchainPath (nil when there is none).
func ReadImageFacts(root string) (helpers []int, manifest map[string]any, err error) {
	paths, err := filepath.Glob(filepath.Join(root, guestUserGlob))
	if err != nil {
		return nil, nil, err
	}
	for _, p := range paths {
		if v, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(p), "greenroom-input-")); err == nil && !slices.Contains(helpers, v) {
			helpers = append(helpers, v)
		}
	}
	slices.Sort(helpers)
	data, err := os.ReadFile(filepath.Join(root, ToolchainPath))
	if errors.Is(err, os.ErrNotExist) {
		return helpers, nil, nil
	}
	if err != nil {
		return helpers, nil, err
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return helpers, nil, fmt.Errorf("the toolchain manifest is not a JSON object: %w", err)
	}
	return helpers, manifest, nil
}

// JudgeImage compares an image's facts with this daemon's helper and recipe.
func JudgeImage(image string, helpers []int, manifest map[string]any) ImageStatus {
	st := ImageStatus{Image: image, State: ImageCurrent, Helpers: helpers}
	if manifest != nil {
		st.Recipe, _ = imageRecipeOf(manifest)
	}
	if !slices.Contains(helpers, inputHelperVersion) {
		found := "none"
		if len(helpers) > 0 {
			found = strconv.Itoa(helpers[len(helpers)-1])
		}
		st.Reasons = append(st.Reasons, fmt.Sprintf("its input helper is %s, this daemon's is %d, so every machine "+
			"compiles the helper while it boots", found, inputHelperVersion))
	}
	if st.Recipe != imageRecipeVersion {
		found := "no image recipe"
		if st.Recipe != 0 {
			found = fmt.Sprintf("image recipe %d", st.Recipe)
		}
		st.Reasons = append(st.Reasons, fmt.Sprintf("it records %s, this daemon's is %d, so it may lack what the "+
			"recipe now puts in every image, such as Xcode", found, imageRecipeVersion))
	}
	if len(st.Reasons) > 0 {
		st.State, st.Rebuild = ImageStale, RebuildArgs(image)
	}
	return st
}

// RebuildArgs are scripts/build-image.sh's arguments that rebuild image in place: BuildArgs
// and -force.
func RebuildArgs(image string) string {
	return strings.TrimSpace(BuildArgs(image) + " -force")
}

// BuildArgs are scripts/build-image.sh's arguments that build image: the lean profile for a
// lean image (greenroom-lean-*), the default name needs none.
func BuildArgs(image string) string {
	args := []string{}
	if strings.HasPrefix(image, "greenroom-lean") {
		args = append(args, "-lean")
	}
	if image != "greenroom-base" {
		args = append(args, "-name", image)
	}
	return strings.Join(args, " ")
}
