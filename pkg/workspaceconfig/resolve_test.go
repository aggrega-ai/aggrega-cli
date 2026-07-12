// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestResolveWorkspaceKeyAnchorsRoot: a config carrying a `workspace:` block
// anchors the root; a bare config nearer the start does NOT anchor and is
// climbed past (item 5 narrowing).
func TestResolveWorkspaceKeyAnchorsRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\n")
	// A bare config in a child must NOT hijack the root.
	child := filepath.Join(root, "sub")
	write(t, filepath.Join(child, ".aggrega-config.yaml"), "default_registry: r.io\n")

	rw, err := workspaceconfig.Resolve(child, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if rw.Root() != root {
		t.Errorf("Root: got %q; want %q (workspace:-bearing ancestor anchors; bare child does not)", rw.Root(), root)
	}
	if rw.Marker() != workspaceconfig.MarkerConfig {
		t.Errorf("Marker: got %q; want config", rw.Marker())
	}
	// The bare child config still contributes to the cascade.
	if rw.Config().DefaultRegistry != "r.io" {
		t.Errorf("DefaultRegistry: got %q; want r.io (bare child config contributes)", rw.Config().DefaultRegistry)
	}
}

// TestResolveBareConfigDoesNotMark: a workspace whose only config is bare
// falls through to CWD (no workspace: anchor, no heuristic marker).
func TestResolveBareConfigDoesNotMark(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	start := filepath.Join(root, "a", "b")
	write(t, filepath.Join(root, ".aggrega-config.yaml"), "default_registry: r.io\n")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}
	rw, err := workspaceconfig.Resolve(start, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if rw.Root() != start {
		t.Errorf("Root: got %q; want %q (bare config does not anchor → CWD fallback)", rw.Root(), start)
	}
	if rw.Marker() != workspaceconfig.MarkerCWD {
		t.Errorf("Marker: got %q; want CWD (empty token)", rw.Marker())
	}
}

// TestResolveHeuristicFallback: a `.aggrega/` dir anchors when no
// workspace:-bearing config exists.
func TestResolveHeuristicFallback(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".aggrega"), 0o755); err != nil {
		t.Fatal(err)
	}
	start := filepath.Join(root, "deep")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}
	rw, err := workspaceconfig.Resolve(start, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if rw.Root() != root {
		t.Errorf("Root: got %q; want %q (.aggrega/ heuristic anchor)", rw.Root(), root)
	}
	if rw.Marker() != workspaceconfig.MarkerAggregaDir {
		t.Errorf("Marker: got %q; want directory", rw.Marker())
	}
}

// TestResolveHeuristicGlobMarkers: each glob marker (aggregate/release/
// manifest) anchors a root; a lockfile-suffixed match does NOT. Preserves the
// coverage the daemon's deleted discovery test carried.
func TestResolveHeuristicGlobMarkers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		file   string
		marker workspaceconfig.Marker
	}{
		{"aggregate glob", "aggregate.yaml", workspaceconfig.MarkerAggregateGlob},
		{"aggregate suffixed", "aggregate-staging.yaml", workspaceconfig.MarkerAggregateGlob},
		{"release glob", "release.yaml", workspaceconfig.MarkerReleaseGlob},
		{"manifest glob", "manifest.yaml", workspaceconfig.MarkerManifestGlob},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			write(t, filepath.Join(root, tc.file), "kind: x\n")
			rw, err := workspaceconfig.Resolve(root, "")
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if rw.Marker() != tc.marker {
				t.Errorf("Marker: got %q; want %q", rw.Marker(), tc.marker)
			}
		})
	}

	t.Run("lockfile does not anchor", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		write(t, filepath.Join(root, "release.lock.yaml"), "kind: x\n")
		start := filepath.Join(root, "sub")
		if err := os.MkdirAll(start, 0o755); err != nil {
			t.Fatal(err)
		}
		rw, err := workspaceconfig.Resolve(start, "")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if rw.Marker() != workspaceconfig.MarkerCWD {
			t.Errorf("Marker: got %q; want CWD (a lockfile alone must not anchor)", rw.Marker())
		}
	})
}

// TestResolveWithinDirPriority: within one directory a workspace:-bearing
// config outranks a heuristic glob.
func TestResolveWithinDirPriority(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\n")
	write(t, filepath.Join(root, "aggregate.yaml"), "kind: Aggregate\n")
	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if rw.Marker() != workspaceconfig.MarkerConfig {
		t.Errorf("Marker: got %q; want config (workspace: outranks glob in same dir)", rw.Marker())
	}
}

// TestResolveDeepestWins: a nearer heuristic marker outranks a farther one.
func TestResolveDeepestWins(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\n")
	child := filepath.Join(root, "svc")
	write(t, filepath.Join(child, "aggregate.yaml"), "kind: Aggregate\n")
	// From child: the nearer aggregate.yaml heuristic anchors child, not the
	// farther workspace: config — wait, workspace: outranks across dirs.
	// Deepest-wins applies among heuristics; a workspace: ancestor still wins.
	rw, err := workspaceconfig.Resolve(child, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if rw.Root() != root {
		t.Errorf("Root: got %q; want %q (a workspace: ancestor wins over a nearer heuristic)", rw.Root(), root)
	}
}

// TestResolveCascadeNearerWins: a nearer config overrides a farther one;
// `_append` accumulates across the cascade.
func TestResolveCascadeNearerWins(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, filepath.Join(root, ".aggrega-config.yaml"),
		"workspace:\n  catalog_dirs: [base]\ndefault_registry: root.io\n")
	child := filepath.Join(root, "svc")
	write(t, filepath.Join(child, ".aggrega-config.yaml"),
		"default_registry: child.io\nworkspace:\n  catalog_dirs_append: [extra]\n")

	rw, err := workspaceconfig.Resolve(child, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Root is the nearest workspace:-bearing ancestor — here, child itself.
	if rw.Root() != child {
		t.Errorf("Root: got %q; want %q (nearest workspace: wins)", rw.Root(), child)
	}
	if rw.Config().DefaultRegistry != "child.io" {
		t.Errorf("DefaultRegistry: got %q; want child.io (nearer overrides)", rw.Config().DefaultRegistry)
	}
}

// TestResolveFlagRootOverrides: --workspace-root wins over discovery and
// reports no marker.
func TestResolveFlagRootOverrides(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\n")
	other := t.TempDir()

	rw, err := workspaceconfig.Resolve(root, other)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if rw.Root() != other {
		t.Errorf("Root: got %q; want %q (flag overrides)", rw.Root(), other)
	}
	if rw.Marker() != workspaceconfig.MarkerNone || rw.Marker().String() != "" {
		t.Errorf("Marker: got %q; want none (empty token)", rw.Marker())
	}
}

// TestResolveCascadeAppendAndTopLevel exercises the two cascade dimensions:
// (1) workspace.catalog_dirs_append accumulates across the global layer and the
// workspace:-bearing root (the only layers that can carry a workspace: block);
// (2) a top-level setting (default_registry) cascades independently through a
// bare child rung that nearer-overrides the root and global. This is the
// cross-layer accumulation the shared applyAppends loop performs for all six
// `_append` fields, with catalog_dirs as the representative.
func TestResolveCascadeAppendAndTopLevel(t *testing.T) {
	root := t.TempDir() // not parallel: sets env
	gpath := filepath.Join(t.TempDir(), "global.yaml")
	write(t, gpath, "workspace:\n  catalog_dirs_append: [g]\ndefault_registry: global.io\n")
	t.Setenv("AGGREGA_CONFIG", gpath)

	// Root carries the workspace: block (base catalog_dirs + an append).
	write(t, filepath.Join(root, ".aggrega-config.yaml"),
		"workspace:\n  catalog_dirs: [base]\n  catalog_dirs_append: [p]\ndefault_registry: root.io\n")
	// A bare child rung (no workspace: block) contributes only top-level
	// settings; it does not anchor a root and carries no catalog_dirs.
	child := filepath.Join(root, "svc")
	write(t, filepath.Join(child, ".aggrega-config.yaml"), "default_registry: child.io\n")

	rw, err := workspaceconfig.Resolve(child, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if rw.Root() != root {
		t.Fatalf("Root: got %q; want %q (root carries the only workspace: block)", rw.Root(), root)
	}
	// catalog_dirs: base + global append + root append (the bare child carries
	// none). Each entry is resolved to absolute against the dir of the file that
	// DECLARED it — the global append against the global file's dir, the root
	// base+append against the root dir — proving declaring-file resolution, not
	// a single shared base.
	gdir := filepath.Dir(gpath)
	got := rw.Config().Workspace.CatalogDirs
	want := []string{
		filepath.Join(root, "base"),
		filepath.Join(gdir, "g"),
		filepath.Join(root, "p"),
	}
	if len(got) != len(want) {
		t.Fatalf("CatalogDirs: got %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("CatalogDirs[%d]: got %q; want %q (full: %v)", i, got[i], want[i], got)
		}
	}
	// default_registry cascades top-level: child (nearest) overrides root and global.
	if reg := rw.Config().DefaultRegistry; reg != "child.io" {
		t.Errorf("DefaultRegistry: got %q; want child.io (bare child rung nearer-overrides)", reg)
	}
}

// TestResolveDeclaringFilePaths pins declaring-file path resolution and its
// idempotence across cascade layers: a relative path anchors to the dir of the
// file that wrote it; an absolute value carried from a farther layer is NOT
// re-anchored when a nearer layer overlays; and scheme URIs (oci://) and
// absolute paths pass through untouched.
func TestResolveDeclaringFilePaths(t *testing.T) {
	root := t.TempDir() // sets env, not parallel
	gdir := t.TempDir()
	gpath := filepath.Join(gdir, "global.yaml")
	// Global declares a base linter plugin (relative, anchored to gdir) and an
	// oci policy plugin via append (scheme URI, must pass through).
	write(t, gpath, "plugins:\n  linter:\n    - uri: ./glob.wasm\n  policy_append:\n    - uri: oci://h/p:1\n")
	t.Setenv("AGGREGA_CONFIG", gpath)
	// Root: the workspace: marker, no plugins.linter (so the global's base
	// survives the overlay — the idempotence case), plus a relative and an
	// absolute mutator.
	absPlugin := filepath.Join(root, "abs", "m.wasm")
	write(t, filepath.Join(root, ".aggrega-config.yaml"),
		"workspace: {}\nplugins:\n  mutator:\n    - uri: ./rel.wasm\n    - uri: "+absPlugin+"\n")

	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	cfg := rw.Config()
	// linter base was declared in the global file → anchored to gdir, and the
	// root layer's overlay must NOT re-anchor that absolute to root.
	if got, want := cfg.Plugins.Linter[0].URI, filepath.Join(gdir, "glob.wasm"); got != want {
		t.Errorf("linter URI: got %q; want %q (declaring file is the global; a nearer overlay must not re-anchor it)", got, want)
	}
	// oci:// append passes through unchanged.
	if len(cfg.Plugins.Policy) != 1 || cfg.Plugins.Policy[0].URI != "oci://h/p:1" {
		t.Errorf("policy URI: got %v; want [oci://h/p:1] (scheme URI not resolved)", cfg.Plugins.Policy)
	}
	// root mutator: relative → root-anchored; absolute → unchanged.
	if got, want := cfg.Plugins.Mutator[0].URI, filepath.Join(root, "rel.wasm"); got != want {
		t.Errorf("mutator[0]: got %q; want %q (relative anchored to declaring root)", got, want)
	}
	if got := cfg.Plugins.Mutator[1].URI; got != absPlugin {
		t.Errorf("mutator[1]: got %q; want %q (absolute unchanged)", got, absPlugin)
	}
}

// TestResolvePluginSourcePaths pins that a plugin attachment's with.sources
// entries resolve declaring-file-relative exactly as the plugin URI does: a
// relative source anchors to the dir of the file that wrote it, an absolute
// source passes through untouched, and an append layer's sources anchor to their
// own declaring file rather than the merge's.
func TestResolvePluginSourcePaths(t *testing.T) {
	root := t.TempDir()
	gdir := t.TempDir()
	gpath := filepath.Join(gdir, "global.yaml")
	// Global declares a policy plugin via append whose source is relative to the
	// global file's dir.
	write(t, gpath, "plugins:\n  policy_append:\n    - uri: oci://h/p:1\n      with:\n        sources:\n          - ./g-src.def\n")
	t.Setenv("AGGREGA_CONFIG", gpath)
	// Root: the workspace marker plus a base policy plugin with one relative and
	// one absolute source.
	absSrc := filepath.Join(root, "abs", "policy.def")
	write(t, filepath.Join(root, ".aggrega-config.yaml"),
		"workspace: {}\nplugins:\n  policy:\n    - uri: oci://h/base:1\n      with:\n        sources:\n          - ./rel.def\n          - "+absSrc+"\n")

	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	cfg := rw.Config()
	if len(cfg.Plugins.Policy) != 2 {
		t.Fatalf("policy plugins: got %d, want 2 (base + global append): %+v", len(cfg.Plugins.Policy), cfg.Plugins.Policy)
	}
	base := cfg.Plugins.Policy[0]
	if base.With == nil || len(base.With.Sources) != 2 {
		t.Fatalf("base plugin sources: got %+v, want 2", base.With)
	}
	if got, want := base.With.Sources[0], filepath.Join(root, "rel.def"); got != want {
		t.Errorf("base source[0]: got %q; want %q (relative anchored to declaring root)", got, want)
	}
	if got := base.With.Sources[1]; got != absSrc {
		t.Errorf("base source[1]: got %q; want %q (absolute unchanged)", got, absSrc)
	}
	// The appended plugin's source anchors to the global file's dir, not root.
	appended := cfg.Plugins.Policy[1]
	if appended.With == nil || len(appended.With.Sources) != 1 {
		t.Fatalf("appended plugin sources: got %+v, want 1", appended.With)
	}
	if got, want := appended.With.Sources[0], filepath.Join(gdir, "g-src.def"); got != want {
		t.Errorf("appended source[0]: got %q; want %q (declaring file is the global)", got, want)
	}
}

// appendLens reports the length of each of the six `_append`-target fields, so
// a per-field test can assert an append landed in exactly the right field.
func appendLens(c workspaceconfig.Config) map[string]int {
	return map[string]int{
		"catalog_dirs": len(c.Workspace.CatalogDirs),
		"narrowing":    len(c.Lint.PublishingDiscipline.NarrowingExemptions),
		"mutator":      len(c.Plugins.Mutator),
		"policy":       len(c.Plugins.Policy),
		"linter":       len(c.Plugins.Linter),
		"importer":     len(c.Plugins.Importer),
	}
}

// TestResolveAppendPerField exercises each of the six `_append` fields
// independently. applyAppends is six hand-written append statements, not a
// generic loop, so a cross-wired statement (e.g. policy appending the mutator
// slice) is only caught by a per-field assertion. Each case sets exactly one
// `_append` field with one entry and asserts that field grew by one while every
// other field stayed at its baseline.
func TestResolveAppendPerField(t *testing.T) {
	t.Parallel()
	baselineDir := t.TempDir()
	write(t, filepath.Join(baselineDir, ".aggrega-config.yaml"), "workspace: {}\n")
	baselineRW, err := workspaceconfig.Resolve(baselineDir, "")
	if err != nil {
		t.Fatalf("baseline Resolve: %v", err)
	}
	baseline := appendLens(baselineRW.Config())

	cases := []struct {
		field  string
		config string
	}{
		{"catalog_dirs", "workspace:\n  catalog_dirs_append: [\"x\"]\n"},
		{"narrowing", "workspace: {}\nlint:\n  publishing_discipline:\n    narrowing_exemptions_append:\n      - entity: e\n        paths: [p]\n        reason: r\n"},
		{"mutator", "workspace: {}\nplugins:\n  mutator_append:\n    - uri: oci://x@v1\n"},
		{"policy", "workspace: {}\nplugins:\n  policy_append:\n    - uri: oci://x@v1\n"},
		{"linter", "workspace: {}\nplugins:\n  linter_append:\n    - uri: oci://x@v1\n"},
		{"importer", "workspace: {}\nplugins:\n  importer_append:\n    - uri: oci://x@v1\n"},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			write(t, filepath.Join(dir, ".aggrega-config.yaml"), tc.config)
			rw, err := workspaceconfig.Resolve(dir, "")
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			got := appendLens(rw.Config())
			for field, base := range baseline {
				want := base
				if field == tc.field {
					want = base + 1
				}
				if got[field] != want {
					t.Errorf("setting %s_append: field %q has len %d; want %d (an append must land only in its own field)", tc.field, field, got[field], want)
				}
			}
		})
	}
}

// TestResolveEntryPointParity is the parity guard: every entry point — build,
// pull, inspect, lint, and the daemon — resolves the workspace through this one
// Resolve function, so resolution must be invariant under the starting
// directory. From the root and from two different sub-directories, Resolve
// yields a byte-identical root, merged config, and catalog set. A regression
// that made any entry point resolve differently by CWD (Finding #1's archetype)
// breaks this.
func TestResolveEntryPointParity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, filepath.Join(root, ".aggrega-config.yaml"),
		"workspace:\n  catalog_dirs: [catalog, vendor/catalog]\ndefault_registry: reg.example\n")
	subA := filepath.Join(root, "services", "api")
	subB := filepath.Join(root, "deploy")
	for _, d := range []string{subA, subB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	fromRoot, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("Resolve(root): %v", err)
	}
	for _, start := range []string{subA, subB} {
		rw, err := workspaceconfig.Resolve(start, "")
		if err != nil {
			t.Fatalf("Resolve(%s): %v", start, err)
		}
		if rw.Root() != fromRoot.Root() {
			t.Errorf("Root from %s: got %q; want %q (resolution must not vary by entry CWD)", start, rw.Root(), fromRoot.Root())
		}
		if rw.Config().DefaultRegistry != fromRoot.Config().DefaultRegistry {
			t.Errorf("DefaultRegistry from %s: got %q; want %q", start, rw.Config().DefaultRegistry, fromRoot.Config().DefaultRegistry)
		}
		gotDirs, wantDirs := rw.Config().Workspace.CatalogDirs, fromRoot.Config().Workspace.CatalogDirs
		if len(gotDirs) != len(wantDirs) {
			t.Errorf("CatalogDirs from %s: got %v; want %v", start, gotDirs, wantDirs)
			continue
		}
		for i := range wantDirs {
			if gotDirs[i] != wantDirs[i] {
				t.Errorf("CatalogDirs[%d] from %s: got %q; want %q", i, start, gotDirs[i], wantDirs[i])
			}
		}
	}
}

// TestResolveGlobalLayer: $AGGREGA_CONFIG contributes beneath the tree.
func TestResolveGlobalLayer(t *testing.T) {
	root := t.TempDir() // not parallel: sets env
	gpath := filepath.Join(t.TempDir(), "global.yaml")
	write(t, gpath, "default_registry: global.io\n")
	t.Setenv("AGGREGA_CONFIG", gpath)
	write(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\n")

	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if rw.Config().DefaultRegistry != "global.io" {
		t.Errorf("DefaultRegistry: got %q; want global.io (global layer applies under empty workspace)", rw.Config().DefaultRegistry)
	}
	if rw.Source() != workspaceconfig.SourceWorkspace {
		t.Errorf("Source: got %q; want workspace (a workspace file was loaded)", rw.Source())
	}
}
