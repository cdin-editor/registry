//go:build !linux && !darwin && !windows

package workspace

import (
	"errors"
	"os"
)

var errAtomicNoReplaceUnsupported = errors.New("atomic no-replace directory publication is unsupported on this operating system")

// renameDirectoryNoReplace fails closed on platforms without a reviewed native
// no-replace primitive. It never falls back to check-then-os.Rename.
func renameDirectoryNoReplace(oldPath, newPath string) error {
	return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: errAtomicNoReplaceUnsupported}
}
