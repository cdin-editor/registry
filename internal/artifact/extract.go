package artifact

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	maxArchiveEntries = 100000
	maxExpandedBytes  = int64(4 << 30)
	maxArchiveFile    = int64(2 << 30)
	maxArchiveDepth   = 128
	maxArchivePath    = 4096
)

type entryKind uint8

const (
	entryFile entryKind = iota + 1
	entryDirectory
)

type archivePaths struct {
	explicit map[string]entryKind
	nodes    map[string]entryKind
}

func newArchivePaths() *archivePaths {
	return &archivePaths{explicit: make(map[string]entryKind), nodes: make(map[string]entryKind)}
}

func (p *archivePaths) add(name string, kind entryKind) (string, error) {
	clean, key, err := canonicalArchivePath(name)
	if err != nil {
		return "", err
	}
	if _, exists := p.explicit[key]; exists {
		return "", fmt.Errorf("duplicate archive path %q", clean)
	}
	segments := strings.Split(clean, "/")
	if len(segments) > maxArchiveDepth {
		return "", fmt.Errorf("archive path exceeds the nesting limit")
	}
	for i := 1; i < len(segments); i++ {
		parent := strings.Join(segments[:i], "/")
		parentKey := archivePathKey(parent)
		if existing, ok := p.nodes[parentKey]; ok && existing != entryDirectory {
			return "", fmt.Errorf("archive path conflicts with a file ancestor")
		}
		p.nodes[parentKey] = entryDirectory
	}
	if existing, ok := p.nodes[key]; ok {
		if existing != kind || kind == entryDirectory && p.explicit[key] == entryDirectory {
			return "", fmt.Errorf("archive path collides with an existing file or directory")
		}
	}
	if kind == entryFile {
		prefix := key + "/"
		for existing := range p.nodes {
			if strings.HasPrefix(existing, prefix) {
				return "", fmt.Errorf("archive file conflicts with an existing directory subtree")
			}
		}
	}
	p.nodes[key] = kind
	p.explicit[key] = kind
	return clean, nil
}

func archivePathKey(value string) string {
	return strings.ToLower(norm.NFC.String(value))
}

func canonicalArchivePath(value string) (string, string, error) {
	if value == "" || len(value) > maxArchivePath || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return "", "", fmt.Errorf("archive contains an empty, oversized or invalid UTF-8 path")
	}
	if strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return "", "", fmt.Errorf("archive path is absolute or uses a backslash")
	}
	if len(value) >= 2 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' {
		return "", "", fmt.Errorf("archive path contains a drive-letter prefix")
	}
	trimmed := strings.TrimSuffix(value, "/")
	if trimmed == "" {
		return "", "", fmt.Errorf("archive root entry is not allowed")
	}
	clean := path.Clean(trimmed)
	if clean != trimmed || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", "", fmt.Errorf("archive path escapes or aliases its package root")
	}
	for _, segment := range strings.Split(clean, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.Contains(segment, ":") {
			return "", "", fmt.Errorf("archive path contains an unsafe segment")
		}
		if strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") {
			return "", "", fmt.Errorf("archive path has a Windows trailing-dot/space collision")
		}
		base := strings.ToUpper(strings.SplitN(segment, ".", 2)[0])
		if isWindowsReservedName(base) {
			return "", "", fmt.Errorf("archive path contains a reserved Windows device name")
		}
	}
	return clean, archivePathKey(clean), nil
}

func isWindowsReservedName(base string) bool {
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}

func safeDestination(root, relative string) (string, error) {
	candidate := filepath.Join(root, filepath.FromSlash(relative))
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absoluteRoot, absolute)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("archive path escapes the package destination")
	}
	return absolute, nil
}

func ensureNoSymlinkAncestors(value string) error {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return err
	}
	current := filepath.Clean(absolute)
	for {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive destination contains a symlink ancestor")
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

type extractBudget struct {
	entries int
	bytes   int64
}

func (b *extractBudget) reserve(size int64) error {
	b.entries++
	if b.entries > maxArchiveEntries {
		return fmt.Errorf("archive entry count exceeds the configured limit")
	}
	if size < 0 || size > maxArchiveFile || size > maxExpandedBytes-b.bytes {
		return fmt.Errorf("archive expanded size exceeds the configured limit")
	}
	b.bytes += size
	return nil
}

func extractArchive(archivePath, format, destination string) error {
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("package destination already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect package destination: %w", err)
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return fmt.Errorf("create package destination: %w", err)
	}
	if err := ensureNoSymlinkAncestors(destination); err != nil {
		return fmt.Errorf("unsafe package destination: %w", err)
	}
	switch format {
	case "tar.gz":
		return extractTarGzip(archivePath, destination)
	case "zip":
		return extractZip(archivePath, destination)
	default:
		return fmt.Errorf("unsupported package archive format %q", format)
	}
}

func extractTarGzip(archivePath, destination string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open tar.gz archive: %w", err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	paths := newArchivePaths()
	budget := &extractBudget{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}
		if err := budget.reserve(header.Size); err != nil {
			return err
		}
		var kind entryKind
		switch header.Typeflag {
		case tar.TypeDir:
			kind = entryDirectory
		case tar.TypeReg, tar.TypeRegA:
			kind = entryFile
		default:
			return fmt.Errorf("tar archive contains a link or special file")
		}
		if header.Mode&07000 != 0 {
			return fmt.Errorf("tar archive contains unsafe special permission bits")
		}
		relative, err := paths.add(header.Name, kind)
		if err != nil {
			return err
		}
		target, err := safeDestination(destination, relative)
		if err != nil {
			return err
		}
		if kind == entryDirectory {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create archive directory: %w", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create archive parent directory: %w", err)
		}
		if err := ensureNoSymlinkAncestors(filepath.Dir(target)); err != nil {
			return fmt.Errorf("unsafe archive parent directory: %w", err)
		}
		mode := os.FileMode(header.Mode & 0777)
		if mode == 0 {
			mode = 0644
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return fmt.Errorf("create archive file: %w", err)
		}
		written, copyErr := io.CopyN(output, reader, header.Size)
		closeErr := output.Close()
		if copyErr != nil || written != header.Size {
			if copyErr == nil {
				copyErr = io.ErrUnexpectedEOF
			}
			return fmt.Errorf("write archive file: %w", copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close archive file: %w", closeErr)
		}
	}
	return nil
}

func extractZip(archivePath, destination string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open ZIP archive: %w", err)
	}
	defer reader.Close()
	paths := newArchivePaths()
	budget := &extractBudget{}
	for _, entry := range reader.File {
		isDir := entry.FileInfo().IsDir()
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || mode&os.ModeType != 0 && !mode.IsRegular() && !isDir {
			return fmt.Errorf("ZIP archive contains a link or special file")
		}
		var kind entryKind
		if isDir {
			kind = entryDirectory
		} else {
			kind = entryFile
		}
		if mode&07000 != 0 {
			return fmt.Errorf("ZIP archive contains unsafe special permission bits")
		}
		if entry.UncompressedSize64 > uint64(maxArchiveFile) {
			return fmt.Errorf("ZIP entry exceeds the configured file size limit")
		}
		if err := budget.reserve(int64(entry.UncompressedSize64)); err != nil {
			return err
		}
		relative, err := paths.add(entry.Name, kind)
		if err != nil {
			return err
		}
		target, err := safeDestination(destination, relative)
		if err != nil {
			return err
		}
		if kind == entryDirectory {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create ZIP directory: %w", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create ZIP parent directory: %w", err)
		}
		if err := ensureNoSymlinkAncestors(filepath.Dir(target)); err != nil {
			return fmt.Errorf("unsafe ZIP parent directory: %w", err)
		}
		input, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open ZIP entry: %w", err)
		}
		fileMode := mode.Perm()
		if fileMode == 0 {
			fileMode = 0644
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
		if err != nil {
			_ = input.Close()
			return fmt.Errorf("create ZIP file: %w", err)
		}
		written, copyErr := io.CopyN(output, input, int64(entry.UncompressedSize64))
		inputCloseErr := input.Close()
		outputCloseErr := output.Close()
		if copyErr != nil || written != int64(entry.UncompressedSize64) {
			if copyErr == nil {
				copyErr = io.ErrUnexpectedEOF
			}
			return fmt.Errorf("write ZIP file: %w", copyErr)
		}
		if inputCloseErr != nil {
			return fmt.Errorf("close ZIP input: %w", inputCloseErr)
		}
		if outputCloseErr != nil {
			return fmt.Errorf("close ZIP output: %w", outputCloseErr)
		}
	}
	return nil
}
