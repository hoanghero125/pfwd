// Package forward runs port forwards. A local forward listens on a local
// port and pipes every accepted connection through a Dialer (an SSH
// direct-tcpip channel in production), counting bytes both ways.
package forward
