//go:build !linux && !freebsd && !darwin && !windows

package notify

import (
	"context"
	"errors"
)

// Notify reports that this platform has no desktop notifications.
func (n *Notifier) Notify(context.Context, string, string) error {
	return errors.New("desktop notifications are not supported on this platform; set ui.notifications = false")
}
