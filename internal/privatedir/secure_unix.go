//go:build !windows

package privatedir

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func secureTree(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("privatedir: inspect cache root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("privatedir: cache root is not a real directory")
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("privatedir: cache tree contains a symlink")
		}
		if info.IsDir() {
			return os.Chmod(path, 0o700)
		}
		if info.Mode().IsRegular() {
			return os.Chmod(path, 0o600)
		}
		return fmt.Errorf("privatedir: cache tree contains a special file")
	})
}
