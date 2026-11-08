package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cdin-editor/registry/internal/catalog"
	"github.com/cdin-editor/registry/internal/resolver"
)

// Downloader is implemented by the bounded HTTPS transport at the CLI
// composition boundary. Tests may inject an in-memory/local implementation.
type Downloader interface {
	DownloadTo(context.Context, string, string, int64) (catalog.DownloadReceipt, error)
}

type Materialized struct {
	ComponentID       string
	Variant           string
	Artifact          catalog.Artifact
	WorkspacePath     string
	SourceProvenance  []catalog.SourceUnit
	VerifiedArchiveSHA string
}

type Options struct {
	Downloader Downloader
	CacheDir   string
}

// Materialize retrieves, verifies and extracts only artifacts already selected
// by Resolver. It performs no version selection and never falls back to source.
func Materialize(ctx context.Context, components []resolver.ResolvedComponent, workspaceRoot string, options Options) ([]Materialized, error) {
	if ctx == nil {
		return nil, fmt.Errorf("artifact: context is required")
	}
	if options.Downloader == nil {
		for _, component := range components {
			if component.Delivery == "artifact" {
				return nil, fmt.Errorf("artifact: HTTPS downloader is required")
			}
		}
	}
	if workspaceRoot == "" {
		return nil, fmt.Errorf("artifact: workspace root is required")
	}
	if err := ensureNoSymlinkAncestors(workspaceRoot); err != nil {
		return nil, fmt.Errorf("artifact: unsafe workspace root: %w", err)
	}
	var cacheRoot string
	if options.CacheDir != "" {
		cacheRoot = filepath.Join(options.CacheDir, "artifacts")
		if err := secureDirectory(cacheRoot); err != nil {
			return nil, fmt.Errorf("artifact: configure cache: %w", err)
		}
	}
	out := make([]Materialized, 0)
	seen := make(map[string]struct{})
	for _, component := range components {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if component.Delivery != "artifact" {
			continue
		}
		if component.Artifact == nil {
			return nil, fmt.Errorf("artifact: selected component %s/%s has no immutable artifact descriptor", component.ComponentID, component.Variant)
		}
		selected := *component.Artifact
		if selected.SizeBytes < 1 || selected.SizeBytes > catalog.MaxDeclaredArtifactBytes {
			return nil, fmt.Errorf("artifact: selected artifact %q has an invalid declared size", selected.ID)
		}
		relative := filepath.ToSlash(filepath.Join("components", component.ComponentID, component.Variant, "package"))
		key := component.ComponentID + "\x00" + component.Variant
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("artifact: duplicate selected package destination")
		}
		seen[key] = struct{}{}
		destination, err := safeDestination(workspaceRoot, relative)
		if err != nil {
			return nil, fmt.Errorf("artifact: unsafe package destination: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return nil, fmt.Errorf("artifact: create package parent: %w", err)
		}
		if err := ensureNoSymlinkAncestors(filepath.Dir(destination)); err != nil {
			return nil, fmt.Errorf("artifact: package parent changed unexpectedly: %w", err)
		}
		archivePath, cleanup, err := verifiedArchive(ctx, component, selected, cacheRoot, options.Downloader, filepath.Dir(destination))
		if err != nil {
			return nil, err
		}
		if err := extractArchive(archivePath, selected.Format, destination); err != nil {
			cleanup()
			_ = os.RemoveAll(destination)
			return nil, fmt.Errorf("artifact: extract %s/%s@%s: %w", component.ComponentID, component.Variant, component.Version, err)
		}
		cleanup()
		provenance := make([]catalog.SourceUnit, 0, len(selected.SourceUnitIDs))
		for _, sourceID := range selected.SourceUnitIDs {
			for _, source := range component.SourceUnits {
				if source.ID == sourceID {
					provenance = append(provenance, source)
					break
				}
			}
		}
		out = append(out, Materialized{
			ComponentID: component.ComponentID,
			Variant: component.Variant,
			Artifact: selected,
			WorkspacePath: relative,
			SourceProvenance: provenance,
			VerifiedArchiveSHA: strings.ToLower(selected.SHA256),
		})
	}
	return out, nil
}

func verifiedArchive(ctx context.Context, component resolver.ResolvedComponent, selected catalog.Artifact, cacheRoot string, downloader Downloader, tempRoot string) (string, func(), error) {
	if err := ctx.Err(); err != nil {
		return "", func() {}, err
	}
	expectedSHA := strings.ToLower(selected.SHA256)
	cacheKeyInput := strings.Join([]string{
		strings.ToLower(component.ReleaseMetadataSHA),
		selected.ID,
		selected.Target.OS,
		selected.Target.Architecture,
		selected.Target.ABI,
		expectedSHA,
	}, "\x00")
	sum := sha256.Sum256([]byte(cacheKeyInput))
	cachePath := ""
	if cacheRoot != "" {
		cachePath = filepath.Join(cacheRoot, hex.EncodeToString(sum[:])+".archive")
		if ok, err := verifyFile(cachePath, selected.SizeBytes, expectedSHA); err == nil {
			if ok {
				return cachePath, func() {}, nil
			}
			// A present but corrupt cache entry is never reused. Remove it
			// before a new atomic publication (Windows rename does not replace).
			if err := os.Remove(cachePath); err != nil && !os.IsNotExist(err) {
				return "", func() {}, fmt.Errorf("artifact: remove corrupt cache entry: %w", err)
			}
		} else if !os.IsNotExist(err) {
			if removeErr := os.Remove(cachePath); removeErr != nil && !os.IsNotExist(removeErr) {
				return "", func() {}, fmt.Errorf("artifact: remove invalid cache entry: %w", removeErr)
			}
		}
	}

	target := cachePath
	targetRoot := cacheRoot
	if target == "" {
		targetRoot = tempRoot
		if err := secureDirectory(targetRoot); err != nil {
			return "", func() {}, fmt.Errorf("artifact: prepare temporary download directory: %w", err)
		}
	}
	temp, err := os.CreateTemp(targetRoot, ".artifact-download-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("artifact: create download staging file: %w", err)
	}
	tempPath := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", func() {}, fmt.Errorf("artifact: close download staging file: %w", err)
	}
	if err := os.Remove(tempPath); err != nil {
		return "", func() {}, fmt.Errorf("artifact: prepare download staging file: %w", err)
	}
	receipt, err := downloader.DownloadTo(ctx, selected.URI, tempPath, selected.SizeBytes)
	if err != nil {
		_ = os.Remove(tempPath)
		return "", func() {}, fmt.Errorf("artifact: download %s/%s@%s: %w", component.ComponentID, component.Variant, component.Version, err)
	}
	if receipt.SizeBytes != selected.SizeBytes {
		_ = os.Remove(tempPath)
		return "", func() {}, fmt.Errorf("artifact: ARTIFACT_SIZE_MISMATCH for %s/%s@%s", component.ComponentID, component.Variant, component.Version)
	}
	if !strings.EqualFold(receipt.SHA256, expectedSHA) {
		_ = os.Remove(tempPath)
		return "", func() {}, fmt.Errorf("artifact: ARTIFACT_DIGEST_MISMATCH for %s/%s@%s", component.ComponentID, component.Variant, component.Version)
	}
	if err := ctx.Err(); err != nil {
		_ = os.Remove(tempPath)
		return "", func() {}, err
	}
	if cachePath == "" {
		return tempPath, func() { _ = os.Remove(tempPath) }, nil
	}
	if err := os.Rename(tempPath, cachePath); err != nil {
		if ok, verifyErr := verifyFile(cachePath, selected.SizeBytes, expectedSHA); verifyErr == nil && ok {
			_ = os.Remove(tempPath)
			return cachePath, func() {}, nil
		}
		_ = os.Remove(tempPath)
		return "", func() {}, fmt.Errorf("artifact: publish verified cache entry: %w", err)
	}
	if ok, err := verifyFile(cachePath, selected.SizeBytes, expectedSHA); err != nil || !ok {
		_ = os.Remove(cachePath)
		return "", func() {}, fmt.Errorf("artifact: cached package failed post-publication verification")
	}
	return cachePath, func() {}, nil
}

func verifyFile(filePath string, expectedSize int64, expectedSHA string) (bool, error) {
	info, err := os.Lstat(filePath)
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != expectedSize {
		return false, fmt.Errorf("cached artifact metadata differs")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return false, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := ioCopy(hash, file); err != nil {
		return false, err
	}
	return hex.EncodeToString(hash.Sum(nil)) == expectedSHA, nil
}

func ioCopy(destination io.Writer, source io.Reader) (int64, error) {
	return io.Copy(destination, source)
}

func secureDirectory(value string) error {
	if err := os.MkdirAll(value, 0o700); err != nil {
		return err
	}
	if err := ensureNoSymlinkAncestors(value); err != nil {
		return err
	}
	info, err := os.Lstat(value)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("cache path is not a real directory")
	}
	return os.Chmod(value, 0o700)
}
