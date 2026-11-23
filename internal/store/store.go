package store

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	path string
}

// busyTimeout is how long a writer waits for the lock before giving up.
// The manager and every `agent-manager mcp` process share this database,
// so without it a collision between the poller's write and a session
// tool's write fails instantly with SQLITE_BUSY instead of waiting.
// _txlock=immediate takes the write lock at BEGIN, so a multi-statement
// transaction cannot fail halfway through trying to upgrade.
const busyTimeout = "?_pragma=busy_timeout(5000)&_txlock=immediate"

func Open(path string) (*Store, error) {
	absPath, err := resolveStorePath(path)
	if err != nil {
		return nil, err
	}

	if err := restrictToOwner(absPath); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", absPath+busyTimeout)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}
	store := &Store{db: db, path: absPath}
	if err := store.init(); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.importConfigFile(filepath.Dir(path)); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

// A deliberate dangling database symlink still needs its actual target
// created privately before SQLite opens it, with sidecars beside that target.
func resolveStorePath(path string) (string, error) {
	path, err := rawAbsoluteStorePath(path)
	if err != nil {
		return "", fmt.Errorf("resolve store path: %w", err)
	}
	var seen []os.FileInfo
	for {
		directory, base := filepath.Split(path)
		parent, err := filepath.EvalSymlinks(directory)
		if err != nil {
			return "", fmt.Errorf("resolve store path: %w", err)
		}
		entry := filepath.Join(parent, base)
		info, err := os.Lstat(entry)
		if errors.Is(err, fs.ErrNotExist) {
			return entry, nil
		}
		if err != nil {
			return "", fmt.Errorf("resolve store path: %w", err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return entry, nil
		}
		for _, previous := range seen {
			if os.SameFile(info, previous) {
				return "", fmt.Errorf("resolve store path: symlink cycle at %s", entry)
			}
		}
		seen = append(seen, info)
		target, err := os.Readlink(entry)
		if err != nil {
			return "", fmt.Errorf("resolve store path: %w", err)
		}
		switch {
		case filepath.IsAbs(target):
			path = target
		case filepath.VolumeName(target) != "":
			path, err = rawAbsoluteStorePath(target)
			if err != nil {
				return "", fmt.Errorf("resolve store path: %w", err)
			}
		case os.IsPathSeparator(target[0]):
			path = filepath.VolumeName(parent) + target
		default:
			path = parent + string(os.PathSeparator) + target
		}
	}
}

// Prefix a physical current directory without cleaning a raw symlink/..
// traversal. A drive-relative Windows path uses that drive's own directory.
func rawAbsoluteStorePath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}
	var parent string
	var err error
	if volume := filepath.VolumeName(path); volume != "" {
		parent, err = filepath.Abs(volume + ".")
		path = path[len(volume):]
	} else {
		parent, err = os.Getwd()
	}
	if err != nil {
		return "", err
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	if path != "" && os.IsPathSeparator(path[0]) {
		return filepath.VolumeName(parent) + path, nil
	}
	return parent + string(os.PathSeparator) + path, nil
}

// restrictToOwner keeps the database readable by this user alone, since
// environment profiles store tokens in it. SQLite gives its WAL and shared
// memory files the database's own mode, so creating it 0600 covers them; a
// database from an earlier release, created 0644, is tightened in place.
func restrictToOwner(path string) error {
	// Closing a raw descriptor to an existing inode would drop SQLite's POSIX locks.
	file, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("open store: %w", err)
	}
	if err == nil {
		file.Close()
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("open store: %s is not a regular file", path)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("restrict store: %w", err)
	}
	for _, name := range []string{path + "-wal", path + "-shm"} {
		if err := os.Chmod(name, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("restrict store: %w", err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// requireRowOrNoop resolves a guarded UPDATE that matched zero rows: either
// the WHERE condition already held (success no-op) or the session is gone.
// Distinguish so callers still see ErrSessionGone.
func (s *Store) requireRowOrNoop(res sql.Result, id string) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	var exists int
	err = s.db.QueryRow(`SELECT 1 FROM sessions WHERE id = ?`, id).Scan(&exists)
	if err == sql.ErrNoRows {
		return fmt.Errorf("session %s: %w", id, ErrSessionGone)
	}
	return err
}

func requireRow(res sql.Result, id string) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("session %s: %w", id, ErrSessionGone)
	}
	return nil
}

// escapeLike escapes the LIKE metacharacters so a group path is matched
// literally in a `? || '/%'` prefix pattern, paired with ESCAPE '\'. Group
// names may contain '_' or '%', which LIKE would otherwise treat as
// wildcards and let one group's subtree bleed into another's.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
