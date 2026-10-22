// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasmfixture

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var fixtureKeySeq int64

// uniqueFixtureKey returns a memo key no other test or repetition shares,
// and drops it from the package's caches afterwards so a repeated run does
// not inherit the previous run's memoised result.
func uniqueFixtureKey(t *testing.T) string {
	t.Helper()
	key := t.Name() + "-" + strconv.FormatInt(atomic.AddInt64(&fixtureKeySeq, 1), 10) + ".wasm"
	t.Cleanup(func() {
		fixtureResults.Delete(key)
		fixtureBuildLocks.Delete(key)
	})
	return key
}

// TestBuildFixtureOnceRunsOneBuilderPerArtifact is the guard on the
// single-flight. Fixture builds write to a path every caller reads, so two
// builders running at once leave that path readable while it is being
// written and a test can load a truncated artifact. Each caller here holds
// its build open until every other caller has arrived, so any second
// builder that gets through is counted rather than merely likely to be
// observed.
func TestBuildFixtureOnceRunsOneBuilderPerArtifact(t *testing.T) {
	t.Parallel()

	const callers = 16
	// Bounds the hold when the single-flight is doing its job and only one
	// caller ever reaches the build.
	const holdWindow = 100 * time.Millisecond

	key := uniqueFixtureKey(t)

	var (
		mu       sync.Mutex
		inFlight int
		peak     int
		builds   int
		arrivals int
	)
	allArrived := make(chan struct{})

	build := func() (string, error) {
		mu.Lock()
		builds++
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		arrivals++
		if arrivals == callers {
			close(allArrived)
		}
		mu.Unlock()

		select {
		case <-allArrived:
		case <-time.After(holdWindow):
		}

		mu.Lock()
		inFlight--
		mu.Unlock()
		return "artifact", nil
	}

	paths := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			paths[i], errs[i] = BuildFixtureOnce(key, build)
		}(i)
	}
	wg.Wait()

	mu.Lock()
	gotPeak, gotBuilds := peak, builds
	mu.Unlock()

	if gotPeak != 1 {
		t.Errorf("peak concurrent builders = %d, want 1: %d callers raced into one artifact's build, which leaves its output path readable mid-write",
			gotPeak, callers)
	}
	if gotBuilds != 1 {
		t.Errorf("build invocations = %d, want 1: one artifact was built more than once", gotBuilds)
	}
	for i := 0; i < callers; i++ {
		if errs[i] != nil {
			t.Errorf("caller %d: unexpected error %v", i, errs[i])
		}
		if paths[i] != "artifact" {
			t.Errorf("caller %d: path = %q, want %q — every caller must receive the builder's result", i, paths[i], "artifact")
		}
	}
}

// TestBuildFixtureOnceDoesNotSerialiseDifferentArtifacts holds the
// single-flight to per-artifact scope. One global lock would also stop
// concurrent builds of the same artifact, and would additionally stall
// every unrelated fixture behind whichever build happens to be running.
func TestBuildFixtureOnceDoesNotSerialiseDifferentArtifacts(t *testing.T) {
	t.Parallel()

	keyA := uniqueFixtureKey(t)
	keyB := uniqueFixtureKey(t)

	var (
		mu       sync.Mutex
		inFlight int
	)
	bothInFlight := make(chan struct{})

	build := func() (string, error) {
		mu.Lock()
		inFlight++
		reached := inFlight
		mu.Unlock()

		if reached == 2 {
			close(bothInFlight)
		}

		select {
		case <-bothInFlight:
			return "artifact", nil
		case <-time.After(5 * time.Second):
			return "", errors.New("second builder never started")
		}
	}

	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, key := range []string{keyA, keyB} {
		wg.Add(1)
		go func(i int, key string) {
			defer wg.Done()
			_, errs[i] = BuildFixtureOnce(key, build)
		}(i, key)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("builder %d: %v — builds of two different artifacts must be able to run at the same time", i, err)
		}
	}
}

// TestBuildFixtureOnceCachesAndSharesASkipReason pins the failure half of
// the memo: a build that could not run is answered once and the reason is
// handed to every later caller, rather than every caller re-attempting a
// build that is not going to work.
func TestBuildFixtureOnceCachesAndSharesASkipReason(t *testing.T) {
	t.Parallel()

	key := uniqueFixtureKey(t)
	want := errors.New("a required build tool is not on PATH")

	var builds int
	build := func() (string, error) {
		builds++
		return "", want
	}

	for i := 0; i < 3; i++ {
		path, err := BuildFixtureOnce(key, build)
		if !errors.Is(err, want) {
			t.Fatalf("caller %d: err = %v, want %v", i, err, want)
		}
		if path != "" {
			t.Errorf("caller %d: path = %q, want empty on a failed build", i, path)
		}
	}

	if builds != 1 {
		t.Errorf("build invocations = %d, want 1: a skip reason must be derived once and shared, not re-derived per caller", builds)
	}
}

// TestPublishArtifactReplacesTheDestinationWholeAndAtOnce is the guard on
// the atomic-write half. A lock only reaches the goroutines of one test
// binary, and every test package is its own process, so publishing by
// rename is what keeps a reader in another process from seeing a partial
// artifact. The reader opened before the publish stands in for that
// cross-process reader: after a rename it still holds the artifact it
// opened, whole, whereas a publish that wrote into the destination in place
// would show it the new content or a mixture.
func TestPublishArtifactReplacesTheDestinationWholeAndAtOnce(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dst := filepath.Join(dir, "artifact.wasm")

	previous := bytes.Repeat([]byte{0xAA}, 64*1024)
	if err := os.WriteFile(dst, previous, 0o644); err != nil {
		t.Fatalf("seed destination: %v", err)
	}

	reader, err := os.Open(dst)
	if err != nil {
		t.Fatalf("open destination: %v", err)
	}
	defer reader.Close()

	stageDir, err := NewStageDir(dir)
	if err != nil {
		t.Fatalf("newStageDir: %v", err)
	}
	staged := filepath.Join(stageDir, "artifact.wasm")
	fresh := bytes.Repeat([]byte{0xBB}, 128*1024)
	if err := os.WriteFile(staged, fresh, 0o644); err != nil {
		t.Fatalf("seed staged artifact: %v", err)
	}

	if err := PublishArtifact(staged, dst); err != nil {
		t.Fatalf("publishArtifact: %v", err)
	}

	held, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read through the open handle: %v", err)
	}
	if !bytes.Equal(held, previous) {
		t.Errorf("a reader holding the destination open saw %d bytes of altered content (want the %d bytes it opened): the destination was written in place rather than replaced by rename",
			len(held), len(previous))
	}

	published, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read published destination: %v", err)
	}
	if !bytes.Equal(published, fresh) {
		t.Errorf("published destination has %d bytes, want the staged artifact's %d", len(published), len(fresh))
	}

	if _, err := os.Stat(staged); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("staged path still present after publish (stat err = %v): the publish copied instead of moving", err)
	}
}
