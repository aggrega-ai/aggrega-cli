// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// cachedEntry holds a parsed YAML document alongside the raw bytes
// the parse consumed and the timestamp the parse completed at. The
// cachedAt timestamp is compared against the file's on-disk mtime on
// every Get; entries with mtime newer than cachedAt are evicted and
// reported as misses.
//
// The raw content is retained so position-conversion helpers (LSP
// UTF-16 character ↔ yaml.v3 byte column) can run without re-reading
// the file. Editor-supplied content (validate's params.Content) and
// on-disk bytes (workspace-walk reads) flow through the same cache
// shape; both populate content at Put time.
type cachedEntry struct {
	node     *yaml.Node
	content  []byte
	cachedAt time.Time
}

// cache is the daemon's parse cache. Get returns the cached parse when
// the file's on-disk mtime is older than or equal to cachedAt;
// otherwise evicts the entry and reports miss. External file
// mutations (operator edits outside the IDE, branch switches, etc.)
// invalidate the cache transparently — the daemon does not require
// push-based invalidation from the IDE for correctness.
type cache struct {
	entries map[string]*cachedEntry
}

// newCache constructs an empty cache. The cache is not concurrency-
// safe; the daemon's sequential dispatch loop is the only intended
// caller.
func newCache() *cache {
	return &cache{entries: map[string]*cachedEntry{}}
}

// Get returns the cached entry for file and true when the on-disk
// mtime is older than or equal to the entry's cachedAt. Returns nil
// and false on cache miss (either no entry, or entry evicted as
// stale, or the file no longer exists).
func (c *cache) Get(file string) (*cachedEntry, bool) {
	entry, ok := c.entries[file]
	if !ok {
		return nil, false
	}
	stat, err := os.Stat(file)
	if err != nil {
		delete(c.entries, file)
		return nil, false
	}
	if stat.ModTime().After(entry.cachedAt) {
		delete(c.entries, file)
		return nil, false
	}
	return entry, true
}

// Put inserts or replaces the cache entry for file with the supplied
// parsed node and the raw bytes that produced it. The cachedAt
// timestamp is captured as time.Now(); the subsequent Get's mtime
// comparison uses this value as the staleness boundary.
func (c *cache) Put(file string, node *yaml.Node, content []byte) {
	c.entries[file] = &cachedEntry{
		node:     node,
		content:  content,
		cachedAt: time.Now(),
	}
}

// Drop removes the cache entry for file if present. No-op when the
// entry does not exist.
func (c *cache) Drop(file string) {
	delete(c.entries, file)
}
