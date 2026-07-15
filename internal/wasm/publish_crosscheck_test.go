// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// loadFrpStepGoBytes builds the frp-step-go fixture (export "step",
// operates=patch, scope=own, no custom host-function imports) and returns its
// bytes. Skips cleanly when the Wasm toolchain is absent.
func loadFrpStepGoBytes(t *testing.T) []byte {
	t.Helper()
	path := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

// TestCrossCheckPublishedPlugin_Agrees proves the publish-side entrypoint passes
// when the entity's authored export claim agrees with the .wasm's describe().
func TestCrossCheckPublishedPlugin_Agrees(t *testing.T) {
	wasmBytes := loadFrpStepGoBytes(t)
	claim := EntityContractClaim{Exports: []EntityExportClaim{
		{Name: "step", Operates: operatesPatch, Scope: ScopeOwn},
	}}
	// The frp-step-go guest fits the platform default cap; a zero pages value
	// applies that default.
	if err := CrossCheckPublishedPlugin(context.Background(), "frp/patch:1.0.0", claim, wasmBytes, 0, string(PluginTypeFRPStep)); err != nil {
		t.Fatalf("agreeing claim should pass the publish cross-check; got %v", err)
	}
}

// TestCrossCheckPublishedPlugin_CapabilityMismatch proves a disagreeing export
// claim (scope=host where the plugin reports scope=own) halts with
// ErrWasmContractMismatch through the publish entrypoint.
func TestCrossCheckPublishedPlugin_CapabilityMismatch(t *testing.T) {
	wasmBytes := loadFrpStepGoBytes(t)
	claim := EntityContractClaim{Exports: []EntityExportClaim{
		{Name: "step", Operates: operatesPatch, Scope: ScopeHost},
	}}
	err := CrossCheckPublishedPlugin(context.Background(), "frp/patch:1.0.0", claim, wasmBytes, 0, string(PluginTypeFRPStep))
	if !errors.Is(err, ErrWasmContractMismatch) {
		t.Fatalf("a disagreeing claim should halt with ErrWasmContractMismatch; got %v", err)
	}
	if !strings.Contains(err.Error(), "frp/patch") {
		t.Errorf("halt should name the entity identity; got %v", err)
	}
}

// buildCLIArgsExitGuest builds the cli-args-exit fixture — a streaming CLI guest
// that self-describes as pluginType cli and imports the terminal streaming host
// function aggrega_cli_write_stdout — to a .wasm artifact and returns its bytes.
// The fixture is a standalone module (its own go.mod), so the build runs with
// GOWORK=off, mirroring the PDK-example build scripts. The Go toolchain is the
// test toolchain itself and the wasip1 target ships with it, so a build failure
// is a genuine failure, never a toolchain skip.
func buildCLIArgsExitGuest(t *testing.T) []byte {
	t.Helper()
	srcDir := filepath.Join("..", "cliplugin", "testdata", "cli-args-exit")
	if _, err := os.Stat(filepath.Join(srcDir, "main.go")); err != nil {
		t.Fatalf("cli-args-exit fixture source missing at %s: %v", srcDir, err)
	}
	out := filepath.Join(t.TempDir(), "cli-args-exit.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, "./")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOOS=wasip1", "GOARCH=wasm")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build cli-args-exit guest: %v\noutput:\n%s", err, output)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read built cli-args-exit guest: %v", err)
	}
	return b
}

// TestCrossCheckPublishedPlugin_StreamingCLIType proves the publish cross-check
// admits a streaming CLI plugin: a guest importing the terminal streaming host
// function aggrega_cli_write_stdout loads under the runtime CLI profile when the
// entity's authored pluginType is cli, so describe() runs and the cross-check
// completes. This is the case that could not be published before the profile was
// selected by pluginType — the streaming import is absent from the build
// profile's shared allowlist and would be rejected at load before describe().
func TestCrossCheckPublishedPlugin_StreamingCLIType(t *testing.T) {
	wasmBytes := buildCLIArgsExitGuest(t)
	// The guest authors no capability claim; an empty claim cross-checks as a
	// no-op, so the test isolates the load-profile selection.
	if err := CrossCheckPublishedPlugin(context.Background(), "cli/args-exit:1.0.0", EntityContractClaim{}, wasmBytes, 0, string(PluginTypeCLI)); err != nil {
		t.Fatalf("streaming CLI plugin should pass the publish cross-check under the CLI load profile; got %v", err)
	}
}

// TestCrossCheckPublishedPlugin_StreamingUnderBuildProfileRejected proves the
// pluginType-driven profile selection is load-bearing on the publish path: the
// same streaming guest, cross-checked as a non-CLI type, loads under the build
// profile whose shared allowlist rejects the streaming import at load with an
// error wrapping ErrWasmManifestRejected — the exact failure the CLI profile
// selection avoids.
func TestCrossCheckPublishedPlugin_StreamingUnderBuildProfileRejected(t *testing.T) {
	wasmBytes := buildCLIArgsExitGuest(t)
	err := CrossCheckPublishedPlugin(context.Background(), "cli/args-exit:1.0.0", EntityContractClaim{}, wasmBytes, 0, string(PluginTypeFRPStep))
	if !errors.Is(err, ErrWasmManifestRejected) {
		t.Fatalf("a streaming guest under the build profile should be rejected at load with ErrWasmManifestRejected; got %v", err)
	}
}
