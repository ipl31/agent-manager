//go:build darwin || linux

package store

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestStoreRecordLockProbe(t *testing.T) {
	args := os.Args
	for i, arg := range args {
		if arg != "store-lock-probe" {
			continue
		}
		file, err := os.OpenFile(args[i+1], os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		lock := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0, Start: 0, Len: 1}
		err = syscall.FcntlFlock(file.Fd(), syscall.F_SETLK, &lock)
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EACCES) {
			fmt.Println("LOCKED")
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("UNLOCKED")
		return
	}
}

func TestSecondStoreOpenPreservesExistingPOSIXRecordLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	initial, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.Close(); err != nil {
		t.Fatal(err)
	}
	// Use a byte outside SQLite's lock range. Closing any unrelated FD on
	// this inode still drops it, exactly the initialization regression.
	held, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	lock := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0, Start: 0, Len: 1}
	if err := syscall.FcntlFlock(held.Fd(), syscall.F_SETLK, &lock); err != nil {
		t.Fatal(err)
	}
	probe := func() {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestStoreRecordLockProbe$", "--", "store-lock-probe", path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("record lock probe: %v\n%s", err, out)
		}
		if !strings.HasPrefix(string(out), "LOCKED\n") {
			t.Fatalf("existing record lock was lost: %s", out)
		}
	}
	probe()
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	probe()
}
