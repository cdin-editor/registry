package collector

import (
	"context"
	"testing"
)

func BenchmarkGitObjectCacheGarbageCollectEmpty(b *testing.B) {
	cache, err := NewGitObjectCache(b.TempDir())
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
