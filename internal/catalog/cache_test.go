package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func cacheManifestJSON(catalogURI, catalogSHA, releaseURI, releaseSHA string) []byte {
	document := struct {
		Catalogs []struct {
			MetadataURI string `json:"metadata_uri"`
			MetadataSHA string `json:"metadata_sha256"`
		} `json:"catalogs"`
		Components []struct {
			ReleaseURI string `json:"release_metadata_uri"`
			ReleaseSHA string `json:"release_metadata_sha256"`
		} `json:"components"`
	}{}
	if catalogURI != "" {
		item := struct {
			MetadataURI string `json:"metadata_uri"`
			MetadataSHA string `json:"metadata_sha256"`
		}{catalogURI, catalogSHA}
		document.Catalogs = append(document.Catalogs, item)
	}
	if releaseURI != "" {
		item := struct {
			ReleaseURI string `json:"release_metadata_uri"`
			ReleaseSHA string `json:"release_metadata_sha256"`
		}{releaseURI, releaseSHA}
		document.Components = append(document.Components, item)
	}
	encoded, _ := json.Marshal(document)
	return encoded
}

func writeCacheFixture(t *testing.T, cache *MetadataCache, uri, digest, body string, modTime time.Time) string {
	t.Helper()
	path := filepath.Join(cache.root, metadataCacheKey(uri, digest)+".json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMetadataCacheGCPreservesManifestPinsAndActiveReplayLeases(t *testing.T) {
	cache, err := newMetadataCacheWithPolicy(t.TempDir(), 1<<20, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	shaA := expectedDigest("metadata-a")
	shaB := expectedDigest("metadata-b")
	shaC := expectedDigest("metadata-c")
	pathA := writeCacheFixture(t, cache, "https://catalog.example.invalid/a.json", shaA, "a", old)
	pathB := writeCacheFixture(t, cache, "https://catalog.example.invalid/b.json", shaB, "b", old)
	pathC := writeCacheFixture(t, cache, "https://catalog.example.invalid/c.json", shaC, "c", old)

	pinnedManifest := cacheManifestJSON("https://catalog.example.invalid/a.json", shaA, "", "")
	pinID, err := cache.RetainManifest(pinnedManifest)
	if err != nil {
		t.Fatal(err)
	}
	leaseManifest := cacheManifestJSON("", "", "https://catalog.example.invalid/b.json", shaB)
	release, err := cache.BeginReplayLease(context.Background(), leaseManifest)
	if err != nil {
		t.Fatal(err)
	}
	result, err := cache.GarbageCollect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedEntries != 1 {
		t.Fatalf("removed entries = %d, want only the unprotected entry", result.RemovedEntries)
	}
	for name, path := range map[string]string{"pinned": pathA, "leased": pathB} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s cache entry was removed: %v", name, err)
		}
	}
	if _, err := os.Stat(pathC); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unprotected stale entry still exists: %v", err)
	}

	release()
	if err := cache.ReleaseManifest(pinID); err != nil {
		t.Fatal(err)
	}
	result, err = cache.GarbageCollect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedEntries != 2 {
		t.Fatalf("second GC removed %d entries, want 2", result.RemovedEntries)
	}
}

func TestMetadataCacheGCBudgetEvictsLeastRecentlyVerifiedUnpinnedEntries(t *testing.T) {
	cache, err := newMetadataCacheWithPolicy(t.TempDir(), 5, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	shaA, shaB, shaC := expectedDigest("a"), expectedDigest("b"), expectedDigest("c")
	pathA := writeCacheFixture(t, cache, "https://catalog.example.invalid/a", shaA, "aaaa", now.Add(-3*time.Hour))
	pathB := writeCacheFixture(t, cache, "https://catalog.example.invalid/b", shaB, "bbbb", now.Add(-2*time.Hour))
	pathC := writeCacheFixture(t, cache, "https://catalog.example.invalid/c", shaC, "cccc", now.Add(-time.Hour))
	result, err := cache.GarbageCollect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedEntries != 2 || result.RemainingBytes != 4 {
		t.Fatalf("GC result = %+v, want two entries removed and four bytes retained", result)
	}
	if _, err := os.Stat(pathA); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest entry was not evicted: %v", err)
	}
	if _, err := os.Stat(pathB); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second-oldest entry was not evicted: %v", err)
	}
	if _, err := os.Stat(pathC); err != nil {
		t.Fatalf("newest entry should remain: %v", err)
	}
}

func TestMetadataCacheGCFailsClosedOnIncompleteReferenceSnapshot(t *testing.T) {
	cache, err := newMetadataCacheWithPolicy(t.TempDir(), 1, time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	path := writeCacheFixture(t, cache, "https://catalog.example.invalid/a", expectedDigest("a"), "data", time.Now().Add(-time.Hour))
	if err := os.WriteFile(filepath.Join(cache.pins, "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := cache.GarbageCollect(context.Background())
	if err == nil {
		t.Fatal("GC unexpectedly accepted a malformed pin snapshot")
	}
	if result.RemovedEntries != 0 {
		t.Fatalf("GC removed %d entries after snapshot validation failed", result.RemovedEntries)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cache entry was removed despite fail-closed GC: %v", err)
	}
}

func TestMetadataCacheGCRejectsConcurrentMaintenance(t *testing.T) {
	cache, err := NewMetadataCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(cache.lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_ = lock.Close()
	if _, err := cache.GarbageCollect(context.Background()); !errors.Is(err, ErrCacheMaintenanceBusy) {
		t.Fatalf("GC error = %v, want ErrCacheMaintenanceBusy", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := cache.BeginReplayLease(ctx, cacheManifestJSON("", "", "", "")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lease error = %v, want context deadline", err)
	}
}

func TestMetadataCachePinIdentityUsesExactManifestBytesAndStableMetadataKeys(t *testing.T) {
	cache, err := NewMetadataCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := cacheManifestJSON("https://catalog.example.invalid/index.json", expectedDigest("index"), "", "")
	first, err := cache.RetainManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cache.RetainManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if first != sha256Hex(data) || second != first {
		t.Fatalf("pin IDs = %q and %q, want exact Manifest SHA-256", first, second)
	}
	if metadataCacheKey("https://catalog.example.invalid/a", expectedDigest("a")) == metadataCacheKey("https://catalog.example.invalid/a", expectedDigest("b")) {
		t.Fatal("cache identity did not include the expected digest")
	}
	if metadataCacheKey("https://catalog.example.invalid/a", expectedDigest("a")) == metadataCacheKey("https://catalog.example.invalid/b", expectedDigest("a")) {
		t.Fatal("cache identity did not include the exact URI")
	}
}

func TestMetadataCachePinAndGCUseFullArtifactIdentity(t *testing.T) {
	cache, err := newMetadataCacheWithPolicy(t.TempDir(), 1<<20, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	releaseURI := "https://catalog.example.invalid/releases/runtime.json"
	releaseSHA := expectedDigest("runtime-release-metadata")
	payload := "immutable runtime package"
	descriptor := validRuntimeArtifact()
	descriptor.URI = "https://catalog.example.invalid/releases/runtime.tar.gz"
	descriptor.SHA256 = expectedDigest(payload)
	descriptor.SizeBytes = int64(len(payload))
	artifactKey, err := ArtifactCacheKey(releaseSHA, descriptor)
	if err != nil {
		t.Fatal(err)
	}

	manifestBytes, err := json.Marshal(struct {
		SchemaVersion string `json:"schema_version"`
		Catalogs      []any  `json:"catalogs"`
		Components    []struct {
			ReleaseMetadataURI string   `json:"release_metadata_uri"`
			ReleaseMetadataSHA string   `json:"release_metadata_sha256"`
			SelectedArtifact   Artifact `json:"selected_artifact"`
		} `json:"components"`
	}{
		SchemaVersion: "1.1.0",
		Components: []struct {
			ReleaseMetadataURI string   `json:"release_metadata_uri"`
			ReleaseMetadataSHA string   `json:"release_metadata_sha256"`
			SelectedArtifact   Artifact `json:"selected_artifact"`
		}{{
			ReleaseMetadataURI: releaseURI,
			ReleaseMetadataSHA: releaseSHA,
			SelectedArtifact:   descriptor,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	old := time.Now().Add(-2 * time.Hour)
	releasePath := writeCacheFixture(t, cache, releaseURI, releaseSHA, "metadata", old)
	artifactPath := filepath.Join(cache.root, artifactKey+".json")
	if err := os.WriteFile(artifactPath, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(artifactPath, old, old); err != nil {
		t.Fatal(err)
	}
	unpinnedPath := writeCacheFixture(t, cache, "https://catalog.example.invalid/unpinned.json", expectedDigest("unpinned"), "other", old)

	pinID, err := cache.RetainManifest(manifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := metadataKeysFromManifest(manifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !containsKey(keys, metadataCacheKey(releaseURI, releaseSHA)) || !containsKey(keys, artifactKey) {
		t.Fatalf("manifest cache keys = %v; expected release metadata and full artifact keys", keys)
	}

	result, err := cache.GarbageCollect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(releasePath); err != nil {
		t.Fatalf("pinned release metadata was removed: %v", err)
	}
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("pinned artifact was removed: %v", err)
	}
	if _, err := os.Stat(unpinnedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unpinned stale cache entry remains: %v (GC %+v)", err, result)
	}

	if err := cache.ReleaseManifest(pinID); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.GarbageCollect(context.Background()); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"release": releasePath, "artifact": artifactPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unpinned %s cache entry remains after GC: %v", name, err)
		}
	}
}

func containsKey(keys []string, expected string) bool {
	for _, key := range keys {
		if key == expected {
			return true
		}
	}
	return false
}

func TestMetadataCacheManifestPinsExactADR0028ReleaseAssetIdentity(t *testing.T) {
	cache, err := newMetadataCacheWithPolicy(t.TempDir(), 1<<20, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	const repositoryURI = "https://github.com/acme/engine"
	const metadataURI = "https://github.com/acme/engine/releases/download/v1.2.3/engine-1.2.3.json"
	discovery := ReleaseDiscoveryProvenance{
		SourceID: "engine-source", Provider: githubReleaseProvider,
		RepositoryURI: repositoryURI, ReleaseID: 123,
		ReleaseAPIURI: "https://api.github.com/repos/acme/engine/releases/123",
		Tag:               "v1.2.3",
		TagCommit:         strings.Repeat("a", 40),
		MetadataAssetName: "engine-1.2.3.json",
		MetadataAssetSize: 456,
		PublishedAt: "2026-11-10T18:00:00Z",
	}
	metadataSHA := expectedDigest("immutable-release-metadata")
	assetKey, err := GitHubReleaseAssetCacheKey(discovery, metadataURI, metadataSHA)
	if err != nil { t.Fatal(err) }
	manifestBytes, err := json.Marshal(struct {
		SchemaVersion string `json:"schema_version"`
		Catalogs []any `json:"catalogs"`
		Components []struct {
				ComponentID        string                      `json:"component_id"`
			Variant            string                      `json:"variant"`
			Version            string                      `json:"version"`
			ReleaseMetadataURI string                      `json:"release_metadata_uri"`
			ReleaseMetadataSHA string                      `json:"release_metadata_sha256"`
			ReleaseDiscovery   ReleaseDiscoveryProvenance   `json:"release_discovery"`
		} `json:"components"`
	}{
		SchemaVersion: "1.2.0",
		Components: []struct {
			ComponentID string `json:"component_id"`
			Variant string `json:"variant"`
			Version string `json:"version"`
			ReleaseMetadataURI string `json:"release_metadata_uri"`
			ReleaseMetadataSHA string `json:"release_metadata_sha256"`
			ReleaseDiscovery ReleaseDiscoveryProvenance `json:"release_discovery"`
		}{{
			ComponentID: "engine", Variant: "default", Version: "1.2.3",
			ReleaseMetadataURI: metadataURI, ReleaseMetadataSHA: metadataSHA,
			ReleaseDiscovery: discovery,
		}},
	})
	if err != nil { t.Fatal(err) }
	keys, err := metadataKeysFromManifest(manifestBytes)
	if err != nil { t.Fatal(err) }
	if !containsKey(keys, metadataCacheKey(metadataURI, metadataSHA)) || !containsKey(keys, assetKey) {
		t.Fatalf("Manifest cache pins = %v; want URI/digest and ADR-0028 identity keys", keys)
	}
	old := time.Now().Add(-2 * time.Hour)
	metadataPath := writeCacheFixture(t, cache, metadataURI, metadataSHA, "metadata", old)
	assetPath := filepath.Join(cache.root, assetKey+".json")
	if err := os.WriteFile(assetPath, []byte("immutable asset bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(assetPath, old, old); err != nil {
		t.Fatal(err)
	}
	pinID, err := cache.RetainManifest(manifestBytes)
	if err != nil { t.Fatal(err) }
	result, err := cache.GarbageCollect(context.Background())
	if err != nil { t.Fatal(err) }
	for name, path := range map[string]string{"metadata": metadataPath, "identity-bound asset": assetPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s cache entry was removed despite Manifest pin: %v (GC %+v)", name, err, result)
		}
	}
	if err := cache.ReleaseManifest(pinID); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.GarbageCollect(context.Background()); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"metadata": metadataPath, "identity-bound asset": assetPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unpinned %s cache entry remains: %v", name, err)
		}
	}
}

func TestMetadataCacheGCCanEvictExpiredGitHubAPISnapshots(t *testing.T) {
	cache, err := newMetadataCacheWithPolicy(t.TempDir(), 1<<20, time.Hour)
	if err != nil { t.Fatal(err) }
	fetcher, err := NewHTTPFetcher(HTTPFetcherOptions{
		AllowedHosts: []string{"github.com"}, CacheDir: cache.root, MaxBytes: 1 << 20,
	})
	if err != nil { t.Fatal(err) }
	source := ReleaseSource{
		SourceID: "engine-source", Provider: githubReleaseProvider,
		RepositoryURI: "https://github.com/acme/engine", ComponentID: "engine",
		Variant: "default", TagPrefix: "v",
	}
	requestURI := "https://api.github.com/repos/acme/engine/releases?page=1&per_page=100"
	if err := fetcher.writeGitHubAPISnapshot(source, requestURI, []byte("[]")); err != nil { t.Fatal(err) }
	files, err := filepath.Glob(filepath.Join(cache.root, "github-*.snapshot.json"))
	if err != nil || len(files) != 1 { t.Fatalf("snapshot files = %v, err = %v", files, err) }
	expired := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(files[0], expired, expired); err != nil { t.Fatal(err) }
	result, err := cache.GarbageCollect(context.Background())
	if err != nil { t.Fatal(err) }
	if result.RemovedEntries != 1 { t.Fatalf("GC removed %d entries, want one expired GitHub API snapshot", result.RemovedEntries) }
	if _, err := os.Stat(files[0]); !errors.Is(err, os.ErrNotExist) { t.Fatalf("expired API snapshot remains after GC: %v", err) }
}
