package sshx

import "errors"

// Error classes for errors.Is. The CLI maps them to exit codes: connection
// failed 3, auth failed 4, host key mismatch 5.
var (
	// ErrConnect means the host could not be reached, the SSH handshake
	// failed, or an established connection was lost.
	ErrConnect = errors.New("connection failed")
	// ErrAuth means every authentication method was rejected.
	ErrAuth = errors.New("authentication failed")
	// ErrHostKey means the host key changed, or an unknown host key was
	// not trusted.
	ErrHostKey = errors.New("host key verification failed")
	// ErrUnsupported means the ssh config asks for something pfwd does not
	// support yet.
	ErrUnsupported = errors.New("not supported yet")
	// ErrForwardingRefused means the server refuses TCP forwarding
	// (AllowTcpForwarding no, or DisableForwarding yes, in sshd_config).
	ErrForwardingRefused = errors.New("server refuses TCP forwarding")
)

// Error is a user-facing error: Msg is a complete sentence with a suggested
// fix, Kind is one of the Err* classes above, and Err is the cause.
type Error struct {
	Kind error
	Msg  string
	Err  error
}

// Error returns the user-facing message.
func (e *Error) Error() string { return e.Msg }

// Unwrap lets errors.Is match both the class and the cause.
func (e *Error) Unwrap() []error {
	if e.Err == nil {
		return []error{e.Kind}
	}
	return []error{e.Kind, e.Err}
}
