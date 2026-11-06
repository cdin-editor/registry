package catalog

import (
	"context"
	"testing"
)

func BenchmarkMetadataCacheGarbageCollectEmpty(b *testing.B) {
	cache, err := NewMetadataCache(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := cache.GarbageCollect(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
