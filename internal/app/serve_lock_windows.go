//go:build windows

package app

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func tryServeLock(path string) (release func(), acquired bool, err error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open serve lock: %w", err)
	}
	acquired, err = tryOwnershipFileLock(file)
	if err != nil || !acquired {
		file.Close()
		return nil, false, err
	}
	return func() { _ = file.Close() }, true, nil
}

func tryOwnershipFileLock(file *os.File) (bool, error) {
	// Like flock, the lock belongs to this handle and ends when it closes,
	// including when the process holding it dies.
	err := windows.LockFileEx(windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, new(windows.Overlapped))
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock %s: %w", file.Name(), err)
	}
	return true, nil
}
