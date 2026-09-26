// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"container/list"
	"os"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"
)

// cachedEntry holds a parsed YAML document alongside the raw bytes
// the parse consumed and the timestamp the parse completed at. The
// cachedAt timestamp is compared against the file's on-disk mtime on
// every Get; entries with mtime newer than cachedAt are evicted and
// reported as misses.
//
// The raw content is retained so position-conversion helpers (a session
// position ↔ a yaml.v3 node's character column) can run without re-reading
// the file. Editor-supplied content (validate's params.Content) and
// on-disk bytes (workspace-walk reads) flow through the same cache
// shape; both populate content at Put time.
//
// lru is the back-pointer into the cache's recency list; the element
// holds the file path as its Value so an LRU-back eviction can find
// the entry to delete from the map. size mirrors len(content) so
// totalBytes accounting on remove does not need a fresh sizeof of
// already-released bytes.
type cachedEntry struct {
	node     *yaml.Node
	content  []byte
	cachedAt time.Time
	lru      *list.Element
	size     int
}

// cache is the daemon's parse cache. Get returns the cached parse when
// the file's on-disk mtime is older than or equal to cachedAt;
// otherwise evicts the entry and reports miss. External file
// mutations (operator edits outside the IDE, branch switches, etc.)
// invalidate the cache transparently — the daemon does not require
// push-based invalidation from the IDE for correctness.
//
// LRU eviction policy: every Get on a hit refreshes the entry's
// recency (move-to-front); every Put inserts/replaces at the front
// and evicts least-recently-used entries from the back until both
// caps are satisfied. Two caps apply simultaneously: maxEntries
// bounds the count, maxBytes bounds the cumulative len(content)
// byte total. Either-cap = 0 means "unbounded on that axis".
//
// Concurrency contract: the cache is concurrency-safe. A single mutex
// guards every operation. The lock is unavoidable rather than
// copy-on-read because the LRU mutates on every access — Get moves a
// hit to the front and evicts a stale entry, Put inserts and runs the
// eviction loop, Drop removes — so there is no immutable snapshot to
// hand out. All exported entry points (Get, Put, Drop, Range) take the
// lock; the unexported helpers (remove, evictUntilWithinCaps) run
// lock-free because they are only ever reached from an already-locked
// exported method. The lock is held only for the in-memory map + list
// work; Range invokes its callback under the lock, so callers must keep
// the callback cheap and must not call back into the cache from it.
type cache struct {
	mu         sync.Mutex
	entries    map[string]*cachedEntry
	lruList    *list.List
	totalBytes int64
	maxEntries int
	maxBytes   int64
}

// newCache constructs an empty cache with the supplied caps. A
// zero-valued cap on either axis disables eviction on that axis.
// Callers pass 0/0 for an unbounded cache (the prior behavior before
// LRU eviction shipped).
func newCache(maxEntries int, maxBytes int) *cache {
	return &cache{
		entries:    map[string]*cachedEntry{},
		lruList:    list.New(),
		maxEntries: maxEntries,
		maxBytes:   int64(maxBytes),
	}
}

// Get returns the cached entry for file and true when the on-disk
// mtime is older than or equal to the entry's cachedAt. Returns nil
// and false on cache miss (either no entry, or entry evicted as
// stale, or the file no longer exists).
//
// Hits refresh the entry's LRU recency (move to front). Misses
// caused by mtime-staleness remove the entry from both the map and
// the LRU list.
func (c *cache) Get(file string) (*cachedEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[file]
	if !ok {
		return nil, false
	}
	stat, err := os.Stat(file)
	if err != nil {
		c.remove(file, entry)
		return nil, false
	}
	if stat.ModTime().After(entry.cachedAt) {
		c.remove(file, entry)
		return nil, false
	}
	c.lruList.MoveToFront(entry.lru)
	return entry, true
}

// Put inserts or replaces the cache entry for file with the supplied
// parsed node and the raw bytes that produced it. The cachedAt
// timestamp is captured as time.Now(); the subsequent Get's mtime
// comparison uses this value as the staleness boundary.
//
// After insertion, the eviction loop runs until both caps are
// satisfied. Either-cap = 0 disables eviction on that axis.
func (c *cache) Put(file string, node *yaml.Node, content []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.entries[file]; ok {
		c.remove(file, existing)
	}
	entry := &cachedEntry{
		node:     node,
		content:  content,
		cachedAt: time.Now(),
		size:     len(content),
	}
	entry.lru = c.lruList.PushFront(file)
	c.entries[file] = entry
	c.totalBytes += int64(entry.size)
	c.evictUntilWithinCaps()
}

// Drop removes the cache entry for file if present. No-op when the
// entry does not exist.
func (c *cache) Drop(file string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[file]; ok {
		c.remove(file, entry)
	}
}

// Range invokes fn for every live cache entry under the cache lock,
// passing the file path plus the entry's parsed node and raw content.
// Returning false from fn stops the iteration early. The callback runs
// while the lock is held, so it must stay cheap and must not call any
// other cache method (re-entrant locking would deadlock). Range is the
// concurrency-safe replacement for ranging the entries map directly:
// the workspace-wide reference/hover/completion scans use it so their
// reads never race a concurrent Get/Put/Drop mutation of the map.
func (c *cache) Range(fn func(file string, node *yaml.Node, content []byte) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for file, entry := range c.entries {
		if !fn(file, entry.node, entry.content) {
			return
		}
	}
}

// remove deletes entry from the map AND the LRU list, decrementing
// totalBytes. Internal helper used by Get's mtime-stale eviction,
// Put's replace-existing path, Drop, and the LRU-back eviction loop.
func (c *cache) remove(file string, entry *cachedEntry) {
	c.lruList.Remove(entry.lru)
	delete(c.entries, file)
	c.totalBytes -= int64(entry.size)
}

// evictUntilWithinCaps removes least-recently-used entries from the
// back of the LRU list until both caps are satisfied. Either-cap = 0
// disables that axis (the corresponding check is skipped).
func (c *cache) evictUntilWithinCaps() {
	for {
		entriesExceeded := c.maxEntries > 0 && len(c.entries) > c.maxEntries
		bytesExceeded := c.maxBytes > 0 && c.totalBytes > c.maxBytes
		if !entriesExceeded && !bytesExceeded {
			return
		}
		back := c.lruList.Back()
		if back == nil {
			return
		}
		file := back.Value.(string)
		entry := c.entries[file]
		c.remove(file, entry)
	}
}
