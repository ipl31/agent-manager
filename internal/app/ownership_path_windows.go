package app

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func databaseFileIdentity(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return ownershipFileIdentity(file)
}

func ownershipFileIdentity(file *os.File) (string, error) {
	// FILE_ID_INFO contains the volume serial followed by the filesystem's
	// full 128-bit ID, including on volumes whose IDs do not fit 64 bits.
	var identity struct {
		Volume uint64
		File   [16]byte
	}
	err := windows.GetFileInformationByHandleEx(windows.Handle(file.Fd()), windows.FileIdInfo, (*byte)(unsafe.Pointer(&identity)), uint32(unsafe.Sizeof(identity)))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("windows:%x:%x", identity.Volume, identity.File), nil
}
