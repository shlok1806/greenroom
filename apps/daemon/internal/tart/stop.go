package tart

import (
	"context"
	"strconv"
	"time"
)

// StopWithin shuts a running VM down, giving the guest grace to shut down cleanly before
// tart forces it off (`tart stop --timeout`). The VM and its disk stay; `tart run` boots
// it again (machine_reboot, daemon ADR 0004).
func (c *Client) StopWithin(ctx context.Context, name string, grace time.Duration) error {
	_, err := c.run(ctx, "stop", name, "--timeout", strconv.Itoa(int(grace.Seconds())))
	return err
}
