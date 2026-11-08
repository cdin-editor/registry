package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cdin-editor/registry/internal/privatedir"
)

const (
	metadataCacheControlDir = ".registry-cache"
	metadataCacheBudget     = int64(256 << 20)
	metadataCacheMaxAge     = 90 * 24 * time.Hour
)

var ErrCacheMaintenanceBusy = errors.New("catalog: metadata cache maintenance is already running")

// MetadataCache manages the lifecycle of digest-addressed metadata entries.
// Cache bytes remain an optimization: every caller must still verify the
// expected digest before using an entry.
type MetadataCache struct {
	root    string
	control string
	pins    string
	leases  string
	lock    string
	budget  int64
	maxAge  time.Duration
}

type metadataCacheReference struct {
	Version int      `json:"version"`
	Keys    []string `json:"keys"`
}

// MetadataCacheGCResult reports the effect of one complete GC pass.
type MetadataCacheGCResult struct {
	RemovedEntries   int   `json:"removed_entries"`
	RemovedBytes     int64 `json:"removed_bytes"`
	RemainingBytes   int64 `json:"remaining_bytes"`
	ProtectedEntries int   `json:"protected_entries"`
}

// NewMetadataCache opens a private cache directory and initializes the
// reference/lease control plane. The top-level *.json entries retain the
// historical URI+expected-digest filename format.
func NewMetadataCache(directory string) (*MetadataCache, error) {
	return newMetadataCacheWithPolicy(directory, metadataCacheBudget, metadataCacheMaxAge)
}

func newMetadataCacheWithPolicy(directory string, budget int64, maxAge time.Duration) (*MetadataCache, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, fmt.Errorf("catalog: metadata cache directory is required")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("catalog: resolve metadata cache directory: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("catalog: create metadata cache directory: %w", err)
	}
	root, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("catalog: canonicalize metadata cache directory: %w", err)
	}
	if err := rejectSymlinkAncestors(root); err != nil {
		return nil, fmt.Errorf("catalog: unsafe metadata cache path: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("catalog: secure metadata cache directory: %w", err)
	}
	control := filepath.Join(root, metadataCacheControlDir)
	pins := filepath.Join(control, "pins")
	leases := filepath.Join(control, "leases")
	for _, path := range []string{control, pins, leases} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return nil, fmt.Errorf("catalog: initialize metadata cache control directory: %w", err)
		}
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("catalog: metadata cache control path is not a real directory")
		}
		if err := os.Chmod(path, 0o700); err != nil {
			return nil, fmt.Errorf("catalog: secure metadata cache control directory: %w", err)
		}
	}
	if err := privatedir.SecureTree(root); err != nil {
		return nil, fmt.Errorf("catalog: secure metadata cache permissions: %w", err)
	}
	if budget <= 0 || maxAge <= 0 {
		return nil, fmt.Errorf("catalog: metadata cache GC budget and age must be positive")
	}
	return &MetadataCache{
		root: root, control: control, pins: pins, leases: leases,
		lock: filepath.Join(control, "gc.lock"), budget: budget, maxAge: maxAge,
	}, nil
}

// RetainManifest pins every metadata identity recorded by a verified Manifest.
// The returned ID is SHA-256 of the exact Manifest bytes and is stable across
// machines. Callers must verify the Manifest before invoking this method.
func (c *MetadataCache) RetainManifest(data []byte) (string, error) {
	if c == nil {
		return "", fmt.Errorf("catalog: metadata cache is not initialized")
	}
	keys, err := metadataKeysFromManifest(data)
	if err != nil {
		return "", err
	}
	id := sha256Hex(data)
	unlock, err := c.acquireMaintenanceLock()
	if err != nil {
		return "", err
	}
	defer unlock()
	reference := metadataCacheReference{Version: 1, Keys: keys}
	encoded, err := json.Marshal(reference)
	if err != nil {
		return "", fmt.Errorf("catalog: encode metadata pin: %w", err)
	}
	encoded = append(encoded, '\n')
	path := filepath.Join(c.pins, id+".json")
	if existing, readErr := os.ReadFile(path); readErr == nil {
		if string(existing) != string(encoded) {
			return "", fmt.Errorf("catalog: existing metadata pin is inconsistent")
		}
		return id, nil
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return "", fmt.Errorf("catalog: read existing metadata pin: %w", readErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("catalog: create metadata pin: %w", err)
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("catalog: write metadata pin: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("catalog: sync metadata pin: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("catalog: close metadata pin: %w", err)
	}
	return id, nil
}

// ReleaseManifest removes a previously retained Manifest pin. The SHA-256
// identity is required so a caller cannot accidentally remove another pin.
func (c *MetadataCache) ReleaseManifest(manifestSHA256 string) error {
	if c == nil || !isSHA256(manifestSHA256) {
		return fmt.Errorf("catalog: invalid retained Manifest SHA-256")
	}
	unlock, err := c.acquireMaintenanceLock()
	if err != nil {
		return err
	}
	defer unlock()
	err = os.Remove(filepath.Join(c.pins, manifestSHA256+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("catalog: remove metadata pin: %w", err)
	}
	return nil
}

// BeginReplayLease protects all metadata identities in a Manifest for the
// duration of a replay. The returned release function is idempotent.
func (c *MetadataCache) BeginReplayLease(ctx context.Context, manifestBytes []byte) (func(), error) {
	keys, err := metadataKeysFromManifest(manifestBytes)
	if err != nil {
		return nil, err
	}
	return c.beginLease(ctx, keys)
}

func (c *MetadataCache) beginEntryLease(ctx context.Context, metadataURI, expectedSHA string) (func(), error) {
	if metadataURI == "" || !isSHA256(expectedSHA) {
		return nil, fmt.Errorf("catalog: invalid metadata cache lease identity")
	}
	return c.beginKeyLease(ctx, metadataCacheKey(metadataURI, expectedSHA))
}

// beginKeyLease also protects domain-separated artifact identities. The caller
// must derive the key from a verified Manifest or a fully validated descriptor.
func (c *MetadataCache) beginKeyLease(ctx context.Context, key string) (func(), error) {
	if !isSHA256(key) {
		return nil, fmt.Errorf("catalog: invalid cache lease key")
	}
	return c.beginLease(ctx, []string{key})
}

func (c *MetadataCache) beginLease(ctx context.Context, keys []string) (func(), error) {
	if c == nil {
		return nil, fmt.Errorf("catalog: metadata cache is not initialized")
	}
	if ctx == nil {
		return nil, fmt.Errorf("catalog: metadata cache lease context is required")
	}
	keys = uniqueSortedKeys(keys)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		busy, err := c.maintenanceLockPresent()
		if err != nil {
			return nil, err
		}
		if busy {
			if err := waitForCacheMaintenance(ctx); err != nil {
				return nil, err
			}
			continue
		}
		file, err := os.CreateTemp(c.leases, "lease-*.json")
		if err != nil {
			return nil, fmt.Errorf("catalog: create metadata cache lease: %w", err)
		}
		name := file.Name()
		reference := metadataCacheReference{Version: 1, Keys: keys}
		encoded, marshalErr := json.Marshal(reference)
		if marshalErr == nil {
			encoded = append(encoded, '\n')
			_, marshalErr = file.Write(encoded)
		}
		if marshalErr == nil {
			marshalErr = file.Sync()
		}
		closeErr := file.Close()
		if marshalErr != nil || closeErr != nil {
			_ = os.Remove(name)
			if marshalErr != nil {
				return nil, fmt.Errorf("catalog: write metadata cache lease: %w", marshalErr)
			}
			return nil, fmt.Errorf("catalog: close metadata cache lease: %w", closeErr)
		}
		// Double-check after publishing the lease. If GC won the race before
		// the lease became visible, discard it and retry after maintenance.
		busy, err = c.maintenanceLockPresent()
		if err != nil {
			_ = os.Remove(name)
			return nil, err
		}
		if busy {
			_ = os.Remove(name)
			if err := waitForCacheMaintenance(ctx); err != nil {
				return nil, err
			}
			continue
		}
		var once sync.Once
		return func() {
			once.Do(func() { _ = os.Remove(name) })
		}, nil
	}
}

// GarbageCollect removes only unpinned, unleased metadata identities (REG-019/026).
// It validates the complete reference snapshot before deleting anything;
// an unreadable, malformed or unsafe snapshot fails closed with zero removals.
// Spec: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#8-security-and-trust-boundary
// Design: docs/implementation/catalog-transport.md#metadata-cache-retention-and-eviction-policy.
func (c *MetadataCache) GarbageCollect(ctx context.Context) (MetadataCacheGCResult, error) {
	if c == nil {
		return MetadataCacheGCResult{}, fmt.Errorf("catalog: metadata cache is not initialized")
	}
	if ctx == nil {
		return MetadataCacheGCResult{}, fmt.Errorf("catalog: metadata cache GC context is required")
	}
	if err := ctx.Err(); err != nil {
		return MetadataCacheGCResult{}, err
	}
	unlock, err := c.acquireMaintenanceLock()
	if err != nil {
		return MetadataCacheGCResult{}, err
	}
	defer unlock()

	protected, err := c.readProtectedSnapshot()
	if err != nil {
		return MetadataCacheGCResult{}, fmt.Errorf("catalog: metadata cache reference snapshot is incomplete; no entries removed: %w", err)
	}
	entries, total, err := c.readCacheEntries()
	if err != nil {
		return MetadataCacheGCResult{}, fmt.Errorf("catalog: metadata cache inventory is incomplete; no entries removed: %w", err)
	}

	type candidate struct {
		path string
		key  string
		size int64
		time time.Time
	}
	removal := make(map[string]candidate)
	now := time.Now()
	for _, entry := range entries {
		if _, ok := protected[entry.key]; ok {
			continue
		}
		if now.Sub(entry.modTime) > c.maxAge {
			removal[entry.path] = candidate{path: entry.path, key: entry.key, size: entry.size, time: entry.modTime}
		}
	}
	for _, item := range removal {
		total -= item.size
	}
	if total > c.budget {
		ordered := make([]candidate, 0, len(entries))
		for _, entry := range entries {
			if _, ok := protected[entry.key]; ok {
				continue
			}
			ordered = append(ordered, candidate{path: entry.path, key: entry.key, size: entry.size, time: entry.modTime})
		}
		sort.Slice(ordered, func(i, j int) bool {
			if ordered[i].time.Equal(ordered[j].time) {
				return ordered[i].key < ordered[j].key
			}
			return ordered[i].time.Before(ordered[j].time)
		})
		for _, item := range ordered {
			if total <= c.budget {
				break
			}
			if _, exists := removal[item.path]; exists {
				continue
			}
			removal[item.path] = item
			total -= item.size
		}
	}
	result := MetadataCacheGCResult{ProtectedEntries: len(protected)}
	for _, item := range removal {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := os.Remove(item.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return result, fmt.Errorf("catalog: remove metadata cache entry: %w", err)
		}
		result.RemovedEntries++
		result.RemovedBytes += item.size
	}
	result.RemainingBytes = total
	return result, nil
}

type metadataCacheEntry struct {
	path    string
	key     string
	size    int64
	modTime time.Time
}

func (c *MetadataCache) readProtectedSnapshot() (map[string]struct{}, error) {
	protected := make(map[string]struct{})
	for _, directory := range []string{c.pins, c.leases} {
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("reference directory is missing or unsafe")
		}
		files, err := os.ReadDir(directory)
		if err != nil {
			return nil, err
		}
		for _, item := range files {
			if item.IsDir() || !strings.HasSuffix(item.Name(), ".json") {
				return nil, fmt.Errorf("unexpected reference entry")
			}
			path := filepath.Join(directory, item.Name())
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("reference is not a regular file")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			var reference metadataCacheReference
			if err := json.Unmarshal(data, &reference); err != nil || reference.Version != 1 {
				return nil, fmt.Errorf("reference document is malformed")
			}
			if !sort.StringsAreSorted(reference.Keys) {
				return nil, fmt.Errorf("reference keys are not sorted")
			}
			for i, key := range reference.Keys {
				if !isSHA256(key) || i > 0 && reference.Keys[i-1] == key {
					return nil, fmt.Errorf("reference contains an invalid or duplicate key")
				}
				protected[key] = struct{}{}
			}
		}
	}
	return protected, nil
}

func (c *MetadataCache) readCacheEntries() ([]metadataCacheEntry, int64, error) {
	items, err := os.ReadDir(c.root)
	if err != nil {
		return nil, 0, err
	}
	entries := make([]metadataCacheEntry, 0, len(items))
	var total int64
	for _, item := range items {
		if !strings.HasSuffix(item.Name(), ".json") {
			continue
		}
		key := strings.TrimSuffix(item.Name(), ".json")
		if !isSHA256(key) {
			return nil, 0, fmt.Errorf("unexpected JSON file in cache root")
		}
		path := filepath.Join(c.root, item.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, 0, fmt.Errorf("cache entry is not a regular file")
		}
		entry := metadataCacheEntry{path: path, key: key, size: info.Size(), modTime: info.ModTime()}
		entries = append(entries, entry)
		total += entry.size
	}
	return entries, total, nil
}

func (c *MetadataCache) acquireMaintenanceLock() (func(), error) {
	file, err := os.OpenFile(c.lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, ErrCacheMaintenanceBusy
		}
		return nil, fmt.Errorf("catalog: acquire metadata cache maintenance lock: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(c.lock)
		return nil, fmt.Errorf("catalog: sync metadata cache maintenance lock: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(c.lock)
		return nil, fmt.Errorf("catalog: close metadata cache maintenance lock: %w", err)
	}
	return func() { _ = os.Remove(c.lock) }, nil
}

func (c *MetadataCache) maintenanceLockPresent() (bool, error) {
	info, err := os.Lstat(c.lock)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return true, fmt.Errorf("catalog: inspect metadata cache maintenance lock: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return true, fmt.Errorf("catalog: metadata cache maintenance lock is unsafe")
	}
	return true, nil
}

func waitForCacheMaintenance(ctx context.Context) error {
	timer := time.NewTimer(10 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func metadataKeysFromManifest(data []byte) ([]string, error) {
	var document struct {
		Catalogs []struct {
			MetadataURI string `json:"metadata_uri"`
			MetadataSHA string `json:"metadata_sha256"`
		} `json:"catalogs"`
		Components []struct {
			ReleaseMetadataURI string `json:"release_metadata_uri"`
			ReleaseMetadataSHA string `json:"release_metadata_sha256"`
			SelectedArtifact   *struct {
				ID     string `json:"artifact_id"`
				Target Target `json:"target"`
				URI    string `json:"uri"`
				SHA256 string `json:"sha256"`
			} `json:"selected_artifact"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("catalog: decode Manifest metadata references: %w", err)
	}
	keys := make([]string, 0, len(document.Catalogs)+2*len(document.Components))
	for _, item := range document.Catalogs {
		key, err := metadataReferenceKey(item.MetadataURI, item.MetadataSHA)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	for _, item := range document.Components {
		key, err := metadataReferenceKey(item.ReleaseMetadataURI, item.ReleaseMetadataSHA)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
		if item.SelectedArtifact != nil {
			artifactKey, artifactErr := ArtifactCacheKey(item.ReleaseMetadataSHA, Artifact{
				ID:     item.SelectedArtifact.ID,
				Target: item.SelectedArtifact.Target,
				URI:    item.SelectedArtifact.URI,
				SHA256: item.SelectedArtifact.SHA256,
			})
			if artifactErr != nil {
				return nil, fmt.Errorf("catalog: Manifest contains an invalid artifact cache identity")
			}
			keys = append(keys, artifactKey)
		}
	}
	return uniqueSortedKeys(keys), nil
}

func metadataReferenceKey(uri, digest string) (string, error) {
	digest = strings.ToLower(digest)
	if strings.TrimSpace(uri) == "" || !isSHA256(digest) {
		return "", fmt.Errorf("catalog: Manifest contains an invalid metadata cache identity")
	}
	return metadataCacheKey(uri, digest), nil
}

func metadataCacheKey(uri, expectedSHA string) string {
	sum := sha256.Sum256([]byte(uri + "\x00" + strings.ToLower(expectedSHA)))
	return hex.EncodeToString(sum[:])
}

func uniqueSortedKeys(keys []string) []string {
	set := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		set[key] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for key := range set {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
