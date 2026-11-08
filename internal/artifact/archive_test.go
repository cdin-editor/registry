package artifact

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/cdin-editor/registry/internal/catalog"
)

const validReleaseMetadataSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func zipBytes(t *testing.T, names ...string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if name[len(name)-1:] != "/" {
			if _, err := entry.Write([]byte("payload")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func tarGzipBytes(t *testing.T, entries ...struct {
	name string
	kind byte
	mode int64
	body string
}) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(gz)
	for _, item := range entries {
		size := int64(0)
		linkname := ""
		if item.kind == tar.TypeReg || item.kind == tar.TypeRegA {
			size = int64(len(item.body))
		}
		if item.kind == tar.TypeSymlink || item.kind == tar.TypeLink {
			linkname = item.body
		}
		header := &tar.Header{
			Name:     item.name,
			Typeflag: item.kind,
			Mode:     item.mode,
			Size:     size,
			Linkname: linkname,
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if size > 0 {
			if _, err := writer.Write([]byte(item.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestExtractZipValidPackage(t *testing.T) {
	root := t.TempDir()
	data := zipBytes(t, "include/api.h", "lib/libbackend.a")
	if err := Extract(data, "zip", root, Limits{}); err != nil {
		t.Fatalf("valid zip rejected: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(root, "include", "api.h")); err != nil || string(content) != "payload" {
		t.Fatalf("package file missing or corrupt: %q %v", content, err)
	}
}

func TestExtractTarGzipValidPackage(t *testing.T) {
	root := t.TempDir()
	data := tarGzipBytes(t, struct {
		name string
		kind byte
		mode int64
		body string
	}{name: "include/api.h", kind: tar.TypeReg, mode: 0o644, body: "tar-payload"})
	if err := Extract(data, "tar.gz", root, Limits{}); err != nil {
		t.Fatalf("valid tar.gz rejected: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(root, "include", "api.h")); err != nil || string(content) != "tar-payload" {
		t.Fatalf("tar package file missing or corrupt: %q %v", content, err)
	}
}

func TestExtractTarGzipRejectsPrivilegedModeAndLinks(t *testing.T) {
	tests := []struct {
		name  string
		entry struct {
			name string
			kind byte
			mode int64
			body string
		}
	}{
		{
			name: "setuid",
			entry: struct {
				name string
				kind byte
				mode int64
				body string
			}{name: "bin/tool", kind: tar.TypeReg, mode: 0o4755, body: "payload"},
		},
		{
			name: "symlink",
			entry: struct {
				name string
				kind byte
				mode int64
				body string
			}{name: "bin/current", kind: tar.TypeSymlink, mode: 0o777, body: "../tool"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := Extract(tarGzipBytes(t, test.entry), "tar.gz", root, Limits{}); err == nil {
				t.Fatal("unsafe tar entry was accepted")
			}
		})
	}
}

func TestExtractRejectsTraversalAndCollisions(t *testing.T) {
	tests := []struct {
		name    string
		entries []string
	}{
		{"parent traversal", []string{"../escape"}},
		{"absolute path", []string{"/etc/passwd"}},
		{"drive path", []string{"C:/Windows/system.ini"}},
		{"backslash traversal", []string{"..\\escape"}},
		{"case collision", []string{"bin/Tool.dll", "bin/tool.dll"}},
		{"trailing dot collision", []string{"bin/tool", "bin/tool."}},
		{"reserved name", []string{"bin/CON"}},
		{"alternate stream", []string{"bin/tool.exe:stream"}},
		{"file parent", []string{"lib/item", "lib/item/child"}},
		{"implicit parent case collision", []string{"Lib/A.h", "lib/B.h"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := Extract(zipBytes(t, test.entries...), "zip", root, Limits{}); err == nil {
				t.Fatal("unsafe archive was accepted")
			}
		})
	}
}

func TestExtractRejectsUnicodeNormalizationCollision(t *testing.T) {
	root := t.TempDir()
	data := zipBytes(t, "data/é.txt", "data/é.txt")
	if err := Extract(data, "zip", root, Limits{}); err == nil {
		t.Fatal("normalization collision was accepted")
	}
}

func TestExtractHonorsEntryAndExpandedLimits(t *testing.T) {
	root := t.TempDir()
	if err := Extract(zipBytes(t, "one", "two"), "zip", root, Limits{MaxEntries: 1}); err == nil {
		t.Fatal("entry count limit was ignored")
	}
	root = t.TempDir()
	if err := Extract(zipBytes(t, "one"), "zip", root, Limits{MaxExpandedBytes: 1}); err == nil {
		t.Fatal("expanded byte limit was ignored")
	}
}

type fakeDigestFetcher struct {
	data []byte
}

func (f fakeDigestFetcher) FetchWithDigest(_ context.Context, _, _ string) ([]byte, error) {
	return f.data, nil
}

func TestMaterializeChecksSizeAndDigestBeforeExtraction(t *testing.T) {
	data := zipBytes(t, "include/api.h")
	sum := sha256.Sum256(data)
	descriptor := catalog.Artifact{
		URI:       "https://example.invalid/pkg.zip",
		SHA256:    hex.EncodeToString(sum[:]),
		SizeBytes: int64(len(data)),
		Format:    "zip",
	}
	root := t.TempDir()
	if err := Materialize(context.Background(), fakeDigestFetcher{data: data}, validReleaseMetadataSHA, descriptor, root, Limits{}); err != nil {
		t.Fatalf("valid package failed: %v", err)
	}

	descriptor.SizeBytes++
	root = t.TempDir()
	if err := Materialize(context.Background(), fakeDigestFetcher{data: data}, validReleaseMetadataSHA, descriptor, root, Limits{}); err == nil {
		t.Fatal("size mismatch accepted")
	}
}

func TestMaterializeUsesDefaultCompressedSizeLimitBeforeFetch(t *testing.T) {
	descriptor := catalog.Artifact{
		URI:       "https://example.invalid/pkg.zip",
		SHA256:    hex.EncodeToString(make([]byte, sha256.Size)),
		SizeBytes: DefaultMaxCompressedBytes + 1,
		Format:    "zip",
	}
	fetcher := fakeDigestFetcher{data: []byte("should not be requested")}
	err := Materialize(context.Background(), fetcher, validReleaseMetadataSHA, descriptor, t.TempDir(), Limits{})
	if err == nil {
		t.Fatal("oversized descriptor was fetched")
	}
}
