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
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
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

// loadOrCreateKey reads the data key, or creates it when there is none. The key file is
// created with O_EXCL, so two copies starting at the same moment can never each write
// their own key (one would then encrypt data the other cannot open): whoever loses the
// race reads the winner's key instead.
func loadOrCreateKey(keyPath string) ([]byte, error) {
	waitUntil := time.Now().Add(staleKeyAge + 2*time.Second)
	for replaced := 0; ; {
		key, err := readKey(keyPath)
		if err == nil {
			return key, nil
		}
		if errors.Is(err, errKeyIncomplete) {
			fi, statErr := os.Stat(keyPath)
			if statErr == nil && time.Since(fi.ModTime()) > staleKeyAge && replaced < 3 {
				// Left by a crash between creating the file and writing the key: it will
				// never be completed.
				replaced++
				if err := replaceStaleKey(keyPath, fi); err != nil {
					return nil, err
				}
				continue
			}
			// Another copy has just created the file and is still writing it.
			if time.Now().Before(waitUntil) {
				time.Sleep(40 * time.Millisecond)
				continue
			}
			return nil, fmt.Errorf("data key %s is empty or damaged", keyPath)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		wrapped, err := protect(key)
		if err != nil {
			return nil, err
		}
		f, err := os.OpenFile(keyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if errors.Is(err, os.ErrExist) {
			continue // created by another copy just now: read that one
		}
		if err != nil {
			return nil, err
		}
		_, werr := f.Write(wrapped)
		if werr == nil {
			werr = f.Sync()
		}
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			_ = os.Remove(keyPath)
			return nil, werr
		}
		return key, nil
	}
}

// errKeyIncomplete is a key file that exists but cannot be used yet: empty, or too
// short to be a wrapped key (it may still be being written).
var errKeyIncomplete = errors.New("data key file is incomplete")

// staleKeyAge is how long an incomplete key file must be left unchanged before it counts
// as left behind by a crash rather than still being written by another copy. A variable
// so tests can shorten it.
var staleKeyAge = 5 * time.Second

// replaceStaleKey deals with an empty or truncated store.key left by a crash. With no
// encrypted data in the folder nothing can be lost: the file is moved aside and removed,
// and the caller creates a new key (with O_EXCL, as on a first start). With encrypted data
// a new key could never open it, so starting stops with instructions instead.
func replaceStaleKey(keyPath string, stale os.FileInfo) error {
	dir := filepath.Dir(keyPath)
	if found := encryptedFileIn(dir); found != "" {
		return fmt.Errorf("the data key %s is empty or damaged, but the folder holds encrypted data (%s) that only the original key can open.\n"+
			"Close every copy of Pulse, restore store.key from a backup copy of this folder made by this Windows account, and start again.\n"+
			"Do not delete store.key or the data files: a new key cannot open them", keyPath, found)
	}
	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	aside := fmt.Sprintf("%s.empty-%x", keyPath, suffix)
	if err := os.Rename(keyPath, aside); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // another copy dealt with it just now
		}
		return fmt.Errorf("the data key %s is empty and could not be replaced: %w", keyPath, err)
	}
	// Another copy may have replaced the stale file between the check and the rename:
	// only a file that is still incomplete and unchanged since it was found stale is
	// removed; anything else goes back. (os.SameFile cannot be used: on Windows it
	// looks the file up again by its old name, which is gone after the rename.)
	if moved, err := os.Stat(aside); err == nil && moved.Size() < 32 && moved.ModTime().Equal(stale.ModTime()) {
		_ = os.Remove(aside)
		return nil
	}
	if _, err := os.Stat(keyPath); errors.Is(err, os.ErrNotExist) {
		_ = os.Rename(aside, keyPath)
	}
	return nil
}

// encryptedFileIn returns the path (relative to dir) of a file in dir or below that carries
// the encrypted-file header, or "" when there is none.
func encryptedFileIn(dir string) string {
	found := ""
	checked := 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() || strings.HasPrefix(strings.ToLower(d.Name()), "store.key") {
			return nil
		}
		if checked++; checked > 100000 {
			return filepath.SkipAll
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		head := make([]byte, len(magic))
		n, _ := io.ReadFull(f, head)
		f.Close()
		if bytes.Equal(head[:n], magic) {
			if rel, err := filepath.Rel(dir, path); err == nil {
				found = rel
			} else {
				found = path
			}
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func readKey(keyPath string) ([]byte, error) {
	wrapped, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	if len(wrapped) < 32 {
		return nil, errKeyIncomplete
	}
	key, err := unprotect(wrapped)
	if err != nil {
		return nil, fmt.Errorf("cannot unlock data key %s (was it copied from another Windows user?): %w", keyPath, err)
	}
	return key, nil
}

// aadFor binds an encrypted file to its name: a data file swapped for another of Pulse's
// files (contacts.json replaced by messages.json, say) no longer opens. name is the base
// name of the file the data was written to; a .bak copy uses the name of the file it
// backs up.
func aadFor(name string) []byte {
	out := append([]byte(nil), magic...)
	return append(out, strings.ToLower(name)...)
}

func (b *Box) seal(plain []byte, name string) ([]byte, error) {
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
	return b.aead.Seal(out, nonce, plain, aadFor(name)), nil
}

func (b *Box) open(data []byte, name string) ([]byte, error) {
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
	plain, err := b.aead.Open(nil, rest[:ns], rest[ns:], aadFor(name))
	if err != nil {
		// Written by Pulse 2.0.x or older, before files were bound to their name; the
		// next save writes it in the new form. The reverse does not work: 2.0.x cannot
		// open files written by 2.1, so going back to 2.0.x is not supported (restore a
		// backup made by 2.0 instead; see HOW-TO-RUN.txt, UPGRADING).
		if old, oldErr := b.aead.Open(nil, rest[:ns], rest[ns:], magic); oldErr == nil {
			return old, nil
		}
	}
	return plain, err
}

// IsEncrypted reports whether data carries the encrypted-file header.
func IsEncrypted(data []byte) bool { return bytes.HasPrefix(data, magic) }

// WriteFile seals data and atomically replaces path, keeping the previous version as path.bak.
func (b *Box) WriteFile(path string, data []byte) error {
	sealed, err := b.seal(data, filepath.Base(path))
	if err != nil {
		return err
	}
	return writeAtomic(path, sealed, true)
}

// WriteFileNoBackup seals data and atomically replaces path without keeping a .bak copy
// (for files that are themselves backups).
func (b *Box) WriteFileNoBackup(path string, data []byte) error {
	sealed, err := b.seal(data, filepath.Base(path))
	if err != nil {
		return err
	}
	return writeAtomic(path, sealed, false)
}

// ReadFile returns the decrypted contents of path, falling back to path.bak when the
// main file is missing, empty or unreadable.
func (b *Box) ReadFile(path string) ([]byte, error) {
	data, err := b.readOne(path, filepath.Base(path))
	if err == nil && len(bytes.TrimSpace(data)) > 0 {
		return data, nil
	}
	if bak, bErr := b.ReadBackup(path); bErr == nil && len(bytes.TrimSpace(bak)) > 0 {
		return bak, nil
	}
	return data, err
}

// ReadBackup returns the decrypted contents of path.bak (a copy of path, so it carries
// path's name).
func (b *Box) ReadBackup(path string) ([]byte, error) {
	return b.readOne(path+".bak", filepath.Base(path))
}

func (b *Box) readOne(path, name string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return b.open(raw, name)
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
	if err := RenameRetry(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// RenameRetry replaces path with tmp. On Windows a virus scanner, the search indexer or a
// backup tool often holds a just-written file for a moment, and the rename then fails
// with "Access is denied" or a sharing violation; it succeeds a few milliseconds later.
func RenameRetry(tmp, path string) error {
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		if err = os.Rename(tmp, path); err == nil {
			return nil
		}
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond) // about 2 s in all
	}
	return err
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
		// Still unsaved: retry in the background like fire does, so a deletion the owner
		// asked for reaches the disk even if nothing else changes afterwards.
		s.pending = true
		if s.timer == nil {
			s.timer = time.AfterFunc(5*time.Second, s.fire)
		}
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
// stays empty. On macOS and Linux there is no such protector: the value is only encoded,
// not encrypted, and relies on the settings file's permissions (see SecretsSealed).
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

// SecretsSealed reports whether SealString and the data key really are protected by the
// operating system (Windows DPAPI). Elsewhere they rely on file permissions only.
func SecretsSealed() bool { return osProtects }

// IsSealedString reports whether v was produced by SealString.
func IsSealedString(v string) bool { return strings.HasPrefix(v, sealedPrefix) }
