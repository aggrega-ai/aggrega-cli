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

// TestValidatePlugin_CapabilityClaimMismatch proves a disagreeing export claim
// (scope=host where the binary reports scope=own) halts with
// ErrWasmContractMismatch. The declared type matches the reported type and the
// command-mirror and host-function records match (both empty for this fixture),
// so the capability cross-check is the only divergence and the halt isolates it.
func TestValidatePlugin_CapabilityClaimMismatch(t *testing.T) {
	wasmBytes := loadFrpStepGoBytes(t)
	entity := &EntityInputs{
		Claim: EntityContractClaim{Exports: []EntityExportClaim{
			{Name: "step", Operates: operatesPatch, Scope: ScopeHost},
		}},
	}
	_, err := ValidatePlugin(context.Background(), wasmBytes, ValidateOptions{
		Identity:     "frp/patch:1.0.0",
		DeclaredType: string(PluginTypeFRPStep),
		Entity:       entity,
	})
	if !errors.Is(err, ErrWasmContractMismatch) {
		t.Fatalf("a disagreeing capability claim should halt with ErrWasmContractMismatch; got %v", err)
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
