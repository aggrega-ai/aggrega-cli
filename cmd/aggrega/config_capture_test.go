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
func writeGlobalConfig(t *testing.T, path, catalog string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir global dir: %v", err)
	}
	// catalog_dirs lives under the workspace block; a tool-scoped registry rides
	// alongside so the test can prove capture leaves it in the global config.
	body := "workspace:\n  catalog_dirs:\n    - " + catalog + "\n" +
		"plugins:\n" +
		"  providers:\n" +
		"    - capability: helm\n" +
		"      uri: oci://hub.aggrega.ai/plugin/provider/helm:1.0.0\n" +
		"  governance:\n" +
		"    - uri: governance/tag-injector:1.0.0\n" +
		"      role: label-transform\n" +
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
	catalog := filepath.Join(globalDir, "catalog")
	writeGlobalConfig(t, globalPath, catalog)

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
		"governance/tag-injector:1.0.0",
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
	if len(tc.Plugins.Governance) != 1 || tc.Plugins.Governance[0].URI != "governance/tag-injector:1.0.0" {
		t.Errorf("governance attachment not yielded by tree-only resolve: %+v", tc.Plugins.Governance)
	}
	if len(tc.Workspace.CatalogDirs) != 1 || tc.Workspace.CatalogDirs[0] != catalog {
		t.Errorf("catalog_dirs not yielded by tree-only resolve: %+v", tc.Workspace.CatalogDirs)
	}
}

// TestConfigCaptureScopeByTier proves the socket scope: global-only verdict
// attachments (a verdict governance role, linter, importer) are frozen only when
// --all-plugins is set, while the byte-determining attachments (provider bindings
// and the label-transform governance attachment) are always frozen. The default
// keeps a workspace portable for byte-identical output without dragging the
// environment-varying gates along.
func TestConfigCaptureScopeByTier(t *testing.T) {
	t.Parallel()

	// A global config supplying every socket, each with a distinctive URI so the
	// captured workspace file can be probed per socket.
	globalBody := "workspace: {}\n" +
		"plugins:\n" +
		"  providers:\n" +
		"    - capability: helm\n" +
		"      uri: oci://h/provider-helm:1\n" +
		"  governance:\n" +
		"    - uri: oci://h/mutator-plugin:1\n" +
		"      role: label-transform\n" +
		"    - uri: oci://h/policy-plugin:1\n" +
		"      role: ir-verdict\n" +
		"  linter:\n" +
		"    - uri: oci://h/linter-plugin:1\n" +
		"  importer:\n" +
		"    - uri: oci://h/importer-plugin:1\n"

	byteDetermining := []string{"oci://h/provider-helm:1", "oci://h/mutator-plugin:1"}
	gates := []string{"oci://h/policy-plugin:1", "oci://h/linter-plugin:1", "oci://h/importer-plugin:1"}

	capture := func(t *testing.T, allPlugins bool) (workspaceFile string, out string) {
		t.Helper()
		wsDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(wsDir, workspaceconfig.ConfigFilename), []byte("workspace: {}\n"), 0o644); err != nil {
			t.Fatalf("seed workspace: %v", err)
		}
		globalDir := t.TempDir()
		globalPath := filepath.Join(globalDir, "aggrega", "config.yaml")
		if err := os.MkdirAll(filepath.Dir(globalPath), 0o755); err != nil {
			t.Fatalf("mkdir global dir: %v", err)
		}
		if err := os.WriteFile(globalPath, []byte(globalBody), 0o644); err != nil {
			t.Fatalf("write global config: %v", err)
		}
		rw, err := workspaceconfig.Resolve(wsDir, "", frozenEnv(map[string]string{"AGGREGA_CONFIG": globalPath}))
		if err != nil {
			t.Fatalf("resolve with global: %v", err)
		}
		var buf bytes.Buffer
		if err := configCaptureRun(configCaptureConfig{ws: rw, allPlugins: allPlugins, Out: &buf}); err != nil {
			t.Fatalf("configCaptureRun(allPlugins=%v): %v", allPlugins, err)
		}
		body, _ := os.ReadFile(filepath.Join(wsDir, workspaceconfig.ConfigFilename))
		return string(body), buf.String()
	}

	t.Run("default freezes only byte-determining sockets", func(t *testing.T) {
		t.Parallel()
		body, out := capture(t, false)
		for _, want := range byteDetermining {
			if !strings.Contains(body, want) {
				t.Errorf("default capture missing byte-determining %q:\n%s", want, body)
			}
		}
		for _, notWant := range gates {
			if strings.Contains(body, notWant) {
				t.Errorf("default capture froze gate socket %q it should have left global-only:\n%s", notWant, body)
			}
			if strings.Contains(out, notWant) {
				t.Errorf("default capture report named gate socket %q:\n%s", notWant, out)
			}
		}
	})

	t.Run("all-plugins freezes every socket", func(t *testing.T) {
		t.Parallel()
		body, _ := capture(t, true)
		for _, want := range append(append([]string{}, byteDetermining...), gates...) {
			if !strings.Contains(body, want) {
				t.Errorf("--all-plugins capture missing %q:\n%s", want, body)
			}
		}
	})
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
	writeGlobalConfig(t, globalPath, filepath.Join(globalDir, "catalog"))
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
