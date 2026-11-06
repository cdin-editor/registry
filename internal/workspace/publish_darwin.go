//go:build darwin

package workspace

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// renameDirectoryNoReplace uses renamex_np(RENAME_EXCL), so checking and
// moving the staged directory is one atomic no-replace filesystem operation.
func renameDirectoryNoReplace(oldPath, newPath string) error {
	err := unix.RenamexNp(oldPath, newPath, unix.RENAME_EXCL)
	if errors.Is(err, unix.EEXIST) {
		err = os.ErrExist
	}
	if err != nil {
		return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: err}
	}
	return nil
}
