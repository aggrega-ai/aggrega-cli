// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// putFixture writes a small YAML file at dir/name with the supplied
// content, then Puts the parsed node + raw bytes into c. Returns the
// path used so the caller can Get(path) afterwards.
func putFixture(t *testing.T, c *cache, dir, name string, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(content), &node); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	c.Put(path, &node, []byte(content))
	return path
}

// TestCacheLRUEvictsLeastRecentlyUsedOnEntriesCap verifies that
// inserting a third entry into a cache capped at two entries evicts
// the least-recently-used entry (the first Put, since neither has
// been Get-touched).
func TestCacheLRUEvictsLeastRecentlyUsedOnEntriesCap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c := newCache(2, 0)
	a := putFixture(t, c, dir, "a.yaml", "x: 1\n")
	b := putFixture(t, c, dir, "b.yaml", "x: 2\n")
	c2 := putFixture(t, c, dir, "c.yaml", "x: 3\n")

	if _, ok := c.Get(a); ok {
		t.Errorf("a should have been evicted as LRU")
	}
	if _, ok := c.Get(b); !ok {
		t.Errorf("b should be present")
	}
	if _, ok := c.Get(c2); !ok {
		t.Errorf("c should be present")
	}
}

// TestCacheLRUGetRefreshesRecency verifies that a Get on an entry
// moves it to the LRU front, so a subsequent insertion evicts the
// OTHER entry that has not been touched.
func TestCacheLRUGetRefreshesRecency(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c := newCache(2, 0)
	a := putFixture(t, c, dir, "a.yaml", "x: 1\n")
	b := putFixture(t, c, dir, "b.yaml", "x: 2\n")
	if _, ok := c.Get(a); !ok {
		t.Fatalf("a should be present before refresh")
	}
	c2 := putFixture(t, c, dir, "c.yaml", "x: 3\n")

	if _, ok := c.Get(a); !ok {
		t.Errorf("a should still be present (refreshed by Get)")
	}
	if _, ok := c.Get(b); ok {
		t.Errorf("b should have been evicted; got hit")
	}
	if _, ok := c.Get(c2); !ok {
		t.Errorf("c should be present")
	}
}

// TestCacheLRUEvictsOnBytesCapExceedance verifies that the bytes-cap
// axis fires evictions independently of the entries-cap axis.
func TestCacheLRUEvictsOnBytesCapExceedance(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// 100-byte cap, 0 entries cap means "no limit on entry count;
	// byte cap is the binding constraint."
	c := newCache(0, 100)
	// 60 printable bytes each; total would be 120 → exceeds 100-byte cap.
	contentA := "# a comment that exists solely to fill cache bytes: aaaaa\n"
	contentB := "# b comment that exists solely to fill cache bytes: bbbbb\n"
	a := putFixture(t, c, dir, "a.yaml", contentA)
	_ = putFixture(t, c, dir, "b.yaml", contentB)

	if _, ok := c.Get(a); ok {
		t.Errorf("a should have been evicted (bytes cap)")
	}
}

// TestCacheLRUUnboundedWhenBothCapsZero verifies that the cache
// accepts arbitrary insertions without eviction when both caps are
// disabled.
func TestCacheLRUUnboundedWhenBothCapsZero(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c := newCache(0, 0)
	const count = 50
	for i := 0; i < count; i++ {
		name := filepath.Join(dir, "f"+itoa(i)+".yaml")
		if err := os.WriteFile(name, []byte("x: 1\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		var node yaml.Node
		_ = yaml.Unmarshal([]byte("x: 1\n"), &node)
		c.Put(name, &node, []byte("x: 1\n"))
	}
	if got := len(c.entries); got != count {
		t.Errorf("expected %d entries (no eviction); got %d", count, got)
	}
}

// TestCacheLRUMtimeStalenessFiresBeforeLRU verifies the existing
// mtime-staleness contract is preserved: an entry whose on-disk
// mtime has advanced past cachedAt is reported as a miss, regardless
// of LRU position.
func TestCacheLRUMtimeStalenessFiresBeforeLRU(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c := newCache(10, 0)
	path := putFixture(t, c, dir, "a.yaml", "x: 1\n")
	// Advance mtime past cachedAt.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if _, ok := c.Get(path); ok {
		t.Errorf("expected mtime-stale miss; got hit")
	}
}

// TestCacheLRUDropMaintainsBytesAndList verifies that Drop on a
// known entry removes it from the map, the LRU list, and updates
// totalBytes correctly. A subsequent Put on the dropped key
// re-inserts cleanly.
func TestCacheLRUDropMaintainsBytesAndList(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c := newCache(10, 1000)
	path := putFixture(t, c, dir, "a.yaml", "x: 1\n")
	beforeBytes := c.totalBytes
	c.Drop(path)
	if got := len(c.entries); got != 0 {
		t.Errorf("entries: got %d, want 0", got)
	}
	if got := c.lruList.Len(); got != 0 {
		t.Errorf("lruList.Len(): got %d, want 0", got)
	}
	if c.totalBytes != 0 {
		t.Errorf("totalBytes: got %d, want 0 (before=%d)", c.totalBytes, beforeBytes)
	}
	_ = putFixture(t, c, dir, "a.yaml", "x: 2\n")
	if got := len(c.entries); got != 1 {
		t.Errorf("after re-put: got %d entries, want 1", got)
	}
}

// itoa is a tiny test-local int-to-string helper to avoid pulling
// strconv into a fixture loop.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for n := i; n > 0; n /= 10 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
	}
	return string(digits)
}
