//go:build !windows

package securestore

import "os"

// Without an OS key protector the key relies on 0600 file permissions.
func protect(plain []byte) ([]byte, error) { return append([]byte(nil), plain...), nil }

func unprotect(wrapped []byte) ([]byte, error) { return append([]byte(nil), wrapped...), nil }

func protectSecret(plain []byte) ([]byte, error) { return protect(plain) }

func unprotectSecret(wrapped []byte) ([]byte, error) { return unprotect(wrapped) }

func restrictToUser(path string) {
	if fi, err := os.Stat(path); err == nil {
		if fi.IsDir() {
			_ = os.Chmod(path, 0700)
		} else {
			_ = os.Chmod(path, 0600)
		}
	}
}

// ProtectDatabaseFile sets strict 0600 permissions on non-Windows platforms.
func ProtectDatabaseFile(path string) error {
	restrictToUser(path)
	return nil
}
