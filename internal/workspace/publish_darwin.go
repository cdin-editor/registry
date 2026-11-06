//go:build darwin

package workspace

import (
	"os"

	"golang.org/x/sys/unix"
)

// renameDirectoryNoReplace uses renamex_np(RENAME_EXCL), so checking and
// moving the staged directory is one atomic no-replace filesystem operation.
func renameDirectoryNoReplace(oldPath, newPath string) error {
	err := unix.RenamexNp(oldPath, newPath, unix.RENAME_EXCL)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: err}
	}
	return nil
}
