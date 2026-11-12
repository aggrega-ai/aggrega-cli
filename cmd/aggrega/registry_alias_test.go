// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// resolveWorkspaceWithAlias seeds a hermetic temp workspace whose config binds
// alias→host on a registry entry, resolves it, and returns the resolved
// workspace for the push commands to consume.
func resolveWorkspaceWithAlias(t *testing.T, host, alias string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	dir := t.TempDir()
	content := "workspace: {}\nregistries:\n  - host: " + host + "\n    name: " + alias + "\n"
	if err := os.WriteFile(filepath.Join(dir, workspaceconfig.ConfigFilename), []byte(content), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(dir, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return rw
}

// TestRegistryAddPersistsAlias proves `registry add --name` writes the alias
// onto the host-keyed entry so it resolves back after a reload.
func TestRegistryAddPersistsAlias(t *testing.T) {
	t.Parallel()
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{})
	var out bytes.Buffer
	if err := registryAddRun(t.Context(), registryAddConfig{
		Host:      "dev.local:5000",
		Name:      "dev",
		Workspace: true,
		ws:        rw,
		Out:       &out,
	}); err != nil {
		t.Fatalf("registryAddRun: %v", err)
	}
	reloaded, err := workspaceconfig.Resolve(rw.Root(), "")
	if err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	host, err := reloaded.Config().ResolveRegistry("dev")
	if err != nil {
		t.Fatalf("ResolveRegistry: %v", err)
	}
	if host != "dev.local:5000" {
		t.Errorf("alias resolved to %q, want dev.local:5000", host)
	}
}

// TestRegistryAddWithoutNameHostOnly proves an add with no --name leaves the
// entry host-only — no regression on the pre-alias behaviour.
func TestRegistryAddWithoutNameHostOnly(t *testing.T) {
	t.Parallel()
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{})
	var out bytes.Buffer
	if err := registryAddRun(t.Context(), registryAddConfig{
		Host:      "dev.local:5000",
		Workspace: true,
		ws:        rw,
		Out:       &out,
	}); err != nil {
		t.Fatalf("registryAddRun: %v", err)
	}
	reloaded, _ := workspaceconfig.Resolve(rw.Root(), "")
	pol, found := reloaded.Config().RegistryTrustPolicy("dev.local:5000")
	if !found {
		t.Fatal("registry should be found after add")
	}
	if pol.Name != "" {
		t.Errorf("entry should carry no alias, got %q", pol.Name)
	}
}

// TestRegistryAddDuplicateAliasDifferentHost proves adding an alias already
// bound to a different host halts loudly.
func TestRegistryAddDuplicateAliasDifferentHost(t *testing.T) {
	t.Parallel()
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{})
	var out bytes.Buffer
	if err := registryAddRun(t.Context(), registryAddConfig{
		Host:      "a.example.com",
		Name:      "shared",
		Workspace: true,
		ws:        rw,
		Out:       &out,
	}); err != nil {
		t.Fatalf("first registryAddRun: %v", err)
	}
	err := registryAddRun(t.Context(), registryAddConfig{
		Host:      "b.example.com",
		Name:      "shared",
		Workspace: true,
		ws:        rw,
		Out:       &out,
	})
	if err == nil {
		t.Fatal("duplicate alias on a different host should be rejected")
	}
	if !errors.Is(err, workspaceconfig.ErrDuplicateRegistryAlias()) {
		t.Errorf("error = %v, want ErrDuplicateRegistryAlias", err)
	}
}

// TestPushResolvesAlias proves push resolves a --registry alias to its host in
// the composed OCI reference.
func TestPushResolvesAlias(t *testing.T) {
	t.Parallel()
	rw := resolveWorkspaceWithAlias(t, "dev.local:5000", "dev")
	entityPath := filepath.Join(t.TempDir(), "1.0.0", "entity.yaml")
	writeEntityFixture(t, entityPath, `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/api-server
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      type: object
`)
	var out bytes.Buffer
	if err := pushRun(t.Context(), pushConfig{
		EntityPath: entityPath,
		Registry:   "dev",
		DryRun:     true,
		Workspace:  rw,
		Out:        &out,
	}); err != nil {
		t.Fatalf("pushRun: %v", err)
	}
	want := "dev.local:5000/component/core/api-server:1.0.0"
	if !strings.Contains(out.String(), want) {
		t.Errorf("dry-run reference missing %q; got:\n%s", want, out.String())
	}
}

// TestPushArtifactResolvesAlias proves push-artifact resolves a --registry alias
// to its host in the composed docs reference.
func TestPushArtifactResolvesAlias(t *testing.T) {
	t.Parallel()
	rw := resolveWorkspaceWithAlias(t, "dev.local:5000", "dev")
	path, _ := makeDocsBundle(t)
	var out bytes.Buffer
	if err := pushArtifactRun(t.Context(), pushArtifactConfig{
		FilePath:  path,
		Name:      "aggrega-docs",
		Tag:       "1.0.0",
		Registry:  "dev",
		DryRun:    true,
		Workspace: rw,
		Out:       &out,
	}); err != nil {
		t.Fatalf("pushArtifactRun: %v", err)
	}
	want := "dev.local:5000/docs/aggrega-docs:1.0.0"
	if !strings.Contains(out.String(), want) {
		t.Errorf("dry-run reference missing %q; got:\n%s", want, out.String())
	}
}
