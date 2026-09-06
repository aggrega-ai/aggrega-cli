// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// Size-spelling parity at the real entrypoint. Both surfaces that report what
// the caches hold are driven as spawned processes over their own channels — the
// command line over its output stream, the daemon over its wire protocol — and
// the text each one produces for the same cache is compared.
//
// This is the leg the per-package tests structurally cannot be. Those assert
// that the daemon's rendered field is the shared formatter's output; none of
// them observes what the command line prints, so none would notice a surface
// that spelled the same quantity its own way. Here the comparison is between
// the answers two callers actually receive about one cache.
//
// Both processes are pinned to a temp cache root, so the machine-wide classes
// are empty and every reported byte comes from the workspace this test seeded.
// That is what makes the comparison deterministic under a parallel battery,
// where an unrelated test compiling a guest would otherwise move the
// machine-wide total between the two invocations. It is also what keeps the
// test off the developer's own caches.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/cacheops"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// seededInputSizes are the byte counts written into the workspace's
// external-input store, one file each.
//
// They are chosen to make the listing span more than one unit — a count below
// the first unit, an exact binary kilobyte, a half-multiple, and binary
// megabytes — so a surface that agreed on the bytes case and diverged above it
// is still caught. Nothing larger is written: a gigabyte file would cost more
// than the coverage is worth, and the units above these are covered in-process
// where a size is a number rather than a file.
var seededInputSizes = map[string]int64{
	"0000000000000000000000000000000000000000000000000000000000000001": 512,
	"0000000000000000000000000000000000000000000000000000000000000002": 1 << 10,
	"0000000000000000000000000000000000000000000000000000000000000003": 1536,
	"0000000000000000000000000000000000000000000000000000000000000004": 3 << 20,
}

// cacheParityWorkspace builds a workspace whose external-input store holds files
// of known sizes, and returns the workspace root.
//
// The store path comes from the same helper the enumerator resolves it through,
// so a change to the `inputs/` layout moves this fixture with it rather than
// leaving it seeding a directory nothing reads.
func cacheParityWorkspace(t *testing.T) string {
	t.Helper()

	ws := makeWorkspace(t)
	cacheDir := filepath.Join(ws, workspaceconfig.Default().ResolvedArtifactCacheDir())
	store := filepath.Join(externalinputs.StoreRoot(cacheDir), "sha256")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatalf("create the external-input store: %v", err)
	}
	for name, size := range seededInputSizes {
		if err := os.WriteFile(filepath.Join(store, name), make([]byte, size), 0o644); err != nil {
			t.Fatalf("seed a %d-byte input: %v", size, err)
		}
	}
	return ws
}

// pinnedCacheEnv returns an environment addressing a temp machine-wide cache
// root, so a child process reports only what this test seeded and writes
// nothing outside the test's own directories.
func pinnedCacheEnv(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	return append(os.Environ(),
		"HOME="+home,
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
	)
}

// cacheStatusViaCommandLine runs the reporting command in ws and returns what it
// printed to standard output. That stream is the whole of what this surface
// hands a caller.
func cacheStatusViaCommandLine(t *testing.T, bin, ws string, env []string) string {
	t.Helper()

	cmd := exec.Command(bin, "cache", "status")
	cmd.Dir = ws
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("the command refused: %v (%s)", err, stderr.String())
	}
	return stdout.String()
}

// cacheResultViaDaemon spawns the daemon in ws, sends one request, and returns
// its decoded result object.
func cacheResultViaDaemon(t *testing.T, bin, ws, method string, params map[string]any, env []string) map[string]any {
	t.Helper()

	cmd := exec.Command(bin, "serve")
	cmd.Dir = ws
	cmd.Env = env
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("daemon stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("daemon stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})

	if _, err := stdin.Write(framed(1, method, params)); err != nil {
		t.Fatalf("write the daemon request for %s: %v", method, err)
	}
	resp := readOneResponse(t, stdout, responseLivenessCeiling(t))
	if env, isErr := resp["error"]; isErr {
		t.Fatalf("the daemon refused %s: %+v", method, env)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("the daemon's answer to %s carries no result object: %+v", method, resp)
	}
	return result
}

// TestBothReportingSurfacesSpellACacheSizeTheSameWay is the parity proof: every
// size the daemon rendered appears verbatim in what the command line printed
// about the same cache, and each one parses back to the number beside it.
//
// Two things are asserted together on purpose. That the strings match is what
// makes the two surfaces interchangeable to a reader; that each one parses back
// is what makes the text usable as input to an operation that takes a size. A
// surface could satisfy either alone — two surfaces agreeing on an unparseable
// spelling, or a parseable spelling only one of them uses — and neither would be
// the property this exists to hold.
func TestBothReportingSurfacesSpellACacheSizeTheSameWay(t *testing.T) {
	// Not parallel: spawns subprocesses, one of them a daemon.
	bin := buildServeBinary(t)
	ws := cacheParityWorkspace(t)
	env := pinnedCacheEnv(t)

	printed := cacheStatusViaCommandLine(t, bin, ws, env)
	result := cacheResultViaDaemon(t, bin, ws, "aggrega/cacheStatus", map[string]any{}, env)

	// The listing must be non-empty, or every comparison below would hold
	// vacuously over nothing. This is the control: it fails if the fixture
	// stopped landing where the enumerator looks.
	total, ok := result["totalCount"].(float64)
	if !ok || int(total) != len(seededInputSizes) {
		t.Fatalf("the daemon reported %v elements for a cache seeded with %d; "+
			"the fixture is not where the enumeration looks: %+v",
			result["totalCount"], len(seededInputSizes), result)
	}

	// Every rendered element size agrees with the command line and round-trips.
	classes, ok := result["classes"].([]any)
	if !ok {
		t.Fatalf("the daemon's status carries no classes array: %+v", result)
	}
	seen := 0
	for _, rawClass := range classes {
		class, ok := rawClass.(map[string]any)
		if !ok {
			t.Fatalf("a class is not an object: %+v", rawClass)
		}
		elements, _ := class["elements"].([]any)
		for _, rawElement := range elements {
			element, ok := rawElement.(map[string]any)
			if !ok {
				t.Fatalf("an element is not an object: %+v", rawElement)
			}
			// Every seeded element size is an exact multiple or half-multiple of
			// its unit, so exact recovery is required of each one.
			assertRenderedSizeAgrees(t, element, "size", "sizeText", printed, true)
			seen++
		}
	}
	if seen != len(seededInputSizes) {
		t.Errorf("walked %d elements, want %d", seen, len(seededInputSizes))
	}

	// And so does the total. Exact recovery is NOT required of it: the total is
	// the sum of elements spanning three units, and such a sum is not generally
	// exactly representable at one decimal place in the coarser unit its own
	// magnitude selects. The stability and surface-agreement properties still
	// hold, and they are what a caller reads a total for.
	assertRenderedSizeAgrees(t, result, "totalSize", "totalSizeText", printed, false)
}

// TestACallerSuppliedSizeComesBackInTheSameVocabulary drives the one operation
// that takes a size and closes the loop on it: the cap goes in as text a caller
// wrote and comes back as text in the same vocabulary, so the value a caller
// reads is one it could send again unchanged.
//
// The cap is far above anything the pinned cache root holds, so the operation
// reports a no-op and removes nothing. That is deliberate — the assertion is
// about the spelling of the answer, and nothing here needs a removal to happen.
// It also covers a unit no file in this package is large enough to produce.
func TestACallerSuppliedSizeComesBackInTheSameVocabulary(t *testing.T) {
	// Not parallel: spawns subprocesses, one of them a daemon.
	bin := buildServeBinary(t)
	ws := cacheParityWorkspace(t)
	env := pinnedCacheEnv(t)

	const capText = "8GiB"
	result := cacheResultViaDaemon(t, bin, ws, "aggrega/cacheEvict",
		map[string]any{"maxSize": capText}, env)

	if evicted, _ := result["withinCapBefore"].(bool); !evicted {
		t.Fatalf("a cap of %s did not report the cache already within it, so this "+
			"test is no longer the no-op it is written as: %+v", capText, result)
	}
	if removed, _ := result["removed"].([]any); len(removed) != 0 {
		t.Fatalf("a no-op eviction removed %d leaves: %+v", len(removed), removed)
	}

	capBytes, err := cacheops.ParseHumanSize(capText)
	if err != nil {
		t.Fatalf("the parser refused the cap this test supplied: %v", err)
	}
	onTheWire, ok := result["capBytesText"].(string)
	if !ok {
		t.Fatalf("the eviction result carries no rendered cap: %+v", result)
	}
	if want := cacheops.HumanSize(capBytes); onTheWire != want {
		t.Errorf("the rendered cap is %q, want the shared formatter's %q", onTheWire, want)
	}
	// The round trip that matters: the text the daemon answered with is accepted
	// by the parser that read the request, and means the same quantity.
	back, err := cacheops.ParseHumanSize(onTheWire)
	if err != nil {
		t.Fatalf("the parser refused the daemon's own rendered cap %q: %v", onTheWire, err)
	}
	if back != capBytes {
		t.Errorf("the cap did not survive the round trip: %q in, %q out, %d != %d",
			capText, onTheWire, capBytes, back)
	}
}

// assertRenderedSizeAgrees holds one number/text pair to the properties that
// are true of every rendered size, and to exact recovery only where the caller
// says the quantity is exactly representable.
//
// The split is the contract rather than a concession. Above a byte the
// formatter writes one decimal place, so a quantity that is not an exact
// multiple (or half-multiple) of its unit is rendered to the nearest tenth and
// names that tenth thereafter. What holds universally is IDEMPOTENCE: the text
// parses, and the quantity it parses to renders back to the same text, so the
// spelling is stable under any number of round trips and a caller can hand the
// text back to an operation that takes a size. Exact recovery of the original
// byte count is an additional property of exactly-representable values, and it
// is asserted where it applies rather than everywhere — a total is a sum, and a
// sum of mixed-unit elements is generally not exactly representable in the
// coarser unit its own magnitude selects.
func assertRenderedSizeAgrees(t *testing.T, obj map[string]any, numberKey, textKey, printed string, exact bool) {
	t.Helper()

	number, ok := obj[numberKey].(float64)
	if !ok {
		t.Errorf("no %q on %+v", numberKey, obj)
		return
	}
	text, ok := obj[textKey].(string)
	if !ok {
		t.Errorf("%q crosses the wire as a bare number: no %q beside it on %+v",
			numberKey, textKey, obj)
		return
	}

	if want := cacheops.HumanSize(int64(number)); text != want {
		t.Errorf("%q is %q; the shared formatter writes %q for %d", textKey, text, want, int64(number))
	}

	parsed, err := cacheops.ParseHumanSize(text)
	if err != nil {
		t.Errorf("the parser refused the daemon's own %q value %q: %v", textKey, text, err)
		return
	}
	if again := cacheops.HumanSize(parsed); again != text {
		t.Errorf("%q is not stable under a round trip: %q parsed to %d, which renders as %q",
			textKey, text, parsed, again)
	}
	if exact && parsed != int64(number) {
		t.Errorf("%q does not recover its exact byte count: %q parsed back to %d, want %d",
			textKey, text, parsed, int64(number))
	}

	if !strings.Contains(printed, text) {
		t.Errorf("the daemon rendered %q as %q and the command line printed no such text; "+
			"the two surfaces spell this size differently.\ncommand line output:\n%s",
			numberKey, text, printed)
	}
}
