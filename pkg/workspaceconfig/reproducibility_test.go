// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// resolveWithGlobal wires a temp XDG global config plus a temp workspace tree
// under a frozen lookup, resolves, and returns the ResolvedWorkspace. globalYAML
// is written to $XDG_CONFIG_HOME/aggrega/config.yaml (skipped when empty);
// treeYAML is written to the workspace root's config file (skipped when empty).
func resolveWithGlobal(t *testing.T, globalYAML, treeYAML string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	xdg := t.TempDir()
	if globalYAML != "" {
		write(t, filepath.Join(xdg, "aggrega", "config.yaml"), globalYAML)
	}
	root := t.TempDir()
	if treeYAML != "" {
		write(t, filepath.Join(root, ".aggrega-config.yaml"), treeYAML)
	}
	rw, err := workspaceconfig.Resolve(root, "", frozenLookup(map[string]string{
		"XDG_CONFIG_HOME": xdg,
	}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return rw
}

// fieldsOf returns the Field values of the bindings, in order.
func fieldsOf(bindings []workspaceconfig.BuildCriticalBinding) []string {
	out := make([]string, len(bindings))
	for i, b := range bindings {
		out[i] = b.Field
	}
	return out
}

// TestGlobalOnlyBuildCriticalProviderGlobalOnly: a provider binding present only
// in the global layer is reported as global-only, naming the capability.
func TestGlobalOnlyBuildCriticalProviderGlobalOnly(t *testing.T) {
	t.Parallel()
	rw := resolveWithGlobal(t,
		"plugins:\n  providers:\n    - capability: helm\n      uri: oci://h/helm:1\n",
		"",
	)
	got, err := rw.GlobalOnlyBuildCritical()
	if err != nil {
		t.Fatalf("GlobalOnlyBuildCritical: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("bindings: got %d, want 1: %+v", len(got), got)
	}
	if got[0].Field != "plugins.providers" {
		t.Errorf("Field: got %q, want plugins.providers", got[0].Field)
	}
	if !strings.Contains(got[0].Identity, "helm") {
		t.Errorf("Identity: got %q, want it to name the capability helm", got[0].Identity)
	}
}

// TestGlobalOnlyBuildCriticalProviderInBoth: the same provider binding present
// in the workspace file too is NOT reported — the tree carries it.
func TestGlobalOnlyBuildCriticalProviderInBoth(t *testing.T) {
	t.Parallel()
	rw := resolveWithGlobal(t,
		"plugins:\n  providers:\n    - capability: helm\n      uri: oci://h/helm:1\n",
		"workspace: {}\nplugins:\n  providers:\n    - capability: helm\n      uri: oci://h/helm:1\n",
	)
	got, err := rw.GlobalOnlyBuildCritical()
	if err != nil {
		t.Fatalf("GlobalOnlyBuildCritical: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("bindings: got %d, want 0 (capability present in the workspace tree): %+v", len(got), got)
	}
}

// TestGlobalOnlyBuildCriticalAppendInBothIdentity: the load-bearing set-diff
// case. The same socket attachment appended by BOTH layers yields a duplicated
// entry in the full resolution; a multiset diff would wrongly flag the extra
// copy, but the set-difference over entry identity reports nothing because the
// entry's identity is present in the tree-only resolution.
func TestGlobalOnlyBuildCriticalAppendInBothIdentity(t *testing.T) {
	t.Parallel()
	rw := resolveWithGlobal(t,
		"plugins:\n  mutator_append:\n    - uri: oci://h/mut:1\n",
		"workspace: {}\nplugins:\n  mutator_append:\n    - uri: oci://h/mut:1\n",
	)
	// Sanity: the full resolution really does carry the entry twice (both
	// appends concatenate), so this exercises the set-vs-multiset distinction.
	if n := len(rw.Config().Plugins.Mutator); n != 2 {
		t.Fatalf("full mutator list: got %d entries, want 2 (both appends concatenate): %+v", n, rw.Config().Plugins.Mutator)
	}
	got, err := rw.GlobalOnlyBuildCritical()
	if err != nil {
		t.Fatalf("GlobalOnlyBuildCritical: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("bindings: got %d, want 0 (identity present in both layers): %+v", len(got), got)
	}
}

// TestGlobalOnlyBuildCriticalFullyLocal: a workspace that declares every
// build-critical binding itself, with no global layer, reports nothing.
func TestGlobalOnlyBuildCriticalFullyLocal(t *testing.T) {
	t.Parallel()
	rw := resolveWithGlobal(t,
		"",
		"workspace:\n  catalog_dirs:\n    - ./cat\nplugins:\n  providers:\n    - capability: helm\n      uri: oci://h/helm:1\n  mutator:\n    - uri: oci://h/mut:1\n",
	)
	got, err := rw.GlobalOnlyBuildCritical()
	if err != nil {
		t.Fatalf("GlobalOnlyBuildCritical: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("bindings: got %d, want 0 (fully local workspace): %+v", len(got), got)
	}
}

// TestGlobalOnlyBuildCriticalScalarAndCatalogDirs: a global-only
// plugin_sidecar_dir and a global-only catalog directory are both reported.
func TestGlobalOnlyBuildCriticalScalarAndCatalogDirs(t *testing.T) {
	t.Parallel()
	rw := resolveWithGlobal(t,
		"plugin_sidecar_dir: ./sidecar\nworkspace:\n  catalog_dirs:\n    - ./gcat\n",
		"",
	)
	got, err := rw.GlobalOnlyBuildCritical()
	if err != nil {
		t.Fatalf("GlobalOnlyBuildCritical: %v", err)
	}
	if want := []string{"plugin_sidecar_dir", "workspace.catalog_dirs"}; !reflect.DeepEqual(fieldsOf(got), want) {
		t.Fatalf("fields: got %v, want %v: %+v", fieldsOf(got), want, got)
	}
	// The identities are the resolved (absolute) values, so each names a real
	// on-disk path rather than the relative spelling.
	for _, b := range got {
		if !filepath.IsAbs(b.Identity) {
			t.Errorf("Identity %q for %s: want an absolute resolved path", b.Identity, b.Field)
		}
	}
}

// TestGlobalOnlyBuildCriticalSidecarLocalWins: a plugin_sidecar_dir set by BOTH
// layers is not reported — the workspace value wins and the tree carries it.
func TestGlobalOnlyBuildCriticalSidecarLocalWins(t *testing.T) {
	t.Parallel()
	rw := resolveWithGlobal(t,
		"plugin_sidecar_dir: ./gsidecar\n",
		"workspace: {}\nplugin_sidecar_dir: ./lsidecar\n",
	)
	got, err := rw.GlobalOnlyBuildCritical()
	if err != nil {
		t.Fatalf("GlobalOnlyBuildCritical: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("bindings: got %d, want 0 (workspace sets plugin_sidecar_dir): %+v", len(got), got)
	}
}

// TestGlobalOnlyBuildCriticalEnvScalarOutOfScope: catalog_dirs supplied through
// the environment overlay applies to both passes identically, so it cancels out
// of the diff — the guardrail speaks only to the global config file.
func TestGlobalOnlyBuildCriticalEnvScalarOutOfScope(t *testing.T) {
	t.Parallel()
	xdg := t.TempDir()
	write(t, filepath.Join(xdg, "aggrega", "config.yaml"),
		"workspace:\n  catalog_dirs:\n    - /global/cat\n")
	root := t.TempDir()
	rw, err := workspaceconfig.Resolve(root, "", frozenLookup(map[string]string{
		"XDG_CONFIG_HOME":      xdg,
		"AGGREGA_CATALOG_DIRS": "/env/cat",
	}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// The env value whole-replaces catalog_dirs in both passes.
	if got := rw.Config().Workspace.CatalogDirs; !reflect.DeepEqual(got, []string{"/env/cat"}) {
		t.Fatalf("catalog_dirs: got %v, want [/env/cat] (env overlay wins)", got)
	}
	bindings, err := rw.GlobalOnlyBuildCritical()
	if err != nil {
		t.Fatalf("GlobalOnlyBuildCritical: %v", err)
	}
	if len(bindings) != 0 {
		t.Fatalf("bindings: got %d, want 0 (env catalog_dirs cancels across passes): %+v", len(bindings), bindings)
	}
}

// TestGlobalOnlyBuildCriticalDeterministic: two calls on the same resolution
// yield byte-identical binding slices in a stable order.
func TestGlobalOnlyBuildCriticalDeterministic(t *testing.T) {
	t.Parallel()
	rw := resolveWithGlobal(t,
		"plugin_sidecar_dir: ./sidecar\nplugins:\n  providers:\n    - capability: helm\n      uri: oci://h/helm:1\n  mutator:\n    - uri: oci://h/mut:1\n  policy:\n    - uri: oci://h/pol:1\nworkspace:\n  catalog_dirs:\n    - ./gcat\n",
		"",
	)
	first, err := rw.GlobalOnlyBuildCritical()
	if err != nil {
		t.Fatalf("GlobalOnlyBuildCritical (first): %v", err)
	}
	second, err := rw.GlobalOnlyBuildCritical()
	if err != nil {
		t.Fatalf("GlobalOnlyBuildCritical (second): %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("non-deterministic bindings:\n first=%+v\nsecond=%+v", first, second)
	}
	// Fixed field order: providers, mutator, policy, then the scalar, then
	// catalog_dirs.
	want := []string{"plugins.providers", "plugins.mutator", "plugins.policy", "plugin_sidecar_dir", "workspace.catalog_dirs"}
	if !reflect.DeepEqual(fieldsOf(first), want) {
		t.Fatalf("field order: got %v, want %v", fieldsOf(first), want)
	}
}
