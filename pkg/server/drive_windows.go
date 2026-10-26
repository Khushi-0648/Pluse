//go:build windows

package server

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// isNetworkDrive reports whether dir is on a mapped network drive (Z: -> \\server\share).
// Writing there would send this computer's sign-in details to that server, like a \\ path.
func isNetworkDrive(dir string) bool {
	vol := filepath.VolumeName(dir)
	if vol == "" {
		return false
	}
	root, err := windows.UTF16PtrFromString(vol + `\`)
	if err != nil {
		return false
	}
	return windows.GetDriveType(root) == windows.DRIVE_REMOTE
}
