//go:build !windows && !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package main

import (
	"errors"
	"os"
)

// lockInstanceFile has no OS file lock to use on this system: the lock file itself is
// created with O_EXCL and removed at a normal stop. After a crash it stays, and Pulse
// reports that another copy is running until the file is deleted by hand.
func lockInstanceFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil, errInstanceLocked
	}
	return f, err
}

func unlockInstanceFile(f *os.File) {
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
}
