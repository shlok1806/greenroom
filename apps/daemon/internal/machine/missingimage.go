package machine

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// A default image that is not on the host (issue #285, root ADR 0048). The daemon never swaps in
// another image on its own: it says so at serve start, and a machine_create whose clone fails
// names the local images it could use instead and the command that builds the missing one.

// MissingImageError is a local image name tart has no VM for.
type MissingImageError struct {
	Image string
	Local []string // LocalImages: what a caller could clone instead
}

func (e *MissingImageError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "image %q is not on this host: tart has no local VM by that name, so no machine can be cloned from it. ", e.Image)
	if len(e.Local) == 0 {
		b.WriteString("This host has no local greenroom image at all. ")
	} else {
		fmt.Fprintf(&b, "Local greenroom images: %s. Call machine_create again with one of them as image, or ", e.DescribeLocal())
	}
	fmt.Fprintf(&b, "ask the host's owner to build it (from apps/daemon: %s, about 20 GB free) or to start the daemon with -image naming one that exists; "+
		"greenroom image-status checks each against this daemon's input helper and image recipe", e.BuildCommand())
	return b.String()
}

// DescribeLocal lists Local, saying which are named for this daemon's helper and recipe.
func (e *MissingImageError) DescribeLocal() string {
	out := make([]string, 0, len(e.Local))
	for _, n := range e.Local {
		if NamedForThisDaemon(n) {
			n += fmt.Sprintf(" (named for this daemon's input helper %d and image recipe %d)", inputHelperVersion, imageRecipeVersion)
		}
		out = append(out, n)
	}
	return strings.Join(out, ", ")
}

// BuildCommand builds the missing image under its own name, from apps/daemon.
func (e *MissingImageError) BuildCommand() string {
	return strings.TrimSpace("scripts/build-image.sh " + BuildArgs(e.Image))
}

// CheckImageOnHost is a *MissingImageError when image is a local name (an OCI reference has a
// "/", and tart pulls it on clone) and vms has no local VM by that name, else nil.
func CheckImageOnHost(image string, vms []tart.VM) error {
	if image == "" || strings.Contains(image, "/") {
		return nil
	}
	for _, vm := range vms {
		if vm.Source == "local" && vm.Name == image {
			return nil
		}
	}
	return &MissingImageError{Image: image, Local: LocalImages(vms)}
}

// MissingImage asks tart whether image is on this host: a *MissingImageError when it is a
// local name tart has no VM for, nil when it is there, is an OCI reference, or tart cannot list.
func (m *Manager) MissingImage(ctx context.Context, image string) error {
	if image == "" || strings.Contains(image, "/") {
		return nil
	}
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	vms, err := m.tart.List(c)
	if err != nil {
		return nil // clone still fails with tart's own error
	}
	return CheckImageOnHost(image, vms)
}

// versionedImageRE is the VM suite's name for an image: greenroom-base-v<helper>-r<recipe>.
var versionedImageRE = regexp.MustCompile(`-v([0-9]+)-r([0-9]+)$`)

// NamedForThisDaemon says whether name ends in -v<inputHelperVersion>-r<imageRecipeVersion>,
// the VM suite's name for an image of this daemon's helper and recipe. Only the name says so:
// image-status reads the disk.
func NamedForThisDaemon(name string) bool {
	m := versionedImageRE.FindStringSubmatch(name)
	if m == nil {
		return false
	}
	h, _ := strconv.Atoi(m[1])
	r, _ := strconv.Atoi(m[2])
	return h == inputHelperVersion && r == imageRecipeVersion
}

// LocalImages are tart's local VMs that are greenroom images: named greenroom-*, and not a
// run's clone, an image under construction (<name>-building) or check-image's clones
// (<image>-check-<tag>). Those named for this daemon's helper and recipe come first, then by name.
func LocalImages(vms []tart.VM) []string {
	var out []string
	for _, vm := range vms {
		n := vm.Name
		if vm.Source != "local" || !strings.HasPrefix(n, namePrefix) || runCloneRE.MatchString(n) ||
			strings.HasSuffix(n, "-building") || strings.Contains(n, "-check-") || slices.Contains(out, n) {
			continue
		}
		out = append(out, n)
	}
	slices.SortFunc(out, func(a, b string) int {
		if ca, cb := NamedForThisDaemon(a), NamedForThisDaemon(b); ca != cb {
			if ca {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
	return out
}
