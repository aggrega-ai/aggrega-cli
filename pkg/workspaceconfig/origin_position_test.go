// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// originsByField runs ValueOrigins and indexes the result by setting name.
func originsByField(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) map[string]workspaceconfig.ValueOrigin {
	t.Helper()
	origins, err := rw.ValueOrigins()
	if err != nil {
		t.Fatalf("ValueOrigins: %v", err)
	}
	byField := make(map[string]workspaceconfig.ValueOrigin, len(origins))
	for _, o := range origins {
		byField[o.Field] = o
	}
	return byField
}

// keyTextAt is the consumer every position assertion below runs through: it
// takes a reported span and cuts the bytes it addresses out of the file the
// origin names. Nothing here searches for a key — the span decides which bytes
// are read, so a span that is zero, constant, or off by a line yields the wrong
// text (or no text at all) instead of quietly passing.
func keyTextAt(t *testing.T, path string, span *yamlpos.FieldSpan) string {
	t.Helper()
	if span == nil {
		t.Fatal("keyTextAt: no position was reported, so there is nothing to read")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(data), "\n")
	if span.Line < 1 || span.Line > len(lines) {
		t.Fatalf("reported line %d is outside %s, which has %d lines", span.Line, path, len(lines))
	}
	line := lines[span.Line-1]
	if span.Column < 1 || span.EndColumn < span.Column || span.EndColumn-1 > len(line) {
		t.Fatalf("reported columns [%d,%d) do not address line %d of %s (%q)",
			span.Column, span.EndColumn, span.Line, path, line)
	}
	return line[span.Column-1 : span.EndColumn-1]
}

// naiveSearchLine is the negative control for
// TestValueOriginPositionNamesTheKeyUnderTheRightParent: the 1-based line of
// the FIRST textual occurrence of key in the file, which is what positioning by
// string search would report. Zero when the key does not occur at all.
func naiveSearchLine(t *testing.T, path, key string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	i := strings.Index(string(data), key)
	if i < 0 {
		return 0
	}
	return strings.Count(string(data)[:i], "\n") + 1
}

// TestValueOriginPositionFollowsTheAuthoredKey pins the position to the key as
// the document actually carries it. The two rows author the same setting at
// different coordinates in otherwise-equivalent documents, so the expectations
// cannot both be met by a field that reports a constant — moving the setting
// inside the file has to move the assertion with it, and the rows assert that
// the two positions differ as well as that each is right.
func TestValueOriginPositionFollowsTheAuthoredKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		document   string
		wantLine   int
		wantColumn int
	}{
		{
			name: "near the top of a short document",
			document: "workspace: {}\n" +
				"build:\n" +
				"  yaml_indent: 4\n",
			wantLine:   3,
			wantColumn: 3,
		},
		{
			name: "further down, and more deeply indented",
			document: "workspace: {}\n" +
				"default_registry: registry.example\n" +
				"signing:\n" +
				"  required: false\n" +
				"\n" +
				"build:\n" +
				"    yaml_indent: 4\n",
			wantLine:   7,
			wantColumn: 5,
		},
	}

	seen := make(map[[2]int]string, len(cases))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, workspaceconfig.ConfigFilename)
			write(t, path, tc.document)

			rw, err := workspaceconfig.ResolveWithoutGlobal("", root, frozenLookup(nil))
			if err != nil {
				t.Fatalf("ResolveWithoutGlobal: %v", err)
			}
			origin := originsByField(t, rw)["build.yaml_indent"]
			if origin.Source != path {
				t.Fatalf("precondition: build.yaml_indent source = %q; want the workspace file %q", origin.Source, path)
			}
			if origin.Position == nil {
				t.Fatal("build.yaml_indent came from a config file, so it must carry a position")
			}
			if origin.Position.Line != tc.wantLine || origin.Position.Column != tc.wantColumn {
				t.Errorf("build.yaml_indent position = %d:%d; want %d:%d",
					origin.Position.Line, origin.Position.Column, tc.wantLine, tc.wantColumn)
			}
			if got := keyTextAt(t, path, origin.Position); got != "yaml_indent" {
				t.Errorf("the reported span addresses %q; want the key %q", got, "yaml_indent")
			}
		})
		seen[[2]int{tc.wantLine, tc.wantColumn}] = tc.name
	}
	if len(seen) != len(cases) {
		t.Fatalf("the rows expect %d distinct positions but only %d are distinct, "+
			"so a constant position would satisfy them", len(cases), len(seen))
	}
}

// TestValueOriginPositionNamesTheKeyUnderTheRightParent is the trap the
// document walk exists to avoid. The config schema mounts the signing block
// twice — once at the top level and once per registry entry — so the leaf
// `kinds` occurs under two different parents in one document, and the registry
// copy is authored FIRST. A string search for the key finds the registry copy;
// the walk finds the key that actually supplied the effective value. The test
// asserts both halves, so it fails if the walk is ever replaced by a search.
func TestValueOriginPositionNamesTheKeyUnderTheRightParent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, workspaceconfig.ConfigFilename)
	write(t, path, "workspace: {}\n"+
		"registries:\n"+
		"  - host: other.example\n"+
		"    signing:\n"+
		"      kinds:\n"+
		"        - Component\n"+
		"signing:\n"+
		"  kinds:\n"+
		"    - Recipe\n")

	rw, err := workspaceconfig.ResolveWithoutGlobal("", root, frozenLookup(nil))
	if err != nil {
		t.Fatalf("ResolveWithoutGlobal: %v", err)
	}
	origin := originsByField(t, rw)["signing.kinds"]
	if origin.Source != path {
		t.Fatalf("precondition: signing.kinds source = %q; want the workspace file %q", origin.Source, path)
	}
	if origin.Position == nil {
		t.Fatal("signing.kinds came from a config file, so it must carry a position")
	}

	const wantLine = 8 // the `kinds:` under the TOP-LEVEL signing block
	if origin.Position.Line != wantLine || origin.Position.Column != 3 {
		t.Errorf("signing.kinds position = %d:%d; want %d:3 (the top-level signing block)",
			origin.Position.Line, origin.Position.Column, wantLine)
	}
	if got := keyTextAt(t, path, origin.Position); got != "kinds" {
		t.Errorf("the reported span addresses %q; want the key %q", got, "kinds")
	}

	// The negative control: confirm this fixture really does trap a search,
	// rather than merely being a document a search would also get right.
	searchLine := naiveSearchLine(t, path, "kinds:")
	if searchLine == 0 {
		t.Fatal("the fixture no longer contains the key at all, so the control proves nothing")
	}
	if searchLine == wantLine {
		t.Fatalf("the first textual occurrence of %q is on line %d, the same line the walk reports, "+
			"so this fixture no longer distinguishes a document walk from a string search", "kinds:", searchLine)
	}
}

// TestValueOriginPositionAbsentWithoutADocument holds the absence readable. A
// setting an environment override or the built-in defaults determined has no
// file and therefore no position, and the check that says so is a nil test
// rather than a comparison against a coordinate — which is why the same test
// also pins a setting authored as the very first key of a file at line 1,
// column 1. A position at the start of a file and no position at all are
// different values, not the same zero read two ways.
func TestValueOriginPositionAbsentWithoutADocument(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, workspaceconfig.ConfigFilename)
	write(t, path, "default_registry: from-file.example\n"+
		"workspace: {}\n")

	rw, err := workspaceconfig.ResolveWithoutGlobal("", root, frozenLookup(map[string]string{
		"AGGREGA_AUTO_PULL": "true",
	}))
	if err != nil {
		t.Fatalf("ResolveWithoutGlobal: %v", err)
	}
	byField := originsByField(t, rw)

	fromFile := byField["default_registry"]
	if fromFile.Source != path {
		t.Fatalf("precondition: default_registry source = %q; want the workspace file %q", fromFile.Source, path)
	}
	if fromFile.Position == nil {
		t.Fatal("the first key of a config file must carry a position, not report absence")
	}
	if fromFile.Position.Line != 1 || fromFile.Position.Column != 1 {
		t.Errorf("default_registry position = %d:%d; want 1:1", fromFile.Position.Line, fromFile.Position.Column)
	}
	if got := keyTextAt(t, path, fromFile.Position); got != "default_registry" {
		t.Errorf("the reported span addresses %q; want the key %q", got, "default_registry")
	}

	fromEnv := byField["auto_pull"]
	if fromEnv.Source != "env:AGGREGA_AUTO_PULL" {
		t.Fatalf("precondition: auto_pull source = %q; want the environment override", fromEnv.Source)
	}
	if fromEnv.Position != nil {
		t.Errorf("auto_pull came from an environment variable, which has no file; position = %+v, want none", *fromEnv.Position)
	}

	fromDefaults := byField["concurrency.pool_size"]
	if fromDefaults.Source == path || strings.HasPrefix(fromDefaults.Source, "env:") {
		t.Fatalf("precondition: concurrency.pool_size source = %q; want the built-in defaults", fromDefaults.Source)
	}
	if fromDefaults.Position != nil {
		t.Errorf("concurrency.pool_size came from the built-in defaults, which have no file; position = %+v, want none", *fromDefaults.Position)
	}
}

// TestValueOriginPositionFindsTheAppendCompanion covers the one way a layer is
// attributed a list it never whole-replaces: it wrote only the field's additive
// `_append` companion. The base key is absent from that document, so a position
// resolved from the setting's own path alone would find nothing — or, worse,
// would report the enclosing block and read as though the base key were there.
func TestValueOriginPositionFindsTheAppendCompanion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, workspaceconfig.ConfigFilename)
	write(t, path, "workspace:\n"+
		"  catalog_dirs_append:\n"+
		"    - ./extra-catalog\n")

	rw, err := workspaceconfig.ResolveWithoutGlobal("", root, frozenLookup(nil))
	if err != nil {
		t.Fatalf("ResolveWithoutGlobal: %v", err)
	}
	origin := originsByField(t, rw)["workspace.catalog_dirs"]
	if origin.Source != path {
		t.Fatalf("precondition: workspace.catalog_dirs source = %q; want the workspace file %q", origin.Source, path)
	}
	if origin.Position == nil {
		t.Fatal("the layer contributed through the append companion, which is a key in the document")
	}
	if origin.Position.Line != 2 || origin.Position.Column != 3 {
		t.Errorf("workspace.catalog_dirs position = %d:%d; want 2:3 (the append companion)",
			origin.Position.Line, origin.Position.Column)
	}
	if got := keyTextAt(t, path, origin.Position); got != "catalog_dirs_append" {
		t.Errorf("the reported span addresses %q; want the key %q", got, "catalog_dirs_append")
	}
}

// TestValueOriginPositionInTheCIOverlay covers the additive overlay, whose
// contribution is folded rather than whole-replacing. Both the base key and its
// `_append` companion are additive there, and the fold puts the base's entries
// first, so the base key is what supplied the value whenever the overlay
// carries it — which is why the rows expect the base key in the row that has
// one and the companion only in the row that does not.
func TestValueOriginPositionInTheCIOverlay(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		overlay    string
		wantKey    string
		wantLine   int
		wantColumn int
	}{
		{
			name: "the overlay carries only the append companion",
			overlay: "workspace:\n" +
				"  catalog_dirs_append:\n" +
				"    - ./ci-catalog\n",
			wantKey:    "catalog_dirs_append",
			wantLine:   2,
			wantColumn: 3,
		},
		{
			name: "the overlay carries the base list, which folds additively too",
			overlay: "workspace:\n" +
				"  catalog_dirs:\n" +
				"    - ./ci-catalog\n",
			wantKey:    "catalog_dirs",
			wantLine:   2,
			wantColumn: 3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
			overlayPath := filepath.Join(root, workspaceconfig.AppendCIConfigFilename)
			write(t, overlayPath, tc.overlay)

			rw, err := workspaceconfig.ResolveWithoutGlobal("", root, frozenLookup(nil))
			if err != nil {
				t.Fatalf("ResolveWithoutGlobal: %v", err)
			}
			origin := originsByField(t, rw)["workspace.catalog_dirs"]
			if origin.Source != overlayPath {
				t.Fatalf("precondition: workspace.catalog_dirs source = %q; want the CI overlay %q", origin.Source, overlayPath)
			}
			if origin.Position == nil {
				t.Fatal("the overlay carries the key, so it must carry a position")
			}
			if origin.Position.Line != tc.wantLine || origin.Position.Column != tc.wantColumn {
				t.Errorf("workspace.catalog_dirs position = %d:%d; want %d:%d",
					origin.Position.Line, origin.Position.Column, tc.wantLine, tc.wantColumn)
			}
			if got := keyTextAt(t, overlayPath, origin.Position); got != tc.wantKey {
				t.Errorf("the reported span addresses %q; want the key %q", got, tc.wantKey)
			}
		})
	}
}
