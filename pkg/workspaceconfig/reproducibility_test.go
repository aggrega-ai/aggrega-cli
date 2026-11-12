// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
		"plugins:\n  providers:\n    - capability: helm\n      uri: provider/helm:1\n",
		"",
	)
	got, err := rw.GlobalOnlyBuildCritical(false)
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
		"plugins:\n  providers:\n    - capability: helm\n      uri: provider/helm:1\n",
		"workspace: {}\nplugins:\n  providers:\n    - capability: helm\n      uri: provider/helm:1\n",
	)
	got, err := rw.GlobalOnlyBuildCritical(false)
	if err != nil {
		t.Fatalf("GlobalOnlyBuildCritical: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("bindings: got %d, want 0 (capability present in the workspace tree): %+v", len(got), got)
	}
}

// TestGlobalOnlyBuildCriticalMutatorByteDetermining: a label-transform governance
// binding present only in the global layer is byte-determining, so it is reported
// under the default (byte-determining) scope.
func TestGlobalOnlyBuildCriticalMutatorByteDetermining(t *testing.T) {
	t.Parallel()
	rw := resolveWithGlobal(t,
		"plugins:\n  governance:\n    - uri: oci://h/mut:1\n      role: label-transform\n",
		"",
	)
	got, err := rw.GlobalOnlyBuildCritical(false)
	if err != nil {
		t.Fatalf("GlobalOnlyBuildCritical: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("bindings: got %d, want 1 (label-transform is byte-determining): %+v", len(got), got)
	}
	if got[0].Field != "plugins.governance" {
		t.Errorf("Field: got %q, want plugins.governance", got[0].Field)
	}
}

// TestGlobalOnlyBuildCriticalGateSocketScopedByAllPlugins: a global-only verdict
// governance attachment is NOT byte-determining, so it is absent under the default
// scope and reported only when the scope widens to all plugin sockets.
func TestGlobalOnlyBuildCriticalGateSocketScopedByAllPlugins(t *testing.T) {
	t.Parallel()
	rw := resolveWithGlobal(t,
		"plugins:\n  governance:\n    - uri: oci://h/pol:1\n      role: ir-verdict\n",
		"",
	)
	def, err := rw.GlobalOnlyBuildCritical(false)
	if err != nil {
		t.Fatalf("GlobalOnlyBuildCritical(false): %v", err)
	}
	if len(def) != 0 {
		t.Fatalf("default scope: got %d, want 0 (a verdict role is not byte-determining): %+v", len(def), def)
	}
	all, err := rw.GlobalOnlyBuildCritical(true)
	if err != nil {
		t.Fatalf("GlobalOnlyBuildCritical(true): %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("all-plugins scope: got %d, want 1: %+v", len(all), all)
	}
	if all[0].Field != "plugins.governance" {
		t.Errorf("Field: got %q, want plugins.governance", all[0].Field)
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
		"plugins:\n  governance_append:\n    - uri: oci://h/mut:1\n      role: label-transform\n",
		"workspace: {}\nplugins:\n  governance_append:\n    - uri: oci://h/mut:1\n      role: label-transform\n",
	)
	// Sanity: the full resolution really does carry the entry twice (both
	// appends concatenate), so this exercises the set-vs-multiset distinction.
	if n := len(rw.Config().Plugins.Governance); n != 2 {
		t.Fatalf("full governance list: got %d entries, want 2 (both appends concatenate): %+v", n, rw.Config().Plugins.Governance)
	}
	got, err := rw.GlobalOnlyBuildCritical(false)
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
		"workspace:\n  catalog_dirs:\n    - ./cat\nplugins:\n  providers:\n    - capability: helm\n      uri: provider/helm:1\n  governance:\n    - uri: oci://h/mut:1\n      role: label-transform\n",
	)
	got, err := rw.GlobalOnlyBuildCritical(false)
	if err != nil {
		t.Fatalf("GlobalOnlyBuildCritical: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("bindings: got %d, want 0 (fully local workspace): %+v", len(got), got)
	}
}

// TestGlobalOnlyBuildCriticalCatalogDirs: a global-only catalog directory is
// reported, with its identity resolved to an absolute on-disk path.
func TestGlobalOnlyBuildCriticalCatalogDirs(t *testing.T) {
	t.Parallel()
	rw := resolveWithGlobal(t,
		"workspace:\n  catalog_dirs:\n    - ./gcat\n",
		"",
	)
	got, err := rw.GlobalOnlyBuildCritical(false)
	if err != nil {
		t.Fatalf("GlobalOnlyBuildCritical: %v", err)
	}
	if want := []string{"workspace.catalog_dirs"}; !reflect.DeepEqual(fieldsOf(got), want) {
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
	bindings, err := rw.GlobalOnlyBuildCritical(false)
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
		"plugins:\n  providers:\n    - capability: helm\n      uri: provider/helm:1\n  governance:\n    - uri: oci://h/mut:1\n      role: label-transform\n    - uri: oci://h/pol:1\n      role: ir-verdict\nworkspace:\n  catalog_dirs:\n    - ./gcat\n",
		"",
	)
	// The all-plugins scope so every socket field participates in the order check.
	first, err := rw.GlobalOnlyBuildCritical(true)
	if err != nil {
		t.Fatalf("GlobalOnlyBuildCritical (first): %v", err)
	}
	second, err := rw.GlobalOnlyBuildCritical(true)
	if err != nil {
		t.Fatalf("GlobalOnlyBuildCritical (second): %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("non-deterministic bindings:\n first=%+v\nsecond=%+v", first, second)
	}
	// Fixed field order: providers, then the governance attachments in authored
	// order (label-transform, then ir-verdict), then catalog_dirs.
	want := []string{"plugins.providers", "plugins.governance", "plugins.governance", "workspace.catalog_dirs"}
	if !reflect.DeepEqual(fieldsOf(first), want) {
		t.Fatalf("field order: got %v, want %v", fieldsOf(first), want)
	}
}
