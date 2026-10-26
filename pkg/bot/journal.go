package bot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"whatsapp-automation/pkg/securestore"
)

// journal keeps received messages on disk until they have been processed and the
// history holding them is saved. Meta never repeats a delivery it got an answer for, so
// without it a crash or a forced stop between that answer and the next history save would
// lose the message. Messages still listed at the next start are processed then; one that
// was handled and saved before the crash is recognised by its ID in the history and
// skipped, so it is not answered twice.
type journal struct {
	mu        sync.Mutex
	path      string
	box       *securestore.Box
	pending   map[string]journalEntry
	processed map[string]time.Time // handled, waiting for the history save that includes them
	seq       int64
	immediate bool // no history to wait for (tests): remove entries as soon as they are handled
}

// journalEntry is a message plus its arrival order (Meta's timestamps have one-second
// resolution, so two messages from one customer can share a time).
type journalEntry struct {
	Incoming
	Seq int64 `json:"seq"`
}

func openJournal(path string, box *securestore.Box) (*journal, []Incoming) {
	j := &journal{path: path, box: box, pending: map[string]journalEntry{}, processed: map[string]time.Time{}}
	var saved []journalEntry
	data, err := box.ReadFile(path)
	if err == nil && len(data) > 0 {
		err = json.Unmarshal(data, &saved)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// Kept aside instead of being overwritten by the next message, so the messages in
		// it can still be recovered by hand.
		aside := fmt.Sprintf("%s.unreadable-%s", path, time.Now().Format("20060102-150405"))
		if rerr := os.Rename(path, aside); rerr == nil {
			fmt.Printf("[Bot] The list of unprocessed messages could not be read (%v); it was kept as %s.\n", err, aside)
		} else {
			fmt.Printf("[Bot] The list of unprocessed messages could not be read: %v\n", err)
		}
		saved = nil
	}
	// In the order the messages arrived, so replies to one chat keep their order.
	sort.SliceStable(saved, func(a, b int) bool {
		if !saved[a].SentAt.Equal(saved[b].SentAt) {
			return saved[a].SentAt.Before(saved[b].SentAt)
		}
		return saved[a].Seq < saved[b].Seq
	})
	out := make([]Incoming, 0, len(saved))
	for _, e := range saved {
		if e.ID == "" {
			continue
		}
		j.pending[e.ID] = e
		if e.Seq > j.seq {
			j.seq = e.Seq
		}
		out = append(out, e.Incoming)
	}
	return j, out
}

// add records a received message on disk before it is processed.
func (j *journal) add(in Incoming) error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, ok := j.pending[in.ID]; ok {
		return nil
	}
	j.seq++
	j.pending[in.ID] = journalEntry{Incoming: in, Seq: j.seq}
	if err := j.saveLocked(); err != nil {
		delete(j.pending, in.ID)
		return err
	}
	return nil
}

// done marks a message as handled. It stays on disk until the history save that
// includes it (compact), so a crash before that save still replays it.
func (j *journal) done(id string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, ok := j.pending[id]; !ok {
		return
	}
	if j.immediate {
		delete(j.pending, id)
		j.saveOrWarnLocked()
		return
	}
	j.processed[id] = time.Now()
}

// compact removes the messages that were handled before the history copy taken at
// snapshotAt was saved.
func (j *journal) compact(snapshotAt time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	changed := false
	for id, at := range j.processed {
		if !at.After(snapshotAt) {
			delete(j.pending, id)
			delete(j.processed, id)
			changed = true
		}
	}
	if changed {
		j.saveOrWarnLocked()
	}
}

func (j *journal) saveOrWarnLocked() {
	if err := j.saveLocked(); err != nil {
		// Harmless: at the next start such a message is found in the history and skipped.
		fmt.Printf("[Bot] Could not update the list of unprocessed messages: %v\n", err)
	}
}

func (j *journal) saveLocked() error {
	list := make([]journalEntry, 0, len(j.pending))
	for _, e := range j.pending {
		list = append(list, e)
	}
	data, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return j.box.WriteFileNoBackup(j.path, data)
}
