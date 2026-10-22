// Package securestore persists application data files atomically and, when a Box is
// configured, encrypts them with AES-256-GCM using a key protected by the OS
// (Windows DPAPI, user scope).
package securestore

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var magic = []byte("PULSEENC1\n")

// Box encrypts and decrypts file payloads. A nil *Box stores plaintext.
type Box struct {
	aead cipher.AEAD
}

var (
	defaultMu  sync.RWMutex
	defaultBox *Box
)

// SetDefault installs the box used by stores that were not given one explicitly.
func SetDefault(b *Box) {
	defaultMu.Lock()
	defaultBox = b
	defaultMu.Unlock()
}

// Default returns the process-wide box, or nil when encryption is disabled.
func Default() *Box {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultBox
}

// OpenBox loads (or creates) the data key in dir. The key file is wrapped with the
// platform protector; on platforms without one it is stored with 0600 permissions.
func OpenBox(dir string) (*Box, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(dir, "store.key")
	var key []byte
	if wrapped, err := os.ReadFile(keyPath); err == nil {
		key, err = unprotect(wrapped)
		if err != nil {
			return nil, fmt.Errorf("cannot unlock data key %s (was it copied from another Windows user?): %w", keyPath, err)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		wrapped, err := protect(key)
		if err != nil {
			return nil, err
		}
		if err := writeAtomic(keyPath, wrapped, false); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("data key has invalid length")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

func (b *Box) seal(plain []byte) ([]byte, error) {
	if b == nil {
		return plain, nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(magic)+len(nonce)+len(plain)+b.aead.Overhead())
	out = append(out, magic...)
	out = append(out, nonce...)
	return b.aead.Seal(out, nonce, plain, magic), nil
}

func (b *Box) open(data []byte) ([]byte, error) {
	if !bytes.HasPrefix(data, magic) {
		return data, nil // legacy plaintext file; re-encrypted on next save
	}
	if b == nil {
		return nil, errors.New("file is encrypted but no data key is loaded")
	}
	rest := data[len(magic):]
	ns := b.aead.NonceSize()
	if len(rest) < ns {
		return nil, errors.New("encrypted file is truncated")
	}
	return b.aead.Open(nil, rest[:ns], rest[ns:], magic)
}

// IsEncrypted reports whether data carries the encrypted-file header.
func IsEncrypted(data []byte) bool { return bytes.HasPrefix(data, magic) }

// WriteFile seals data and atomically replaces path, keeping the previous version as path.bak.
func (b *Box) WriteFile(path string, data []byte) error {
	sealed, err := b.seal(data)
	if err != nil {
		return err
	}
	return writeAtomic(path, sealed, true)
}

// WriteFileNoBackup seals data and atomically replaces path without keeping a .bak copy
// (for files that are themselves backups).
func (b *Box) WriteFileNoBackup(path string, data []byte) error {
	sealed, err := b.seal(data)
	if err != nil {
		return err
	}
	return writeAtomic(path, sealed, false)
}

// ReadFile returns the decrypted contents of path, falling back to path.bak when the
// main file is missing, empty or unreadable.
func (b *Box) ReadFile(path string) ([]byte, error) {
	data, err := b.readOne(path)
	if err == nil && len(bytes.TrimSpace(data)) > 0 {
		return data, nil
	}
	if bak, bErr := b.readOne(path + ".bak"); bErr == nil && len(bytes.TrimSpace(bak)) > 0 {
		return bak, nil
	}
	return data, err
}

// ReadBackup returns the decrypted contents of path.bak.
func (b *Box) ReadBackup(path string) ([]byte, error) { return b.readOne(path + ".bak") }

func (b *Box) readOne(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return b.open(raw)
}

func writeAtomic(path string, data []byte, keepBackup bool) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	tmpBytes := make([]byte, 6)
	_, _ = rand.Read(tmpBytes)
	tmp := fmt.Sprintf("%s.%x.tmp", path, tmpBytes)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if keepBackup {
		if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
			_ = copyFile(path, path+".bak")
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}

// RemoveBackup deletes path.bak, so data removed on purpose cannot come back from it.
func RemoveBackup(path string) {
	_ = os.Remove(path + ".bak")
}

// RestrictToUser limits a file or directory (and, for directories, everything created
// inside it later) to the current OS user where supported.
func RestrictToUser(path string) { restrictToUser(path) }

// Saver coalesces frequent writes into one save after a short delay. Saves never run
// concurrently, and Flush returns only after every change made before it is on disk.
type Saver struct {
	mu      sync.Mutex
	saveMu  sync.Mutex
	delay   time.Duration
	save    func() error
	timer   *time.Timer
	pending bool
	lastErr error
}

// NewSaver returns a Saver that calls save at most once per delay window.
func NewSaver(delay time.Duration, save func() error) *Saver {
	return &Saver{delay: delay, save: save}
}

// Trigger schedules a save.
func (s *Saver) Trigger() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = true
	if s.timer == nil {
		s.timer = time.AfterFunc(s.delay, s.fire)
	}
}

func (s *Saver) fire() {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.Lock()
	s.timer = nil
	if !s.pending {
		s.mu.Unlock()
		return
	}
	s.pending = false
	s.mu.Unlock()
	err := s.save()
	s.mu.Lock()
	s.lastErr = err
	if err != nil {
		// Usually a file briefly locked by antivirus or the search indexer: keep the
		// change pending and try again shortly, so it is not lost.
		s.pending = true
		if s.timer == nil {
			s.timer = time.AfterFunc(5*time.Second, s.fire)
		}
	}
	s.mu.Unlock()
}

// Flush writes any pending changes immediately, waiting for a save already in progress.
func (s *Saver) Flush() error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	pending := s.pending
	s.pending = false
	s.mu.Unlock()
	if !pending {
		return nil
	}
	err := s.save()
	s.mu.Lock()
	s.lastErr = err
	if err != nil {
		s.pending = true // still unsaved: a later Flush or Trigger retries it
	}
	s.mu.Unlock()
	return err
}

// LastError returns the error from the most recent background save.
func (s *Saver) LastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// sealedPrefix marks a short secret (an access token, an app secret) wrapped with the
// operating system's key protection, so it can sit in the settings file unreadable.
const sealedPrefix = "sealed1:"

// SealString protects a short secret for storage in a text file. Only the same Windows
// user on the same computer can open it again (DPAPI, user scope). An empty secret
// stays empty.
func SealString(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	wrapped, err := protectSecret([]byte(plain))
	if err != nil {
		return "", err
	}
	return sealedPrefix + base64.StdEncoding.EncodeToString(wrapped), nil
}

// OpenString returns the secret sealed by SealString. A value without the sealed
// prefix is refused rather than trusted, so a secret is never used in plain text.
func OpenString(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	if !IsSealedString(sealed) {
		return "", errors.New("the stored secret is not sealed")
	}
	raw, err := base64.StdEncoding.DecodeString(sealed[len(sealedPrefix):])
	if err != nil {
		return "", errors.New("the stored secret is damaged")
	}
	plain, err := unprotectSecret(raw)
	if err != nil {
		return "", fmt.Errorf("the stored secret cannot be opened on this Windows account: %w", err)
	}
	return string(plain), nil
}

// IsSealedString reports whether v was produced by SealString.
func IsSealedString(v string) bool { return strings.HasPrefix(v, sealedPrefix) }
