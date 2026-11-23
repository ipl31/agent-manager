//go:build unix

package app

import (
	"errors"
	"fmt"
	"os"
	"syscall"
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
	var err error
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock %s: %w", file.Name(), err)
	}
	return true, nil
}
