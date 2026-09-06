// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// frozenEnv returns a lookup answering only from vars, so a command's
// global-write-path resolution reads a fixed environment and never the real
// $HOME or $AGGREGA_CONFIG. Deterministic and parallel-safe.
func frozenEnv(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// TestRegistryAddDefaultsToGlobal proves a registry — tool-scoped — lands in the
// global config file by default (no write-target flag), and the resolved global
// destination is printed.
func TestRegistryAddDefaultsToGlobal(t *testing.T) {
	t.Parallel()
	global := filepath.Join(t.TempDir(), "aggrega", "config.yaml")
	lookup := frozenEnv(map[string]string{"AGGREGA_CONFIG": global})
	rw := resolveTempWorkspaceAgainst(t, tempWorkspaceOptions{}, lookup)

	var out bytes.Buffer
	if err := registryAddRun(registryAddConfig{
		Host: "tool.example.com",
		ws:   rw,
		Out:  &out,
	}); err != nil {
		t.Fatalf("registryAddRun: %v", err)
	}

	// The registry landed in the global file, not the workspace file.
	if _, err := os.Stat(global); err != nil {
		t.Fatalf("global config not written at %q: %v", global, err)
	}
	wsFile := filepath.Join(rw.Root(), workspaceconfig.ConfigFilename)
	body, _ := os.ReadFile(wsFile)
	if strings.Contains(string(body), "tool.example.com") {
		t.Errorf("registry leaked into the workspace file %q", wsFile)
	}
	if !strings.Contains(out.String(), global) {
		t.Errorf("output %q should print the global destination %q", out.String(), global)
	}

	// Re-resolve reading the same frozen env: the global layer carries the entry.
	reloaded, err := workspaceconfig.Resolve(rw.Root(), "", lookup)
	if err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	if _, found := reloaded.Config().RegistryTrustPolicy("tool.example.com"); !found {
		t.Error("registry should be found via the global layer after a default add")
	}
}

// TestRegistryAddWorkspaceOverride proves --workspace forces a registry into the
// workspace config file despite its tool nature.
func TestRegistryAddWorkspaceOverride(t *testing.T) {
	t.Parallel()
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{})
	var out bytes.Buffer
	if err := registryAddRun(registryAddConfig{
		Host:      "ws.example.com",
		Workspace: true,
		ws:        rw,
		Out:       &out,
	}); err != nil {
		t.Fatalf("registryAddRun: %v", err)
	}
	wsFile := filepath.Join(rw.Root(), workspaceconfig.ConfigFilename)
	body, _ := os.ReadFile(wsFile)
	if !strings.Contains(string(body), "ws.example.com") {
		t.Errorf("--workspace registry not written to the workspace file %q", wsFile)
	}
	if !strings.Contains(out.String(), wsFile) {
		t.Errorf("output %q should print the workspace destination %q", out.String(), wsFile)
	}
}

// TestRegistryAddBothFlagsUsageError proves setting both write-target flags is a
// usage error rather than an ambiguous write.
func TestRegistryAddBothFlagsUsageError(t *testing.T) {
	t.Parallel()
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{})
	err := registryAddRun(registryAddConfig{
		Host:      "x.example.com",
		Global:    true,
		Workspace: true,
		ws:        rw,
		Out:       &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("both --global and --workspace should be a usage error")
	}
}

// TestRegistryAddGlobalCreatesMissingParentDir proves a --global add whose XDG
// parent directory does not yet exist creates it and writes, rather than failing
// at the write.
func TestRegistryAddGlobalCreatesMissingParentDir(t *testing.T) {
	t.Parallel()
	// $AGGREGA_CONFIG under two not-yet-created directory levels.
	global := filepath.Join(t.TempDir(), "cfg", "aggrega", "config.yaml")
	rw := resolveTempWorkspaceAgainst(t, tempWorkspaceOptions{},
		frozenEnv(map[string]string{"AGGREGA_CONFIG": global}))
	if err := registryAddRun(registryAddConfig{
		Host:   "mkdir.example.com",
		Global: true,
		ws:     rw,
		Out:    &bytes.Buffer{},
	}); err != nil {
		t.Fatalf("registryAddRun --global into a missing parent dir: %v", err)
	}
	if _, err := os.Stat(global); err != nil {
		t.Fatalf("global config not created at %q: %v", global, err)
	}
}

// TestPluginAddProviderDefaultsToWorkspace proves a provider binding —
// build-scoped — lands in the workspace config file by default, and the
// workspace destination is printed.
func TestPluginAddProviderDefaultsToWorkspace(t *testing.T) {
	// Not parallel: mutates process-global globalDisplay, newRepoDeprecationLookup seams other tests read.
	withSearchTarget(t, fakeRepoLister{repos: searchFixtureRepos}, nil)
	rw := resolvedTestWorkspace(t)
	var out bytes.Buffer

	if err := pluginAddRun(context.Background(), pluginAddConfig{
		Socket:     "provider",
		Query:      "helm",
		Capability: "helm",
		ws:         rw,
		Out:        &out,
	}); err != nil {
		t.Fatalf("pluginAddRun (provider): %v", err)
	}
	wsFile := filepath.Join(rw.Root(), workspaceconfig.ConfigFilename)
	body, _ := os.ReadFile(wsFile)
	if !strings.Contains(string(body), "helm") {
		t.Errorf("provider binding not written to the workspace file %q", wsFile)
	}
	if !strings.Contains(out.String(), wsFile) {
		t.Errorf("output %q should print the workspace destination %q", out.String(), wsFile)
	}
}

// TestPluginAddProviderGlobalOverride proves --global forces a provider binding
// into the global config file despite its build nature.
func TestPluginAddProviderGlobalOverride(t *testing.T) {
	// Not parallel: mutates process-global globalDisplay, newRepoDeprecationLookup seams other tests read.
	withSearchTarget(t, fakeRepoLister{repos: searchFixtureRepos}, nil)
	global := filepath.Join(t.TempDir(), "aggrega", "config.yaml")
	rw := resolvedTestWorkspaceAgainst(t, frozenEnv(map[string]string{"AGGREGA_CONFIG": global}))
	var out bytes.Buffer

	if err := pluginAddRun(context.Background(), pluginAddConfig{
		Socket:     "provider",
		Query:      "helm",
		Capability: "helm",
		Global:     true,
		ws:         rw,
		Out:        &out,
	}); err != nil {
		t.Fatalf("pluginAddRun (provider --global): %v", err)
	}
	if _, err := os.Stat(global); err != nil {
		t.Fatalf("global config not written at %q: %v", global, err)
	}
	wsFile := filepath.Join(rw.Root(), workspaceconfig.ConfigFilename)
	body, _ := os.ReadFile(wsFile)
	if strings.Contains(string(body), "provider/helm") {
		t.Errorf("--global provider binding leaked into the workspace file %q", wsFile)
	}
	if !strings.Contains(out.String(), global) {
		t.Errorf("output %q should print the global destination %q", out.String(), global)
	}
}

// TestPluginAddConfigSocketDefaultsToWorkspace proves a config-socket attachment
// — build-scoped — defaults to the workspace config file.
func TestPluginAddConfigSocketDefaultsToWorkspace(t *testing.T) {
	// Not parallel: mutates process-global globalDisplay, newRepoDeprecationLookup seams other tests read.
	withSearchTarget(t, fakeRepoLister{repos: []string{"plugin/governance/tag-injector"}}, nil)
	withTagLister(t, []string{"1.0.0"})
	rw := resolvedTestWorkspace(t)
	var out bytes.Buffer

	if err := pluginAddRun(context.Background(), pluginAddConfig{
		Socket: "governance",
		Role:   "label-transform",
		Query:  "tag-injector",
		ws:     rw,
		Out:    &out,
	}); err != nil {
		t.Fatalf("pluginAddRun (config socket): %v", err)
	}
	wsFile := filepath.Join(rw.Root(), workspaceconfig.ConfigFilename)
	body, _ := os.ReadFile(wsFile)
	if !strings.Contains(string(body), "tag-injector") {
		t.Errorf("config-socket attachment not written to the workspace file %q", wsFile)
	}
	if !strings.Contains(out.String(), wsFile) {
		t.Errorf("output %q should print the workspace destination %q", out.String(), wsFile)
	}
}

// TestPluginAddBothFlagsUsageError proves setting both write-target flags on
// plugin add is a usage error.
func TestPluginAddBothFlagsUsageError(t *testing.T) {
	// Not parallel: mutates process-global globalDisplay, newRepoDeprecationLookup seams other tests read.
	withSearchTarget(t, fakeRepoLister{repos: searchFixtureRepos}, nil)
	rw := resolvedTestWorkspace(t)
	err := pluginAddRun(context.Background(), pluginAddConfig{
		Socket:     "provider",
		Query:      "helm",
		Capability: "helm",
		Global:     true,
		Workspace:  true,
		ws:         rw,
		Out:        &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("both --global and --workspace should be a usage error")
	}
}
