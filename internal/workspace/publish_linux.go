//go:build linux

package workspace

import (
	"os"

	"golang.org/x/sys/unix"
)

// renameDirectoryNoReplace performs an atomic same-filesystem directory rename
// and fails when the destination exists, including when it is an empty directory.
func renameDirectoryNoReplace(oldPath, newPath string) error {
	err := unix.Renameat2(unix.AT_FDCWD, oldPath, unix.AT_FDCWD, newPath, unix.RENAME_NOREPLACE)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: err}
	}
	return nil
}
