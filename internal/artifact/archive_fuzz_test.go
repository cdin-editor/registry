package artifact

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzExtractArchive exercises both supported untrusted archive parsers under
// strict resource limits and asserts that successful extraction remains inside
// the private staging root with regular files/directories only.
func FuzzExtractArchive(f *testing.F) {
	f.Add("zip", []byte("PK\x03\x04\x14\x00\x00\x00"))
	f.Add("zip", []byte{})
	f.Add("tar.gz", []byte{0x1f, 0x8b, 0x08, 0x00})
	f.Fuzz(func(t *testing.T, format string, data []byte) {
		if format != "zip" && format != "tar.gz" {
			t.Skip()
		}
		if len(data) > 1<<20 {
			t.Skip()
		}

		root := filepath.Join(t.TempDir(), "stage")
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		err := Extract(data, format, root, Limits{
			MaxCompressedBytes: 1 << 20,
			MaxExpandedBytes:   1 << 20,
			MaxEntries:         1024,
			MaxDepth:           16,
		})
		if err != nil {
			if _, statErr := os.Lstat(root); !os.IsNotExist(statErr) {
				t.Fatalf("rejected archive left a staging directory: %v", statErr)
			}
			return
		}

		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if relative == ".." || filepath.IsAbs(relative) ||
				len(relative) > 3 && relative[:3] == ".."+string(os.PathSeparator) {
				return os.ErrPermission
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
				return os.ErrPermission
			}
			return nil
		}); err != nil {
			t.Fatalf("successful extraction escaped or created a special entry: %v", err)
		}
	})
}
