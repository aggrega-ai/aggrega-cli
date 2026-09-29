// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	extism "github.com/extism/go-sdk"
)

// buildGuestFromDir builds the fixture guest whose source lives under srcDir to a
// .wasm artifact in a temp directory and returns its path. Each guest is a
// standalone module (its own go.mod), so the build runs with GOWORK=off. The Go
// toolchain is the test toolchain itself and the wasip1 target ships with it, so
// a build failure is a genuine failure (t.Fatalf), never a toolchain skip.
func buildGuestFromDir(t *testing.T, srcDir string) string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(srcDir, "main.go")); err != nil {
		t.Fatalf("fixture source missing at %s: %v", srcDir, err)
	}
	out := filepath.Join(t.TempDir(), "guest.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, "./")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOOS=wasip1", "GOARCH=wasm")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build guest at %s: %v\noutput:\n%s", srcDir, err, output)
	}
	return out
}

// TestLoadForDescribe_DescribesStreamingCLIGuest is the load-bearing describability
// proof: a cli guest that both exports describe() and imports the streaming
// emitter aggrega_cli_write_stdout is describable through the describe-only load
// path, where the build-profile LoadAndDescribe rejects it before describe() can
// run. The build profile validates the streaming import against the shared
// allowlist alone and refuses it; the describe-only profile validates against the
// union allowlist so any plugin type instantiates purely to be introspected.
func TestLoadForDescribe_DescribesStreamingCLIGuest(t *testing.T) {
	ctx := context.Background()
	path := buildGuestFromDir(t, filepath.Join("..", "cliplugin", "testdata", "cli-args-exit"))

	loader := &Loader{}

	// The build profile rejects the streaming guest before describe() can run.
	if _, _, err := loader.LoadAndDescribe(ctx, PluginSpec{Name: "cli-args-exit", Path: path}); err == nil {
		t.Fatal("LoadAndDescribe (build profile) accepted a streaming cli guest; it must reject the streaming import before describe()")
	} else if !errors.Is(err, ErrWasmManifestRejected()) {
		t.Fatalf("LoadAndDescribe error = %v; want an error wrapping ErrWasmManifestRejected", err)
	}

	// The describe-only profile describes the same guest and reports type cli.
	plugin, desc, err := loader.LoadForDescribe(ctx, PluginSpec{Name: "cli-args-exit", Path: path})
	if err != nil {
		t.Fatalf("LoadForDescribe rejected a streaming cli guest it must describe: %v", err)
	}
	t.Cleanup(func() { _ = plugin.Close(ctx) })
	if desc.PluginType != string(PluginTypeCLI) {
		t.Fatalf("describe-only descriptor plugin_type = %q; want %q", desc.PluginType, PluginTypeCLI)
	}
}

// TestLoadForDescribe_BindsInertHostFunctions is the load-bearing hardening proof:
// a guest whose describe() calls the host function aggrega_emit_warning records
// its warning ONLY under the build profile's real host functions, not under the
// describe-only profile's inert stubs. The A/B contrast against the same guest
// proves the describe-only path grants no real host capability during
// introspection — a describe() that calls a host function reaches only a no-op.
func TestLoadForDescribe_BindsInertHostFunctions(t *testing.T) {
	ctx := context.Background()
	path := buildGuestFromDir(t, filepath.Join("testdata", "describe-emits-warning"))

	loader := &Loader{}

	// Build profile: aggrega_emit_warning is in the shared allowlist, so the guest
	// loads and its describe() records the warning through the REAL host function.
	// This establishes the test premise — the side effect is observable.
	realPlugin, _, err := loader.LoadAndDescribe(ctx, PluginSpec{Name: "describe-emits-warning", Path: path})
	if err != nil {
		t.Fatalf("LoadAndDescribe (build profile) failed on the warning-emitting guest: %v", err)
	}
	t.Cleanup(func() { _ = realPlugin.Close(ctx) })
	if got := realPlugin.Warnings(); len(got) == 0 {
		t.Fatal("build profile: describe() recorded no warning; the real aggrega_emit_warning must record it (test premise broken)")
	}

	// Describe-only profile: the same describe() calls the INERT stub, which
	// performs no side effect, so the plugin's warning channel stays empty.
	inertPlugin, _, err := loader.LoadForDescribe(ctx, PluginSpec{Name: "describe-emits-warning", Path: path})
	if err != nil {
		t.Fatalf("LoadForDescribe failed on the warning-emitting guest: %v", err)
	}
	t.Cleanup(func() { _ = inertPlugin.Close(ctx) })
	if got := inertPlugin.Warnings(); len(got) != 0 {
		t.Fatalf("describe-only profile recorded warnings %v; the inert stub must perform no side effect", got)
	}
}

// TestBuildProfileStillRejectsStreamingImport pins that the change does not weaken
// the build profile: a guest importing the streaming emitter is still rejected by
// Load with an error wrapping the generic ErrWasmManifestRejected.
func TestBuildProfileStillRejectsStreamingImport(t *testing.T) {
	ctx := context.Background()
	path := buildGuestFromDir(t, filepath.Join("..", "cliplugin", "testdata", "cli-args-exit"))

	loader := &Loader{}
	_, err := loader.Load(ctx, PluginSpec{Name: "cli-args-exit", Path: path})
	if err == nil {
		t.Fatal("Load accepted a guest importing aggrega_cli_write_stdout; the build profile must reject it")
	}
	if !errors.Is(err, ErrWasmManifestRejected()) {
		t.Fatalf("Load error = %v; want an error wrapping ErrWasmManifestRejected", err)
	}
}

// TestHostFunctionSignaturesSingleSourced proves the per-name signatures are
// single-sourced: for every name in the union allowlist, the real builder and the
// inert describe-only builder both produce a host function whose param/result
// signature equals the one registered in hostFunctionSignatures. A three-way
// equality (real vs registry vs inert) catches any drift between the two builders
// and the single source. It also proves the inert builder covers the FULL union
// set, so a guest of any type instantiates under the describe-only profile.
func TestHostFunctionSignaturesSingleSourced(t *testing.T) {
	names := cliAllowlistedHostFunctionNames()

	realFns := hostFunctionsForDeclaredCLI(names)
	inertFns := describeOnlyHostFunctions(names)
	if len(realFns) != len(names) || len(inertFns) != len(names) {
		t.Fatalf("builder output counts real=%d inert=%d; want %d", len(realFns), len(inertFns), len(names))
	}

	for i, name := range names {
		sig, ok := hostFunctionSignatureFor(name)
		if !ok {
			t.Fatalf("union name %q has no registered signature; the describe-only profile cannot instantiate a guest importing it", name)
		}
		real := realFns[i]
		inert := inertFns[i]
		if real.Name != name {
			t.Fatalf("real stub %d name = %q; want %q", i, real.Name, name)
		}
		if inert.Name != name {
			t.Fatalf("inert stub %d name = %q; want %q", i, inert.Name, name)
		}
		if !valueTypesEqual(real.Params, sig.params) {
			t.Fatalf("real %q params = %v; want registry %v", name, real.Params, sig.params)
		}
		if !valueTypesEqual(real.Returns, sig.results) {
			t.Fatalf("real %q results = %v; want registry %v", name, real.Returns, sig.results)
		}
		if !valueTypesEqual(inert.Params, sig.params) {
			t.Fatalf("inert %q params = %v; want registry %v", name, inert.Params, sig.params)
		}
		if !valueTypesEqual(inert.Returns, sig.results) {
			t.Fatalf("inert %q results = %v; want registry %v", name, inert.Returns, sig.results)
		}
	}
}

// valueTypesEqual reports whether two Wasm value-type lists carry the same types
// in the same order. A nil list and an empty list compare equal (a void result
// is expressed either way).
func valueTypesEqual(a, b []extism.ValueType) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
