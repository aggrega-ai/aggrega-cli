// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

// A halting diagnostic names its category exactly once, and the assertion is
// written against the count rather than against the shape that supplies it.
//
// Two failures are possible and they pull in opposite directions. A message
// that writes the category into its own sentence while the rendered head also
// carries it says the same word twice, in two shapes, on one line. A message
// that writes nothing and reaches a reader through a chain the head cannot be
// composed from names no category at all, and the reader loses the routing the
// category exists to give. Counting catches both: two occurrences is the first,
// none is the second.
//
// Counting rather than matching a prefix is what keeps the assertion true of
// either supplier. A diagnostic whose category comes from the composed head and
// one whose category comes from its own text both satisfy it, so the test pins
// the property the reader depends on and leaves the mechanism free.

// diagnosticCategoryTokens are the category words a diagnostic can name, read
// off the declared values rather than re-spelled here, so a value whose
// spelling changes moves this list with it.
//
// The enumeration itself is still written out, because the values are declared
// as constants and Go offers no walk over them. A value added beside them and
// not added here would leave its occurrences uncounted, which reads as a clean
// result rather than as a gap.
var diagnosticCategoryTokens = []string{
	string(cerrors.DeveloperError),
	string(cerrors.OpsError),
	string(cerrors.CatalogError),
	string(cerrors.TranslationError),
	string(cerrors.FetchError),
	string(cerrors.PolicyError),
	string(cerrors.TrustError),
}

// countCategoryTokens totals the category words appearing in rendered text.
func countCategoryTokens(rendered string) int {
	total := 0
	for _, token := range diagnosticCategoryTokens {
		total += strings.Count(rendered, token)
	}
	return total
}

// assertCategoryNamedOnce pins both halves: the category is present, and it is
// present once. Asserting only that it appears at most once passes on text that
// lost it altogether, which is the failure that reads as a success.
func assertCategoryNamedOnce(t *testing.T, rendered string) {
	t.Helper()
	switch n := countCategoryTokens(rendered); {
	case n == 0:
		t.Errorf("the diagnostic names no category, so the reader is told nothing about which concern to look at\nrendered: %s", rendered)
	case n > 1:
		t.Errorf("the diagnostic names a category %d times; one line carries it once\nrendered: %s", n, rendered)
	}
}

// runToHalt executes cmd with args and returns the diagnostic exactly as a
// reader meets it: the error the command returns, put through the one formatter
// the root command prints with.
func runToHalt(t *testing.T, cmd *cobra.Command, workspaceRoot string, args ...string) string {
	t.Helper()
	var sink bytes.Buffer
	cmd.SetOut(&sink)
	cmd.SetErr(&sink)
	cmd.SetContext(withWorkspaceResolver(context.Background(), workspaceRoot, nil))
	cmd.SetArgs(args)
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected a halt from %v; got none", args)
	}
	return formatCompilerError(err)
}

// TestCommandHaltsNameTheCategoryOnce drives the command-level halts that write
// a category word into their own text and reads what the formatter makes of
// each.
//
// A row per halt, so a halt whose rendering changes names itself in the output
// rather than hiding inside a single aggregate verdict. A halt added to a
// command and not added here is uncovered; nothing detects that, so the row goes
// in with the halt.
func TestCommandHaltsNameTheCategoryOnce(t *testing.T) {
	dir := t.TempDir()
	releasePath := filepath.Join(dir, "release.yaml")
	entityPath := filepath.Join(dir, "component.yaml")
	// The binary is present but empty: both plugin guards below are argument
	// checks that fire before the artifact is parsed, and an absent path halts
	// on the read instead, which is a different failure.
	wasmPath := filepath.Join(dir, "guest.wasm")
	if err := os.WriteFile(wasmPath, nil, 0o600); err != nil {
		t.Fatalf("write plugin artifact fixture: %v", err)
	}

	aggregatePath := filepath.Join(dir, "aggregate.yaml")
	if err := os.WriteFile(aggregatePath, []byte(`apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: a
  version: "1.0.0"
spec:
  components: {}
`), 0o600); err != nil {
		t.Fatalf("write aggregate fixture: %v", err)
	}

	// A schema-valid Plugin declaring a provider type, so the cross-check
	// against a disagreeing --type is the halt that fires. Its presence is
	// asserted here rather than left to the command, because a missing file
	// halts on the read instead — with no category — and that row would then
	// fail naming the wrong cause.
	pluginEntity := filepath.Join("testdata", "validate", "catalog", "plugin", "provider", "helm", "0.4.0-30-g031f5f7", "plugin.yaml")
	if _, err := os.Stat(pluginEntity); err != nil {
		t.Fatalf("the Plugin fixture this table needs is not at %s: %v", pluginEntity, err)
	}

	cases := []struct {
		name string
		cmd  func() *cobra.Command
		args []string
	}{
		{"lint release with an entity argument", newLintCmd, []string{"--release", releasePath, entityPath}},
		{"lint pre-publish with release", newLintCmd, []string{"--pre-publish", "--release", releasePath}},
		{"lint baseline without pre-publish", newLintCmd, []string{"--baseline", "1.0.0", entityPath}},
		{"lint conventions with no catalog directory", newLintCmd, nil},
		{"validate ir-graph outside the release scope", newValidateCmd, []string{"--ir-graph", entityPath}},
		{"validate type without plugin", newValidateCmd, []string{"--type", "provider", entityPath}},
		{"validate plugin with release", newValidateCmd, []string{"--plugin", wasmPath, "--release", releasePath}},
		{"validate release with an entity argument", newValidateCmd, []string{"--release", releasePath, entityPath}},
		{"validate plugin binary with no declared type", newValidateCmd, []string{"--plugin", wasmPath}},
		{"validate plugin type disagreeing with the entity", newValidateCmd, []string{"--type", "linter", "--plugin", wasmPath, pluginEntity}},
		{"validate whole catalog with no catalog directory", newValidateCmd, nil},
		{"inspect ir-graph on an Aggregate", newInspectCmd, []string{"--ir-graph", aggregatePath}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertCategoryNamedOnce(t, runToHalt(t, tc.cmd(), dir, tc.args...))
		})
	}
}

const categoryOnceValuesManifest = `
apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/db-pool:
          feature: core/db-pool:1.0.0
          maps:
            values:
              - pool
`

const categoryOnceInfraManifest = `
apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/rewrite-host:
          feature: core/rewrite-host:1.0.0
          maps:
            infra:
              - upstream
`

// categoryOnceManifest parses a Manifest fixture.
func categoryOnceManifest(t *testing.T, src string) *types.Manifest {
	t.Helper()
	var m types.Manifest
	if err := yaml.Unmarshal([]byte(src), &m); err != nil {
		t.Fatalf("manifest unmarshal: %v", err)
	}
	return &m
}

// TestAnnotationExpansionHaltNamesTheCategoryOnce reads the annotation halt at
// the boundary an author meets it: the rules over what an author wrote in an
// annotation raise it, and the formatter renders it.
//
// 🔴 Both cases drive ValidateAuthoredAnnotations, and that entry point is what
// the halt's own home dictates rather than a convenience. The side-versus-pillar
// partition is a declared rule asked over the authored entry BEFORE expansion
// consumes it, and it is the same entry point the compile pipeline asks one step
// ahead of expansion. Expansion itself routes a crossed entry nowhere and
// returns no error, so a call into ExpandAnnotations here would assert against a
// nil and this file would quietly stop covering the rendering it exists for.
//
// Each finding carries a registered condition and reaches the formatter through
// the conversion that derives its category, which is the shape where a second
// category word can reach one line — the head supplies the word, and anything
// the finding's own text contributes lands on the same line. The scope the
// finding adds, the boundary and component and annotation key, is what the
// condition alone cannot know, so it is asserted too: it has to survive
// rendering rather than be replaced.
func TestAnnotationExpansionHaltNamesTheCategoryOnce(t *testing.T) {
	t.Parallel()

	t.Run("release side", func(t *testing.T) {
		t.Parallel()
		var rel types.Release
		if err := yaml.Unmarshal([]byte(`
apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: test
  version: "1.0.0"
  environment: testing
spec:
  aggregate: test
  manifest: test
  boundaries:
    prod:
      components:
        api:
          annotations:
            feature.myco/db-pool: "shared-pool"
`), &rel); err != nil {
			t.Fatalf("release unmarshal: %v", err)
		}
		rr := &resolver.ResolveResult{
			Manifest:    categoryOnceManifest(t, categoryOnceValuesManifest),
			Release:     &rel,
			ReleasePath: "release.yaml",
		}
		err := resolver.ValidateAuthoredAnnotations(nil, rr, resolver.CatalogLookup{})
		if err == nil {
			t.Fatal("expected the side-vs-pillar halt; got nil")
		}
		rendered := formatCompilerError(err)
		assertCategoryNamedOnce(t, rendered)
		for _, want := range []string{"prod", "api", "feature.myco/db-pool"} {
			if !strings.Contains(rendered, want) {
				t.Errorf("the rendered halt drops the scope %q\nrendered: %s", want, rendered)
			}
		}
	})

	t.Run("aggregate side", func(t *testing.T) {
		t.Parallel()
		var agg types.Aggregate
		if err := yaml.Unmarshal([]byte(`
apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server:1.0.0
      annotations:
        feature.myco/rewrite-host: "upstream.example"
`), &agg); err != nil {
			t.Fatalf("aggregate unmarshal: %v", err)
		}
		rr := &resolver.ResolveResult{
			Manifest:      categoryOnceManifest(t, categoryOnceInfraManifest),
			Aggregate:     types.Normalize(&agg).Aggregate(),
			AggregatePath: "aggregate.yaml",
		}
		err := resolver.ValidateAuthoredAnnotations(nil, rr, resolver.CatalogLookup{})
		if err == nil {
			t.Fatal("expected the side-vs-pillar halt; got nil")
		}
		rendered := formatCompilerError(err)
		assertCategoryNamedOnce(t, rendered)
		for _, want := range []string{"api", "feature.myco/rewrite-host"} {
			if !strings.Contains(rendered, want) {
				t.Errorf("the rendered halt drops the scope %q\nrendered: %s", want, rendered)
			}
		}
	})
}
