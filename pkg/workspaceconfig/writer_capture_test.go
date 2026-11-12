// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// TestCaptureBuildCritical_WritesSubsetWithFidelity proves the capture writer
// materializes every build-critical field — provider bindings, the socket lists,
// and the catalog directories — at full fidelity (a socket
// attachment's function override and its `with` inputs survive), and leaves a
// schema-valid file the resolver reads back. It captures with the widened socket
// scope so a verdict-role governance attachment participates in the fidelity check.
func TestCaptureBuildCritical_WritesSubsetWithFidelity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	if err := os.WriteFile(path, []byte("workspace: {}\ndefault_registry: hub.aggrega.ai\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	catalog := filepath.Join(dir, "catalog")
	cfg := Config{
		Workspace: WorkspaceConfig{CatalogDirs: []string{catalog}},
		Plugins: PluginsConfig{
			Providers: []PluginRef{
				{Capability: "helm", URI: "provider/helm:1.0.0"},
			},
			Governance: []PluginRef{
				{
					URI:      "governance/deny-gate:1.0.0",
					Function: "verdict_ir",
					Role:     plugintype.RoleIRVerdict,
					With: &PluginRefWith{
						Sources:  []string{filepath.Join(dir, "policy.rego")},
						Settings: map[string]any{"strict": true},
					},
				},
			},
		},
	}

	if err := CaptureBuildCritical(path, cfg, true); err != nil {
		t.Fatalf("CaptureBuildCritical: %v", err)
	}

	rw, err := Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after capture: %v", err)
	}
	got := rw.Config()

	if got.DefaultRegistry != "hub.aggrega.ai" {
		t.Errorf("pre-existing key not preserved: default_registry=%q", got.DefaultRegistry)
	}
	if len(got.Plugins.Providers) != 1 || got.Plugins.Providers[0].Capability != "helm" ||
		got.Plugins.Providers[0].URI != "provider/helm:1.0.0" {
		t.Fatalf("provider binding not captured: %+v", got.Plugins.Providers)
	}
	if len(got.Plugins.Governance) != 1 {
		t.Fatalf("governance socket not captured: %+v", got.Plugins.Governance)
	}
	p := got.Plugins.Governance[0]
	if p.URI != "governance/deny-gate:1.0.0" || p.Function != "verdict_ir" {
		t.Errorf("socket ref uri/function not preserved: %+v", p)
	}
	if p.With == nil || len(p.With.Sources) != 1 || p.With.Settings["strict"] != true {
		t.Errorf("socket ref `with` inputs not preserved at full fidelity: %+v", p.With)
	}
	if len(got.Workspace.CatalogDirs) != 1 || got.Workspace.CatalogDirs[0] != catalog {
		t.Errorf("catalog_dirs not captured: %+v", got.Workspace.CatalogDirs)
	}
}

// TestCaptureBuildCritical_Idempotent proves capturing an unchanged effective
// configuration twice produces a byte-identical file.
func TestCaptureBuildCritical_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	if err := os.WriteFile(path, []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg := Config{
		Plugins: PluginsConfig{
			Providers:  []PluginRef{{Capability: "helm", URI: "provider/helm:1.0.0"}},
			Governance: []PluginRef{{URI: "governance/tag-injector:1.0.0", Role: plugintype.RoleLabelTransform}},
		},
		Workspace: WorkspaceConfig{CatalogDirs: []string{filepath.Join(dir, "catalog")}},
	}

	if err := CaptureBuildCritical(path, cfg, false); err != nil {
		t.Fatalf("capture (first): %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read first: %v", err)
	}
	if err := CaptureBuildCritical(path, cfg, false); err != nil {
		t.Fatalf("capture (second): %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read second: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("capture is not idempotent:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

// TestCaptureBuildCritical_ExcludesRegistries proves capture never writes a
// tool-scoped setting (a registry trust policy) into the workspace file, even
// when the effective config carries one — the nature split is preserved.
func TestCaptureBuildCritical_ExcludesRegistries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	if err := os.WriteFile(path, []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg := Config{
		Plugins:    PluginsConfig{Governance: []PluginRef{{URI: "governance/tag-injector:1.0.0", Role: plugintype.RoleLabelTransform}}},
		Registries: []RegistryConfig{{Host: "reg.example.com"}},
	}
	if err := CaptureBuildCritical(path, cfg, false); err != nil {
		t.Fatalf("CaptureBuildCritical: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(body), "reg.example.com") {
		t.Errorf("registries leaked into the captured workspace file:\n%s", body)
	}
	if !strings.Contains(string(body), "governance/tag-injector:1.0.0") {
		t.Errorf("build-critical label-transform attachment not captured:\n%s", body)
	}
}
