// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cacheops

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

const testCacheDir = ".aggrega/cache"

// writeCompileLeaf writes a synthetic compiled-module cache leaf of the given
// byte size and modification time, creating dir if needed, and returns its path.
func writeCompileLeaf(t *testing.T, dir, name string, size int, mtime time.Time) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir compile dir: %v", err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, bytes.Repeat([]byte("x"), size), 0o644); err != nil {
		t.Fatalf("write compile leaf: %v", err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	return p
}

// TestCacheEntitySlotEncoding guards that an entity slot's display label
// round-trips the URL-encoded on-disk directory name back to a readable URI.
func TestCacheEntitySlotEncoding(t *testing.T) {
	wsRoot := t.TempDir()
	if err := oci.WriteCached(wsRoot, testCacheDir, "oci://hub.aggrega.ai/component/core/api", "1.0.0", []byte("entity-bytes")); err != nil {
		t.Fatalf("WriteCached: %v", err)
	}
	loc := Locations{WorkspaceRoot: wsRoot, CacheDir: testCacheDir}

	els := enumEntities(loc.entitiesRoot())
	if len(els) == 0 {
		t.Fatal("no entity elements enumerated")
	}
	for _, el := range els {
		if _, err := url.PathUnescape(el.Label); err != nil {
			t.Errorf("entity label not decodable: %q", el.Label)
		}
	}
}

// TestCacheClassReachIsDeclared pins which classes are shared across every
// project on the machine and which belong to the workspace the caller named.
//
// It is the assertion clause 3 of this capability's contract rests on: a surface
// reporting what a mutation changed derives its scope from these values, so a
// class silently changing side would make every such report wrong at once while
// nothing else failed.
func TestCacheClassReachIsDeclared(t *testing.T) {
	wsRoot := t.TempDir()
	if err := oci.WriteCached(wsRoot, testCacheDir, "oci://hub.aggrega.ai/component/core/api", "1.0.0", []byte("entity-bytes")); err != nil {
		t.Fatalf("WriteCached: %v", err)
	}
	loc := Locations{
		WorkspaceRoot: wsRoot,
		CacheDir:      testCacheDir,
		CompileDir:    t.TempDir(),
		DescribeDir:   t.TempDir(),
	}

	want := map[string]Reach{
		"workspace entity cache":          ReachWorkspace,
		"workspace source-artifact cache": ReachWorkspace,
		"workspace external-input store":  ReachWorkspace,
		"workspace docs-bundle cache":     ReachWorkspace,
		CompiledModuleClassName:           ReachMachine,
		"plugin-describe cache":           ReachMachine,
	}

	classes := Collect(loc)
	seen := map[string]bool{}
	for _, cl := range classes {
		seen[cl.Name] = true
		w, known := want[cl.Name]
		if !known {
			t.Errorf("class %q declares no expected reach; add it to this table and decide which side it is on", cl.Name)
			continue
		}
		if cl.Reach != w {
			t.Errorf("class %q reach = %q, want %q", cl.Name, cl.Reach, w)
		}
	}
	// Named rather than counted: a count-parity check passes while both sides are
	// short by one, and the missing class is what a reader needs.
	for name := range want {
		if !seen[name] {
			t.Errorf("expected class %q was not enumerated", name)
		}
	}

	machine := MachineWideClasses(classes)
	if len(machine) != 2 {
		t.Fatalf("MachineWideClasses = %v, want the two machine-wide classes", machine)
	}
	for _, name := range machine {
		if want[name] != ReachMachine {
			t.Errorf("MachineWideClasses reported %q, which is not machine-wide", name)
		}
	}
}

// TestCompileLeafMembershipUnified asserts the single membership predicate both
// the inventory and the eviction enumerations consult excludes a half-written
// *.tmp transient from BOTH — so the listing and the eviction leaf set agree on
// which files are cache content. The *.tmp exclusion is the point: an in-flight
// compile's staging file must never be listed or evicted on either path.
func TestCompileLeafMembershipUnified(t *testing.T) {
	compileDir := t.TempDir()
	modDir := filepath.Join(compileDir, "wazero-1.0-amd64-linux")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	module := writeCompileLeaf(t, modDir, "abcdef0123", 100, base)
	tmp := writeCompileLeaf(t, modDir, "abcdef0123.tmp", 100, base)
	lock := writeCompileLeaf(t, compileDir, ".compile-deadbeef.lock", 100, base)

	// Inventory enumeration: only the finished module is a listed element.
	els := enumCompileModules(compileDir, "", "")
	if len(els) != 1 {
		t.Fatalf("inventory enumeration returned %d element(s), want 1:\n%+v", len(els), els)
	}
	if !strings.HasSuffix(filepath.ToSlash(els[0].Path), "abcdef0123") {
		t.Fatalf("inventory enumeration listed a non-module leaf: %s", els[0].Path)
	}
	for _, el := range els {
		if strings.HasSuffix(el.Label, ".tmp") {
			t.Errorf("inventory enumeration listed a *.tmp transient: %s", el.Label)
		}
		if strings.HasPrefix(filepath.Base(el.Path), ".compile-") {
			t.Errorf("inventory enumeration listed a compile-lock sidecar: %s", el.Path)
		}
	}

	// Reclamation enumeration: the same single leaf, agreeing with the inventory.
	// It is the shared collector the size cap uses, so this asserts the listing
	// agrees with what a cap would actually remove — not with a second local copy
	// of the membership rule.
	leaves, err := wasm.CollectCompileCacheLeaves(compileDir)
	if err != nil {
		t.Fatalf("CollectCompileCacheLeaves: %v", err)
	}
	if len(leaves) != 1 {
		t.Fatalf("reclamation enumeration returned %d leaf(s), want 1:\n%+v", len(leaves), leaves)
	}
	if leaves[0].Path != module {
		t.Fatalf("reclamation enumeration leaf %q, want the module %q", leaves[0].Path, module)
	}

	// The predicate itself, exercised directly on each name.
	if !isCompileLeaf("abcdef0123") {
		t.Errorf("isCompileLeaf rejected a finished module name")
	}
	if isCompileLeaf("abcdef0123.tmp") {
		t.Errorf("isCompileLeaf admitted a *.tmp transient")
	}
	if isCompileLeaf(".compile-deadbeef.lock") {
		t.Errorf("isCompileLeaf admitted a compile-lock sidecar")
	}

	// The fixtures exist; nothing was mutated by enumeration.
	for _, p := range []string{module, tmp, lock} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("enumeration mutated the tree; missing %s", p)
		}
	}
}

// TestParseHumanSize covers the shared binary-unit vocabulary ParseHumanSize and
// HumanSize agree on, plus the malformed-input error paths.
func TestParseHumanSize(t *testing.T) {
	ok := []struct {
		in   string
		want int64
	}{
		{"1024", 1024},
		{"0", 0},
		{"512 B", 512},
		{"1KiB", 1 << 10},
		{"1 kib", 1 << 10},
		{"512MiB", 512 << 20},
		{"5GiB", 5 << 30},
		{"4.5 GiB", int64(4.5 * float64(int64(1)<<30))},
		{"1TiB", 1 << 40},
	}
	for _, c := range ok {
		got, err := ParseHumanSize(c.in)
		if err != nil {
			t.Errorf("ParseHumanSize(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseHumanSize(%q) = %d, want %d", c.in, got, c.want)
		}
	}

	bad := []string{"", "   ", "GiB", "abc", "5 ZiB", "1.5", "MiB"}
	for _, in := range bad {
		if _, err := ParseHumanSize(in); err == nil {
			t.Errorf("ParseHumanSize(%q) expected an error, got nil", in)
		}
	}

	// Round-trip: an exact unit multiple HumanSize prints parses back to itself,
	// proving the display and parse sides share one unit scheme.
	for _, n := range []int64{512, 1 << 10, 512 << 20, 5 << 30, 1 << 40} {
		rt, err := ParseHumanSize(HumanSize(n))
		if err != nil {
			t.Errorf("round-trip ParseHumanSize(HumanSize(%d)) error: %v", n, err)
			continue
		}
		if rt != n {
			t.Errorf("round-trip %d -> %q -> %d", n, HumanSize(n), rt)
		}
	}
}

// TestPruneWithoutWorkspaceRefuses pins that a prune with no workspace refuses on
// the named condition rather than pruning against an empty reference set, which
// would evict every entry.
func TestPruneWithoutWorkspaceRefuses(t *testing.T) {
	_, err := Prune(Locations{CompileDir: t.TempDir(), DescribeDir: t.TempDir()})
	if err == nil {
		t.Fatal("prune with no workspace returned no error")
	}
	if err != ErrPruneNeedsWorkspace {
		t.Fatalf("prune refused with %v, want ErrPruneNeedsWorkspace", err)
	}
}

// TestEvictWithoutCompileDirRefuses pins the loud refusal when no
// compiled-module cache directory resolves.
func TestEvictWithoutCompileDirRefuses(t *testing.T) {
	res, err := Evict(Locations{}, 100)
	if err == nil {
		t.Fatal("evict with no compiled-module cache directory returned no error")
	}
	if len(res.Removed) != 0 {
		t.Errorf("a refused eviction reported %d removal(s)", len(res.Removed))
	}
}

// TestStatusIsAPureRead proves the inventory removes nothing: the same fixture
// enumerates identically twice and every seeded file survives.
func TestStatusIsAPureRead(t *testing.T) {
	compileDir := t.TempDir()
	modDir := filepath.Join(compileDir, "wazero-1.0-amd64-linux")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	leaf := writeCompileLeaf(t, modDir, "abcdef0123", 100, base)

	loc := Locations{CompileDir: compileDir}
	a := Status(loc)
	b := Status(loc)
	if a.TotalCount != b.TotalCount || a.TotalSize != b.TotalSize {
		t.Errorf("status is not a pure read: %+v then %+v", a, b)
	}
	if a.TotalCount != 1 {
		t.Errorf("status counted %d element(s), want 1", a.TotalCount)
	}
	if _, err := os.Stat(leaf); err != nil {
		t.Errorf("status removed a leaf: %v", err)
	}
	_ = digest.BareHex("sha256:abc")
}

// TestRemoveEmptyParentsStopsAtPathBoundary pins that the parent climb is bounded
// by a path boundary rather than by a raw string prefix. The names used here are
// the ones the caches really occupy — a normal invocation owns "aggrega" beneath
// the user cache directory and a test binary owns the sibling "aggrega-test" — so
// one root's name is a string prefix of the other's, which is exactly the shape a
// prefix comparison accepts and a boundary comparison refuses. Every path is
// inside the test's own temporary directory.
func TestRemoveEmptyParentsStopsAtPathBoundary(t *testing.T) {
	t.Run("sibling whose name extends the boundary is refused", func(t *testing.T) {
		tmp := t.TempDir()
		stopAt := filepath.Join(tmp, "aggrega")
		sibling := filepath.Join(tmp, "aggrega-test")
		siblingClass := filepath.Join(sibling, "wasm-compile")
		siblingLeaf := filepath.Join(siblingClass, "sha256-abc")
		if err := os.MkdirAll(stopAt, 0o700); err != nil {
			t.Fatalf("mkdir boundary: %v", err)
		}
		if err := os.MkdirAll(siblingLeaf, 0o700); err != nil {
			t.Fatalf("mkdir sibling leaf: %v", err)
		}

		// The slot the caller has already removed. It never existed on disk; only
		// its parents are candidates for the climb.
		removeEmptyParents(filepath.Join(siblingLeaf, "module.bin"), stopAt)

		// Nothing under the sibling may be touched: it is not inside stopAt, it
		// merely starts with the same characters.
		for _, dir := range []string{siblingLeaf, siblingClass, sibling} {
			if _, err := os.Stat(dir); err != nil {
				t.Errorf("climb escaped the boundary and removed %q, which is a sibling of %q rather than a directory inside it: %v", dir, stopAt, err)
			}
		}
	})

	t.Run("empty parents inside the boundary are removed", func(t *testing.T) {
		tmp := t.TempDir()
		stopAt := filepath.Join(tmp, "aggrega")
		class := filepath.Join(stopAt, "wasm-compile")
		leaf := filepath.Join(class, "sha256-abc")
		if err := os.MkdirAll(leaf, 0o700); err != nil {
			t.Fatalf("mkdir leaf: %v", err)
		}

		removeEmptyParents(filepath.Join(leaf, "module.bin"), stopAt)

		for _, dir := range []string{leaf, class} {
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Errorf("empty parent %q inside %q survived the climb (stat err %v)", dir, stopAt, err)
			}
		}
		// The climb stops at the boundary and never removes it.
		if _, err := os.Stat(stopAt); err != nil {
			t.Errorf("the climb removed the boundary %q itself: %v", stopAt, err)
		}
	})

	t.Run("non-empty parent ends the climb", func(t *testing.T) {
		tmp := t.TempDir()
		stopAt := filepath.Join(tmp, "aggrega")
		class := filepath.Join(stopAt, "wasm-compile")
		leaf := filepath.Join(class, "sha256-abc")
		if err := os.MkdirAll(leaf, 0o700); err != nil {
			t.Fatalf("mkdir leaf: %v", err)
		}
		sibling := filepath.Join(class, "sha256-def")
		if err := os.MkdirAll(sibling, 0o700); err != nil {
			t.Fatalf("mkdir retained sibling: %v", err)
		}

		removeEmptyParents(filepath.Join(leaf, "module.bin"), stopAt)

		if _, err := os.Stat(leaf); !os.IsNotExist(err) {
			t.Errorf("the empty leaf directory %q survived (stat err %v)", leaf, err)
		}
		// The class directory still holds another entry, so the climb ends there.
		if _, err := os.Stat(class); err != nil {
			t.Errorf("the climb removed the non-empty directory %q: %v", class, err)
		}
		if _, err := os.Stat(sibling); err != nil {
			t.Errorf("the climb reached into a retained entry %q: %v", sibling, err)
		}
	})

	t.Run("a directory above the boundary is not climbed into", func(t *testing.T) {
		tmp := t.TempDir()
		// The boundary sits deeper than the slot's own parent, so the climb's
		// first candidate is already above it. Above is not inside, so nothing
		// is removed — including the sibling subtree the candidate belongs to.
		above := filepath.Join(tmp, "aggrega")
		stopAt := filepath.Join(above, "wasm-compile")
		outside := filepath.Join(above, "describe")
		if err := os.MkdirAll(stopAt, 0o700); err != nil {
			t.Fatalf("mkdir boundary: %v", err)
		}
		if err := os.MkdirAll(outside, 0o700); err != nil {
			t.Fatalf("mkdir outside: %v", err)
		}

		removeEmptyParents(filepath.Join(outside, "module.bin"), stopAt)

		for _, dir := range []string{outside, above, stopAt} {
			if _, err := os.Stat(dir); err != nil {
				t.Errorf("the climb removed %q, which is not inside the boundary %q: %v", dir, stopAt, err)
			}
		}
	})

	t.Run("the boundary is never removed however it is spelled", func(t *testing.T) {
		tmp := t.TempDir()
		// The boundary handed in carries a trailing separator, so it is not the
		// spelling the climb's own candidates take. Containment is decided on
		// where a candidate lands rather than on how the boundary was typed, so
		// the interior empties still go and the boundary still stays.
		boundary := filepath.Join(tmp, "aggrega")
		class := filepath.Join(boundary, "wasm-compile")
		leaf := filepath.Join(class, "sha256-abc")
		if err := os.MkdirAll(leaf, 0o700); err != nil {
			t.Fatalf("mkdir leaf: %v", err)
		}

		removeEmptyParents(filepath.Join(leaf, "module.bin"), boundary+string(filepath.Separator))

		for _, dir := range []string{leaf, class} {
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Errorf("empty parent %q inside %q survived the climb (stat err %v)", dir, boundary, err)
			}
		}
		if _, err := os.Stat(boundary); err != nil {
			t.Errorf("the climb removed the boundary %q itself: %v", boundary, err)
		}
	})

	t.Run("no boundary touches nothing", func(t *testing.T) {
		tmp := t.TempDir()
		// Every cache root is composed from a workspace or user directory plus a
		// named segment, and an unresolvable one arrives empty. An empty
		// boundary names no directory to climb towards, so the climb does not
		// start: were it to, the candidates here are empty and would go.
		class := filepath.Join(tmp, "aggrega", "wasm-compile")
		leaf := filepath.Join(class, "sha256-abc")
		if err := os.MkdirAll(leaf, 0o700); err != nil {
			t.Fatalf("mkdir leaf: %v", err)
		}

		removeEmptyParents(filepath.Join(leaf, "module.bin"), "")

		for _, dir := range []string{leaf, class, filepath.Join(tmp, "aggrega")} {
			if _, err := os.Stat(dir); err != nil {
				t.Errorf("the climb removed %q with no boundary to climb towards: %v", dir, err)
			}
		}
	})
}
