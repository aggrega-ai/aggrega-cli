// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestPushPluginCapabilityTheBinaryDoesNotReportHaltsNoBytes proves publish
// refuses a Plugin entity whose capability list names a capability the .wasm's
// describe() does not report, naming that capability and pushing no bytes, while
// the same entity with the list narrowed to one reported capability publishes.
// The frp-step-go fixture is a pipeline step reporting helm and helm-chart.
func TestPushPluginCapabilityTheBinaryDoesNotReportHaltsNoBytes(t *testing.T) {
	ctx := context.Background()
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")

	root := t.TempDir()
	t.Chdir(root)
	entityPath := filepath.Join(root, "frp-step-patch.yaml")
	writeEntityFixture(t, entityPath, strings.Replace(agreeingPluginEntity,
		"    capabilities:\n      - helm\n",
		"    capabilities:\n      - helm\n      - compose\n", 1))

	store := memory.New()
	err := pushRun(t.Context(), pushConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		PushTarget: store,
	})
	if !errors.Is(err, wasm.ErrWasmContractMismatch()) {
		t.Fatalf("want ErrWasmContractMismatch for a capability the plugin does not report, got %v", err)
	}
	if !strings.Contains(err.Error(), `"compose"`) {
		t.Errorf("the refusal should name the capability the plugin does not report; got %v", err)
	}
	if _, exists, rerr := oci.Resolve(ctx, store, "1.0.0"); rerr != nil || exists {
		t.Errorf("version tag must not exist after a halted publish (exists=%v err=%v)", exists, rerr)
	}

	// The narrowed list — helm alone, of the two the plugin reports — publishes.
	writeEntityFixture(t, entityPath, agreeingPluginEntity)
	if err := pushRun(t.Context(), pushConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		PushTarget: store,
	}); err != nil {
		t.Fatalf("a narrowed capability list should publish; got %v", err)
	}
	if _, exists, rerr := oci.Resolve(ctx, store, "1.0.0"); rerr != nil || !exists {
		t.Errorf("the narrowed entity should be published (exists=%v err=%v)", exists, rerr)
	}
}
