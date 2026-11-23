//go:build !windows

package main

import (
	"os"
	"syscall"
)

// restart replaces this process with the binary at path.
func restart(path string, args []string) error {
	return syscall.Exec(path, args, os.Environ())
}
