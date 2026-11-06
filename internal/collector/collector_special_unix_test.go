//go:build linux || darwin || freebsd

package collector

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestREG020RejectsSpecialFiles ensures the source copier accepts only regular
// files and directories. A FIFO must not become a special file in the workspace.
func TestREG020RejectsSpecialFiles(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "a.txt"), []byte("regular"), 0o644); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(source, "z.pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("named pipe creation unavailable: %v", err)
	}
	destination := filepath.Join(t.TempDir(), "copied")
	err := copyTree(context.Background(), source, destination)
	if err == nil || !strings.Contains(err.Error(), "unsupported special file") {
		t.Fatalf("copyTree error = %v, want special-file rejection", err)
	}
}
