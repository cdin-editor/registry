package artifact

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
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

type cancelAfterErrChecksContext struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (ctx *cancelAfterErrChecksContext) Err() error {
	ctx.remaining--
	if ctx.remaining <= 0 {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestExtractContextCancellationRemovesPartialExtraction(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.CreateHeader(&zip.FileHeader{Name: "large/payload.bin", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("x"), 1<<20)
	if _, err := entry.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(t.TempDir(), "stage")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAfterErrChecksContext{Context: base, cancel: cancel, remaining: 5}

	err = ExtractContext(ctx, buffer.Bytes(), "zip", destination, Limits{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ExtractContext error = %v, want context.Canceled", err)
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("canceled extraction left a partial destination: %v", err)
	}
}

func TestExtractTarGzipValidatesChecksumTrailer(t *testing.T) {
	data := tarGzipBytes(t, struct {
		name string
		kind byte
		mode int64
		body string
	}{name: "include/api.h", kind: tar.TypeReg, mode: 0o644, body: "payload"})
	// The gzip trailer starts with the CRC32, followed by ISIZE.
	data[len(data)-8] ^= 0xff
	if err := Extract(data, "tar.gz", t.TempDir(), Limits{}); err == nil {
		t.Fatal("corrupt gzip checksum trailer was accepted")
	}
}

func TestExtractTarGzipRejectsPAXPathTraversal(t *testing.T) {
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(gz)
	// Force archive/tar to emit an extended PAX path record by exceeding
	// the legacy USTAR path limit. The effective path escapes the staging root.
	effectivePath := "../" + strings.Repeat("a", 130) + "/" + strings.Repeat("b", 130) + ".txt"
	header := &tar.Header{
		Name:     effectivePath,
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len("payload")),
		Format:   tar.FormatPAX,
	}
	if err := writer.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	if err := Extract(buffer.Bytes(), "tar.gz", t.TempDir(), Limits{}); err == nil {
		t.Fatal("PAX path traversal override was accepted")
	}
}

func zipBytesWithUnicodePathExtra(t *testing.T, storedName, unicodeName, checksumName string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	payload := make([]byte, 5, 5+len(unicodeName))
	payload[0] = 1
	binary.LittleEndian.PutUint32(payload[1:5], crc32.ChecksumIEEE([]byte(checksumName)))
	payload = append(payload, []byte(unicodeName)...)
	extra := make([]byte, 4, 4+len(payload))
	binary.LittleEndian.PutUint16(extra[0:2], zipUnicodePathExtraID)
	binary.LittleEndian.PutUint16(extra[2:4], uint16(len(payload)))
	extra = append(extra, payload...)

	header := &zip.FileHeader{Name: storedName, Method: zip.Store}
	header.Extra = extra
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestExtractZipValidatesUnicodePathExtraField(t *testing.T) {
	tests := []struct {
		name         string
		storedName   string
		unicodeName  string
		checksumName string
		wantErr      bool
	}{
		{
			name:         "matching Unicode path",
			storedName:   "include/api.h",
			unicodeName:  "include/api.h",
			checksumName: "include/api.h",
		},
		{
			name:         "path traversal override",
			storedName:   "safe.txt",
			unicodeName:  "../escape",
			checksumName: "safe.txt",
			wantErr:      true,
		},
		{
			name:         "invalid filename checksum",
			storedName:   "safe.txt",
			unicodeName:  "../escape",
			checksumName: "different.txt",
			wantErr:      true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			destination := t.TempDir()
			data := zipBytesWithUnicodePathExtra(t, test.storedName, test.unicodeName, test.checksumName)
			err := Extract(data, "zip", destination, Limits{})
			if test.wantErr {
				if err == nil {
					t.Fatal("unsafe Unicode path extra field was accepted")
				}
				return
			}
			if err != nil {
				t.Fatalf("valid matching Unicode path extra field was rejected: %v", err)
			}
			if _, err := os.Stat(filepath.Join(destination, filepath.FromSlash(test.storedName))); err != nil {
				t.Fatalf("stored path was not extracted: %v", err)
			}
		})
	}
}

func TestExtractTarGzipRejectsGNULongNameTraversal(t *testing.T) {
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(gz)
	// Force a GNU long-name record whose effective path escapes the staging root.
	effectivePath := "../" + strings.Repeat("g", 130) + "/" + strings.Repeat("n", 130) + ".txt"
	header := &tar.Header{
		Name:     effectivePath,
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len("payload")),
		Format:   tar.FormatGNU,
	}
	if err := writer.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	if err := Extract(buffer.Bytes(), "tar.gz", t.TempDir(), Limits{}); err == nil {
		t.Fatal("GNU long-name traversal override was accepted")
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
		{"file parent after implicit directory", []string{"lib/item/child", "lib/item"}},
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
