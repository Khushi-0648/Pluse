//go:build windows

package securestore

import (
	"os"
	"os/exec"
	"os/user"
	"unsafe"

	"golang.org/x/sys/windows"
)

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

// restrictToUser removes inherited ACEs and grants full control to the current user only.
func restrictToUser(path string) {
	u, err := user.Current()
	if err != nil || u.Username == "" {
		return
	}
	perm := u.Username + ":(F)"
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		perm = u.Username + ":(OI)(CI)(F)"
	}
	cmd := exec.Command("icacls", path, "/inheritance:r", "/grant:r", perm)
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	_ = cmd.Run()
}

var (
	modAdvapi32      = windows.NewLazySystemDLL("advapi32.dll")
	procEncryptFileW = modAdvapi32.NewProc("EncryptFileW")
)

// ProtectDatabaseFile applies user-only ACLs and Windows EFS filesystem-level encryption.
func ProtectDatabaseFile(path string) error {
	restrictToUser(path)
	if path == "" {
		return nil
	}
	pUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	r1, _, err := procEncryptFileW.Call(uintptr(unsafe.Pointer(pUTF16)))
	if r1 == 0 {
		return err
	}
	return nil
}
