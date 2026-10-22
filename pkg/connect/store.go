package connect

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Queue limits per install: older or surplus items are dropped and counted.
const (
	queueMaxAge   = 7 * 24 * time.Hour
	queueMaxItems = 10000
)

// Install is one Pulse install connected through Embedded Signup. Only a hash of its
// relay key is kept.
type Install struct {
	ID            string    `json:"install_id"`
	RelayKeyHash  string    `json:"relay_key_hash"`
	WABAID        string    `json:"waba_id"`
	PhoneNumberID string    `json:"phone_number_id"`
	CreatedAt     time.Time `json:"created_at"`
	Dropped       int       `json:"dropped,omitempty"` // items dropped by the queue limits
}

// Item is one webhook body waiting for an install.
type Item struct {
	ID   string          `json:"id"`
	At   time.Time       `json:"at"`
	Body json.RawMessage `json:"body"`
}

// store keeps installs and queues in memory and on disk, every file encrypted with
// AES-256-GCM and replaced atomically.
type store struct {
	dir  string
	aead cipher.AEAD
	now  func() time.Time

	mu       sync.Mutex
	installs map[string]*Install // by install ID
	queues   map[string][]Item
	wake     map[string]chan struct{} // closed when an install's queue gets items
}

var magic = []byte("PULSECONNECT1\n")

func openStore(dir string, key []byte) (*store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	s := &store{dir: dir, aead: aead, now: time.Now, installs: map[string]*Install{}, queues: map[string][]Item{}, wake: map[string]chan struct{}{}}
	var list []*Install
	if err := s.read("installs", &list); err != nil {
		return nil, fmt.Errorf("cannot read installs: %w", err)
	}
	for _, in := range list {
		s.installs[in.ID] = in
		var q []Item
		if err := s.read("queue-"+in.ID, &q); err != nil {
			return nil, fmt.Errorf("cannot read queue of %s: %w", in.ID, err)
		}
		s.queues[in.ID] = q
	}
	return s, nil
}

func (s *store) path(name string) string { return filepath.Join(s.dir, name+".enc") }

func (s *store) read(name string, v any) error {
	raw, err := os.ReadFile(s.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(raw) < len(magic)+s.aead.NonceSize() || string(raw[:len(magic)]) != string(magic) {
		return errors.New("not an encrypted connect file")
	}
	rest := raw[len(magic):]
	plain, err := s.aead.Open(nil, rest[:s.aead.NonceSize()], rest[s.aead.NonceSize():], []byte(name))
	if err != nil {
		return errors.New("cannot decrypt (wrong CONNECT_DATA_KEY?)")
	}
	return json.Unmarshal(plain, v)
}

// write encrypts v and replaces the file atomically. Call with s.mu held.
func (s *store) write(name string, v any) error {
	plain, err := json.Marshal(v)
	if err != nil {
		return err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	out := append(append(append([]byte{}, magic...), nonce...), s.aead.Seal(nil, nonce, plain, []byte(name))...)
	tmp := s.path(name) + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	_, werr := f.Write(out)
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, s.path(name))
}

func (s *store) saveInstalls() error {
	list := make([]*Install, 0, len(s.installs))
	for _, in := range s.installs {
		list = append(list, in)
	}
	return s.write("installs", list)
}

func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// addInstall registers an install. An earlier install for the same phone number ID is
// removed with its queue: only the newest connection receives that number's messages.
func (s *store) addInstall(in *Install) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, old := range s.installs {
		if old.PhoneNumberID == in.PhoneNumberID {
			s.removeLocked(id)
		}
	}
	s.installs[in.ID] = in
	s.queues[in.ID] = nil
	return s.saveInstalls()
}

// byKey finds the install whose relay key is key, comparing hashes in constant time.
func (s *store) byKey(key string) (Install, bool) {
	if key == "" {
		return Install{}, false
	}
	h := []byte(hashKey(key))
	s.mu.Lock()
	defer s.mu.Unlock()
	var found *Install
	for _, in := range s.installs {
		if subtle.ConstantTimeCompare(h, []byte(in.RelayKeyHash)) == 1 {
			found = in
		}
	}
	if found == nil {
		return Install{}, false
	}
	return *found, true
}

func (s *store) byPhone(phoneID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, in := range s.installs {
		if in.PhoneNumberID == phoneID {
			return id, true
		}
	}
	return "", false
}

func (s *store) removeLocked(id string) {
	delete(s.installs, id)
	delete(s.queues, id)
	if ch, ok := s.wake[id]; ok {
		close(ch)
		delete(s.wake, id)
	}
	_ = os.Remove(s.path("queue-" + id))
}

func (s *store) removeInstall(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeLocked(id)
	return s.saveInstalls()
}

// enqueue appends a body to an install's queue, applying the age and size limits.
func (s *store) enqueue(id string, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.installs[id]
	if !ok {
		return nil
	}
	now := s.now()
	q := s.queues[id]
	kept := q[:0:0]
	for _, it := range q {
		if now.Sub(it.At) <= queueMaxAge {
			kept = append(kept, it)
		}
	}
	dropped := len(q) - len(kept)
	kept = append(kept, Item{ID: randomID(), At: now, Body: append(json.RawMessage{}, body...)})
	if over := len(kept) - queueMaxItems; over > 0 {
		kept = kept[over:]
		dropped += over
	}
	s.queues[id] = kept
	if err := s.write("queue-"+id, kept); err != nil {
		return err
	}
	if dropped > 0 {
		in.Dropped += dropped
		_ = s.saveInstalls()
	}
	if ch, ok := s.wake[id]; ok {
		close(ch)
		delete(s.wake, id)
	}
	return nil
}

// take returns up to max waiting items, and a channel that is closed when more arrive.
func (s *store) take(id string, max int) ([]Item, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queues[id]
	if len(q) > max {
		q = q[:max]
	}
	if len(q) > 0 {
		return append([]Item(nil), q...), nil
	}
	ch, ok := s.wake[id]
	if !ok {
		ch = make(chan struct{})
		s.wake[id] = ch
	}
	return nil, ch
}

// ack deletes delivered items.
func (s *store) ack(id string, ids []string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.installs[id]; !ok {
		return 0, nil
	}
	done := map[string]bool{}
	for _, x := range ids {
		done[x] = true
	}
	q := s.queues[id]
	kept := q[:0:0]
	for _, it := range q {
		if !done[it.ID] {
			kept = append(kept, it)
		}
	}
	n := len(q) - len(kept)
	if n == 0 {
		return 0, nil
	}
	s.queues[id] = kept
	return n, s.write("queue-"+id, kept)
}

func randomID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
