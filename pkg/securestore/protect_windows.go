//go:build windows

package securestore

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// osProtects: the data key and sealed secrets are wrapped with DPAPI.
const osProtects = true

var entropy = []byte("pulse-securestore-v1")

func blob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

func takeBlob(out windows.DataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	res := make([]byte, out.Size)
	copy(res, unsafe.Slice(out.Data, out.Size))
	return res
}

func protect(plain []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptProtectData(blob(plain), nil, blob(entropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return takeBlob(out), nil
}

// secretEntropy keeps sealed settings secrets separate from the data key.
var secretEntropy = []byte("pulse-secret-v1")

func protectSecret(plain []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptProtectData(blob(plain), nil, blob(secretEntropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return takeBlob(out), nil
}

func unprotectSecret(wrapped []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(blob(wrapped), nil, blob(secretEntropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return takeBlob(out), nil
}

func unprotect(wrapped []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(blob(wrapped), nil, blob(entropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return takeBlob(out), nil
}

// restrictWarned holds paths whose restriction failed and was already reported.
var restrictWarned sync.Map

// restrictToUser removes inherited ACEs and grants full control to the current user only.
// A failure is reported (once per path): the data would then stay readable by other
// Windows accounts on this computer, which the owner should know.
func restrictToUser(path string) {
	u, err := user.Current()
	if err != nil || u.Username == "" {
		warnRestrict(path, err)
		return
	}
	perm := u.Username + ":(F)"
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		perm = u.Username + ":(OI)(CI)(F)"
	}
	cmd := exec.Command("icacls", path, "/inheritance:r", "/grant:r", perm)
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if out, err := cmd.CombinedOutput(); err != nil {
		warnRestrict(path, fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out))))
	}
}

func warnRestrict(path string, err error) {
	if _, seen := restrictWarned.LoadOrStore(path, true); !seen {
		fmt.Printf("[Security] Could not limit %s to your Windows account (%v). Other accounts on this computer may be able to read it.\n", path, err)
	}
}
