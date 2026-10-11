package notify

import (
	"context"
	"fmt"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// Notify shows a toast with Windows PowerShell, which every Windows 10 and
// 11 has. The toast is listed under Windows PowerShell.
func (n *Notifier) Notify(ctx context.Context, title, body string) error {
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		return fmt.Errorf("show a notification: find the system directory: %w", err)
	}
	ps := filepath.Join(dir, "WindowsPowerShell", "v1.0", "powershell.exe")
	args, env := toastCommand(cleanText(title), cleanText(body))
	if err := n.command(ctx, env, ps, args...); err != nil {
		return fmt.Errorf("show a notification: %w; set ui.notifications = false to stop trying", err)
	}
	return nil
}

// sysProcAttr starts helpers without a console window, so they can't
// touch the terminal pfwd draws in.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
}
