//go:build unix

package app

import (
	"fmt"
	"os"
	"syscall"
)

// Stat, rather than opening state.db, preserves SQLite's POSIX record locks:
// closing an unrelated descriptor to its inode can release those locks.
func databaseFileIdentity(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	return unixFileIdentity(info)
}

func ownershipFileIdentity(file *os.File) (string, error) {
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	return unixFileIdentity(info)
}

func unixFileIdentity(info os.FileInfo) (string, error) {
	status, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("file has no native identity")
	}
	return fmt.Sprintf("unix:%x:%x", uint64(status.Dev), uint64(status.Ino)), nil
}
