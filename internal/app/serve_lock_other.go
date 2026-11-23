//go:build !unix && !windows

package app

import (
	"fmt"
	"os"
	"runtime"
)

func tryServeLock(string) (func(), bool, error) {
	return nil, false, fmt.Errorf("serve cannot lock its profile on %s", runtime.GOOS)
}

func tryOwnershipFileLock(*os.File) (bool, error) {
	return false, fmt.Errorf("manager cannot lock its server on %s", runtime.GOOS)
}
