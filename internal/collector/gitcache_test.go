package collector

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGitObjectCacheWithObjectPopulatesThenReusesVerifiedRepository(t *testing.T) {
	cache, err := NewGitObjectCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	oid := "0123456789abcdef0123456789abcdef01234567"
	var populated atomic.Int32
	verify := func(path string) error {
		data, err := os.ReadFile(filepath.Join(path, "fixture.object"))
		if err != nil {
			return err
		}
		if string(data) != oid {
			return errors.New("object identity mismatch")
		}
		return nil
	}
	populate := func(path string) error {
		populated.Add(1)
		return os.WriteFile(filepath.Join(path, "fixture.object"), []byte(oid), 0o600)
	}
	use := func(path string) error { return verify(path) }
	hit, err := cache.withObject(context.Background(), oid, verify, populate, use)
	if err != nil {
		t.Fatal(err)
	}
	if hit {
		t.Fatal("first cache lookup unexpectedly hit")
	}
	hit, err = cache.withObject(context.Background(), oid, verify, populate, use)
	if err != nil {
		t.Fatal(err)
	}
	if !hit {
		t.Fatal("second cache lookup did not hit")
	}
	if got := populated.Load(); got != 1 {
		t.Fatalf("populate called %d times, want once", got)
	}
}

func TestGitObjectCacheCorruptHitIsDiscardedAndRefetched(t *testing.T) {
	cache, err := NewGitObjectCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	oid := "0123456789abcdef0123456789abcdef01234567"
	var populated atomic.Int32
	verify := func(path string) error {
		data, err := os.ReadFile(filepath.Join(path, "fixture.object"))
		if err != nil {
			return err
		}
		if string(data) != "verified" {
			return errors.New("corrupt cache payload")
		}
		return nil
	}
	populate := func(path string) error {
		populated.Add(1)
		return os.WriteFile(filepath.Join(path, "fixture.object"), []byte("verified"), 0o600)
	}
	use := func(path string) error { return verify(path) }
	if _, err := cache.withObject(context.Background(), oid, verify, populate, use); err != nil {
		t.Fatal(err)
	}
	path := cache.objectPath(oid)
	if err := os.WriteFile(filepath.Join(path, "fixture.object"), []byte("corrupted"), 0o600); err != nil {
		t.Fatal(err)
	}
	hit, err := cache.withObject(context.Background(), oid, verify, populate, use)
	if err != nil {
		t.Fatal(err)
	}
	if hit {
		t.Fatal("corrupt cache entry was incorrectly reported as a hit")
	}
	if populated.Load() != 2 {
		t.Fatalf("populate calls = %d, want corrupt entry to be refetched", populated.Load())
	}
}

func TestGitObjectCachePreservesRetainedManifestsAndActiveReplayLeases(t *testing.T) {
	cache, err := newGitObjectCacheWithPolicy(t.TempDir(), 1<<20, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	nowOld := time.Now().Add(-2 * time.Hour)
	oidA := "1111111111111111111111111111111111111111"
	oidB := "2222222222222222222222222222222222222222"
	oidC := "3333333333333333333333333333333333333333"
	pathA := writeGitObjectFixture(t, cache, oidA, "a", nowOld)
	pathB := writeGitObjectFixture(t, cache, oidB, "b", nowOld)
	pathC := writeGitObjectFixture(t, cache, oidC, "c", nowOld)
	manifestA := gitTestManifest(oidA)
	pinID, err := cache.RetainManifest(manifestA)
	if err != nil {
		t.Fatal(err)
	}
	manifestB := gitTestManifest(oidB)
	release, err := cache.BeginReplayLease(context.Background(), manifestB)
	if err != nil {
		t.Fatal(err)
	}
	result, err := cache.GarbageCollect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedObjects != 1 {
		t.Fatalf("GC removed %d objects, want only the unprotected object", result.RemovedObjects)
	}
	for name, path := range map[string]string{"pinned": pathA, "leased": pathB} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s object was evicted: %v", name, err)
		}
	}
	if _, err := os.Stat(pathC); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unprotected object remains: %v", err)
	}
	release()
	if err := cache.ReleaseManifest(pinID); err != nil {
		t.Fatal(err)
	}
	result, err = cache.GarbageCollect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedObjects != 2 {
		t.Fatalf("second GC removed %d objects, want 2", result.RemovedObjects)
	}
}

func TestGitObjectCacheGCEvictsOldestUnprotectedObjectsToBudget(t *testing.T) {
	cache, err := newGitObjectCacheWithPolicy(t.TempDir(), 5, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	oidA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	oidB := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	oidC := "cccccccccccccccccccccccccccccccccccccccc"
	pathA := writeGitObjectFixture(t, cache, oidA, "aaaa", now.Add(-3*time.Hour))
	pathB := writeGitObjectFixture(t, cache, oidB, "bbbb", now.Add(-2*time.Hour))
	pathC := writeGitObjectFixture(t, cache, oidC, "cccc", now.Add(-time.Hour))
	result, err := cache.GarbageCollect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedObjects != 2 || result.RemainingBytes != 4 {
		t.Fatalf("GC result = %+v, want two removed and four bytes retained", result)
	}
	for name, path := range map[string]string{"oldest": pathA, "second oldest": pathB} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s object should have been evicted: %v", name, err)
		}
	}
	if _, err := os.Stat(pathC); err != nil {
		t.Fatalf("newest object should remain: %v", err)
	}
}

func TestGitObjectCacheGCFailsClosedOnMalformedReferenceSnapshot(t *testing.T) {
	cache, err := newGitObjectCacheWithPolicy(t.TempDir(), 1, time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	path := writeGitObjectFixture(t, cache, "dddddddddddddddddddddddddddddddddddddddd", "data", time.Now().Add(-time.Hour))
	if err := os.WriteFile(filepath.Join(cache.pins, "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := cache.GarbageCollect(context.Background())
	if err == nil {
		t.Fatal("GC unexpectedly accepted malformed reference snapshot")
	}
	if result.RemovedObjects != 0 {
		t.Fatalf("GC removed %d objects after snapshot validation failed", result.RemovedObjects)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fail-closed GC removed object: %v", err)
	}
}

func TestGitObjectCacheWaitsForObjectLockAndCleansCanceledLease(t *testing.T) {
	cache, err := NewGitObjectCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	oid := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	lock := filepath.Join(cache.locks, oid+".lock")
	if err := os.WriteFile(lock, []byte("held"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err = cache.withObject(ctx, oid,
		func(string) error { return nil },
		func(string) error { return nil },
		func(string) error { return nil },
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WithObject error = %v, want context deadline", err)
	}
	leases, err := os.ReadDir(cache.leases)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 0 {
		t.Fatalf("canceled operation left %d leases", len(leases))
	}
}

func TestGitObjectLockContentionRecognizesWindowsAccessDenied(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ffffffffffffffffffffffffffffffffffffffff.lock")
	if err := os.WriteFile(path, []byte("held"), 0o600); err != nil {
		t.Fatal(err)
	}
	accessDenied := &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
	busy, err := gitObjectLockContention(path, accessDenied)
	if err != nil {
		t.Fatalf("existing lock permission collision returned an error: %v", err)
	}
	if !busy {
		t.Fatal("existing regular lock file was not recognized as contention")
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	busy, err = gitObjectLockContention(path, accessDenied)
	if err != nil {
		t.Fatalf("permission error without an existing lock returned an error: %v", err)
	}
	if busy {
		t.Fatal("permission error without an existing lock was treated as contention")
	}

	busy, err = gitObjectLockContention(path, errors.New("unrelated open failure"))
	if err != nil || busy {
		t.Fatalf("unrelated error = busy %t, err %v; want no contention", busy, err)
	}
}

func TestGitObjectCacheConcurrentAccessPopulatesOnce(t *testing.T) {
	cache, err := NewGitObjectCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	oid := "ffffffffffffffffffffffffffffffffffffffff"
	var populated atomic.Int32
	var inUse atomic.Int32
	var maximumInUse atomic.Int32
	start := make(chan struct{})
	var wait sync.WaitGroup
	errs := make(chan error, 2)
	hits := make(chan bool, 2)
	verify := func(path string) error {
		data, err := os.ReadFile(filepath.Join(path, "fixture.object"))
		if err != nil {
			return err
		}
		if string(data) != oid {
			return errors.New("wrong object identity")
		}
		return nil
	}
	populate := func(path string) error {
		populated.Add(1)
		time.Sleep(20 * time.Millisecond)
		return os.WriteFile(filepath.Join(path, "fixture.object"), []byte(oid), 0o600)
	}
	use := func(path string) error {
		current := inUse.Add(1)
		for old := maximumInUse.Load(); current > old && !maximumInUse.CompareAndSwap(old, current); old = maximumInUse.Load() {
		}
		time.Sleep(30 * time.Millisecond)
		inUse.Add(-1)
		return verify(path)
	}
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			hit, err := cache.withObject(context.Background(), oid, verify, populate, use)
			hits <- hit
			errs <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errs)
	close(hits)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if populated.Load() != 1 {
		t.Fatalf("concurrent populate count = %d, want 1", populated.Load())
	}
	if got := maximumInUse.Load(); got != 1 {
		t.Fatalf("simultaneous uses of one object = %d, want serialized use", got)
	}
	hitCount := 0
	for hit := range hits {
		if hit {
			hitCount++
		}
	}
	if hitCount != 1 {
		t.Fatalf("cache hit count = %d, want one hit and one population", hitCount)
	}
}

func gitTestManifest(oid string) []byte {
	type sourceUnit struct {
		Revision string `json:"revision"`
	}
	type component struct {
		SourceUnits []sourceUnit `json:"source_units"`
	}
	encoded, _ := json.Marshal(struct {
		Components []component `json:"components"`
	}{
		Components: []component{{SourceUnits: []sourceUnit{{Revision: oid}}}},
	})
	return encoded
}

func writeGitObjectFixture(t *testing.T, cache *GitObjectCache, oid, body string, modTime time.Time) string {
	t.Helper()
	path := cache.objectPath(oid)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "fixture.object"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	return path
}
