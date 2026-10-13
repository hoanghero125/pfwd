// Package ipc is pfwd's local control channel: the daemon serves it, and
// the CLI and pfwd attach talk to it. Messages are JSON objects, one per
// line. On Unix they go over a socket with mode 0600 in a directory only
// the user can enter, and the server checks each peer's UID where the OS
// says it. On Windows they go over a named pipe that only the current user
// can open.
package ipc

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Version is the protocol version a request carries.
const Version = 1

// maxLine caps one message.
const maxLine = 1 << 20

var (
	// ErrNotRunning means no daemon listens at the path.
	ErrNotRunning = errors.New("no pfwd daemon is running; start one with `pfwd daemon start`")
	// ErrRunning means a daemon already listens at the path.
	ErrRunning = errors.New("a pfwd daemon is already running; `pfwd daemon status` shows it")
)

// Request is one call.
type Request struct {
	V      int             `json:"v"`
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response answers the Request with the same ID: a result or an error.
type Response struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Event is one message of a watch stream.
type Event struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// Error codes, which the CLI maps to exit codes.
const (
	CodeUsage    = "usage"
	CodeConnect  = "connect"
	CodeAuth     = "auth"
	CodeHostKey  = "host_key"
	CodeBind     = "bind"
	CodeNotFound = "not_found"
	CodeInternal = "internal"
)

// Error is a failed call: a code and a message for the user.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// Errorf returns an *Error with code and a formatted message.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// asError turns a handler's error into an *Error; anything that isn't one
// is internal.
func asError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: CodeInternal, Message: err.Error()}
}
