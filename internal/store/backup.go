package store

// Backups of the history database: a copy a day (VACUUM INTO, a consistent
// copy while the app writes), in <store dir>/backups/history-YYYY-MM-DD.db,
// the newest KeepBackups kept. Lessons, tool records and the trigger queue
// live only in the database, so a damaged file would lose them otherwise.
// To go back to a copy: quit the app and put it in place of history.db.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// KeepBackups is how many daily copies are kept.
const KeepBackups = 7

// BackupFile is one copy.
type BackupFile struct {
	Name  string    `json:"name"`
	Path  string    `json:"path"`
	Bytes int64     `json:"bytes"`
	At    time.Time `json:"at"`
}

// DBInfo describes the history database and its copies.
type DBInfo struct {
	Open    bool         `json:"open"`
	Error   string       `json:"error,omitempty"` // why it is not open
	Path    string       `json:"path"`
	Bytes   int64        `json:"bytes"` // the file and its write-ahead log
	Runs    int          `json:"runs"`
	Lessons int          `json:"lessons"`
	Calls   int          `json:"toolCalls"`
	Queued  int          `json:"queued"`
	Backups []BackupFile `json:"backups"`
	Folder  string       `json:"folder"`
}

func (s *Store) backupDir() string { return filepath.Join(s.dir, "backups") }

// Backup writes today's copy now (replacing an earlier one of today) and
// drops the oldest beyond KeepBackups.
func (s *Store) Backup() (BackupFile, error) {
	if s.db == nil {
		return BackupFile{}, ErrNoDB
	}
	if err := os.MkdirAll(s.backupDir(), 0o700); err != nil {
		return BackupFile{}, err
	}
	now := time.Now()
	p := filepath.Join(s.backupDir(), "history-"+now.Format("2006-01-02")+".db")
	tmp := p + ".tmp"
	_ = os.Remove(tmp)
	if _, err := s.db.Exec(`VACUUM INTO ?`, tmp); err != nil {
		_ = os.Remove(tmp)
		return BackupFile{}, err
	}
	_ = os.Chmod(tmp, 0o600)
	if err := os.Rename(tmp, p); err != nil {
		return BackupFile{}, err
	}
	s.pruneBackups()
	fi, _ := os.Stat(p)
	b := BackupFile{Name: filepath.Base(p), Path: p, At: now}
	if fi != nil {
		b.Bytes = fi.Size()
	}
	return b, nil
}

// BackupDaily makes today's copy if there is none yet (cheap otherwise).
func (s *Store) BackupDaily() error {
	if s.db == nil {
		return nil
	}
	p := filepath.Join(s.backupDir(), "history-"+time.Now().Format("2006-01-02")+".db")
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	_, err := s.Backup()
	return err
}

func (s *Store) backups() []BackupFile {
	ents, _ := os.ReadDir(s.backupDir())
	var out []BackupFile
	for _, e := range ents {
		n := e.Name()
		if !strings.HasPrefix(n, "history-") || !strings.HasSuffix(n, ".db") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, BackupFile{Name: n, Path: filepath.Join(s.backupDir(), n), Bytes: fi.Size(), At: fi.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name }) // newest first (dates in the name)
	return out
}

func (s *Store) pruneBackups() {
	for i, b := range s.backups() {
		if i >= KeepBackups {
			_ = os.Remove(b.Path)
		}
	}
}

// Info says how big the history database is and which copies there are.
func (s *Store) Info() DBInfo {
	p := filepath.Join(s.dir, "history.db")
	in := DBInfo{Open: s.db != nil, Path: p, Folder: s.backupDir(), Backups: s.backups()}
	if in.Backups == nil {
		in.Backups = []BackupFile{}
	}
	if s.dbErr != nil {
		in.Error = s.dbErr.Error()
	}
	for _, f := range []string{p, p + "-wal"} {
		if fi, err := os.Stat(f); err == nil {
			in.Bytes += fi.Size()
		}
	}
	if s.db != nil {
		_ = s.db.QueryRow(`SELECT count(*) FROM runs`).Scan(&in.Runs)
		_ = s.db.QueryRow(`SELECT count(*) FROM lessons`).Scan(&in.Lessons)
		_ = s.db.QueryRow(`SELECT count(*) FROM tool_calls`).Scan(&in.Calls)
		_ = s.db.QueryRow(`SELECT count(*) FROM trigger_queue`).Scan(&in.Queued)
	}
	return in
}
