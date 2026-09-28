package transpiler

import (
	"os"
	"strings"
	"sync"
	"time"
)

// ScanCache makes repeated [Scan]s of the same tree incremental: per-file
// hits are cached keyed on the file's (mtime, size), so a rescan re-parses
// only the files that changed since the last one — a dev loop that scans on
// every save pays roughly one file's parse instead of the whole tree's.
//
// The cache stores unfiltered hits, so one cache serves any keyword set.
// Hits returned from a cached scan are shared — treat them as read-only.
// Safe for concurrent use. The zero value is NOT usable; call NewScanCache.
type ScanCache struct {
	mu      sync.Mutex
	entries map[string]scanEntry
}

type scanEntry struct {
	mtime time.Time
	size  int64
	hits  []Hit
}

// NewScanCache returns an empty cache.
func NewScanCache() *ScanCache {
	return &ScanCache{entries: map[string]scanEntry{}}
}

// Scan is [Scan] with this cache's incremental reuse. Deleted files drop out
// naturally (the walk no longer visits them); stale entries for them linger
// harmlessly until the process exits.
func (c *ScanCache) Scan(dir string, keywords ...string) ([]Hit, error) {
	want := make(map[string]bool, len(keywords))
	for _, k := range keywords {
		want[strings.TrimPrefix(strings.TrimSpace(k), "@")] = true
	}
	return scanTree(dir, want, c)
}

// fileHits returns path's directives, from the cache when the file is
// unchanged. A nil receiver always parses (the plain Scan path).
func (c *ScanCache) fileHits(path string) ([]Hit, error) {
	if c == nil {
		return scanFile(path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	e, ok := c.entries[path]
	c.mu.Unlock()
	if ok && e.mtime.Equal(info.ModTime()) && e.size == info.Size() {
		return e.hits, nil
	}
	hits, err := scanFile(path)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.entries[path] = scanEntry{mtime: info.ModTime(), size: info.Size(), hits: hits}
	c.mu.Unlock()
	return hits, nil
}
