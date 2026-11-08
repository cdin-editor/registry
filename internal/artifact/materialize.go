package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/cdin-editor/registry/internal/catalog"
)

// DigestFetcher is the minimal transport capability needed to materialize
// immutable package assets. Production uses the Registry HTTPS allow-list
// transport, not an arbitrary URL fetch implementation.
type DigestFetcher interface {
	FetchWithDigest(context.Context, string, string) ([]byte, error)
}

// Materialize retrieves an immutable package, verifies size and digest, then
// extracts it into an existing empty private staging directory.
func Materialize(ctx context.Context, fetcher DigestFetcher, descriptor catalog.Artifact, destination string, limits Limits) error {
	if ctx == nil { return fmt.Errorf("ARTIFACT_FETCH_FAILED: context is required") }
	if err := ctx.Err(); err != nil { return err }
	if fetcher == nil { return fmt.Errorf("ARTIFACT_FETCH_FAILED: digest-aware HTTPS fetcher is required") }
	if descriptor.SizeBytes <= 0 || descriptor.SizeBytes > limits.MaxCompressedBytes && limits.MaxCompressedBytes > 0 {
		return fmt.Errorf("ARTIFACT_LIMIT_EXCEEDED: declared artifact size exceeds configured compressed limit")
	}
	data, err := fetcher.FetchWithDigest(ctx, descriptor.URI, descriptor.SHA256)
	if err != nil { return fmt.Errorf("ARTIFACT_FETCH_FAILED: immutable asset retrieval failed: %w", err) }
	if err := ctx.Err(); err != nil { return err }
	if int64(len(data)) != descriptor.SizeBytes {
		return fmt.Errorf("ARTIFACT_SIZE_MISMATCH: downloaded byte count differs from release metadata")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != descriptor.SHA256 {
		return fmt.Errorf("ARTIFACT_DIGEST_MISMATCH: downloaded bytes differ from release metadata")
	}
	info, err := os.Lstat(destination)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("ARTIFACT_EXTRACTION_FAILED: destination staging directory is unsafe")
	}
	if err := Extract(data, descriptor.Format, destination, limits); err != nil { return err }
	return nil
}
