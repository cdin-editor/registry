//go:build windows

package workspace

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// renameDirectoryNoReplace calls MoveFileEx without REPLACE_EXISTING or
// COPY_ALLOWED. Existing destinations therefore fail instead of being replaced,
// and a cross-volume move cannot silently degrade into a copy.
func renameDirectoryNoReplace(oldPath, newPath string) error {
	oldName, err := windows.UTF16PtrFromString(oldPath)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: err}
	}
	newName, err := windows.UTF16PtrFromString(newPath)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: err}
	}
	if err := windows.MoveFileEx(oldName, newName, 0); err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_FILE_EXISTS) {
			err = os.ErrExist
		}
		return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: err}
	}
	return nil
}
