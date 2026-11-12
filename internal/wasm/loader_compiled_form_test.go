// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/attribindex"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	extism "github.com/extism/go-sdk"
	"github.com/tetratelabs/wazero"
)

// isolatedGuest returns the named fixture's bytes with a custom section appended
// that carries tag. The guest's code is untouched — a custom section is ignored by
// validation and by execution — but its content digest, which every process-wide
// table in this package keys on, and its module identity, which the runtime keys
// its compiled modules on, are both distinct to the tag. That keeps a test's first
// loads first, whatever else the test process has already loaded.
func isolatedGuest(t *testing.T, fixture, tag string) []byte {
	t.Helper()
	b, err := os.ReadFile(wasmtest.EnsureFixtureBuilt(t, fixture))
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixture, err)
	}
	return withCustomSection(b, "aggrega-test-isolation", tag)
}

// withCustomSection appends a WebAssembly custom section named name, carrying
// payload, to module. Sizes are unsigned LEB128, which is the encoding
// binary.AppendUvarint writes.
func withCustomSection(module []byte, name, payload string) []byte {
	var body []byte
	body = binary.AppendUvarint(body, uint64(len(name)))
	body = append(body, name...)
	body = append(body, payload...)
	out := append(append([]byte(nil), module...), 0x00) // custom section id
	out = binary.AppendUvarint(out, uint64(len(body)))
	return append(out, body...)
}

// manyTypesGuest returns a module with no imports that declares types function
// types and exports one function, describe, of the LAST type: no parameters, one
// i32 result, returning 0. Every other type takes and returns nothing and no function
// uses it. A tag in a custom section makes the module distinct to the caller, as
// isolatedGuest does for a fixture.
//
// The type count is the point. When the runtime reads a module from the on-disk
// compilation cache, it puts it into the engine's shared table and then compiles one
// entry preamble per declared function type, in declaration order; describe's is
// compiled last. So the time during which the table holds this module with
// describe's preamble missing grows with types, which is what lets a test make a
// concurrent load land in it.
func manyTypesGuest(types int, tag string) []byte {
	section := func(id byte, content []byte) []byte {
		out := []byte{id}
		out = binary.AppendUvarint(out, uint64(len(content)))
		return append(out, content...)
	}
	var typeSec []byte
	typeSec = binary.AppendUvarint(typeSec, uint64(types))
	for i := 0; i < types-1; i++ {
		typeSec = append(typeSec, 0x60, 0x00, 0x00) // func type: no params, no results
	}
	typeSec = append(typeSec, 0x60, 0x00, 0x01, 0x7f) // func type: no params, one i32 result
	var funcSec []byte
	funcSec = binary.AppendUvarint(funcSec, 1)
	funcSec = binary.AppendUvarint(funcSec, uint64(types-1))
	var exportSec []byte
	exportSec = binary.AppendUvarint(exportSec, 1)
	exportSec = binary.AppendUvarint(exportSec, uint64(len(describeExportName)))
	exportSec = append(exportSec, describeExportName...)
	exportSec = append(exportSec, 0x00, 0x00) // a function, index 0
	body := []byte{0x00, 0x41, 0x00, 0x0b}    // no locals; i32.const 0; end
	var codeSec []byte
	codeSec = binary.AppendUvarint(codeSec, 1)
	codeSec = binary.AppendUvarint(codeSec, uint64(len(body)))
	codeSec = append(codeSec, body...)
	m := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	m = append(m, section(1, typeSec)...)
	m = append(m, section(3, funcSec)...)
	m = append(m, section(7, exportSec)...)
	m = append(m, section(10, codeSec)...)
	return withCustomSection(m, "aggrega-test-isolation", tag)
}

// compileDirIn returns a fresh compile directory for the test. It sits one level
// below a temporary directory of its own because the attribution index lives in
// the compile directory's parent: two compile directories taken straight from
// t.TempDir would share one index.
func compileDirIn(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "wasm-compile")
}

// diskCache returns a compilation cache over dir, closed when the test ends. Each
// call is a fresh engine — nothing compiled in memory, whatever is on disk — which
// is the state a new process starts in.
func diskCache(t *testing.T, dir string) wazero.CompilationCache {
	t.Helper()
	c, err := wazero.NewCompilationCacheWithDir(dir)
	if err != nil {
		t.Fatalf("NewCompilationCacheWithDir(%s): %v", dir, err)
	}
	t.Cleanup(func() { closeCache(c) })
	return c
}

// closeCache closes a cache a test opened and drops what this package keeps for it
// for the life of the process — its compiled forms and its first-load record — so
// the test process does not hold every form of every cache its tests opened. Only
// the closing test ever used the cache, so nothing else can reach what is dropped.
func closeCache(c wazero.CompilationCache) {
	compiledPlugins.Range(func(k, _ any) bool {
		if k.(compiledKey).cache == c {
			compiledPlugins.Delete(k)
		}
		return true
	})
	firstLoadDone.Delete(c)
	_ = c.Close(context.Background())
}

// describeWith loads spec under profile through l, calls describe(), closes the
// instance, and returns what describe() returned.
func describeWith(ctx context.Context, l *Loader, profile loadProfile, spec PluginSpec) ([]byte, error) {
	p, err := l.loadCore(ctx, spec, profile, nil, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = p.Close(ctx) }()
	return p.Call(ctx, describeExportName, nil)
}

// uncachedDescribe is describeWith through a Loader with no cache: the output
// every cached load of the same guest and profile must reproduce.
func uncachedDescribe(t *testing.T, profile loadProfile, spec PluginSpec) []byte {
	t.Helper()
	out, err := describeWith(context.Background(), &Loader{}, profile, spec)
	if err != nil {
		t.Fatalf("describe %q under the %s profile with no cache: %v", spec.Name, profile.name, err)
	}
	return out
}

// leafFiles returns every compiled-module entry beneath dir, keyed by its path
// relative to dir, with the file's identity, so a later snapshot can tell an entry
// left alone from one written again. Staging files and lock sidecars are not
// entries (isCompileCacheLeaf).
func leafFiles(t *testing.T, dir string) map[string]os.FileInfo {
	t.Helper()
	out := map[string]os.FileInfo{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !isCompileCacheLeaf(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = info
		return nil
	})
	if err != nil {
		t.Fatalf("list compiled modules under %s: %v", dir, err)
	}
	return out
}

// leafChanges describes how after differs from before — entries added, removed,
// or written again under the same name — or returns "" when nothing changed.
func leafChanges(before, after map[string]os.FileInfo) string {
	var changes []string
	for name, a := range after {
		b, ok := before[name]
		switch {
		case !ok:
			changes = append(changes, "added "+name)
		case !os.SameFile(a, b):
			changes = append(changes, "rewrote "+name)
		}
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			changes = append(changes, "removed "+name)
		}
	}
	sort.Strings(changes)
	return strings.Join(changes, "; ")
}

// removeLeaves removes every compiled-module entry beneath dir.
func removeLeaves(t *testing.T, dir string) {
	t.Helper()
	for name := range leafFiles(t, dir) {
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(name))); err != nil {
			t.Fatalf("remove %s: %v", name, err)
		}
	}
}

// TestConcurrentLoadsOfOneGuestAreRaceFree loads one guest from many goroutines at
// once through one Loader, first against an empty on-disk cache and then, with a
// fresh engine, against the entries that first round wrote. Half the loads use the
// build profile and half the describe-only profile, so the builds of two compiled
// forms of one guest contend as well as the loads of each, and every load after a
// form's first build makes its instance from a form other loads are using at the
// same moment.
//
// Run under the race detector it checks that neither the build path nor the reuse
// path shares anything the detector finds unsynchronized. That check is bounded by
// what the detector can see: the runtime's half-built window, for one, is a race it
// finds ordered on the plugin SDK's path, which is why
// TestFirstLoadsOfAGuestFromDiskTakeTurns pins that window by its effect instead.
// Run without it, it still proves every load succeeds with exactly the output a
// load with no cache produces, and that the warm round read every compiled module
// from disk rather than compiling one.
func TestConcurrentLoadsOfOneGuestAreRaceFree(t *testing.T) {
	ctx := context.Background()
	spec := PluginSpec{Name: "policy-go", Bytes: isolatedGuest(t, "policy-go.wasm", t.Name())}
	profiles := []loadProfile{buildProfile, describeProfile}
	want := map[string][]byte{}
	for _, profile := range profiles {
		want[profile.name] = uncachedDescribe(t, profile, spec)
	}

	const workers = 8
	dir := compileDirIn(t)
	var afterCold map[string]os.FileInfo
	for _, round := range []string{"cold", "warm"} {
		l := &Loader{Cache: diskCache(t, dir), compileDir: dir}
		errs := make([]error, workers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				profile := profiles[i%len(profiles)]
				out, err := describeWith(ctx, l, profile, spec)
				if err == nil && !bytes.Equal(out, want[profile.name]) {
					err = fmt.Errorf("describe() under the %s profile differs from a load with no cache\n got: %s\nwant: %s",
						profile.name, out, want[profile.name])
				}
				errs[i] = err
			}(i)
		}
		close(start)
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Errorf("%s round, load %d: %v", round, i, err)
			}
		}
		if t.Failed() {
			return
		}

		leaves := leafFiles(t, dir)
		switch round {
		case "cold":
			if len(leaves) == 0 {
				t.Fatal("the cold round wrote no compiled module to disk, so the warm round would read nothing from it")
			}
			afterCold = leaves
		case "warm":
			if changes := leafChanges(afterCold, leaves); changes != "" {
				t.Fatalf("the warm round was meant to read every compiled module from disk, but it changed the cache: %s", changes)
			}
		}
	}
}

// Environment the first-load round's worker process reads. Absent them, the
// worker test is inert.
const (
	firstLoadWorkerDirEnv    = "AGGREGA_FIRST_LOAD_WORKER_DIR"
	firstLoadWorkerTargetEnv = "AGGREGA_FIRST_LOAD_WORKER_TARGET"
	firstLoadWorkerOtherEnv  = "AGGREGA_FIRST_LOAD_WORKER_OTHER"
)

// firstLoadRound opens a fresh cache over dir — which already holds every compiled
// module target and other need — completes one load of other, and then starts
// loads loads of target at once, each under its own memory ceiling. It returns the
// first failure, or nil when every load succeeded with the same describe() output.
//
// The load of other comes first so the runtime kernel every guest shares is
// finished in this engine and the kernel half of the first-load gate has opened;
// what orders the loads of target is then the per-guest half alone. Distinct
// ceilings are distinct compiled forms, so no single-build slot orders them
// either.
func firstLoadRound(ctx context.Context, dir string, target, other []byte, loads int) error {
	cache, err := wazero.NewCompilationCacheWithDir(dir)
	if err != nil {
		return err
	}
	defer closeCache(cache)
	l := &Loader{Cache: cache}

	if _, err := describeWith(ctx, l, buildProfile, PluginSpec{Name: "other", Bytes: other}); err != nil {
		return fmt.Errorf("load the other guest: %w", err)
	}

	outs := make([][]byte, loads)
	errs := make([]error, loads)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < loads; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			spec := PluginSpec{Name: "target", Bytes: target, MaxMemoryPages: DefaultMaxMemoryPages + 1 + uint32(i)}
			outs[i], errs[i] = describeWith(ctx, l, buildProfile, spec)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			return fmt.Errorf("load %d of the target guest: %w", i, err)
		}
		if !bytes.Equal(outs[i], outs[0]) {
			return fmt.Errorf("load %d of the target guest described it differently from load 0\n got: %s\nwant: %s", i, outs[i], outs[0])
		}
	}
	return nil
}

// firstLoadWindowTypes is how many function types the target of
// TestFirstLoadsOfAGuestFromDiskTakeTurns declares, which sets how long the
// half-built window stays open. Measured under the race detector on a 24-core
// machine, with a thousand types the runtime took 49 to 75 ms after publishing the
// target to finish its preambles, and the round's other builds called describe() 1
// to 74 ms after the publish, each before the preamble it read had been written. A
// guest of 16 types held the window open for under 3 ms, and the first other build
// arrived 16 ms after it had closed. The count only has to keep the window open
// well past the time the other builds take to arrive; it measures nothing.
const firstLoadWindowTypes = 1000

// TestFirstLoadsOfAGuestFromDiskTakeTurns pins the reason firstLoadGate exists.
// The runtime puts a compiled module it reads from the on-disk cache into the
// engine's shared table before that module's entry preambles are compiled, so a
// second build of the same guest in that window finds a module that is not
// finished, and a call made from it reads a preamble that does not exist yet: the
// runtime panics, and that load fails.
//
// The window lasts as long as the runtime takes to compile one preamble per
// declared function type. For a guest of a few dozen types that is about a
// millisecond, and a concurrent build finishes decoding the guest and looks it up
// well after the window has closed — so a test of such a guest passes with the
// gate taken out, and the race detector does not report it either: through the
// plugin SDK it finds those accesses ordered. The target here is
// manyTypesGuest with firstLoadWindowTypes types and its one export declared last,
// which holds the window open for the other builds of a round to land in.
//
// Each round runs firstLoadRound against a directory that already holds both
// guests: a fresh engine, nothing compiled in memory, everything on disk — the
// state a new process starts in, and the worker runs one round in a new process
// as well, for the literal case. With the gate, the builds of the target take
// turns and every load succeeds. With the gate taken out, or its per-guest half
// alone, the builds that find the target while another is still compiling its
// preambles fail their describe() call, with or without the race detector, which
// reports the read as well when it is on. The kernel half is not reached: the
// round completes a load of the other guest first so that half is open, and
// TestFirstLoadGateOrdersBuilds pins it.
func TestFirstLoadsOfAGuestFromDiskTakeTurns(t *testing.T) {
	ctx := context.Background()
	const (
		rounds = 6
		loads  = 16
	)
	target := manyTypesGuest(firstLoadWindowTypes, t.Name()+"/target")
	other := manyTypesGuest(1, t.Name()+"/other")
	dir := compileDirIn(t)

	// Put both guests' compiled modules and the kernel on disk.
	warm := &Loader{Cache: diskCache(t, dir), compileDir: dir}
	for _, g := range [][]byte{target, other} {
		if _, err := describeWith(ctx, warm, buildProfile, PluginSpec{Name: "warm", Bytes: g}); err != nil {
			t.Fatalf("warm the on-disk cache: %v", err)
		}
	}
	onDisk := leafFiles(t, dir)
	if len(onDisk) == 0 {
		t.Fatal("warming wrote no compiled module to disk, so no round would read one from it")
	}

	for r := 0; r < rounds; r++ {
		if err := firstLoadRound(ctx, dir, target, other, loads); err != nil {
			t.Fatalf("round %d: %v", r, err)
		}
	}

	// The same round in a process of its own.
	scratch := t.TempDir()
	targetPath := filepath.Join(scratch, "target.wasm")
	otherPath := filepath.Join(scratch, "other.wasm")
	if err := os.WriteFile(targetPath, target, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherPath, other, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestFirstLoadsOfAGuestFromDiskTakeTurnsWorker$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(),
		firstLoadWorkerDirEnv+"="+dir,
		firstLoadWorkerTargetEnv+"="+targetPath,
		firstLoadWorkerOtherEnv+"="+otherPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the round in a new process failed: %v\noutput:\n%s", err, out)
	} else if !strings.Contains(string(out), "--- PASS: TestFirstLoadsOfAGuestFromDiskTakeTurnsWorker") {
		t.Fatalf("the worker process did not run its round\noutput:\n%s", out)
	}

	// Every round was meant to read from disk; one that compiled would have tested
	// nothing about the disk-hit window.
	if changes := leafChanges(onDisk, leafFiles(t, dir)); changes != "" {
		t.Fatalf("a round compiled instead of reading from disk: %s", changes)
	}
}

// TestFirstLoadsOfAGuestFromDiskTakeTurnsWorker is the new-process half of
// TestFirstLoadsOfAGuestFromDiskTakeTurns, and inert unless that test started it.
func TestFirstLoadsOfAGuestFromDiskTakeTurnsWorker(t *testing.T) {
	dir := os.Getenv(firstLoadWorkerDirEnv)
	if dir == "" {
		t.Skip("not a first-load worker invocation")
	}
	target, err := os.ReadFile(os.Getenv(firstLoadWorkerTargetEnv))
	if err != nil {
		t.Fatal(err)
	}
	other, err := os.ReadFile(os.Getenv(firstLoadWorkerOtherEnv))
	if err != nil {
		t.Fatal(err)
	}
	if err := firstLoadRound(context.Background(), dir, target, other, 16); err != nil {
		t.Fatal(err)
	}
}

// firstLoadGateLiveness bounds how long one build inside firstLoadGate waits for a
// second build to enter the gate beside it. It is a liveness ceiling, not a
// performance budget: when the gate holds the second build out, the first waits
// the whole ceiling and then leaves; when the gate lets it in, it enters as soon
// as a goroutine is scheduled. That separation is structural, so the ceiling only
// has to exceed any plausible scheduling delay; the scheduling delay itself was
// not measured here.
const firstLoadGateLiveness = 2 * time.Second

// buildsOverlap runs a build of guest a through l's first-load gate that stays
// inside it until a build of guest b enters as well, or until the liveness
// ceiling passes, and reports whether b entered while a was still inside.
func buildsOverlap(t *testing.T, l *Loader, a, b string) bool {
	t.Helper()
	aInside := make(chan struct{})
	bInside := make(chan struct{})
	overlapped := make(chan bool, 1)
	aDone := make(chan error, 1)
	bDone := make(chan error, 1)

	go func() {
		_, err := l.firstLoadGate(a, func() (*extism.CompiledPlugin, error) {
			close(aInside)
			select {
			case <-bInside:
				overlapped <- true
			case <-time.After(firstLoadGateLiveness):
				overlapped <- false
			}
			return nil, nil
		})
		aDone <- err
	}()
	<-aInside
	go func() {
		_, err := l.firstLoadGate(b, func() (*extism.CompiledPlugin, error) {
			close(bInside)
			return nil, nil
		})
		bDone <- err
	}()
	if err := <-aDone; err != nil {
		t.Fatalf("build of %s: %v", a, err)
	}
	if err := <-bDone; err != nil {
		t.Fatalf("build of %s: %v", b, err)
	}
	return <-overlapped
}

// TestFirstLoadGateOrdersBuilds pins what firstLoadGate lets run together: builds
// of one guest never, and builds of different guests only once a build against the
// same cache has completed, because until then each of them may be publishing the
// runtime kernel they all compile. A failed build completes nothing, so it leaves
// the gate closed. The builds are stand-ins that compile nothing; what is measured
// is only whether the second enters while the first is inside.
func TestFirstLoadGateOrdersBuilds(t *testing.T) {
	completed := func(l *Loader) {
		_, _ = l.firstLoadGate("earlier", func() (*extism.CompiledPlugin, error) { return nil, nil })
	}
	failed := func(l *Loader) {
		_, _ = l.firstLoadGate("earlier", func() (*extism.CompiledPlugin, error) { return nil, errors.New("the build failed") })
	}
	cases := []struct {
		name        string
		before      func(l *Loader) // run against the fresh cache before the pair
		sameGuest   bool
		wantOverlap bool
	}{
		{name: "different guests, no build completed yet", wantOverlap: false},
		{name: "one guest, no build completed yet", sameGuest: true, wantOverlap: false},
		{name: "different guests, after a completed build", before: completed, wantOverlap: true},
		{name: "one guest, after a completed build", before: completed, sameGuest: true, wantOverlap: false},
		{name: "different guests, after a failed build", before: failed, wantOverlap: false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cache := wazero.NewCompilationCache()
			t.Cleanup(func() { closeCache(cache) })
			l := &Loader{Cache: cache}
			if tc.before != nil {
				tc.before(l)
			}
			a := fmt.Sprintf("%s/%d/a", t.Name(), i)
			b := fmt.Sprintf("%s/%d/b", t.Name(), i)
			if tc.sameGuest {
				b = a
			}
			if got := buildsOverlap(t, l, a, b); got != tc.wantOverlap {
				t.Fatalf("second build entered the gate while the first was inside: %v; want %v", got, tc.wantOverlap)
			}
		})
	}
}

// TestSharedEngineKeepsEveryModuleItCompiled pins the runtime behaviour the
// first-load gate's kernel half rests on: once a module is compiled into a cache's
// engine it stays there, whatever this package closes. If it did not, a module
// dropped from memory would be read from disk again by a later build — reopening
// the half-built window after the gate had stopped guarding it.
//
// The engine counts the compiles of each module it holds and drops a module once
// every one of them has been closed. Each build of a compiled form compiles the
// guest and the kernel into the cache's engine, and closing the form closes only
// its runtime, which releases none of those compiles while a cache shares the
// engine — so nothing this package compiles is dropped while the cache is open.
//
// Because the engine counts, a close that released a compile would go unseen
// while any other build still held the module. So each subtest ends one use of a
// compiled module this package makes — an instance of a shared compiled form
// closed, or a compiled form a load owns closed with its instance — in a cache of
// its own, where that use's build is the only compile of the guest and of the
// kernel. It then removes every entry from disk and builds the guest's compiled
// form again against the same cache, outside this package's table. Nothing may be
// written back: the guest and the kernel both came from the engine's memory.
//
// A control then shows the check can see a write-back when one happens. It cannot
// drop the guest itself, whose compiles stay held by the compiled forms built
// before it. Instead a copy of the guest made distinct by a custom section is
// compiled into the same engine once, its entry is removed from disk, and that one
// compile is closed, which drops the copy; compiling it again must write back
// exactly its own entry.
func TestSharedEngineKeepsEveryModuleItCompiled(t *testing.T) {
	cases := []struct {
		name string
		// end loads guest through l and ends that use of it as this package does.
		end func(t *testing.T, l *Loader, guest []byte)
	}{
		{
			name: "an instance of a shared compiled form closed",
			end: func(t *testing.T, l *Loader, guest []byte) {
				ctx := context.Background()
				shared, err := l.Load(ctx, PluginSpec{Name: "shared", Bytes: guest})
				if err != nil {
					t.Fatalf("load the shared compiled form: %v", err)
				}
				if shared.compiled != nil {
					t.Fatal("a load with no plugin configuration through a Loader with a cache must share its compiled form, or this subtest closes no shared instance")
				}
				if _, err := shared.Call(ctx, describeExportName, nil); err != nil {
					t.Fatalf("describe() on the shared compiled form: %v", err)
				}
				if err := shared.Close(ctx); err != nil {
					t.Fatalf("close the shared instance: %v", err)
				}
			},
		},
		{
			name: "a compiled form a load owns closed with its instance",
			end: func(t *testing.T, l *Loader, guest []byte) {
				ctx := context.Background()
				owned, err := l.Load(ctx, PluginSpec{Name: "owned", Bytes: guest, ConfigKV: map[string]string{"owned": "true"}})
				if err != nil {
					t.Fatalf("load an owned compiled form: %v", err)
				}
				if owned.compiled == nil {
					t.Fatal("a load carrying a plugin configuration must own its compiled form, or this subtest closes no owned form")
				}
				if _, err := owned.Call(ctx, describeExportName, nil); err != nil {
					t.Fatalf("describe() on the owned compiled form: %v", err)
				}
				if err := owned.Close(ctx); err != nil {
					t.Fatalf("close the owned instance and its compiled form: %v", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			guest := isolatedGuest(t, "policy-go.wasm", t.Name())
			dir := compileDirIn(t)
			cache := diskCache(t, dir)

			tc.end(t, &Loader{Cache: cache, compileDir: dir}, guest)

			if len(leafFiles(t, dir)) == 0 {
				t.Fatal("the load wrote no compiled module to disk, so removing the entries would prove nothing")
			}
			removeLeaves(t, dir)

			config := wazero.NewRuntimeConfig().WithCloseOnContextDone(true).WithCompilationCache(cache)
			again, err := extism.NewCompiledPlugin(ctx,
				extism.Manifest{Wasm: []extism.Wasm{extism.WasmData{Data: guest}}},
				extism.PluginConfig{EnableWasi: true, RuntimeConfig: config},
				nil)
			if err != nil {
				t.Fatalf("build the compiled form again: %v", err)
			}
			defer func() { _ = again.Close(ctx) }()
			if leaves := leafFiles(t, dir); len(leaves) != 0 {
				t.Fatalf("the engine no longer held a module it had compiled: building the form again recompiled and wrote back %s",
					leafChanges(nil, leaves))
			}

			// Control: a module the engine does drop — its one compile closed — is
			// compiled and written back.
			rt := wazero.NewRuntimeWithConfig(ctx, config)
			defer func() { _ = rt.Close(ctx) }()
			copyOfGuest := withCustomSection(guest, "aggrega-test-isolation", t.Name()+"/control")
			held, err := rt.CompileModule(ctx, copyOfGuest)
			if err != nil {
				t.Fatalf("control: compile the copy of the guest: %v", err)
			}
			removeLeaves(t, dir)
			if err := held.Close(ctx); err != nil {
				t.Fatalf("control: close the copy's one compile, dropping it from the engine: %v", err)
			}
			if _, err := rt.CompileModule(ctx, copyOfGuest); err != nil {
				t.Fatalf("control: compile the copy again after the engine dropped it: %v", err)
			}
			if leaves := leafFiles(t, dir); len(leaves) != 1 {
				changes := leafChanges(nil, leaves)
				if changes == "" {
					changes = "none"
				}
				t.Fatalf("control: after the engine dropped the copy, compiling it again should write back exactly its own entry; the change to the cache: %s",
					changes)
			}
		})
	}
}

// TestHostFunctionsServeTheInstanceThatCalls proves a compiled form shared by many
// instances binds no instance into its host functions. Six loads of one guest make
// six instances from one compiled form; each is given a source of its own, and all
// call at once a guest export that reads its source through a host function. Each
// must read its own: a host function holding the instance it was bound for, or
// finding no instance, would hand every caller one source or none.
func TestHostFunctionsServeTheInstanceThatCalls(t *testing.T) {
	ctx := context.Background()
	guest := isolatedGuest(t, "read-source-probe-go.wasm", t.Name())
	cache := wazero.NewCompilationCache()
	t.Cleanup(func() { closeCache(cache) })
	l := &Loader{Cache: cache}

	const instances = 6
	plugins := make([]*Plugin, instances)
	for i := range plugins {
		p, err := l.Load(ctx, PluginSpec{Name: "read-source-probe-go", Bytes: guest})
		if err != nil {
			t.Fatalf("load %d: %v", i, err)
		}
		t.Cleanup(func() { _ = p.Close(ctx) })
		if p.compiled != nil {
			t.Fatalf("load %d owns its compiled form; every load through a Loader with a cache must share one", i)
		}
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "index.txt"), []byte("chosen.txt\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "chosen.txt"), []byte(fmt.Sprintf("payload-%d", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		p.SetSourceRoot(root)
		plugins[i] = p
	}
	forms := 0
	compiledPlugins.Range(func(k, _ any) bool {
		if k.(compiledKey).cache == cache {
			forms++
		}
		return true
	})
	if forms != 1 {
		t.Fatalf("the six loads were made from %d compiled forms; want 1", forms)
	}

	outs := make([][]byte, instances)
	errs := make([]error, instances)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, p := range plugins {
		wg.Add(1)
		go func(i int, p *Plugin) {
			defer wg.Done()
			<-start
			outs[i], errs[i] = p.Call(ctx, "probe", []byte("index.txt"))
		}(i, p)
	}
	close(start)
	wg.Wait()
	for i := range plugins {
		if errs[i] != nil {
			t.Errorf("instance %d: probe: %v", i, errs[i])
			continue
		}
		if want := fmt.Sprintf("payload-%d", i); string(outs[i]) != want {
			t.Errorf("instance %d read %q through its host function; want its own source's %q", i, outs[i], want)
		}
	}
}

// TestLoadsAfterTheFirstLoadsContextIsCancelledStillWork proves nothing the first
// load of a guest builds — the compiled form, its runtime, the cache's engine —
// keeps and depends on that load's context. The first load runs under a context
// that is then cancelled; a call on that cancelled context is refused, which shows
// the cancellation is real, and a load made afterwards, and an instance made
// before the cancellation, both still work from the same compiled form.
func TestLoadsAfterTheFirstLoadsContextIsCancelledStillWork(t *testing.T) {
	ctx := context.Background()
	spec := PluginSpec{Name: "policy-go", Bytes: isolatedGuest(t, "policy-go.wasm", t.Name())}
	want := uncachedDescribe(t, buildProfile, spec)
	cache := wazero.NewCompilationCache()
	t.Cleanup(func() { closeCache(cache) })
	l := &Loader{Cache: cache}

	first, cancel := context.WithCancel(ctx)
	firstPlugin, err := l.Load(first, spec)
	if err != nil {
		cancel()
		t.Fatalf("first load: %v", err)
	}
	t.Cleanup(func() { _ = firstPlugin.Close(ctx) })
	earlier, err := l.Load(ctx, spec)
	if err != nil {
		cancel()
		t.Fatalf("load before the cancellation: %v", err)
	}
	t.Cleanup(func() { _ = earlier.Close(ctx) })
	cancel()

	if _, err := firstPlugin.Call(first, describeExportName, nil); err == nil {
		t.Fatal("a call on the cancelled context succeeded, so the cancellation this test depends on did not take effect")
	}

	later, err := l.Load(ctx, spec)
	if err != nil {
		t.Fatalf("load after the first load's context was cancelled: %v", err)
	}
	t.Cleanup(func() { _ = later.Close(ctx) })
	for name, p := range map[string]*Plugin{"a load after the cancellation": later, "a load before it": earlier} {
		out, err := p.Call(ctx, describeExportName, nil)
		if err != nil {
			t.Errorf("%s: describe(): %v", name, err)
			continue
		}
		if !bytes.Equal(out, want) {
			t.Errorf("%s: describe() differs from a load with no cache\n got: %s\nwant: %s", name, out, want)
		}
	}
}

// attributionEntries returns the attribution index beside dir in a form two runs
// can be compared in: every entry, referrers sorted, ordered by leaf.
func attributionEntries(t *testing.T, dir string) []attribindex.Entry {
	t.Helper()
	ix, err := attribindex.Load(attribindex.PathForCompileDir(dir))
	if err != nil {
		t.Fatalf("load the attribution index beside %s: %v", dir, err)
	}
	entries := ix.Entries()
	for i := range entries {
		entries[i].Referrers = append([]string(nil), entries[i].Referrers...)
		sort.Strings(entries[i].Referrers)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Leaf < entries[j].Leaf })
	return entries
}

// forgetObservations removes the guests from the process-wide record of guests
// whose first compile has been observed, so their next first load into another
// compile directory is observed as a new process would observe it. The record is
// keyed by guest alone because a process has exactly one compile directory; a
// test that opens a second one in the same process, for guests it has already
// loaded, has to take back what that second process would not have seen.
func forgetObservations(specs []PluginSpec) {
	for _, spec := range specs {
		attribObserved.Delete(digest.ContentDigest(spec.Bytes))
	}
}

// TestAttributionAfterConcurrentFirstLoadsEqualsSerial proves the attribution index
// records the same thing whether a set of guests is first loaded one at a time or
// all at once. The concurrent run is where the observation windows of different
// guests' cold compiles could interleave and attribute one guest's entry to
// another, or to none; each guest is loaded twice, so the second, warm load's
// place in the run varies too.
//
// The two runs stand for two processes loading the same guests into two fresh
// compile directories, and each must observe every guest's first compile: the
// test forgets the serial run's observations before the concurrent run, and
// checks each run observed one first compile per guest, so a run that observed
// nothing cannot pass as a match.
//
// With the attribution mutex taken out, the guests' first compiles after the
// first one run at once, each window between its before and after listings sees
// another guest's new entry, and a window that sees two new entries records
// nothing. That makes the concurrent index miss entries the serial one has. The
// compiles are started together and each takes far longer than the listing that
// opens its window, which is what makes the overlap near-certain; it is not
// guaranteed by construction.
func TestAttributionAfterConcurrentFirstLoadsEqualsSerial(t *testing.T) {
	ctx := context.Background()
	const guests = 4
	var specs []PluginSpec
	for i := 0; i < guests; i++ {
		specs = append(specs, PluginSpec{
			Name:  fmt.Sprintf("guest-%d", i),
			Bytes: isolatedGuest(t, "policy-go.wasm", fmt.Sprintf("%s/%d", t.Name(), i)),
		})
	}
	load := func(l *Loader, spec PluginSpec) error {
		p, err := l.Load(ctx, spec)
		if err != nil {
			return err
		}
		return p.Close(ctx)
	}

	// observed runs one of the two runs and fails unless it observed exactly one
	// first compile per guest.
	observed := func(run string, loads func()) {
		t.Helper()
		before := atomic.LoadInt64(&attribSnapshotCount)
		loads()
		if got := atomic.LoadInt64(&attribSnapshotCount) - before; got != guests {
			t.Fatalf("the %s run observed %d first compiles; want %d, one per guest, or its index is not the one a new process would write",
				run, got, guests)
		}
	}

	forgetObservations(specs)
	serialDir := compileDirIn(t)
	serial := &Loader{Cache: diskCache(t, serialDir), compileDir: serialDir}
	observed("serial", func() {
		for _, spec := range specs {
			for n := 0; n < 2; n++ {
				if err := load(serial, spec); err != nil {
					t.Fatalf("serial load of %s: %v", spec.Name, err)
				}
			}
		}
	})

	forgetObservations(specs)
	concurrentDir := compileDirIn(t)
	concurrent := &Loader{Cache: diskCache(t, concurrentDir), compileDir: concurrentDir}
	observed("concurrent", func() {
		errs := make([]error, 2*guests)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 2*guests; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				errs[i] = load(concurrent, specs[i%guests])
			}(i)
		}
		close(start)
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("concurrent load %d: %v", i, err)
			}
		}
	})

	want := attributionEntries(t, serialDir)
	if len(want) != guests+1 {
		t.Fatalf("the serial run attributed %d entries; want %d — the kernel and one per guest — or the comparison proves nothing: %+v",
			len(want), guests+1, want)
	}
	got := attributionEntries(t, concurrentDir)
	if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
		t.Fatalf("the attribution index after concurrent first loads differs from the serial run's\n got: %+v\nwant: %+v", got, want)
	}
}

// TestSizeCapDuringConcurrentLoadsBreaksNoLoad holds a compile cache to a one-byte
// cap, over and over, while goroutines load guests from it with a fresh engine each
// time, so every load reads from disk or compiles and writes back, and the cap keeps
// removing what they read and write. Every load must succeed with the output a load
// with no cache produces, and an instance loaded before any removal must keep
// working after its entry is gone. A removal only ever costs a recompile: a reader
// holds the entry it opened, and a compile's staging file is not an entry the cap
// removes.
func TestSizeCapDuringConcurrentLoadsBreaksNoLoad(t *testing.T) {
	ctx := context.Background()
	const (
		guests     = 3
		iterations = 4
	)
	var specs []PluginSpec
	for i := 0; i < guests; i++ {
		specs = append(specs, PluginSpec{
			Name:  fmt.Sprintf("guest-%d", i),
			Bytes: isolatedGuest(t, "policy-go.wasm", fmt.Sprintf("%s/%d", t.Name(), i)),
		})
	}
	want := uncachedDescribe(t, buildProfile, specs[0])
	dir := compileDirIn(t)

	held, err := (&Loader{Cache: diskCache(t, dir), compileDir: dir}).Load(ctx, specs[0])
	if err != nil {
		t.Fatalf("load the instance held across the run: %v", err)
	}
	t.Cleanup(func() { _ = held.Close(ctx) })

	stop := make(chan struct{})
	reclaimed := make(chan int, 1)
	go func() {
		removed := 0
		for {
			select {
			case <-stop:
				reclaimed <- removed
				return
			default:
			}
			res, _ := ReclaimCompileCache(dir, 1)
			removed += len(res.Removed)
		}
	}()

	errs := make([]error, guests)
	var wg sync.WaitGroup
	for g := 0; g < guests; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for n := 0; n < iterations; n++ {
				cache, err := wazero.NewCompilationCacheWithDir(dir)
				if err != nil {
					errs[g] = err
					return
				}
				out, err := describeWith(ctx, &Loader{Cache: cache, compileDir: dir}, buildProfile, specs[g])
				closeCache(cache)
				if err != nil {
					errs[g] = fmt.Errorf("iteration %d: %w", n, err)
					return
				}
				if !bytes.Equal(out, want) {
					errs[g] = fmt.Errorf("iteration %d: describe() differs from a load with no cache\n got: %s\nwant: %s", n, out, want)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(stop)
	removed := <-reclaimed

	for g, err := range errs {
		if err != nil {
			t.Errorf("guest %d: %v", g, err)
		}
	}
	if removed == 0 {
		t.Fatal("the cap removed nothing while the loads ran, so this test proved nothing about removal during a load")
	}
	out, err := held.Call(ctx, describeExportName, nil)
	if err != nil {
		t.Fatalf("an instance loaded before its entry was removed stopped working: %v", err)
	}
	if !bytes.Equal(out, want) {
		t.Fatalf("an instance loaded before its entry was removed describes itself differently\n got: %s\nwant: %s", out, want)
	}
}
