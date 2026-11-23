//go:build !unix && !windows

package app

import (
	"errors"
	"os"
)

func databaseFileIdentity(string) (string, error) {
	return "", errors.New("native manager file identity unavailable")
}
func ownershipFileIdentity(*os.File) (string, error) {
	return "", errors.New("native manager file identity unavailable")
}
