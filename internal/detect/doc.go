// Package detect finds the TCP ports listening on a remote host without
// installing anything there. It probes the server once for the tools it
// has, then streams snapshots from one long-lived exec channel running a
// fixed shell loop, and parses them with pure functions.
package detect
