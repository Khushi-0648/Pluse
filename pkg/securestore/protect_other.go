//go:build !windows

package securestore

import "os"

// osProtects is false: without an OS key protector the data key and the "sealed"
// settings secrets (access token, app secret) are stored as they are and rely on 0600
// file permissions only.
const osProtects = false

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
