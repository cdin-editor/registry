package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/cdin-editor/registry/internal/catalog"
)

func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// DigestFetcher is the minimal transport capability needed to materialize
// immutable package assets. Production uses the Registry HTTPS allow-list
// transport, not an arbitrary URL fetch implementation.
type DigestFetcher interface {
	FetchWithDigest(context.Context, string, string) ([]byte, error)
}

// ReleaseArtifactFetcher supports the complete artifact cache identity. The
// metadata digest and descriptor are part of the cache key, not only the URL.
type ReleaseArtifactFetcher interface {
	FetchArtifact(context.Context, string, catalog.Artifact) ([]byte, error)
}

// Materialize retrieves an immutable package, verifies size and digest, then
// extracts it into an existing empty private staging directory.
func Materialize(ctx context.Context, fetcher DigestFetcher, releaseMetadataSHA string, descriptor catalog.Artifact, destination string, limits Limits) error {
	if ctx == nil {
		return fmt.Errorf("ARTIFACT_FETCH_FAILED: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if fetcher == nil {
		return fmt.Errorf("ARTIFACT_FETCH_FAILED: digest-aware HTTPS fetcher is required")
	}
	if !validDigest(releaseMetadataSHA) {
		return fmt.Errorf("ARTIFACT_ARCHIVE_REJECTED: release metadata digest is invalid")
	}
	maxCompressedBytes := limits.MaxCompressedBytes
	if maxCompressedBytes <= 0 {
		maxCompressedBytes = DefaultMaxCompressedBytes
	}
	if descriptor.SizeBytes <= 0 || descriptor.SizeBytes > maxCompressedBytes {
		return fmt.Errorf("ARTIFACT_LIMIT_EXCEEDED: declared artifact size exceeds configured compressed limit")
	}
	var data []byte
	var err error
	if releaseFetcher, ok := fetcher.(ReleaseArtifactFetcher); ok {
		data, err = releaseFetcher.FetchArtifact(ctx, releaseMetadataSHA, descriptor)
	} else {
		// Test transports and embedders that expose only the minimal digest
		// interface remain usable; production HTTPFetcher implements the
		// identity-aware extension above.
		data, err = fetcher.FetchWithDigest(ctx, descriptor.URI, descriptor.SHA256)
	}
	if err != nil {
		if strings.Contains(err.Error(), "ARTIFACT_") {
			return err
		}
		return fmt.Errorf("ARTIFACT_FETCH_FAILED: immutable asset retrieval failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if int64(len(data)) != descriptor.SizeBytes {
		return fmt.Errorf("ARTIFACT_SIZE_MISMATCH: downloaded byte count differs from release metadata")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != strings.ToLower(descriptor.SHA256) {
		return fmt.Errorf("ARTIFACT_DIGEST_MISMATCH: downloaded bytes differ from release metadata")
	}
	info, err := os.Lstat(destination)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("ARTIFACT_EXTRACTION_FAILED: destination staging directory is unsafe")
	}
	if err := ExtractContext(ctx, data, descriptor.Format, destination, limits); err != nil {
		return err
	}
	return nil
}
