// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// writeGlobalConfig writes a schema-valid global config file at path (creating
// its parent directories) carrying build-critical bindings supplied only by the
// global layer, plus a tool-scoped registry entry, and returns nothing. The
// caller points a frozen $AGGREGA_CONFIG lookup at path.
func writeGlobalConfig(t *testing.T, path, sidecar, catalog string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir global dir: %v", err)
	}
	// catalog_dirs lives under the workspace block; a tool-scoped registry rides
	// alongside so the test can prove capture leaves it in the global config.
	body := "workspace:\n  catalog_dirs:\n    - " + catalog + "\n" +
		"plugin_sidecar_dir: " + sidecar + "\n" +
		"plugins:\n" +
		"  providers:\n" +
		"    - capability: helm\n" +
		"      uri: oci://hub.aggrega.ai/plugin/provider/helm:1.0.0\n" +
		"  mutator:\n" +
		"    - uri: mutator/tag-injector:1.0.0\n" +
		"registries:\n" +
		"  - host: global-only.example.com\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write global config: %v", err)
	}
}

// TestConfigCaptureMaterializesGlobalBindings proves the guardrail remedy: a
// workspace whose build-critical bindings come only from the global layer, after
// capture, carries them in its own config file — a tree-only resolve (global
// suppressed) yields the previously-global-only bindings locally — while the
// tool-scoped registry stays out of the workspace file.
func TestConfigCaptureMaterializesGlobalBindings(t *testing.T) {
	t.Parallel()
	wsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(wsDir, workspaceconfig.ConfigFilename), []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	globalDir := t.TempDir()
	globalPath := filepath.Join(globalDir, "aggrega", "config.yaml")
	sidecar := filepath.Join(globalDir, "sidecars")
	catalog := filepath.Join(globalDir, "catalog")
	writeGlobalConfig(t, globalPath, sidecar, catalog)

	withGlobal := frozenEnv(map[string]string{"AGGREGA_CONFIG": globalPath})

	// Resolve with the global layer present; the build-critical bindings come
	// only from the global config here.
	rw, err := workspaceconfig.Resolve(wsDir, "", withGlobal)
	if err != nil {
		t.Fatalf("resolve with global: %v", err)
	}
	if len(rw.Config().Plugins.Providers) == 0 {
		t.Fatalf("precondition: global bindings did not resolve into the effective config")
	}

	var out bytes.Buffer
	if err := configCaptureRun(configCaptureConfig{ws: rw, Out: &out}); err != nil {
		t.Fatalf("configCaptureRun: %v", err)
	}

	wsFile := filepath.Join(wsDir, workspaceconfig.ConfigFilename)
	body, _ := os.ReadFile(wsFile)
	for _, want := range []string{
		"oci://hub.aggrega.ai/plugin/provider/helm:1.0.0",
		"mutator/tag-injector:1.0.0",
		sidecar,
		catalog,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("captured workspace file missing %q:\n%s", want, body)
		}
	}
	// Nature split: the tool-scoped registry must not be captured.
	if strings.Contains(string(body), "global-only.example.com") {
		t.Errorf("tool-scoped registry leaked into the workspace file:\n%s", body)
	}
	if !strings.Contains(out.String(), wsFile) {
		t.Errorf("output %q should name the destination workspace file %q", out.String(), wsFile)
	}

	// Tree-only resolve (global suppressed) now yields the bindings locally.
	treeOnly, err := workspaceconfig.Resolve(wsDir, "", frozenEnv(map[string]string{}))
	if err != nil {
		t.Fatalf("tree-only resolve: %v", err)
	}
	tc := treeOnly.Config()
	if uris := tc.Plugins.ProviderTargetURIs(); uris["helm"] != "oci://hub.aggrega.ai/plugin/provider/helm:1.0.0" {
		t.Errorf("provider binding not yielded by tree-only resolve: %+v", tc.Plugins.Providers)
	}
	if len(tc.Plugins.Mutator) != 1 || tc.Plugins.Mutator[0].URI != "mutator/tag-injector:1.0.0" {
		t.Errorf("mutator attachment not yielded by tree-only resolve: %+v", tc.Plugins.Mutator)
	}
	if tc.PluginSidecarDir != sidecar {
		t.Errorf("plugin_sidecar_dir not yielded by tree-only resolve: %q", tc.PluginSidecarDir)
	}
	if len(tc.Workspace.CatalogDirs) != 1 || tc.Workspace.CatalogDirs[0] != catalog {
		t.Errorf("catalog_dirs not yielded by tree-only resolve: %+v", tc.Workspace.CatalogDirs)
	}
}

// TestConfigCaptureIdempotent proves running capture twice on an unchanged
// effective configuration produces a byte-identical workspace file.
func TestConfigCaptureIdempotent(t *testing.T) {
	t.Parallel()
	wsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(wsDir, workspaceconfig.ConfigFilename), []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	globalDir := t.TempDir()
	globalPath := filepath.Join(globalDir, "aggrega", "config.yaml")
	writeGlobalConfig(t, globalPath, filepath.Join(globalDir, "sidecars"), filepath.Join(globalDir, "catalog"))
	withGlobal := frozenEnv(map[string]string{"AGGREGA_CONFIG": globalPath})

	rw, err := workspaceconfig.Resolve(wsDir, "", withGlobal)
	if err != nil {
		t.Fatalf("resolve with global: %v", err)
	}
	wsFile := filepath.Join(wsDir, workspaceconfig.ConfigFilename)

	if err := configCaptureRun(configCaptureConfig{ws: rw, Out: &bytes.Buffer{}}); err != nil {
		t.Fatalf("capture (first): %v", err)
	}
	first, _ := os.ReadFile(wsFile)

	// Re-resolve after the first capture (global still present) and capture
	// again: the effective build-critical values are unchanged, so the file must
	// be rewritten byte-for-byte.
	rw2, err := workspaceconfig.Resolve(wsDir, "", withGlobal)
	if err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	if err := configCaptureRun(configCaptureConfig{ws: rw2, Out: &bytes.Buffer{}}); err != nil {
		t.Fatalf("capture (second): %v", err)
	}
	second, _ := os.ReadFile(wsFile)

	if string(first) != string(second) {
		t.Errorf("capture is not idempotent:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}
