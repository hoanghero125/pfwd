//go:build linux || freebsd

package notify

import (
	"context"
	"fmt"

	"github.com/godbus/dbus/v5"
)

// Notify shows a notification through org.freedesktop.Notifications on the
// session bus, else with notify-send. It never starts a bus: without one
// running, it goes straight to notify-send.
func (n *Notifier) Notify(ctx context.Context, title, body string) error {
	title, body = cleanText(title), cleanText(body)
	errBus := n.sendDBus(ctx, title, body)
	if errBus == nil {
		return nil
	}
	if err := n.command(ctx, nil, "notify-send", notifySendArgs(n.app, title, body)...); err != nil {
		return fmt.Errorf("show a notification: D-Bus: %w; notify-send: %w; install libnotify (notify-send) or set ui.notifications = false", errBus, err)
	}
	return nil
}

// sendDBus calls Notify on a private session-bus connection. Unlike
// dbus.SessionBus, it doesn't run dbus-launch or change the environment.
func (n *Notifier) sendDBus(ctx context.Context, title, body string) error {
	conn, err := dbus.SessionBusPrivateNoAutoStartup(dbus.WithContext(ctx))
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.Auth(nil); err != nil {
		return err
	}
	if err := conn.Hello(); err != nil {
		return err
	}
	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	return obj.CallWithContext(ctx, "org.freedesktop.Notifications.Notify", 0,
		n.app, uint32(0), "", title, escapeMarkup(body), []string{}, map[string]dbus.Variant{}, int32(-1)).Err
}
