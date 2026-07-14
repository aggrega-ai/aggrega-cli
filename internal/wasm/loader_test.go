// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"

	extism "github.com/extism/go-sdk"
)

func TestTranslateRejectsAllowedHosts(t *testing.T) {
	spec := PluginSpec{
		Name:         "test-plugin",
		Path:         "fixture.wasm",
		AllowedHosts: []string{"example.com"},
	}
	_, err := translateToManifest(spec)
	if !errors.Is(err, ErrWasmManifestRejected) {
		t.Fatalf("expected ErrWasmManifestRejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "AllowedHosts") {
		t.Errorf("error should name AllowedHosts capability; got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "test-plugin") {
		t.Errorf("error should name the plugin identifier; got: %s", err.Error())
	}
}

func TestTranslateRejectsAllowedPaths(t *testing.T) {
	// AllowedPaths is set only via direct manifest construction; the
	// public PluginSpec API does not expose a way to set it (by design;
	// AOT-purity discipline). This test exercises the firewall against
	// a manifest constructed directly with AllowedPaths non-empty.
	manifest := extism.Manifest{
		AllowedPaths: map[string]string{"/etc": "/etc"},
	}
	err := enforceAOTPurityFirewall(manifest, "test-plugin")
	if !errors.Is(err, ErrWasmManifestRejected) {
		t.Fatalf("expected ErrWasmManifestRejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "AllowedPaths") {
		t.Errorf("error should name AllowedPaths capability; got: %s", err.Error())
	}
}

func TestHostFunctionsSubsetRejectsOutsideAllowlist(t *testing.T) {
	err := validateHostFunctionsSubset("test-plugin", []string{"aggrega_log", "rogue_function"})
	if !errors.Is(err, ErrWasmManifestRejected) {
		t.Fatalf("expected ErrWasmManifestRejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "rogue_function") {
		t.Errorf("error should name the violating host function; got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "test-plugin") {
		t.Errorf("error should name the plugin identifier; got: %s", err.Error())
	}
}

func TestHostFunctionsSubsetAcceptsAllowlisted(t *testing.T) {
	if err := validateHostFunctionsSubset("test-plugin", []string{"aggrega_log", "aggrega_emit_warning"}); err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
}

func TestTranslateMinimalSpecPasses(t *testing.T) {
	spec := PluginSpec{
		Name: "test-plugin",
		Path: "fixture.wasm",
	}
	manifest, err := translateToManifest(spec)
	if err != nil {
		t.Fatalf("expected success for minimal spec, got error: %v", err)
	}
	if len(manifest.Wasm) != 1 {
		t.Errorf("expected exactly one wasm source, got %d", len(manifest.Wasm))
	}
	if len(manifest.AllowedHosts) != 0 {
		t.Errorf("expected empty AllowedHosts, got %v", manifest.AllowedHosts)
	}
	if len(manifest.AllowedPaths) != 0 {
		t.Errorf("expected empty AllowedPaths, got %v", manifest.AllowedPaths)
	}
}

func TestTranslateAppliesMemoryAndTimeout(t *testing.T) {
	spec := PluginSpec{
		Name:           "test-plugin",
		Path:           "fixture.wasm",
		MaxMemoryPages: 32,
		TimeoutMs:      1500,
	}
	manifest, err := translateToManifest(spec)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if manifest.Memory == nil {
		t.Fatalf("expected Memory to be set when MaxMemoryPages != 0")
	}
	if manifest.Memory.MaxPages != 32 {
		t.Errorf("expected MaxPages=32, got %d", manifest.Memory.MaxPages)
	}
	if manifest.Timeout != 1500 {
		t.Errorf("expected Timeout=1500, got %d", manifest.Timeout)
	}
}

// TestTranslateAppliesDefaultLimitsWhenZero is the behavior-change proof: a spec
// that declares no limits yields a manifest carrying the platform default cap on
// BOTH memory and per-call wall time — never an unbounded memory (nil Memory) or
// an uncancellable call (zero Timeout). This is the floor every load path gets
// when the entity authors no runtime limits.
func TestTranslateAppliesDefaultLimitsWhenZero(t *testing.T) {
	spec := PluginSpec{
		Name: "test-plugin",
		Path: "fixture.wasm",
		// MaxMemoryPages and TimeoutMs left zero on purpose.
	}
	manifest, err := translateToManifest(spec)
	if err != nil {
		t.Fatalf("expected success for a zero-limit spec, got error: %v", err)
	}
	if manifest.Memory == nil {
		t.Fatalf("zero MaxMemoryPages must apply the default cap, not leave memory unbounded (nil Memory)")
	}
	if manifest.Memory.MaxPages != DefaultMaxMemoryPages {
		t.Errorf("expected MaxPages=%d (DefaultMaxMemoryPages), got %d", DefaultMaxMemoryPages, manifest.Memory.MaxPages)
	}
	if manifest.Timeout != DefaultTimeoutMs {
		t.Errorf("expected Timeout=%d (DefaultTimeoutMs), got %d", DefaultTimeoutMs, manifest.Timeout)
	}
}

func TestAllowlistContainsBaselineHostFunctions(t *testing.T) {
	for _, name := range []string{"aggrega_log", "aggrega_emit_warning"} {
		if _, ok := hostFunctionAllowlist[name]; !ok {
			t.Errorf("baseline host function %q missing from allowlist", name)
		}
	}
}

// TestCallWithPanicRecoveryWrapsPanic exercises the panic-recovery wrap
// without needing a real Wasm-side panic source. The wrap converts a
// Go-side panic into an ErrWasmGuestPanic-wrapped error; this test
// passes a function that panics directly and asserts the wrap shape.
//
// Coverage gap: this test verifies the wrap shape only. It does NOT
// prove that an actual Wasm trap (for example the unreachable
// instruction in guest code, divide-by-zero, out-of-bounds memory
// access) propagates through Extism's call boundary as a panic that
// reaches this wrap. Extism translates most Wasm traps into Go errors
// rather than Go panics; the wrap is the safety net behind that error
// path. End-to-end coverage of a real Wasm-side panic source requires
// a hand-authored panic.wasm fixture — see testdata/README.md for the
// authoring path when a Wasm toolchain is available.
func TestCallWithPanicRecoveryWrapsPanic(t *testing.T) {
	output, err := callWithPanicRecovery("test-plugin", "test_function", func() ([]byte, error) {
		panic("synthetic test panic")
	})
	if err == nil {
		t.Fatalf("expected error from panicking function, got nil with output %q", output)
	}
	if !errors.Is(err, ErrWasmGuestPanic) {
		t.Errorf("expected error to wrap ErrWasmGuestPanic; got: %v", err)
	}
	if !strings.Contains(err.Error(), "test-plugin") {
		t.Errorf("error should name the plugin identifier; got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "test_function") {
		t.Errorf("error should name the called function; got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "synthetic test panic") {
		t.Errorf("error should include the panic value; got: %s", err.Error())
	}
}

func TestCallWithPanicRecoveryPassesNormalReturns(t *testing.T) {
	output, err := callWithPanicRecovery("test-plugin", "test_function", func() ([]byte, error) {
		return []byte("expected output"), nil
	})
	if err != nil {
		t.Fatalf("expected nil error from non-panicking function, got %v", err)
	}
	if string(output) != "expected output" {
		t.Errorf("expected output to pass through; got %q", string(output))
	}
}

func TestCallWithPanicRecoveryPassesNormalErrors(t *testing.T) {
	sentinel := fmt.Errorf("regular error path")
	_, err := callWithPanicRecovery("test-plugin", "test_function", func() ([]byte, error) {
		return nil, sentinel
	})
	if err != sentinel {
		t.Errorf("expected sentinel error to pass through unwrapped; got %v", err)
	}
	if errors.Is(err, ErrWasmGuestPanic) {
		t.Error("non-panic error path should not wrap ErrWasmGuestPanic")
	}
}

// --- Host-function-body tests ----------------------------------------

// fakeMemoryReader is a pluginMemoryReader test double the host-
// function body tests use in place of *extism.CurrentPlugin. The SDK
// exposes no public constructor or test double for *CurrentPlugin;
// the bodies are tested via the interface abstraction in hostfuncs.go
// so the read-format-emit path is exercisable in isolation.
type fakeMemoryReader struct {
	// stringsByOffset returns the string the body should observe at a
	// given stack offset. Missing entries cause ReadString to return
	// an error simulating an OOB or invalid-pointer read.
	stringsByOffset map[uint64]string
	// errOnOffset, if non-zero, forces ReadString to fail at that
	// specific offset even if stringsByOffset has an entry for it.
	// Used to exercise the "one of three reads failed" branches of
	// handleAggregaEmitWarning.
	errOnOffset uint64
}

func (f *fakeMemoryReader) ReadString(offset uint64) (string, error) {
	if f.errOnOffset != 0 && offset == f.errOnOffset {
		return "", fmt.Errorf("simulated read failure at offset %d", offset)
	}
	s, ok := f.stringsByOffset[offset]
	if !ok {
		return "", fmt.Errorf("no string fixture at offset %d", offset)
	}
	return s, nil
}

// captureLogOutput redirects log.Default()'s destination to a buffer
// for the duration of the test, restoring the prior destination via
// t.Cleanup. Tests that consume the captured output are NOT marked
// t.Parallel — log.SetOutput modifies process-wide state and parallel
// tests would interfere.
func captureLogOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	prev := log.Writer()
	prevFlags := log.Flags()
	buf := &bytes.Buffer{}
	log.SetOutput(buf)
	log.SetFlags(0) // strip timestamp prefix for predictable assertions
	t.Cleanup(func() {
		log.SetOutput(prev)
		log.SetFlags(prevFlags)
	})
	return buf
}

func TestPluginWarningsReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()
	p := &Plugin{name: "test-plugin"}
	p.appendWarning("first warning")
	p.appendWarning("second warning")

	got := p.Warnings()
	if len(got) != 2 {
		t.Fatalf("Warnings: want 2 entries; got %d", len(got))
	}
	// Mutate the returned slice; the second call must observe
	// internal state unaffected.
	got[0] = "mutated"

	again := p.Warnings()
	if again[0] != "first warning" {
		t.Errorf("Warnings returned non-defensive copy: caller mutation visible (%q)", again[0])
	}
}

func TestPluginDrainWarningsReturnsAndClears(t *testing.T) {
	t.Parallel()
	p := &Plugin{name: "test-plugin"}
	p.appendWarning("alpha")
	p.appendWarning("beta")

	drained := p.DrainWarnings()
	if len(drained) != 2 || drained[0] != "alpha" || drained[1] != "beta" {
		t.Errorf("DrainWarnings: want [alpha beta]; got %v", drained)
	}

	if got := p.Warnings(); len(got) != 0 {
		t.Errorf("Warnings after drain: want empty; got %v", got)
	}

	// Post-drain accumulation works.
	p.appendWarning("gamma")
	if got := p.Warnings(); len(got) != 1 || got[0] != "gamma" {
		t.Errorf("post-drain Warnings: want [gamma]; got %v", got)
	}
}

func TestPluginWarningsRaceUnderConcurrentAppend(t *testing.T) {
	t.Parallel()
	p := &Plugin{name: "race-plugin"}

	// 100 goroutines each invoking the handleAggregaEmitWarning path
	// (the real append-under-lock path the host-function body uses)
	// via a fake reader that yields three distinct strings. Under
	// `go test -race` any unsynchronised access would surface.
	const concurrency = 100
	reader := &fakeMemoryReader{
		stringsByOffset: map[uint64]string{
			10: "code-id",
			20: "warning text",
			30: "components.x",
		},
	}
	stack := []uint64{10, 20, 30}

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.handleAggregaEmitWarning(reader, stack)
		}()
	}
	wg.Wait()

	if got := len(p.Warnings()); got != concurrency {
		t.Errorf("concurrent append final count: want %d; got %d", concurrency, got)
	}
}

func TestHostFunctionAggregaLogWritesToLogger(t *testing.T) {
	// NOT t.Parallel — captureLogOutput modifies process-wide log state.
	buf := captureLogOutput(t)

	p := &Plugin{name: "log-test-plugin"}
	reader := &fakeMemoryReader{
		stringsByOffset: map[uint64]string{42: "hello from guest"},
	}
	p.handleAggregaLog(reader, []uint64{2, 42}) // level=2 (info), msg at offset 42

	out := buf.String()
	if !strings.Contains(out, `"log-test-plugin"`) {
		t.Errorf("log output missing plugin name: %q", out)
	}
	if !strings.Contains(out, "[info]") {
		t.Errorf("log output missing level label: %q", out)
	}
	if !strings.Contains(out, "hello from guest") {
		t.Errorf("log output missing message: %q", out)
	}
}

func TestHostFunctionAggregaLogHandlesMemoryReadFailure(t *testing.T) {
	// NOT t.Parallel — captureLogOutput modifies process-wide log state.
	buf := captureLogOutput(t)

	p := &Plugin{name: "fail-test-plugin"}
	reader := &fakeMemoryReader{stringsByOffset: map[uint64]string{}} // empty → read fails
	// Must not panic.
	p.handleAggregaLog(reader, []uint64{2, 99})

	out := buf.String()
	if !strings.Contains(out, "aggrega_log: memory read failed") {
		t.Errorf("log output missing memory-read-failure diagnostic: %q", out)
	}
	if !strings.Contains(out, `"fail-test-plugin"`) {
		t.Errorf("log output missing plugin name in diagnostic: %q", out)
	}
}

func TestHostFunctionAggregaEmitWarningAppendsFormattedString(t *testing.T) {
	t.Parallel()
	p := &Plugin{name: "warn-test-plugin"}
	reader := &fakeMemoryReader{
		stringsByOffset: map[uint64]string{
			100: "TFM-001",
			200: "missing required field",
			300: "components.my-app.values.api_port",
		},
	}
	p.handleAggregaEmitWarning(reader, []uint64{100, 200, 300})

	got := p.Warnings()
	if len(got) != 1 {
		t.Fatalf("Warnings: want 1 entry; got %d", len(got))
	}
	for _, want := range []string{"warn-test-plugin", "missing required field", "code=TFM-001", "path=components.my-app.values.api_port"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("formatted warning missing %q: %s", want, got[0])
		}
	}
}

func TestHostFunctionAggregaEmitWarningHandlesMemoryReadFailure(t *testing.T) {
	// NOT t.Parallel — captureLogOutput modifies process-wide log state.
	buf := captureLogOutput(t)

	p := &Plugin{name: "warn-fail-plugin"}
	reader := &fakeMemoryReader{
		stringsByOffset: map[uint64]string{
			100: "TFM-001",
			// 200 deliberately missing → second ReadString fails
			300: "components.x",
		},
	}
	p.handleAggregaEmitWarning(reader, []uint64{100, 200, 300})

	if got := p.Warnings(); len(got) != 0 {
		t.Errorf("memory read failure must not record warning; got %v", got)
	}
	out := buf.String()
	if !strings.Contains(out, "aggrega_emit_warning: memory read failed at message") {
		t.Errorf("log output missing per-parameter diagnostic: %q", out)
	}
}

func TestLevelLabelMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		level uint64
		want  string
	}{
		{0, "trace"},
		{1, "debug"},
		{2, "info"},
		{3, "warn"},
		{4, "error"},
		{7, "level=7"},
		{1000, "level=1000"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			if got := levelLabel(tc.level); got != tc.want {
				t.Errorf("levelLabel(%d): want %q; got %q", tc.level, tc.want, got)
			}
		})
	}
}
