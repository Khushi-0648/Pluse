package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"whatsapp-automation/pkg/securestore"
	"whatsapp-automation/pkg/security"
	"whatsapp-automation/pkg/version"
)

// Automatic backups (FT22): once a day Pulse writes the same bundle as "Download
// backup" (contacts, templates, rules, business details, opening hours), encrypted with
// this computer's key, into <data folder>\backups, and keeps the last seven. A second
// copy can go to a folder the owner chooses, such as a USB drive.

const autoBackupKeep = 7

var backupName = regexp.MustCompile(`^pulse-backup-\d{4}-\d{2}-\d{2}\.json$`)

func (s *Server) backupDir() string { return filepath.Join(s.DataDir, "backups") }

// AutoBackup writes today's backup now.
func (s *Server) AutoBackup() error {
	if s.DataDir == "" {
		return errors.New("no data folder")
	}
	data, err := json.Marshal(s.buildBackup())
	if err != nil {
		return err
	}
	name := "pulse-backup-" + time.Now().Format("2006-01-02") + ".json"
	box := securestore.Default()
	dirs := []string{s.backupDir()}
	if extra := strings.TrimSpace(s.cfg.Snapshot().Storage.BackupCopyDir); extra != "" {
		dirs = append(dirs, extra)
	}
	var firstErr error
	for i, dir := range dirs {
		if err := os.MkdirAll(dir, 0700); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", dir, err)
			}
			continue
		}
		if i == 0 {
			securestore.RestrictToUser(dir)
		}
		if err := box.WriteFileNoBackup(filepath.Join(dir, name), data); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", dir, err)
		}
		pruneBackups(dir)
	}
	return firstErr
}

func pruneBackups(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && backupName.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // dates sort in order
	for len(names) > autoBackupKeep {
		_ = os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}

// RunAutoBackups makes a backup shortly after start (if today's is missing) and then
// once a day, while automatic backups are switched on.
func (s *Server) RunAutoBackups(ctx context.Context) {
	t := time.NewTimer(2 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if s.cfg.Snapshot().Storage.AutoBackup {
			today := filepath.Join(s.backupDir(), "pulse-backup-"+time.Now().Format("2006-01-02")+".json")
			if _, err := os.Stat(today); err != nil {
				if err := s.AutoBackup(); err != nil {
					security.LogAudit("AUTO_BACKUP_FAILED", "local", "system", "Automatic backup failed: "+err.Error(), "failed")
				} else {
					security.LogAudit("AUTO_BACKUP", "local", "system", "Automatic backup saved", "success")
				}
			}
		}
		t.Reset(time.Hour) // checks hourly, so a PC that sleeps at night still gets a daily backup
	}
}

type backupInfo struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	Size      int64     `json:"size"`
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	out := []backupInfo{}
	if entries, err := os.ReadDir(s.backupDir()); err == nil {
		for _, e := range entries {
			if e.IsDir() || !backupName.MatchString(e.Name()) {
				continue
			}
			if fi, err := e.Info(); err == nil {
				out = append(out, backupInfo{Name: e.Name(), CreatedAt: fi.ModTime(), Size: fi.Size()})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	snap := s.cfg.Snapshot().Storage
	writeJSON(w, http.StatusOK, map[string]any{"backups": out, "auto_backup": snap.AutoBackup, "copy_dir": snap.BackupCopyDir, "folder": s.backupDir(), "old_copies": s.oldCopies(), "old_session": len(s.oldSessionFiles())})
}

// oldCopiesDir holds the unencrypted files of versions before 1.4, kept after they were
// imported into the encrypted data folder.
func (s *Server) oldCopiesDir() string { return filepath.Join(s.DataDir, "old-version-backup") }

func (s *Server) oldCopies() int {
	n := 0
	_ = filepath.WalkDir(s.oldCopiesDir(), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// handleDeleteOldCopies removes the unencrypted copies left by the upgrade from 1.3.
func (s *Server) handleDeleteOldCopies(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) || !s.reauth(w, r, req.Password) {
		return
	}
	n := s.oldCopies()
	if s.DataDir == "" || n == 0 {
		writeOK(w, map[string]any{"message": "There are no old unencrypted copies."})
		return
	}
	if err := os.RemoveAll(s.oldCopiesDir()); err != nil {
		writeError(w, http.StatusInternalServerError, "Some files could not be deleted: "+err.Error())
		return
	}
	security.LogAudit("OLD_COPIES_DELETED", clientIP(r), actor(r), fmt.Sprintf("Deleted %d unencrypted files left by the upgrade from an older version", n), "success")
	writeOK(w, map[string]any{"message": fmt.Sprintf("Deleted %d unencrypted old files.", n)})
}

func (s *Server) handleBackupNow(w http.ResponseWriter, r *http.Request) {
	if err := s.AutoBackup(); err != nil {
		security.LogAudit("AUTO_BACKUP_FAILED", clientIP(r), actor(r), "Backup failed: "+err.Error(), "failed")
		writeError(w, http.StatusInternalServerError, "The backup could not be saved: "+err.Error())
		return
	}
	security.LogAudit("AUTO_BACKUP", clientIP(r), actor(r), "Backup saved on request", "success")
	writeOK(w, map[string]any{"message": "Backup saved."})
}

func (s *Server) handleRestoreAutoBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) || !s.reauth(w, r, req.Password) {
		return
	}
	if !backupName.MatchString(req.Name) { // also blocks any path in the name
		writeError(w, http.StatusBadRequest, "Choose a backup from the list.")
		return
	}
	data, err := securestore.Default().ReadFile(filepath.Join(s.backupDir(), req.Name))
	if err != nil {
		writeError(w, http.StatusBadRequest, "This backup could not be opened. It may come from another computer or Windows account.")
		return
	}
	var b backupBundle
	if err := json.Unmarshal(data, &b); err != nil || (b.Product != "" && b.Product != version.Product) {
		writeError(w, http.StatusBadRequest, "This backup file is damaged.")
		return
	}
	nc, nt, nr := s.restoreBundle(b)
	security.LogAudit("BACKUP_RESTORED", clientIP(r), actor(r), fmt.Sprintf("Restored %s: %d contacts, %d templates, %d rules", req.Name, nc, nt, nr), "success")
	writeOK(w, map[string]any{"message": fmt.Sprintf("Restored %d contacts, %d templates and %d rules from %s.", nc, nt, nr, req.Name)})
}
