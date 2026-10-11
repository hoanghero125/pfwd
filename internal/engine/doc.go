// Package engine is pfwd's UI-agnostic core. It owns the SSH sessions, the
// port detector and the forwards, and reports every change as an Event, so
// the TUI (and later the CLI and web UI) only render events and call
// engine methods.
package engine
