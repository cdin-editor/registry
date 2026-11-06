package collector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkCopyLargeSourceTree(b *testing.B) {
	const fileCount = 256
	const bytesPerFile = 4096
	source := b.TempDir()
	payload := make([]byte, bytesPerFile)
	for i := 0; i < fileCount; i++ {
		path := filepath.Join(source, fmt.Sprintf("dir-%02d", i%16), fmt.Sprintf("file-%04d.bin", i))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()
	b.SetBytes(fileCount * bytesPerFile)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		destination := filepath.Join(b.TempDir(), "copy")
		if err := copyTree(context.Background(), source, destination); err != nil {
			b.Fatal(err)
		}
	}
}
