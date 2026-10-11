//go:build !windows

package desktop

// startupInfo only matters on Windows.
func startupInfo() string { return "" }
