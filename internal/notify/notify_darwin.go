package notify

import (
	"context"
	"fmt"
)

// Notify shows a notification with osascript. macOS lists it under Script
// Editor, which needs notification permission in System Settings.
func (n *Notifier) Notify(ctx context.Context, title, body string) error {
	if err := n.command(ctx, nil, "/usr/bin/osascript", osascriptArgs(cleanText(title), cleanText(body))...); err != nil {
		return fmt.Errorf("show a notification: %w; allow notifications for Script Editor in System Settings, or set ui.notifications = false", err)
	}
	return nil
}
