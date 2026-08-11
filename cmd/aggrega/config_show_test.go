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

// writeFile writes body to a fresh file at dir/name and returns its path.
func writeShowFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// showGlobalOnly resolves a workspace with a global config and a pinned root that
// carries no workspace-tree config, so the global file is the only contributor.
func showGlobalOnly(t *testing.T, globalBody string) (*workspaceconfig.ResolvedWorkspace, string, func(string) string) {
	t.Helper()
	root := t.TempDir()
	global := writeShowFile(t, filepath.Join(t.TempDir(), "aggrega"), "config.yaml", globalBody)
	lookup := frozenEnv(map[string]string{workspaceconfig.EnvConfigOverride: global})
	rw, err := workspaceconfig.Resolve("", root, lookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return rw, global, lookup
}

// TestConfigShowGlobalOnly covers scenario (a): a global config and no workspace
// tree. The default view leads with the one global file then the effective
// config; --global dumps it verbatim; --origin attributes its values to it.
func TestConfigShowGlobalOnly(t *testing.T) {
	t.Parallel()
	globalBody := "default_registry: global.example\noffline: true\n"
	rw, global, lookup := showGlobalOnly(t, globalBody)

	// Default view.
	var def bytes.Buffer
	if err := configShowRun(configShowConfig{ws: rw, lookup: lookup, Out: &def}); err != nil {
		t.Fatalf("default show: %v", err)
	}
	out := def.String()
	if !strings.Contains(out, "Configuration loaded from (lowest precedence first):") {
		t.Errorf("default view missing the loaded-from lead:\n%s", out)
	}
	if !strings.Contains(out, global) {
		t.Errorf("default view does not name the global file %q:\n%s", global, out)
	}
	if !strings.Contains(out, "Effective configuration:") || !strings.Contains(out, "default_registry: global.example") {
		t.Errorf("default view missing the effective config:\n%s", out)
	}

	// Single-layer verbatim dump.
	var glob bytes.Buffer
	if err := configShowRun(configShowConfig{ws: rw, lookup: lookup, Global: true, Out: &glob}); err != nil {
		t.Fatalf("--global show: %v", err)
	}
	if glob.String() != globalBody {
		t.Errorf("--global dump = %q; want the file's own bytes %q", glob.String(), globalBody)
	}

	// Origin attribution.
	origins := showOrigins(t, rw)
	if src := origins["default_registry"]; src != global {
		t.Errorf("default_registry source = %q; want the global file %q", src, global)
	}
	if src := origins["offline"]; src != global {
		t.Errorf("offline source = %q; want the global file %q", src, global)
	}
}

// TestConfigShowWorkspaceCascade covers scenario (b): global plus a
// workspace-tree config that overrides some values. The cascade lists both in
// order; the effective config reflects the override; --workspace dumps the tree
// file verbatim; --origin attributes each scalar to the correct last-writer.
func TestConfigShowWorkspaceCascade(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	treeBody := "workspace: {}\ndefault_registry: from-tree.io\n"
	treePath := writeShowFile(t, root, workspaceconfig.ConfigFilename, treeBody)
	global := writeShowFile(t, filepath.Join(t.TempDir(), "aggrega"), "config.yaml",
		"default_registry: from-global.io\noffline: true\n")
	lookup := frozenEnv(map[string]string{workspaceconfig.EnvConfigOverride: global})
	rw, err := workspaceconfig.Resolve("", root, lookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Default view: both files, global before tree; effective reflects override.
	var def bytes.Buffer
	if err := configShowRun(configShowConfig{ws: rw, lookup: lookup, Out: &def}); err != nil {
		t.Fatalf("default show: %v", err)
	}
	out := def.String()
	gi, ti := strings.Index(out, global), strings.Index(out, treePath)
	if gi < 0 || ti < 0 || gi > ti {
		t.Errorf("cascade order wrong: global at %d, tree at %d (want global before tree):\n%s", gi, ti, out)
	}
	if !strings.Contains(out, "default_registry: from-tree.io") {
		t.Errorf("effective default_registry should be the tree override:\n%s", out)
	}

	// --workspace verbatim dump.
	var ws bytes.Buffer
	if err := configShowRun(configShowConfig{ws: rw, lookup: lookup, Workspace: true, Out: &ws}); err != nil {
		t.Fatalf("--workspace show: %v", err)
	}
	if ws.String() != treeBody {
		t.Errorf("--workspace dump = %q; want the tree file's own bytes %q", ws.String(), treeBody)
	}

	// --origin: scalar attribution to the correct last-writer.
	origins := showOrigins(t, rw)
	if src := origins["default_registry"]; src != treePath {
		t.Errorf("default_registry source = %q; want the tree file %q (last writer wins)", src, treePath)
	}
	if src := origins["offline"]; src != global {
		t.Errorf("offline source = %q; want the global file %q", src, global)
	}
	if src := origins["auto_pull"]; src != "(built-in defaults)" {
		t.Errorf("auto_pull source = %q; want the built-in defaults (no layer set it)", src)
	}
}

// TestConfigShowEnvOverlay covers scenario (c): an environment variable the
// resolver overlays. --origin attributes that value to the env var.
func TestConfigShowEnvOverlay(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeShowFile(t, root, workspaceconfig.ConfigFilename, "workspace: {}\ndefault_registry: from-tree.io\n")
	global := writeShowFile(t, filepath.Join(t.TempDir(), "aggrega"), "config.yaml", "default_registry: from-global.io\n")
	lookup := frozenEnv(map[string]string{
		workspaceconfig.EnvConfigOverride: global,
		"AGGREGA_DEFAULT_REGISTRY":        "from-env.io",
	})
	rw, err := workspaceconfig.Resolve("", root, lookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got := rw.Config().DefaultRegistry; got != "from-env.io" {
		t.Fatalf("precondition: effective default_registry = %q; want the env overlay from-env.io", got)
	}

	origins := showOrigins(t, rw)
	if src := origins["default_registry"]; src != "env:AGGREGA_DEFAULT_REGISTRY" {
		t.Errorf("default_registry source = %q; want env:AGGREGA_DEFAULT_REGISTRY", src)
	}

	// The env value is what --origin displays for the setting.
	var buf bytes.Buffer
	if err := configShowRun(configShowConfig{ws: rw, lookup: lookup, Origin: true, Out: &buf}); err != nil {
		t.Fatalf("--origin show: %v", err)
	}
	if !strings.Contains(buf.String(), "from-env.io") {
		t.Errorf("--origin output should display the effective env value from-env.io:\n%s", buf.String())
	}
}

// TestConfigShowSigningRequiredMonotonic proves the one setting that composes
// monotonically rather than whole-replacing per layer is attributed to the first
// layer that turns it on — even when a higher-precedence layer writes it off, the
// effective value stays on and the source names the layer that set it on.
func TestConfigShowSigningRequiredMonotonic(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeShowFile(t, root, workspaceconfig.ConfigFilename, "workspace: {}\nsigning:\n  required: false\n")
	global := writeShowFile(t, filepath.Join(t.TempDir(), "aggrega"), "config.yaml", "signing:\n  required: true\n")
	lookup := frozenEnv(map[string]string{workspaceconfig.EnvConfigOverride: global})
	rw, err := workspaceconfig.Resolve("", root, lookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if !rw.Config().Signing.Required {
		t.Fatal("precondition: signing.required should compose to true (a lower layer set it on; no layer can turn it off)")
	}
	origins := showOrigins(t, rw)
	if src := origins["signing.required"]; src != global {
		t.Errorf("signing.required source = %q; want the global file %q (first layer to turn it on)", src, global)
	}
}

// TestConfigShowDeterministic proves the effective marshal and the origin listing
// are byte-identical across runs of the same input.
func TestConfigShowDeterministic(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeShowFile(t, root, workspaceconfig.ConfigFilename, "workspace: {}\ndefault_registry: from-tree.io\n")
	global := writeShowFile(t, filepath.Join(t.TempDir(), "aggrega"), "config.yaml", "default_registry: from-global.io\noffline: true\n")
	lookup := frozenEnv(map[string]string{workspaceconfig.EnvConfigOverride: global})
	rw, err := workspaceconfig.Resolve("", root, lookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// The workspace is declared in each literal rather than patched in here, so
	// every call site says which workspace it renders. Only the sink and the
	// environment lookup, which are the same for every run, are filled in.
	run := func(cfg configShowConfig) string {
		var buf bytes.Buffer
		cfg.lookup, cfg.Out = lookup, &buf
		if err := configShowRun(cfg); err != nil {
			t.Fatalf("show: %v", err)
		}
		return buf.String()
	}

	if a, b := run(configShowConfig{ws: rw}), run(configShowConfig{ws: rw}); a != b {
		t.Errorf("default view not byte-identical across runs:\n%q\n%q", a, b)
	}
	if a, b := run(configShowConfig{ws: rw, Origin: true}), run(configShowConfig{ws: rw, Origin: true}); a != b {
		t.Errorf("origin view not byte-identical across runs:\n%q\n%q", a, b)
	}
}

// TestConfigShowModeGuards proves the mutually-exclusive mode flags are refused
// and missing single-layer files report an informative line rather than crash.
func TestConfigShowModeGuards(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// A pinned root with no tree config and a global path that does not exist.
	missingGlobal := filepath.Join(t.TempDir(), "aggrega", "config.yaml")
	lookup := frozenEnv(map[string]string{workspaceconfig.EnvConfigOverride: missingGlobal})
	rw, err := workspaceconfig.ResolveWithoutGlobal("", root, lookup)
	if err != nil {
		t.Fatalf("ResolveWithoutGlobal: %v", err)
	}

	if err := configShowRun(configShowConfig{ws: rw, lookup: lookup, Global: true, Workspace: true, Out: &bytes.Buffer{}}); err == nil {
		t.Error("--global and --workspace together should be refused")
	}
	if err := configShowRun(configShowConfig{ws: rw, lookup: lookup, Origin: true, Global: true, Out: &bytes.Buffer{}}); err == nil {
		t.Error("--origin with --global should be refused")
	}

	var glob bytes.Buffer
	if err := configShowRun(configShowConfig{ws: rw, lookup: lookup, Global: true, Out: &glob}); err != nil {
		t.Fatalf("--global on a missing file should not error: %v", err)
	}
	if !strings.Contains(glob.String(), "No global config file exists") {
		t.Errorf("--global on a missing file should say so, got: %q", glob.String())
	}

	var ws bytes.Buffer
	if err := configShowRun(configShowConfig{ws: rw, lookup: lookup, Workspace: true, Out: &ws}); err != nil {
		t.Fatalf("--workspace on a missing file should not error: %v", err)
	}
	if !strings.Contains(ws.String(), "No workspace config file exists") {
		t.Errorf("--workspace on a missing file should say so, got: %q", ws.String())
	}
}

// showOrigins runs ValueOrigins and returns a field→source map for assertions.
func showOrigins(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) map[string]string {
	t.Helper()
	origins, err := rw.ValueOrigins()
	if err != nil {
		t.Fatalf("ValueOrigins: %v", err)
	}
	m := make(map[string]string, len(origins))
	for _, o := range origins {
		m[o.Field] = o.Source
	}
	return m
}
