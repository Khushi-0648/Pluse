//go:build !windows

package server

// isNetworkDrive: only Windows maps network shares to drive letters.
func isNetworkDrive(string) bool { return false }
