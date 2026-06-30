// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"errors"
	"os"
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
	claim := EntityCapabilityClaim{Exports: []EntityExportClaim{
		{Name: "step", Operates: operatesPatch, Scope: ScopeOwn},
	}}
	if err := CrossCheckPublishedPlugin(context.Background(), "frp/patch:1.0.0", claim, wasmBytes); err != nil {
		t.Fatalf("agreeing claim should pass the publish cross-check; got %v", err)
	}
}

// TestCrossCheckPublishedPlugin_CapabilityMismatch proves a disagreeing export
// claim (scope=host where the plugin reports scope=own) halts with
// ErrWasmCapabilityMismatch through the publish entrypoint.
func TestCrossCheckPublishedPlugin_CapabilityMismatch(t *testing.T) {
	wasmBytes := loadFrpStepGoBytes(t)
	claim := EntityCapabilityClaim{Exports: []EntityExportClaim{
		{Name: "step", Operates: operatesPatch, Scope: ScopeHost},
	}}
	err := CrossCheckPublishedPlugin(context.Background(), "frp/patch:1.0.0", claim, wasmBytes)
	if !errors.Is(err, ErrWasmCapabilityMismatch) {
		t.Fatalf("a disagreeing claim should halt with ErrWasmCapabilityMismatch; got %v", err)
	}
	if !strings.Contains(err.Error(), "frp/patch") {
		t.Errorf("halt should name the entity identity; got %v", err)
	}
}
