// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// TestLoadAndDescribe_CapabilityClaimAgrees exercises the full load path with a
// constructed entity capability claim that AGREES with the fixture's describe():
// the plugin loads, the cross-check passes, and the validated descriptor is
// returned. The frp-step-go fixture declares export "step" with operates=patch,
// scope=own and imports no extism:host/user function, so the derived custom
// host-function set is empty and the load succeeds. Skips cleanly when the
// toolchain is absent.
func TestLoadAndDescribe_CapabilityClaimAgrees(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")

	loader := &Loader{}
	ctx := context.Background()

	plugin, desc, err := loader.LoadAndDescribe(ctx, PluginSpec{
		Name: "frp-step-go",
		Path: pluginPath,
		Contract: &EntityContractClaim{Exports: []EntityExportClaim{
			{Name: "step", Operates: operatesPatch, Scope: ScopeOwn},
		}},
	})
	if err != nil {
		t.Fatalf("LoadAndDescribe with an agreeing claim should succeed; got %v", err)
	}
	defer plugin.Close(ctx)

	if !desc.hasExport(frpStepExportName) {
		t.Fatalf("descriptor does not declare the %q export", frpStepExportName)
	}
}

// TestLoadAndDescribe_CapabilityClaimDisagrees exercises the full load path with
// a constructed claim that DISAGREES with the fixture's describe() (the claim
// says scope=host where the plugin reports scope=own): the load halts loudly
// with ErrWasmContractMismatch. Skips cleanly when the toolchain is absent.
func TestLoadAndDescribe_CapabilityClaimDisagrees(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")

	loader := &Loader{}
	ctx := context.Background()

	_, _, err := loader.LoadAndDescribe(ctx, PluginSpec{
		Name: "frp-step-go",
		Path: pluginPath,
		Contract: &EntityContractClaim{Exports: []EntityExportClaim{
			{Name: "step", Operates: operatesPatch, Scope: ScopeHost},
		}},
	})
	if !errors.Is(err, ErrWasmContractMismatch) {
		t.Fatalf("a disagreeing claim should halt with ErrWasmContractMismatch; got %v", err)
	}
	if !strings.Contains(err.Error(), "frp-step-go") {
		t.Errorf("halt diagnostic should name the entity identity; got %v", err)
	}
	if !strings.Contains(err.Error(), "scope") {
		t.Errorf("halt diagnostic should name the disagreeing field; got %v", err)
	}
}

// TestLoadAndDescribe_NilClaimSkipsCrossCheck proves the load path with a nil
// capability claim (every non-entity caller and the publish path) loads without
// any cross-check. Skips cleanly when the toolchain is absent.
func TestLoadAndDescribe_NilClaimSkipsCrossCheck(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")

	loader := &Loader{}
	ctx := context.Background()

	plugin, _, err := loader.LoadAndDescribe(ctx, PluginSpec{
		Name: "frp-step-go",
		Path: pluginPath,
		// Contract nil: no cross-check.
	})
	if err != nil {
		t.Fatalf("a nil claim should skip the cross-check and load; got %v", err)
	}
	_ = plugin.Close(ctx)
}
