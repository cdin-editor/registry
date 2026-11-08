// Package artifact verifies and safely extracts immutable Registry package
// archives. It never executes package content or install hooks.
package artifact

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	DefaultMaxCompressedBytes int64 = 512 << 20
	DefaultMaxExpandedBytes   int64 = 2 << 30
	DefaultMaxEntries               = 100000
	DefaultMaxDepth                 = 32
)

type Limits struct {
	MaxCompressedBytes int64
	MaxExpandedBytes   int64
	MaxEntries         int
	MaxDepth           int
}

type entryKind uint8

const (
	kindFile entryKind = iota + 1
	kindDirectory
)

type extractor struct {
	root        string
	limits      Limits
	entries     int
	expanded    int64
	seen        map[string]string
	names       map[string]string
	kinds    map[string]entryKind
}

// Extract verifies archive structure and writes regular files/directories only.
// The destination must be an empty, private, real directory owned by the caller.
func Extract(data []byte, format, destination string, limits Limits) error {
	if limits.MaxCompressedBytes <= 0 {
		limits.MaxCompressedBytes = DefaultMaxCompressedBytes
	}
	if limits.MaxExpandedBytes <= 0 {
		limits.MaxExpandedBytes = DefaultMaxExpandedBytes
	}
	if limits.MaxEntries <= 0 {
		limits.MaxEntries = DefaultMaxEntries
	}
	if limits.MaxDepth <= 0 {
		limits.MaxDepth = DefaultMaxDepth
	}
	if int64(len(data)) > limits.MaxCompressedBytes {
		return fmt.Errorf("ARTIFACT_LIMIT_EXCEEDED: compressed size limit exceeded")
	}

	info, err := os.Lstat(destination)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("ARTIFACT_EXTRACTION_FAILED: destination is not a real directory")
	}
	children, err := os.ReadDir(destination)
	if err != nil {
		return fmt.Errorf("ARTIFACT_EXTRACTION_FAILED: inspect staging directory")
	}
	if len(children) != 0 {
		return fmt.Errorf("ARTIFACT_EXTRACTION_FAILED: staging directory is not empty")
	}

	x := &extractor{
		root:        destination,
		limits:      limits,
		seen:        make(map[string]string),
		names:       make(map[string]string),
		kinds:  make(map[string]entryKind),
	}
	switch format {
	case "tar.gz":
		err = x.extractTarGzip(data)
	case "zip":
		err = x.extractZip(data)
	default:
		return fmt.Errorf("ARTIFACT_FORMAT_UNSUPPORTED: archive format is not supported")
	}
	if err != nil {
		_ = os.RemoveAll(destination)
		return err
	}
	return nil
}

func (x *extractor) extractTarGzip(data []byte) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: invalid gzip stream")
	}
	defer gz.Close()

	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			// tar.Reader stops after the archive end markers. Drain the gzip
			// stream as well so its checksum and uncompressed-size trailer are
			// verified before the package is accepted.
			if _, err := io.Copy(io.Discard, gz); err != nil {
				return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: invalid gzip checksum or trailer")
			}
			break
		}
		if err != nil {
			return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: invalid tar structure")
		}
		if header == nil {
			return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: empty tar header")
		}

		var kind entryKind
		switch header.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			kind = kindFile
		case tar.TypeDir:
			kind = kindDirectory
		default:
			return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: links and special tar entries are forbidden")
		}
		if header.Mode&07000 != 0 {
			return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: privileged file modes are forbidden")
		}
		if kind == kindDirectory && header.Size != 0 {
			return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: directory entries must be empty")
		}
		if kind == kindFile && header.Size < 0 {
			return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: negative tar entry size")
		}
		if err := x.addEntry(header.Name, kind, reader, header.Size, header.Mode); err != nil {
			return err
		}
	}
	return nil
}

func (x *extractor) extractZip(data []byte) error {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: invalid zip structure")
	}
	for _, entry := range reader.File {
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || (mode&os.ModeType != 0 && !mode.IsDir() && !mode.IsRegular()) {
			return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: links and special zip entries are forbidden")
		}
		if mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: privileged file modes are forbidden")
		}

		kind := kindFile
		if entry.FileInfo().IsDir() {
			kind = kindDirectory
		}
		size := int64(entry.UncompressedSize64)
		if kind == kindDirectory && size != 0 {
			return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: directory entries must be empty")
		}
		file, err := entry.Open()
		if err != nil {
			return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: unreadable zip entry")
		}
		err = x.addEntry(entry.Name, kind, file, size, int64(mode.Perm()))
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: zip entry close failed")
		}
	}
	return nil
}

func (x *extractor) addEntry(raw string, kind entryKind, source io.Reader, declaredSize, mode int64) error {
	if x.entries >= x.limits.MaxEntries {
		return fmt.Errorf("ARTIFACT_LIMIT_EXCEEDED: archive entry count exceeded")
	}
	name, folded, err := normalizeArchivePath(raw, x.limits.MaxDepth)
	if err != nil {
		return err
	}
	if _, exists := x.seen[folded]; exists {
		return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: duplicate or colliding archive path")
	}
	if previous, exists := x.names[folded]; exists && previous != name {
		return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: case-folded or normalized path collision")
	}

	// Check only the candidate's ancestors and its descendant marker. This
	// keeps validation bounded by path depth instead of scanning every prior
	// archive entry (which would be quadratic for large but valid packages).
	parts := strings.Split(folded, "/")
	for index := 1; index < len(parts); index++ {
		ancestor := strings.Join(parts[:index], "/")
		if existing, ok := x.kinds[ancestor]; ok && existing == kindFile {
			return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: file/directory prefix collision")
		}
	}
	if existing, ok := x.kinds[folded]; ok {
		if existing == kindFile || kind == kindFile {
			return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: file/directory path collision")
		}
	}

	if kind == kindDirectory {
		if declaredSize != 0 {
			return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: directory entries must be empty")
		}
	} else {
		if declaredSize < 0 || declaredSize > x.limits.MaxExpandedBytes-x.expanded {
			return fmt.Errorf("ARTIFACT_LIMIT_EXCEEDED: expanded archive size limit exceeded")
		}
	}

	// Register implicit parents before touching the filesystem. The names map
	// catches case/normalization aliases even when only child entries are present.
	parent := path.Dir(name)
	if parent != "." {
		accumulated := ""
		for _, part := range strings.Split(parent, "/") {
			accumulated = path.Join(accumulated, part)
			fold := cases.Fold().String(norm.NFC.String(accumulated))
			if previous, ok := x.names[fold]; ok && previous != accumulated {
				return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: implicit parent path collision")
			}
			if existing, ok := x.kinds[fold]; ok && existing == kindFile {
				return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: file/directory parent collision")
			}
			x.names[fold] = accumulated
			x.kinds[fold] = kindDirectory
		}
	}

	target := filepath.Join(x.root, filepath.FromSlash(name))
	if !withinRoot(x.root, target) {
		return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: archive path escapes staging root")
	}
	if err := ensureSafeParents(x.root, filepath.Dir(target)); err != nil {
		return fmt.Errorf("ARTIFACT_EXTRACTION_FAILED: unsafe extraction parent")
	}

	if kind == kindDirectory {
		if err := os.Mkdir(target, 0o755); err != nil && !os.IsExist(err) {
			return fmt.Errorf("ARTIFACT_EXTRACTION_FAILED: create package directory")
		}
		info, err := os.Lstat(target)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: directory path replaced by non-directory")
		}
	} else {
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return fmt.Errorf("ARTIFACT_EXTRACTION_FAILED: create package file")
		}
		written, copyErr := io.Copy(file, io.LimitReader(source, declaredSize+1))
		if copyErr == nil && written != declaredSize {
			copyErr = fmt.Errorf("entry size mismatch")
		}
		if copyErr == nil {
			copyErr = file.Sync()
		}
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			_ = os.Remove(target)
			return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: package file content mismatch")
		}
		x.expanded += declaredSize

		permissions := os.FileMode(0o644)
		if mode&0111 != 0 {
			permissions = 0o755
		}
		if err := os.Chmod(target, permissions); err != nil {
			return fmt.Errorf("ARTIFACT_EXTRACTION_FAILED: set normalized package permissions")
		}
	}

	x.seen[folded] = name
	x.names[folded] = name
	x.kinds[folded] = kind
	x.entries++
	return nil
}

func normalizeArchivePath(raw string, maxDepth int) (string, string, error) {
	if raw == "" || !utf8.ValidString(raw) || strings.ContainsRune(raw, 0) ||
		strings.Contains(raw, "\\") || strings.HasPrefix(raw, "/") {
		return "", "", fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: absolute or malformed archive path")
	}
	if len(raw) >= 2 &&
		((raw[0] >= 'A' && raw[0] <= 'Z') || (raw[0] >= 'a' && raw[0] <= 'z')) &&
		raw[1] == ':' {
		return "", "", fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: drive-letter archive path")
	}

	clean := path.Clean(raw)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") ||
		clean != strings.TrimSuffix(raw, "/") {
		return "", "", fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: non-normalized or traversing archive path")
	}
	parts := strings.Split(clean, "/")
	if len(parts) > maxDepth {
		return "", "", fmt.Errorf("ARTIFACT_LIMIT_EXCEEDED: archive path nesting limit exceeded")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.Contains(part, ":") ||
			strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return "", "", fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: unsafe archive path segment")
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" ||
			(len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) &&
				base[3] >= '1' && base[3] <= '9') {
			return "", "", fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: reserved Windows device name")
		}
	}

	normalized := norm.NFC.String(clean)
	folded := cases.Fold().String(normalized)
	return normalized, folded, nil
}

func withinRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != "." && rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func ensureSafeParents(root, parent string) error {
	rel, err := filepath.Rel(root, parent)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("outside root")
	}
	current := root
	if rel == "." {
		return nil
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o755); err != nil && !os.IsExist(err) {
				return err
			}
			info, err = os.Lstat(current)
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe parent")
		}
	}
	return nil
}
