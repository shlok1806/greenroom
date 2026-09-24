package mcpserver

import (
	"context"
	"strings"
	"testing"
)

// Issue #32: the description told agents the guest is Retina, but the default image is 1024x768
// at scale 1, so an agent could halve coordinates it read off the image.
func TestScreenshotDescriptionDoesNotClaimRetina(t *testing.T) {
	h := newHarness(t)
	res, err := h.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name != "machine_screenshot" {
			continue
		}
		if strings.Contains(tool.Description, "Retina display") || !strings.Contains(tool.Description, "scale") {
			t.Fatalf("machine_screenshot description = %q", tool.Description)
		}
		return
	}
	t.Fatal("no machine_screenshot tool")
}
