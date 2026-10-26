package main

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// lockFileName is held with an exclusive OS lock by the running copy of Pulse for its
// data folder, from before any data file is read until it exits. The operating system
// releases the lock when the process ends, even after a crash, so a stale file never
// blocks the next start (except on the rare systems where only the O_EXCL fallback
// exists).
const lockFileName = "pulse.lock"

// errInstanceLocked means another copy of Pulse is using the data folder.
var errInstanceLocked = errors.New("another copy of Pulse is using this data folder")

// instanceLock keeps the lock file open (and so locked) for the life of the process. It
// must stay referenced: a closed or collected *os.File would release the lock.
var instanceLock *os.File

// acquireInstanceLock takes the single-copy lock for dataDir and keeps it until the
// process exits. It returns errInstanceLocked when another copy holds it.
func acquireInstanceLock(dataDir string) error {
	f, err := lockInstanceFile(filepath.Join(dataDir, lockFileName))
	if err != nil {
		return err
	}
	instanceLock = f
	return nil
}

// releaseInstanceLock gives the lock back at a normal stop (the OS does it anyway when
// the process ends; the O_EXCL fallback needs the file removed).
func releaseInstanceLock() {
	if instanceLock != nil {
		unlockInstanceFile(instanceLock)
		instanceLock = nil
	}
}

// configLockName is held for a moment while a copy of Pulse creates, moves or rewrites a
// settings file, so two copies started at the same moment never both create one (each
// with its own one-time password) or move the same old settings.
const configLockName = "config.lock"

// configLockWait is how long a copy waits for another one that holds the settings lock.
var configLockWait = 15 * time.Second

// lockConfigDir takes the settings lock in dir, waiting while another copy holds it, and
// returns the function that gives it back. If the lock cannot be taken (a read-only
// folder, or a stale lock file on a system without OS file locks), Pulse carries on
// without it, as before the lock existed.
func lockConfigDir(dir string) (unlock func()) {
	path := filepath.Join(dir, configLockName)
	deadline := time.Now().Add(configLockWait)
	for {
		f, err := lockInstanceFile(path)
		if err == nil {
			var once sync.Once
			return func() { once.Do(func() { unlockInstanceFile(f) }) }
		}
		if !errors.Is(err, errInstanceLocked) || time.Now().After(deadline) {
			return func() {}
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// instanceLockHeld reports whether a running copy of Pulse holds dataDir's lock. It
// takes the lock for a moment and gives it back when it is free.
func instanceLockHeld(dataDir string) bool {
	if instanceLock != nil {
		return false // this process is the running copy
	}
	f, err := lockInstanceFile(filepath.Join(dataDir, lockFileName))
	if err != nil {
		return errors.Is(err, errInstanceLocked)
	}
	unlockInstanceFile(f)
	return false
}
