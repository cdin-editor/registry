package collector

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cdin-editor/registry/internal/privatedir"
)

const (
	gitObjectCacheControlDir = ".registry-cache"
	gitObjectCacheBudget     = int64(1 << 30)
	gitObjectCacheMaxAge     = 90 * 24 * time.Hour
)

var ErrGitObjectCacheBusy = errors.New("collector: Git-object cache maintenance is already running")

// GitObjectCache stores complete, shallow Git object databases by immutable
// commit ID. Object IDs are global content identities, not repository names.
type GitObjectCache struct {
	root    string
	objects string
	control string
	pins    string
	leases  string
	locks   string
	gcLock  string
	budget  int64
	maxAge  time.Duration
}

// GitObjectCacheGCResult reports one complete Git-object cache collection pass.
type GitObjectCacheGCResult struct {
	RemovedObjects   int   `json:"removed_objects"`
	RemovedBytes     int64 `json:"removed_bytes"`
	RemainingBytes   int64 `json:"remaining_bytes"`
	ProtectedObjects int   `json:"protected_objects"`
}

type gitObjectCacheReference struct {
	Version int      `json:"version"`
	Keys    []string `json:"keys"`
}

type gitObjectCacheEntry struct {
	path    string
	oid     string
	size    int64
	modTime time.Time
}

// NewGitObjectCache opens a private content-addressed bare-repository cache.
// Every hit is verified by Git before any object is checked out or copied.
func NewGitObjectCache(directory string) (*GitObjectCache, error) {
	return newGitObjectCacheWithPolicy(directory, gitObjectCacheBudget, gitObjectCacheMaxAge)
}

func newGitObjectCacheWithPolicy(directory string, budget int64, maxAge time.Duration) (*GitObjectCache, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, fmt.Errorf("collector: Git-object cache directory is required")
	}
	if budget <= 0 || maxAge <= 0 {
		return nil, fmt.Errorf("collector: Git-object cache budget and age must be positive")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("collector: resolve Git-object cache directory: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("collector: create Git-object cache directory: %w", err)
	}
	root, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("collector: canonicalize Git-object cache directory: %w", err)
	}
	if err := ensureNoSymlinkAncestors(root); err != nil {
		return nil, fmt.Errorf("collector: unsafe Git-object cache path: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("collector: secure Git-object cache directory: %w", err)
	}
	objects := filepath.Join(root, "objects")
	control := filepath.Join(root, gitObjectCacheControlDir)
	pins := filepath.Join(control, "pins")
	leases := filepath.Join(control, "leases")
	locks := filepath.Join(control, "locks")
	for _, path := range []string{objects, filepath.Join(objects, "sha1"), filepath.Join(objects, "sha256"), control, pins, leases, locks} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return nil, fmt.Errorf("collector: initialize Git-object cache directory: %w", err)
		}
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("collector: Git-object cache control path is not a real directory")
		}
		if err := os.Chmod(path, 0o700); err != nil {
			return nil, fmt.Errorf("collector: secure Git-object cache directory: %w", err)
		}
	}
	if err := privatedir.SecureTree(root); err != nil {
		return nil, fmt.Errorf("collector: secure Git-object cache permissions: %w", err)
	}
	return &GitObjectCache{
		root: root, objects: objects, control: control, pins: pins,
		leases: leases, locks: locks, gcLock: filepath.Join(control, "gc.lock"),
		budget: budget, maxAge: maxAge,
	}, nil
}

// WithObject provides a verified bare object database for one full commit ID.
// The cache lock and lease remain held through use, preventing GC and concurrent
// checkouts from mutating or removing the shared bare repository.
func (c *GitObjectCache) withObject(
	ctx context.Context,
	oid string,
	verify func(string) error,
	populate func(string) error,
	use func(string) error,
) (cacheHit bool, err error) {
	if c == nil {
		return false, fmt.Errorf("collector: Git-object cache is not initialized")
	}
	if ctx == nil {
		return false, fmt.Errorf("collector: Git-object cache context is required")
	}
	if !isFullObjectID(oid) || strings.ToLower(oid) != oid {
		return false, fmt.Errorf("collector: Git-object cache requires a lowercase full object ID")
	}
	if verify == nil || populate == nil || use == nil {
		return false, fmt.Errorf("collector: Git-object cache verify, populate and use callbacks are required")
	}
	oid = strings.ToLower(oid)
	releaseLease, err := c.beginLease(ctx, []string{oid})
	if err != nil {
		return false, err
	}
	defer releaseLease()

	unlockObject, err := c.acquireObjectLock(ctx, oid)
	if err != nil {
		return false, err
	}
	defer unlockObject()

	path := c.objectPath(oid)
	exists, err := c.inspectObjectPath(path)
	if err != nil {
		return false, err
	}
	if exists {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		_, inventoryErr := gitObjectTreeSize(path)
		verifyErr := inventoryErr
		if verifyErr == nil {
			verifyErr = verify(path)
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if verifyErr != nil {
			// Corrupt objects are not reusable. Removal is safe only after the
			// cache entry has been proven to be a real directory under our root.
			if err := os.RemoveAll(path); err != nil {
				return false, fmt.Errorf("collector: discard corrupt Git-object cache entry: %w", err)
			}
			exists = false
		} else {
			_ = os.Chtimes(path, time.Now(), time.Now())
			if err := ctx.Err(); err != nil {
				return false, err
			}
			return true, use(path)
		}
	}
	if !exists {
		parent := filepath.Dir(path)
		staging, err := os.MkdirTemp(parent, ".stage-"+oid+"-")
		if err != nil {
			return false, fmt.Errorf("collector: create Git-object cache staging directory: %w", err)
		}
		defer os.RemoveAll(staging)
		if err := populate(staging); err != nil {
			return false, err
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if _, err := gitObjectTreeSize(staging); err != nil {
			return false, fmt.Errorf("collector: fetched Git-object database contains an unsafe filesystem entry: %w", err)
		}
		if err := verify(staging); err != nil {
			return false, fmt.Errorf("collector: fetched Git-object database failed integrity verification")
		}
		if err := os.Rename(staging, path); err != nil {
			return false, fmt.Errorf("collector: publish Git-object cache entry: %w", err)
		}
		if err := os.Chtimes(path, time.Now(), time.Now()); err != nil {
			return false, fmt.Errorf("collector: update Git-object cache recency: %w", err)
		}
		return false, use(path)
	}
	return false, fmt.Errorf("collector: Git-object cache state changed unexpectedly")
}

// RetainManifest pins every full source commit ID named by a verified Manifest.
// The caller verifies canonical bytes, schema and provenance before pinning.
func (c *GitObjectCache) RetainManifest(data []byte) (string, error) {
	if c == nil {
		return "", fmt.Errorf("collector: Git-object cache is not initialized")
	}
	keys, err := gitObjectIDsFromManifest(data)
	if err != nil {
		return "", err
	}
	id := gitManifestIdentity(data)
	unlock, err := c.acquireMaintenanceLock()
	if err != nil {
		return "", err
	}
	defer unlock()
	encoded, err := json.Marshal(gitObjectCacheReference{Version: 1, Keys: keys})
	if err != nil {
		return "", fmt.Errorf("collector: encode Git-object pin: %w", err)
	}
	encoded = append(encoded, '\n')
	path := filepath.Join(c.pins, id+".json")
	if existing, readErr := os.ReadFile(path); readErr == nil {
		if string(existing) != string(encoded) {
			return "", fmt.Errorf("collector: existing Git-object pin is inconsistent")
		}
		return id, nil
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return "", fmt.Errorf("collector: read existing Git-object pin: %w", readErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("collector: create Git-object pin: %w", err)
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("collector: write Git-object pin: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("collector: sync Git-object pin: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("collector: close Git-object pin: %w", err)
	}
	return id, nil
}

// ReleaseManifest drops a retained Manifest's Git commit references.
func (c *GitObjectCache) ReleaseManifest(manifestSHA256 string) error {
	if c == nil || !isFullSHA256(manifestSHA256) {
		return fmt.Errorf("collector: invalid retained Manifest SHA-256")
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
		return fmt.Errorf("collector: remove Git-object pin: %w", err)
	}
	return nil
}

// BeginReplayLease protects every source commit in a Manifest for the full
// duration of replay, including commits that have not yet been collected.
func (c *GitObjectCache) BeginReplayLease(ctx context.Context, manifestBytes []byte) (func(), error) {
	keys, err := gitObjectIDsFromManifest(manifestBytes)
	if err != nil {
		return nil, err
	}
	return c.beginLease(ctx, keys)
}

// GarbageCollect evicts only unpinned, unleased bare repositories (REG-018/026).
// It validates the protection snapshot and object inventory before deletion;
// malformed or incomplete state fails closed.
// Spec: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#6-source-collection-and-workspace
// Trust boundary: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#8-security-and-trust-boundary
// Design: https://github.com/cdin-editor/registry/blob/main/docs/implementation/collector.md.
func (c *GitObjectCache) GarbageCollect(ctx context.Context) (GitObjectCacheGCResult, error) {
	if c == nil {
		return GitObjectCacheGCResult{}, fmt.Errorf("collector: Git-object cache is not initialized")
	}
	if ctx == nil {
		return GitObjectCacheGCResult{}, fmt.Errorf("collector: Git-object cache GC context is required")
	}
	if err := ctx.Err(); err != nil {
		return GitObjectCacheGCResult{}, err
	}
	unlock, err := c.acquireMaintenanceLock()
	if err != nil {
		return GitObjectCacheGCResult{}, err
	}
	defer unlock()

	protected, err := c.readProtectedSnapshot()
	if err != nil {
		return GitObjectCacheGCResult{}, fmt.Errorf("collector: Git-object reference snapshot is incomplete; no objects removed: %w", err)
	}
	entries, total, err := c.readObjectInventory()
	if err != nil {
		return GitObjectCacheGCResult{}, fmt.Errorf("collector: Git-object inventory is incomplete; no objects removed: %w", err)
	}
	type candidate struct {
		entry gitObjectCacheEntry
	}
	remove := make(map[string]candidate)
	now := time.Now()
	for _, entry := range entries {
		if _, ok := protected[entry.oid]; ok {
			continue
		}
		if now.Sub(entry.modTime) > c.maxAge {
			remove[entry.oid] = candidate{entry: entry}
			total -= entry.size
		}
	}
	if total > c.budget {
		ordered := make([]gitObjectCacheEntry, 0, len(entries))
		for _, entry := range entries {
			if _, ok := protected[entry.oid]; !ok {
				ordered = append(ordered, entry)
			}
		}
		sort.Slice(ordered, func(i, j int) bool {
			if ordered[i].modTime.Equal(ordered[j].modTime) {
				return ordered[i].oid < ordered[j].oid
			}
			return ordered[i].modTime.Before(ordered[j].modTime)
		})
		for _, entry := range ordered {
			if total <= c.budget {
				break
			}
			if _, already := remove[entry.oid]; already {
				continue
			}
			remove[entry.oid] = candidate{entry: entry}
			total -= entry.size
		}
	}
	result := GitObjectCacheGCResult{ProtectedObjects: len(protected)}
	for oid, item := range remove {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := os.RemoveAll(item.entry.path); err != nil {
			return result, fmt.Errorf("collector: remove Git-object cache entry %s: %w", oid, err)
		}
		result.RemovedObjects++
		result.RemovedBytes += item.entry.size
	}
	result.RemainingBytes = total
	return result, nil
}

func (c *GitObjectCache) objectPath(oid string) string {
	algorithm := "sha1"
	if len(oid) == 64 {
		algorithm = "sha256"
	}
	return filepath.Join(c.objects, algorithm, oid+".git")
}

func (c *GitObjectCache) inspectObjectPath(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("collector: inspect Git-object cache entry: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("collector: Git-object cache entry is not a real directory")
	}
	return true, nil
}

func (c *GitObjectCache) beginLease(ctx context.Context, keys []string) (func(), error) {
	if ctx == nil {
		return nil, fmt.Errorf("collector: Git-object cache lease context is required")
	}
	keys = uniqueGitObjectIDs(keys)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		busy, err := c.maintenanceLockPresent()
		if err != nil {
			return nil, err
		}
		if busy {
			if err := waitGitCacheMaintenance(ctx); err != nil {
				return nil, err
			}
			continue
		}
		file, err := os.CreateTemp(c.leases, "lease-*.json")
		if err != nil {
			return nil, fmt.Errorf("collector: create Git-object lease: %w", err)
		}
		name := file.Name()
		encoded, marshalErr := json.Marshal(gitObjectCacheReference{Version: 1, Keys: keys})
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
				return nil, fmt.Errorf("collector: write Git-object lease: %w", marshalErr)
			}
			return nil, fmt.Errorf("collector: close Git-object lease: %w", closeErr)
		}
		busy, err = c.maintenanceLockPresent()
		if err != nil {
			_ = os.Remove(name)
			return nil, err
		}
		if busy {
			_ = os.Remove(name)
			if err := waitGitCacheMaintenance(ctx); err != nil {
				return nil, err
			}
			continue
		}
		var once sync.Once
		return func() { once.Do(func() { _ = os.Remove(name) }) }, nil
	}
}

// gitObjectLockContention recognizes both the normal O_EXCL collision and
// Windows' Access Denied response when another process has just created the
// lock file. In the latter case we only treat the error as contention after
// Lstat proves that the path is an existing regular lock file; all other
// permission failures and unsafe filesystem entries still fail closed.
func gitObjectLockContention(path string, openErr error) (bool, error) {
	if errors.Is(openErr, os.ErrExist) {
		return true, nil
	}
	if !errors.Is(openErr, os.ErrPermission) {
		return false, nil
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		// The owner may have released the lock between OpenFile and Lstat.
		// Retry rather than reporting an unrelated permission error.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("collector: inspect Git-object lock after permission collision: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("collector: Git-object lock path is not a regular file")
	}
	return true, nil
}

func (c *GitObjectCache) acquireObjectLock(ctx context.Context, oid string) (func(), error) {
	path := filepath.Join(c.locks, oid+".lock")
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			if syncErr := file.Sync(); syncErr != nil {
				_ = file.Close()
				_ = os.Remove(path)
				return nil, fmt.Errorf("collector: sync Git-object lock: %w", syncErr)
			}
			if closeErr := file.Close(); closeErr != nil {
				_ = os.Remove(path)
				return nil, fmt.Errorf("collector: close Git-object lock: %w", closeErr)
			}
			return func() { _ = os.Remove(path) }, nil
		}
		busy, contentionErr := gitObjectLockContention(path, err)
		if contentionErr != nil {
			return nil, contentionErr
		}
		if !busy {
			return nil, fmt.Errorf("collector: acquire Git-object lock: %w", err)
		}
		if err := waitGitCacheMaintenance(ctx); err != nil {
			return nil, err
		}
	}
}

func (c *GitObjectCache) acquireMaintenanceLock() (func(), error) {
	file, err := os.OpenFile(c.gcLock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		busy, contentionErr := gitObjectLockContention(c.gcLock, err)
		if contentionErr != nil {
			return nil, contentionErr
		}
		if busy {
			return nil, ErrGitObjectCacheBusy
		}
		return nil, fmt.Errorf("collector: acquire Git-object cache maintenance lock: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(c.gcLock)
		return nil, fmt.Errorf("collector: sync Git-object cache maintenance lock: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(c.gcLock)
		return nil, fmt.Errorf("collector: close Git-object cache maintenance lock: %w", err)
	}
	return func() { _ = os.Remove(c.gcLock) }, nil
}

func (c *GitObjectCache) maintenanceLockPresent() (bool, error) {
	info, err := os.Lstat(c.gcLock)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return true, fmt.Errorf("collector: inspect Git-object maintenance lock: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return true, fmt.Errorf("collector: Git-object maintenance lock is unsafe")
	}
	return true, nil
}

func (c *GitObjectCache) readProtectedSnapshot() (map[string]struct{}, error) {
	protected := make(map[string]struct{})
	for _, directory := range []string{c.pins, c.leases} {
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("Git-object reference directory is missing or unsafe")
		}
		files, err := os.ReadDir(directory)
		if err != nil {
			return nil, err
		}
		for _, item := range files {
			if item.IsDir() || !strings.HasSuffix(item.Name(), ".json") {
				return nil, fmt.Errorf("unexpected Git-object reference entry")
			}
			path := filepath.Join(directory, item.Name())
			entryInfo, err := os.Lstat(path)
			if err != nil || !entryInfo.Mode().IsRegular() || entryInfo.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("Git-object reference is not a regular file")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			var reference gitObjectCacheReference
			if err := json.Unmarshal(data, &reference); err != nil || reference.Version != 1 {
				return nil, fmt.Errorf("Git-object reference document is malformed")
			}
			if !sort.StringsAreSorted(reference.Keys) {
				return nil, fmt.Errorf("Git-object reference keys are not sorted")
			}
			for i, key := range reference.Keys {
				if !isFullObjectID(key) || strings.ToLower(key) != key || i > 0 && reference.Keys[i-1] == key {
					return nil, fmt.Errorf("Git-object reference contains an invalid or duplicate key")
				}
				protected[key] = struct{}{}
			}
		}
	}
	return protected, nil
}

func (c *GitObjectCache) readObjectInventory() ([]gitObjectCacheEntry, int64, error) {
	var entries []gitObjectCacheEntry
	var total int64
	for _, algorithm := range []string{"sha1", "sha256"} {
		directory := filepath.Join(c.objects, algorithm)
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, 0, fmt.Errorf("Git-object inventory directory is missing or unsafe")
		}
		items, err := os.ReadDir(directory)
		if err != nil {
			return nil, 0, err
		}
		for _, item := range items {
			name := strings.TrimSuffix(item.Name(), ".git")
			if item.IsDir() == false || !strings.HasSuffix(item.Name(), ".git") ||
				!isFullObjectID(name) || strings.ToLower(name) != name ||
				(algorithm == "sha1" && len(name) != 40) || (algorithm == "sha256" && len(name) != 64) {
				return nil, 0, fmt.Errorf("unexpected Git-object cache entry")
			}
			path := filepath.Join(directory, item.Name())
			entryInfo, err := os.Lstat(path)
			if err != nil || !entryInfo.IsDir() || entryInfo.Mode()&os.ModeSymlink != 0 {
				return nil, 0, fmt.Errorf("Git-object cache entry is not a real directory")
			}
			size, err := gitObjectTreeSize(path)
			if err != nil {
				return nil, 0, err
			}
			entry := gitObjectCacheEntry{path: path, oid: name, size: size, modTime: entryInfo.ModTime()}
			entries = append(entries, entry)
			total += size
		}
	}
	return entries, total, nil
}

func gitObjectTreeSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !(info.IsDir() || info.Mode().IsRegular()) {
			relative, relErr := filepath.Rel(root, path)
			if relErr != nil {
				relative = "<unknown>"
			}
			return fmt.Errorf("Git-object cache entry %q has unsupported mode %s", filepath.ToSlash(relative), info.Mode().Type())
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

func gitObjectIDsFromManifest(data []byte) ([]string, error) {
	var document struct {
		Components []struct {
			SourceUnits []struct {
				Revision string `json:"revision"`
			} `json:"source_units"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("collector: decode Manifest Git-object references: %w", err)
	}
	keys := make([]string, 0)
	for _, component := range document.Components {
		for _, source := range component.SourceUnits {
			oid := strings.ToLower(source.Revision)
			if !isFullObjectID(oid) {
				return nil, fmt.Errorf("collector: Manifest contains an invalid full Git object ID")
			}
			keys = append(keys, oid)
		}
	}
	return uniqueGitObjectIDs(keys), nil
}

func uniqueGitObjectIDs(keys []string) []string {
	set := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		set[strings.ToLower(key)] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for key := range set {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func gitManifestIdentity(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func gitObjectHash(oid string, object []byte) string {
	header := []byte("commit " + strconv.Itoa(len(object)) + "\x00")
	payload := make([]byte, 0, len(header)+len(object))
	payload = append(payload, header...)
	payload = append(payload, object...)
	if len(oid) == 64 {
		sum := sha256.Sum256(payload)
		return hex.EncodeToString(sum[:])
	}
	sum := sha1.Sum(payload)
	return hex.EncodeToString(sum[:])
}

func isFullSHA256(value string) bool {
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

func waitGitCacheMaintenance(ctx context.Context) error {
	timer := time.NewTimer(10 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
