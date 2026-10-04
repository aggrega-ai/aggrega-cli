// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// What the daemon's three reclamation answers say about an element another
// process holds open. Windows refuses to remove a file another handle holds
// open, and each operation then leaves that file, carries on with the rest, and
// says what it left; every other platform removes the file as it removes any
// other. The command line names what it left on every platform that leaves
// something, and the answers here carry the same facts as data.
//
// Each request goes through the real dispatch, and each assertion reads the
// encoded answer rather than a typed decode of it. A typed decode would hand a
// missing key back as an empty list or a zero, which is exactly the omission
// these tests exist to catch, so presence and shape are read off the raw JSON.
//
// The handles are this process's own. They meet the same refusal another
// process's would, because the refusal belongs to how a handle was opened rather
// than to who holds it.
//
// None of these tests runs in parallel: each points the session's cache roots at
// its own fixture through a package-level seam, and the roots it would otherwise
// address are the stores every test process on the machine shares.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/cacheops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/filelock"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// heldOpenCacheDir is the workspace-relative artifact cache directory every
// fixture here seeds.
const heldOpenCacheDir = ".aggrega/cache"

// addressCacheRootsAt points every cache root the session addresses at loc for
// the rest of the test, and restores the production resolution afterwards.
func addressCacheRootsAt(t *testing.T, loc cacheops.Locations) {
	t.Helper()
	if testCacheLocations != nil {
		t.Fatalf("the cache roots are already redirected by another test; a test that redirects them must not run in parallel")
	}
	testCacheLocations = func() cacheops.Locations { return loc }
	t.Cleanup(func() { testCacheLocations = nil })
}

// holdFilesOpen opens each path for reading and returns the function that
// closes them all.
func holdFilesOpen(t *testing.T, paths ...string) func() {
	t.Helper()
	var files []*os.File
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			for _, g := range files {
				_ = g.Close()
			}
			t.Fatalf("open %s: %v", p, err)
		}
		files = append(files, f)
	}
	return func() {
		for _, f := range files {
			_ = f.Close()
		}
	}
}

// cacheAnswerOnTheWire sends one request through the real dispatch and returns
// its answer as the encoded object's keys, each still raw.
func cacheAnswerOnTheWire(t *testing.T, srv *Server, method string, params any) map[string]json.RawMessage {
	t.Helper()
	resp := callDaemonMethod(t, srv, method, params)
	if resp.Error != nil {
		t.Fatalf("%s refused: %d %s", method, resp.Error.Code, resp.Error.Message)
	}
	var answer map[string]json.RawMessage
	if err := json.Unmarshal(resp.Result, &answer); err != nil {
		t.Fatalf("decode the %s answer: %v (%s)", method, err, resp.Result)
	}
	return answer
}

// arrayOnTheWire reads key off an encoded object and requires it to be present
// and to be an array — not absent, and not null — returning its entries raw.
func arrayOnTheWire(t *testing.T, object map[string]json.RawMessage, key string) []map[string]json.RawMessage {
	t.Helper()
	raw, present := object[key]
	if !present {
		t.Fatalf("the answer carries no %q key at all", key)
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '[' {
		t.Fatalf("%q is %s on the wire; it must be an array, empty where nothing was left, so a client iterates it unconditionally", key, raw)
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("decode %q: %v (%s)", key, err, raw)
	}
	return entries
}

// stringOnTheWire reads key off an encoded object as a string.
func stringOnTheWire(t *testing.T, object map[string]json.RawMessage, key string) string {
	t.Helper()
	raw, present := object[key]
	if !present {
		t.Fatalf("the entry carries no %q key: %v", key, object)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("%q is not a string: %v (%s)", key, err, raw)
	}
	return s
}

// numberOnTheWire reads key off an encoded object as an integer.
func numberOnTheWire(t *testing.T, object map[string]json.RawMessage, key string) int64 {
	t.Helper()
	raw, present := object[key]
	if !present {
		t.Fatalf("the entry carries no %q key: %v", key, object)
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		t.Fatalf("%q is not an integer: %v (%s)", key, err, raw)
	}
	return n
}

// enumeratedCacheElement finds the element the shared class assembly enumerates
// for target and returns its class and label, so a held-open entry is held to
// what the enumeration calls the element rather than to a spelling written here.
func enumeratedCacheElement(t *testing.T, loc cacheops.Locations, target string) (class, label string) {
	t.Helper()
	want, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat %s: %v", target, err)
	}
	classes, err := cacheops.Collect(t.Context(), loc)
	if err != nil {
		t.Fatalf("the shared class assembly refused: %v", err)
	}
	for _, cl := range classes {
		for _, el := range cl.Elements {
			if got, err := os.Stat(el.Path); err == nil && os.SameFile(got, want) {
				return cl.Name, el.Label
			}
		}
	}
	t.Fatalf("no enumerated element is %s", target)
	return "", ""
}

// writeHeldOpenCompileLeaf writes a synthetic compiled-module cache leaf of the
// given size and modification time and returns its path.
func writeHeldOpenCompileLeaf(t *testing.T, dir, name string, size int, mtime time.Time) string {
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

// TestTheDaemonsPruneNamesWhatAnotherProcessHasOpen prunes a workspace whose
// external-input store holds one input its lockfile pins and one it does not,
// with the unpinned one held open. On Windows the answer names it in heldOpen
// and the file stays; elsewhere the prune removes it and heldOpen is present and
// empty.
func TestTheDaemonsPruneNamesWhatAnotherProcessHasOpen(t *testing.T) {
	wsRoot := t.TempDir()
	store := filepath.Join(externalinputs.StoreRoot(wsRoot, heldOpenCacheDir), "sha256")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatalf("create the external-input store: %v", err)
	}
	pinnedHex := cacheops.NormalizeDigestHex(digest.ContentDigest([]byte("pinned-input")))
	unpinnedHex := cacheops.NormalizeDigestHex(digest.ContentDigest([]byte("unpinned-input")))
	pinned := filepath.Join(store, pinnedHex)
	unpinned := filepath.Join(store, unpinnedHex)
	for path, content := range map[string]string{pinned: "pinned-input", unpinned: "unpinned-input"} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}
	lockPath := lockfile.PathFor(filepath.Join(wsRoot, "release.yaml"))
	if err := lockfile.WriteLockfile(lockPath, &lockfile.Lockfile{
		Version:     1,
		Release:     "release.yaml",
		GeneratedAt: "2026-01-01T00:00:00Z",
		ExternalInputs: []lockfile.ExternalInput{{
			URL: "https://example.test/pinned", Sha256: pinnedHex, Size: int64(len("pinned-input")),
			RequiredBy: []string{"release.yaml"},
		}},
	}); err != nil {
		t.Fatalf("write the lockfile: %v", err)
	}
	if _, _, err := lockfile.LoadLockfile(lockPath); err != nil {
		t.Fatalf("the fixture lockfile does not load through the loader a prune uses: %v", err)
	}

	loc := cacheops.Locations{
		WorkspaceRoot: wsRoot, CacheDir: heldOpenCacheDir,
		CompileDir: t.TempDir(), DescribeDir: t.TempDir(),
	}
	addressCacheRootsAt(t, loc)
	wantClass, wantLabel := enumeratedCacheElement(t, loc, unpinned)

	closeAll := holdFilesOpen(t, unpinned)
	defer closeAll()

	srv := newTestServer(t, wsRoot, nil, nil)
	answer := cacheAnswerOnTheWire(t, srv, "aggrega/cachePrune", map[string]any{})
	held := arrayOnTheWire(t, answer, "heldOpen")
	removed := arrayOnTheWire(t, answer, "removed")

	if _, err := os.Stat(pinned); err != nil {
		t.Errorf("the prune removed an input the lockfile pins: %v", err)
	}

	if runtime.GOOS == "windows" {
		if len(held) != 1 {
			t.Fatalf("heldOpen carries %d entries, want the one input held open: %v", len(held), held)
		}
		if got := stringOnTheWire(t, held[0], "class"); got != wantClass {
			t.Errorf("the held entry's class is %q, want %q", got, wantClass)
		}
		if got := stringOnTheWire(t, held[0], "label"); got != wantLabel {
			t.Errorf("the held entry's label is %q, want %q", got, wantLabel)
		}
		if got := stringOnTheWire(t, held[0], "reach"); got != string(cacheops.ReachWorkspace) {
			t.Errorf("the held entry's reach is %q, want %q", got, cacheops.ReachWorkspace)
		}
		size := numberOnTheWire(t, held[0], "size")
		if got, want := stringOnTheWire(t, held[0], "sizeText"), cacheops.HumanSize(size); got != want {
			t.Errorf("the held entry's sizeText is %q, want the shared formatter's %q", got, want)
		}
		if _, err := os.Stat(unpinned); err != nil {
			t.Errorf("the input another process holds open is gone: %v", err)
		}
		for _, r := range removed {
			if stringOnTheWire(t, r, "label") == wantLabel {
				t.Errorf("the answer reports as removed an input it left in place: %v", r)
			}
		}
		return
	}

	if len(held) != 0 {
		t.Errorf("heldOpen names %d entries on a platform that removes open files: %v", len(held), held)
	}
	if _, err := os.Stat(unpinned); !os.IsNotExist(err) {
		t.Errorf("the prune retained an input no lockfile pins (stat: %v)", err)
	}
	named := false
	for _, r := range removed {
		if stringOnTheWire(t, r, "label") == wantLabel {
			named = true
		}
	}
	if !named {
		t.Errorf("the answer does not name the input it removed (%q) in removed: %v", wantLabel, removed)
	}
}

// TestTheDaemonsTrimNamesALeafAnotherProcessHasOpen trims the compiled-module
// cache while its oldest leaf is held open. On Windows the answer names that leaf
// in heldOpen and the trim removes the next-oldest instead; elsewhere it removes
// the oldest and heldOpen is present and empty.
func TestTheDaemonsTrimNamesALeafAnotherProcessHasOpen(t *testing.T) {
	compileDir := t.TempDir()
	modDir := filepath.Join(compileDir, "wazero-1.0-amd64-linux")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	oldest := writeHeldOpenCompileLeaf(t, modDir, "leaf-old-1", 100, base)
	writeHeldOpenCompileLeaf(t, modDir, "leaf-old-2", 100, base.Add(time.Hour))
	writeHeldOpenCompileLeaf(t, modDir, "leaf-new", 100, base.Add(2*time.Hour))
	const oldestLabel = "wazero-1.0-amd64-linux/leaf-old-1"
	const nextLabel = "wazero-1.0-amd64-linux/leaf-old-2"

	wsRoot := t.TempDir()
	addressCacheRootsAt(t, cacheops.Locations{CompileDir: compileDir})

	closeAll := holdFilesOpen(t, oldest)
	defer closeAll()

	srv := newTestServer(t, wsRoot, nil, nil)
	// The total is 300 B; a 200 B cap needs one leaf removed.
	answer := cacheAnswerOnTheWire(t, srv, "aggrega/cacheEvict", map[string]any{"maxSize": "200"})
	held := arrayOnTheWire(t, answer, "heldOpen")
	removed := arrayOnTheWire(t, answer, "removed")

	removedLabels := map[string]bool{}
	for _, r := range removed {
		removedLabels[stringOnTheWire(t, r, "label")] = true
	}

	if runtime.GOOS == "windows" {
		if len(held) != 1 {
			t.Fatalf("heldOpen carries %d entries, want the one leaf held open: %v", len(held), held)
		}
		if got := stringOnTheWire(t, held[0], "label"); got != oldestLabel {
			t.Errorf("the held entry's label is %q, want %q", got, oldestLabel)
		}
		if got := stringOnTheWire(t, held[0], "class"); got != cacheops.CompiledModuleClassName {
			t.Errorf("the held entry's class is %q, want %q", got, cacheops.CompiledModuleClassName)
		}
		if got := stringOnTheWire(t, held[0], "reach"); got != string(cacheops.ReachMachine) {
			t.Errorf("the held entry's reach is %q, want %q", got, cacheops.ReachMachine)
		}
		if !removedLabels[nextLabel] {
			t.Errorf("the trim did not go on to the next-oldest leaf %q: removed %v", nextLabel, removed)
		}
		if _, err := os.Stat(oldest); err != nil {
			t.Errorf("the leaf another process holds open is gone: %v", err)
		}
		return
	}

	if len(held) != 0 {
		t.Errorf("heldOpen names %d entries on a platform that removes open files: %v", len(held), held)
	}
	if !removedLabels[oldestLabel] {
		t.Errorf("the trim did not remove the oldest leaf %q: removed %v", oldestLabel, removed)
	}
}

// TestTheDaemonsClearCountsWhatAnotherProcessHasOpen clears while a compile in
// this process holds its per-guest lock file. Every cleared cache carries the
// held-open count, its bytes and their written form; on Windows the
// compiled-module cache counts the lock file it left, and elsewhere every count
// is zero.
func TestTheDaemonsClearCountsWhatAnotherProcessHasOpen(t *testing.T) {
	compileDir := t.TempDir()
	writeHeldOpenCompileLeaf(t, filepath.Join(compileDir, "wazero-1.0-amd64-linux"), "abcdef0123", 64, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	compileLock := filepath.Join(compileDir, ".compile-"+cacheops.NormalizeDigestHex(digest.ContentDigest([]byte("a-guest")))+".lock")
	if err := os.WriteFile(compileLock, nil, 0o644); err != nil {
		t.Fatalf("write the compile lock: %v", err)
	}

	wsRoot := t.TempDir()
	addressCacheRootsAt(t, cacheops.Locations{
		WorkspaceRoot: wsRoot, CacheDir: heldOpenCacheDir,
		CompileDir: compileDir, DescribeDir: t.TempDir(),
	})

	release := filelock.Acquire(compileLock)
	defer release()

	srv := newTestServer(t, wsRoot, nil, nil)
	answer := cacheAnswerOnTheWire(t, srv, "aggrega/cacheClear", map[string]any{})
	cleared := arrayOnTheWire(t, answer, "cleared")

	sawCompiled := false
	for _, c := range cleared {
		class := stringOnTheWire(t, c, "class")
		files := numberOnTheWire(t, c, "heldOpenFiles")
		heldBytes := numberOnTheWire(t, c, "heldOpenBytes")
		if got, want := stringOnTheWire(t, c, "heldOpenBytesText"), cacheops.HumanSize(heldBytes); got != want {
			t.Errorf("%s: heldOpenBytesText is %q, want the shared formatter's %q", class, got, want)
		}
		if class != cacheops.CompiledModuleClassName {
			if files != 0 || heldBytes != 0 {
				t.Errorf("%s: counts %d held-open file(s), %d byte(s), where nothing was held open", class, files, heldBytes)
			}
			continue
		}
		sawCompiled = true
		wantFiles := int64(0)
		if runtime.GOOS == "windows" {
			wantFiles = 1
		}
		if files != wantFiles {
			t.Errorf("%s: heldOpenFiles is %d, want %d on %s", class, files, wantFiles, runtime.GOOS)
		}
	}
	if !sawCompiled {
		t.Fatalf("the answer names no cleared %s, so the fixture is not where the clear looks: %v",
			cacheops.CompiledModuleClassName, cleared)
	}
}
