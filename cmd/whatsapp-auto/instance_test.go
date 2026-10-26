package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// B1: only one copy may hold a data folder; the lock is free again after release.
func TestInstanceLockIsExclusive(t *testing.T) {
	dir := t.TempDir()
	if instanceLockHeld(dir) {
		t.Fatal("a fresh folder must not look locked")
	}
	first, err := lockInstanceFile(filepath.Join(dir, lockFileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockInstanceFile(filepath.Join(dir, lockFileName)); !errors.Is(err, errInstanceLocked) {
		t.Fatalf("second lock: err = %v, want errInstanceLocked", err)
	}
	if !instanceLockHeld(dir) {
		t.Fatal("a held lock must be reported as running")
	}
	unlockInstanceFile(first)
	if instanceLockHeld(dir) {
		t.Fatal("a released lock must be free")
	}
	// The check itself must not leave the folder locked.
	again, err := lockInstanceFile(filepath.Join(dir, lockFileName))
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	unlockInstanceFile(again)
}

func TestAcquireAndReleaseInstanceLock(t *testing.T) {
	dir := t.TempDir()
	if err := acquireInstanceLock(dir); err != nil {
		t.Fatal(err)
	}
	if instanceLock == nil {
		t.Fatal("the lock must be kept for the life of the process")
	}
	if _, err := lockInstanceFile(filepath.Join(dir, lockFileName)); !errors.Is(err, errInstanceLocked) {
		t.Fatalf("another copy must be refused, err = %v", err)
	}
	releaseInstanceLock()
	if instanceLock != nil || instanceLockHeld(dir) {
		t.Fatal("release must free the lock")
	}
}

// n6Child runs setup the way "run" does, in a child process (setup may exit), and
// reports whether it became the running copy. It keeps the locks for a moment so the
// other children started with it see them.
func n6Child() {
	home := os.Getenv("PULSE_DATA_DIR")
	e := setup(filepath.Join(home, "config.yaml"), "", 18993, true)
	if e.otherCopy {
		fmt.Println("N6:OTHER")
	} else {
		fmt.Println("N6:OWNER")
	}
	time.Sleep(1500 * time.Millisecond)
	os.Exit(0)
}

func startN6Child(t *testing.T, home string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSettingsCreatedUnderLocks$")
	cmd.Env = append(os.Environ(), "PULSE_TEST_N6_CHILD=1", "PULSE_DATA_DIR="+home)
	out := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd, out
}

// N6: settings are only created while holding the settings lock, and of several copies
// started at the same moment on a new computer exactly one becomes the running copy.
func TestSettingsCreatedUnderLocks(t *testing.T) {
	if os.Getenv("PULSE_TEST_N6_CHILD") == "1" {
		n6Child()
	}
	// While another copy holds the settings lock, no settings file is created.
	home := t.TempDir()
	unlock := lockConfigDir(home)
	cmd, out := startN6Child(t, home)
	time.Sleep(800 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(home, "config.yaml")); !os.IsNotExist(err) {
		unlock()
		t.Fatalf("settings were created while the settings lock was held: %v\n%s", err, out)
	}
	unlock()
	if err := cmd.Wait(); err != nil || !strings.Contains(out.String(), "N6:OWNER") {
		t.Fatalf("child: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(home, "config.yaml")); err != nil {
		t.Fatalf("settings not created after the lock was free: %v", err)
	}

	// Several first starts at the same moment: exactly one becomes the running copy.
	home2 := t.TempDir()
	const n = 4
	cmds, outs := make([]*exec.Cmd, n), make([]*bytes.Buffer, n)
	for i := range n {
		cmds[i], outs[i] = startN6Child(t, home2)
	}
	owners := 0
	for i := range n {
		if err := cmds[i].Wait(); err != nil {
			t.Fatalf("child %d: %v\n%s", i, err, outs[i])
		}
		if strings.Contains(outs[i].String(), "N6:OWNER") {
			owners++
		}
	}
	if owners != 1 {
		for i := range n {
			t.Logf("child %d:\n%s", i, outs[i])
		}
		t.Fatalf("%d copies became the running copy, want 1", owners)
	}
}

// B8: an empty settings file with no usable backup counts as missing next to old data.
func TestSettingsEmptyCountsAsMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PULSE_DATA_DIR", dir)
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte(" \r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if settingsMissing(cfg) {
		t.Fatal("without data an empty file is a damaged first start, not lost settings")
	}
	if err := os.WriteFile(filepath.Join(dir, "store.key"), []byte("k"), 0600); err != nil {
		t.Fatal(err)
	}
	if !settingsMissing(cfg) {
		t.Fatal("an empty settings file next to existing data must be treated as missing")
	}
	if err := os.WriteFile(cfg+".bak", []byte("server:\n  port: 8085\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if settingsMissing(cfg) {
		t.Fatal("a usable backup restores the settings")
	}
}
