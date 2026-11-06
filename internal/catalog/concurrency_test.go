package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cdin-editor/registry/internal/validation"
)

type boundedTestFetcher struct {
	payloads    map[string][]byte
	gate        <-chan struct{}
	entered     chan struct{}
	delayURI    string
	delay       time.Duration
	releaseURI  string
	releaseGate chan struct{}

	mu      sync.Mutex
	active  int
	max     int
	calls   int
	failURI string
}

func (f *boundedTestFetcher) Fetch(ctx context.Context, uri string) ([]byte, error) {
	f.mu.Lock()
	f.active++
	f.calls++
	if f.active > f.max {
		f.max = f.active
	}
	f.mu.Unlock()

	if f.entered != nil {
		select {
		case f.entered <- struct{}{}:
		default:
		}
	}
	if uri == f.releaseURI && f.releaseGate != nil {
		select {
		case <-f.releaseGate:
		case <-ctx.Done():
		}
	} else if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
		}
	}
	if uri == f.delayURI && f.delay > 0 {
		timer := time.NewTimer(f.delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
		}
	}
	if err := ctx.Err(); err != nil {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
		return nil, err
	}

	payload, ok := f.payloads[uri]
	f.mu.Lock()
	f.active--
	f.mu.Unlock()
	if uri == f.failURI {
		return nil, fmt.Errorf("fixture transport failure")
	}
	if !ok {
		return nil, fmt.Errorf("missing fixture")
	}
	return append([]byte(nil), payload...), nil
}

func (f *boundedTestFetcher) snapshot() (active, maximum, calls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active, f.max, f.calls
}

func waitForFetchStarts(t *testing.T, fetcher *boundedTestFetcher, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		select {
		case <-fetcher.entered:
		case <-time.After(3 * time.Second):
			t.Fatalf("only %d of %d expected metadata fetches started", i, count)
		}
	}
}

func boundedCatalogFixture(t *testing.T, count int) ([]byte, *boundedTestFetcher, []string) {
	t.Helper()
	entries := make([]Entry, 0, count)
	payloads := make(map[string][]byte, count)
	components := make([]string, 0, count)
	for i := 0; i < count; i++ {
		component := fmt.Sprintf("engine-%02d", i)
		uri := fmt.Sprintf("mem://release/%02d", i)
		payload := releaseBytes(component, "default", "1.0.0")
		entries = append(entries, Entry{
			ComponentID: component,
			Variant:     "default",
			Version:     "1.0.0",
			MetadataURI: uri,
			MetadataSHA: digest(payload),
		})
		payloads[uri] = payload
		components = append(components, component)
	}
	return indexBytes(entries...), &boundedTestFetcher{payloads: payloads}, components
}

func TestLoadBoundsConcurrentMetadataFetchesAndPreservesIndexOrder(t *testing.T) {
	const count = 12
	index, fetcher, components := boundedCatalogFixture(t, count)
	fetcher.entered = make(chan struct{}, count)
	gate := make(chan struct{})
	fetcher.gate = gate
	fetcher.delayURI = "mem://release/00"
	fetcher.delay = 50 * time.Millisecond

	result := make(chan struct {
		catalog *Catalog
		err     error
	}, 1)
	go func() {
		loaded, err := LoadWithOptions(context.Background(), index, fetcher, &acceptingValidator{}, LoadOptions{MaxConcurrentMetadataFetches: maxConcurrentReleaseMetadataFetches})
		result <- struct {
			catalog *Catalog
			err     error
		}{catalog: loaded, err: err}
	}()

	waitForFetchStarts(t, fetcher, maxConcurrentReleaseMetadataFetches)
	active, maximum, _ := fetcher.snapshot()
	if active != maxConcurrentReleaseMetadataFetches || maximum > maxConcurrentReleaseMetadataFetches {
		t.Fatalf("active/max fetches = %d/%d, want initial window of %d", active, maximum, maxConcurrentReleaseMetadataFetches)
	}
	close(gate)

	completed := <-result
	if completed.err != nil {
		t.Fatalf("Load() error = %v", completed.err)
	}
	if completed.catalog == nil || len(completed.catalog.Releases) != count {
		t.Fatalf("loaded releases = %d, want %d", len(completed.catalog.Releases), count)
	}
	for i, component := range components {
		if got := completed.catalog.Releases[i].ComponentID; got != component {
			t.Fatalf("release[%d] component = %q, want index order %q", i, got, component)
		}
	}
	active, maximum, calls := fetcher.snapshot()
	if active != 0 {
		t.Fatalf("%d metadata fetches remained active after Load returned", active)
	}
	if maximum > maxConcurrentReleaseMetadataFetches || maximum != maxConcurrentReleaseMetadataFetches {
		t.Fatalf("maximum concurrent fetches = %d, want exactly %d and never more", maximum, maxConcurrentReleaseMetadataFetches)
	}
	if calls != count {
		t.Fatalf("metadata fetch calls = %d, want %d", calls, count)
	}
}

func TestLoadCancellationJoinsAllMetadataWorkers(t *testing.T) {
	index, fetcher, _ := boundedCatalogFixture(t, 16)
	fetcher.entered = make(chan struct{}, 16)
	gate := make(chan struct{})
	fetcher.gate = gate

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := LoadWithOptions(ctx, index, fetcher, &acceptingValidator{}, LoadOptions{MaxConcurrentMetadataFetches: maxConcurrentReleaseMetadataFetches})
		result <- err
	}()

	waitForFetchStarts(t, fetcher, 2)
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Load() error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Load() did not return after cancellation")
	}
	active, maximum, calls := fetcher.snapshot()
	if active != 0 {
		t.Fatalf("Load() returned with %d fetches still active", active)
	}
	if maximum > maxConcurrentReleaseMetadataFetches {
		t.Fatalf("maximum concurrent fetches = %d, exceeds bound %d", maximum, maxConcurrentReleaseMetadataFetches)
	}
	if calls > maxConcurrentReleaseMetadataFetches {
		t.Fatalf("started %d requests after cancellation window; bound is %d", calls, maxConcurrentReleaseMetadataFetches)
	}
}

func TestLoadRejectsFetchFailureWithoutReturningPartialCatalog(t *testing.T) {
	index, fetcher, _ := boundedCatalogFixture(t, 16)
	fetcher.entered = make(chan struct{}, 16)
	fetcher.failURI = "mem://release/00"
	fetcher.releaseURI = fetcher.failURI
	fetcher.gate = make(chan struct{})
	fetcher.releaseGate = make(chan struct{})

	validator := &acceptingValidator{}
	type loadResult struct {
		catalog *Catalog
		err     error
	}
	result := make(chan loadResult, 1)
	go func() {
		loaded, err := LoadWithOptions(context.Background(), index, fetcher, validator, LoadOptions{MaxConcurrentMetadataFetches: maxConcurrentReleaseMetadataFetches})
		result <- loadResult{catalog: loaded, err: err}
	}()

	waitForFetchStarts(t, fetcher, maxConcurrentReleaseMetadataFetches)
	close(fetcher.releaseGate)
	completed := <-result
	if completed.err == nil || completed.catalog != nil {
		t.Fatalf("Load() returned catalog=%v err=%v; want an error and no partial Catalog", completed.catalog, completed.err)
	}
	if !strings.Contains(completed.err.Error(), "fetch entry 0 metadata: transport failure") {
		t.Fatalf("Load() error = %v, want stable index-ordered transport failure", completed.err)
	}
	active, maximum, _ := fetcher.snapshot()
	if active != 0 {
		t.Fatalf("Load() returned with %d metadata fetches still active", active)
	}
	if maximum > maxConcurrentReleaseMetadataFetches {
		t.Fatalf("maximum concurrent fetches = %d, exceeds bound %d", maximum, maxConcurrentReleaseMetadataFetches)
	}
}

type rejectingReleaseValidator struct{}

func (rejectingReleaseValidator) Validate(kind validation.Kind, _ []byte, _ string) error {
	if kind == validation.ComponentRelease {
		return fmt.Errorf("fixture schema rejection")
	}
	return nil
}

func TestLoadValidationFailureCancelsAndJoinsOtherMetadataWorkers(t *testing.T) {
	index, fetcher, _ := boundedCatalogFixture(t, 16)
	fetcher.entered = make(chan struct{}, 16)
	fetcher.gate = make(chan struct{})
	fetcher.releaseURI = "mem://release/00"
	fetcher.releaseGate = make(chan struct{})

	result := make(chan error, 1)
	go func() {
		_, err := LoadWithOptions(context.Background(), index, fetcher, rejectingReleaseValidator{}, LoadOptions{MaxConcurrentMetadataFetches: maxConcurrentReleaseMetadataFetches})
		result <- err
	}()

	waitForFetchStarts(t, fetcher, maxConcurrentReleaseMetadataFetches)
	close(fetcher.releaseGate)
	if err := <-result; err == nil || !strings.Contains(err.Error(), "validate release engine-00/default@1.0.0") {
		t.Fatalf("Load() error = %v, want first-entry validation failure", err)
	}
	active, maximum, calls := fetcher.snapshot()
	if active != 0 {
		t.Fatalf("Load() returned with %d metadata fetches still active", active)
	}
	if maximum > maxConcurrentReleaseMetadataFetches || calls != maxConcurrentReleaseMetadataFetches {
		t.Fatalf("fetch active/max/calls = %d/%d/%d; want joined initial window of %d", active, maximum, calls, maxConcurrentReleaseMetadataFetches)
	}
}

func TestLoadUsesBoundedConcurrencyByDefault(t *testing.T) {
	index, fetcher, _ := boundedCatalogFixture(t, 12)
	fetcher.entered = make(chan struct{}, 12)
	gate := make(chan struct{})
	fetcher.gate = gate

	result := make(chan error, 1)
	go func() {
		_, err := Load(context.Background(), index, fetcher, &acceptingValidator{})
		result <- err
	}()
	waitForFetchStarts(t, fetcher, maxConcurrentReleaseMetadataFetches)
	active, maximum, calls := fetcher.snapshot()
	if active != maxConcurrentReleaseMetadataFetches || maximum != maxConcurrentReleaseMetadataFetches || calls != maxConcurrentReleaseMetadataFetches {
		t.Fatalf("default Load active/max/calls = %d/%d/%d; want bounded initial window %d/%d/%d",
			active, maximum, calls, maxConcurrentReleaseMetadataFetches, maxConcurrentReleaseMetadataFetches, maxConcurrentReleaseMetadataFetches)
	}
	close(gate)
	if err := <-result; err != nil {
		t.Fatalf("bounded default Load() error = %v", err)
	}
	active, maximum, calls = fetcher.snapshot()
	if active != 0 || maximum != maxConcurrentReleaseMetadataFetches || calls != 12 {
		t.Fatalf("completed default Load active/max/calls = %d/%d/%d; want 0/%d/12",
			active, maximum, calls, maxConcurrentReleaseMetadataFetches)
	}
}
func TestLoadRejectsConcurrencyAboveHardLimit(t *testing.T) {
	_, err := LoadWithOptions(context.Background(), []byte("{}"), &countingFetcher{}, &acceptingValidator{}, LoadOptions{
		MaxConcurrentMetadataFetches: maxConcurrentReleaseMetadataFetches + 1,
	})
	if err == nil || !strings.Contains(err.Error(), "metadata fetch concurrency") {
		t.Fatalf("LoadWithOptions() error = %v, want concurrency limit rejection", err)
	}
}
