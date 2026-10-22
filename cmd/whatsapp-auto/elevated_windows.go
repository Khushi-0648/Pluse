//go:build windows

package main

import "golang.org/x/sys/windows"

// isElevated reports whether this process runs with Windows administrator rights.
func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}
