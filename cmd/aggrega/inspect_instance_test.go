// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// fullstackAppDir is the resolved-instance fixture root: an Aggregate (with a
// db-module sub-aggregate import and uses-edges), a Manifest, and a Release
// (three boundaries, directories, annotations). All
// three resolved-instance renderers and the no-divergence proof drive off it.
const (
	fullstackAggregate = "../../internal/compiler/testdata/fullstack-app/aggregate.yaml"
	fullstackManifest  = "../../internal/compiler/testdata/fullstack-app/manifest.yaml"
	fullstackRelease   = "../../internal/compiler/testdata/fullstack-app/release.yaml"
	fullstackCatalog   = "../../internal/compiler/testdata/fullstack-app/catalog"
)

// inspectInstance drives dispatchInspect against an instance-kind file with the
// fullstack catalog set, returning the captured output. mutate adjusts the
// default-level opts before dispatch (level, mode, output, flat).
func inspectInstance(t *testing.T, path string, mutate func(*inspectOptions)) string {
	t.Helper()
	var buf bytes.Buffer
	opts := testInspectOptions(&buf)
	if mutate != nil {
		mutate(&opts)
	}
	if err := dispatchInspect(resolveTempWorkspace(t, tempWorkspaceOptions{}), path, []string{fullstackCatalog}, opts); err != nil {
		t.Fatalf("dispatchInspect(%q): %v", path, err)
	}
	return buf.String()
}

// --- Manifest ---------------------------------------------------------------

func TestInspectManifest_DefaultText_Golden(t *testing.T) {
	t.Parallel()
	got := inspectInstance(t, fullstackManifest, nil)
	compareGoldenKind(t, "manifest", "default.text.golden", []byte(got))
}

func TestInspectManifest_List_Golden(t *testing.T) {
	t.Parallel()
	got := inspectInstance(t, fullstackManifest, func(o *inspectOptions) { o.level = levelList })
	compareGoldenKind(t, "manifest", "list.text.golden", []byte(got))
}

func TestInspectManifest_Raw_Golden(t *testing.T) {
	t.Parallel()
	// --raw is the as-authored bypass: no extends resolution, file verbatim.
	got := inspectInstance(t, fullstackManifest, func(o *inspectOptions) { o.mode = modeForcedRaw })
	compareGoldenKind(t, "manifest", "raw.golden", []byte(got))
}

func TestInspectManifest_YAML_Golden(t *testing.T) {
	t.Parallel()
	// --output yaml is the resolved-as-YAML form (extends applied, then marshalled).
	got := inspectInstance(t, fullstackManifest, func(o *inspectOptions) { o.output = outputYAML })
	compareGoldenKind(t, "manifest", "yaml.golden", []byte(got))
}

// TestInspectManifest_FlatIsNoOp proves --flat does not change the tabular
// Manifest output (decision: --flat is a no-op for tabular kinds, no error).
func TestInspectManifest_FlatIsNoOp(t *testing.T) {
	t.Parallel()
	plain := inspectInstance(t, fullstackManifest, nil)
	flat := inspectInstance(t, fullstackManifest, func(o *inspectOptions) { o.flat = true })
	if plain != flat {
		t.Errorf("--flat should be a no-op for the tabular Manifest;\n--- plain ---\n%s\n--- flat ---\n%s", plain, flat)
	}
}

// --- Aggregate --------------------------------------------------------------

func TestInspectAggregate_DefaultText_Golden(t *testing.T) {
	t.Parallel()
	got := inspectInstance(t, fullstackAggregate, nil)
	compareGoldenKind(t, "aggregate", "default.text.golden", []byte(got))
}

func TestInspectAggregate_Flat_Golden(t *testing.T) {
	t.Parallel()
	got := inspectInstance(t, fullstackAggregate, func(o *inspectOptions) { o.flat = true })
	compareGoldenKind(t, "aggregate", "flat.text.golden", []byte(got))
}

func TestInspectAggregate_List_Golden(t *testing.T) {
	t.Parallel()
	got := inspectInstance(t, fullstackAggregate, func(o *inspectOptions) { o.level = levelList })
	compareGoldenKind(t, "aggregate", "list.text.golden", []byte(got))
}

func TestInspectAggregate_Raw_Golden(t *testing.T) {
	t.Parallel()
	got := inspectInstance(t, fullstackAggregate, func(o *inspectOptions) { o.mode = modeForcedRaw })
	compareGoldenKind(t, "aggregate", "raw.golden", []byte(got))
}

// --- Release ----------------------------------------------------------------

func TestInspectRelease_DefaultText_Golden(t *testing.T) {
	t.Parallel()
	got := inspectInstance(t, fullstackRelease, nil)
	compareGoldenKind(t, "release", "default.text.golden", []byte(got))
}

func TestInspectRelease_Flat_Golden(t *testing.T) {
	t.Parallel()
	got := inspectInstance(t, fullstackRelease, func(o *inspectOptions) { o.flat = true })
	compareGoldenKind(t, "release", "flat.text.golden", []byte(got))
}

func TestInspectRelease_List_Golden(t *testing.T) {
	t.Parallel()
	got := inspectInstance(t, fullstackRelease, func(o *inspectOptions) { o.level = levelList })
	compareGoldenKind(t, "release", "list.text.golden", []byte(got))
}

func TestInspectRelease_Raw_Golden(t *testing.T) {
	t.Parallel()
	got := inspectInstance(t, fullstackRelease, func(o *inspectOptions) { o.mode = modeForcedRaw })
	compareGoldenKind(t, "release", "raw.golden", []byte(got))
}

// TestInspectRelease_IRGraph_Subject proves the graph is asked for by its own
// flag: the emission carries the graph shape rather than the authored Release
// shape.
//
// It is the same assertion that was made of the format flag before the graph had
// a flag of its own, re-pointed rather than retired: what is worth pinning is
// that a Release's graph is reachable from the command line at all, and only the
// spelling of the request moved.
func TestInspectRelease_IRGraph_Subject(t *testing.T) {
	t.Parallel()
	got := inspectInstance(t, fullstackRelease, func(o *inspectOptions) { o.irGraph = true })
	if !strings.Contains(got, `"nodes"`) {
		t.Errorf("--ir-graph should emit the resolved IR graph; got:\n%s", got[:min(len(got), 400)])
	}
}

// TestInspectRelease_CrossBoundaryEdgeMarked proves the J9 marking: the
// frontend → backend edge crosses from the frontend-ns boundary to the
// backend-ns boundary and is marked in the resolved tree.
func TestInspectRelease_CrossBoundaryEdgeMarked(t *testing.T) {
	t.Parallel()
	got := inspectInstance(t, fullstackRelease, nil)
	if !strings.Contains(got, "[cross-boundary]") {
		t.Errorf("expected a [cross-boundary] marking on the frontend → backend edge; got:\n%s", got)
	}
}

// TestInspectAggregate_ParametersDeclarations proves the I6 surface: the typed
// parameter-contract declarations the Aggregate exposes render with their type
// and default. This is the validated authoring field's render proof — the
// fullstack fixture declares none, so a dedicated fixture exercises it.
func TestInspectAggregate_ParametersDeclarations(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "param-app.yaml")
	writeFixture(t, path, `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: param-app
  version: "1.0.0"
spec:
  parameters:
    db_size:
      type: string
      default: "20Gi"
      description: storage size
  components:
    api:
      type: core/compute/api-server
`)
	got := inspectInstance(t, path, nil)
	if !strings.Contains(got, "Parameters:") {
		t.Errorf("expected a Parameters section; got:\n%s", got)
	}
	if !strings.Contains(got, "db_size") || !strings.Contains(got, "type=string") || !strings.Contains(got, "default=20Gi") {
		t.Errorf("parameter declaration should render name, type, and default; got:\n%s", got)
	}
}

// TestInspectManifest_ExtendsChainAndAnnotations proves H3 / H6: a child
// Manifest extending a base renders the flat extends-chain string, the resolved
// (child-wins) component mapping, and the annotation-binding table. The base +
// child live in the same temp dir so the same-directory extends reference
// resolves.
func TestInspectManifest_ExtendsChainAndAnnotations(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "base.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: base
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/compute/api-server: base-api:1.0.0
        core/datastore/postgres: base-pg:1.0.0
`)
	child := filepath.Join(root, "child.yaml")
	writeFixture(t, child, `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: child
  version: "2.0.0"
spec:
  extends: base
  mappings:
    components:
      type:
        core/compute/api-server: child-api:2.0.0
    features:
      type:
        core/rewrite-host: rewrite-host-patcher:1.0.0
      annotations:
        feature.myco/rewrite-host:
          feature: core/rewrite-host
          maps:
            values:
              - host
`)
	var buf bytes.Buffer
	if err := dispatchInspect(resolveTempWorkspace(t, tempWorkspaceOptions{}), child, nil, testInspectOptions(&buf)); err != nil {
		t.Fatalf("inspect child manifest: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "Extends: child which extends base") {
		t.Errorf("expected flat extends chain string; got:\n%s", got)
	}
	// Child wins the colliding api-server key; the base-only postgres key survives.
	if !strings.Contains(got, "child-api:2.0.0") {
		t.Errorf("child mapping should win the collision; got:\n%s", got)
	}
	if !strings.Contains(got, "base-pg:1.0.0") {
		t.Errorf("base-only mapping should survive composition; got:\n%s", got)
	}
	if !strings.Contains(got, "feature.myco/rewrite-host") || !strings.Contains(got, "core/rewrite-host") {
		t.Errorf("annotation binding should render key → feature; got:\n%s", got)
	}
}

// TestInspectManifest_UnknownMappingKeyIsRefused pins the mapping key surface at
// the inspector: `components` and `features` are the whole surface, so a
// Manifest declaring any other key under `mappings:` is refused rather than
// rendered with the unrecognised section quietly dropped.
//
// It is asserted here as well as at the loader because a reader reaches for
// inspect precisely when a document is not behaving as expected, and an
// inspector that rendered such a Manifest as if it were well formed would be
// the surface most likely to mislead.
func TestInspectManifest_UnknownMappingKeyIsRefused(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "unknown-mapping-key.yaml")
	writeFixture(t, path, `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: unknown-mapping-key-manifest
  version: "1.0.0"
spec:
  mappings:
    helm:
      components:
        type:
          core/compute/api-server: some-api:1.0.0
`)
	var buf bytes.Buffer
	err := dispatchInspect(resolveTempWorkspace(t, tempWorkspaceOptions{}), path, nil, testInspectOptions(&buf))
	if err == nil {
		t.Fatalf("expected inspect to refuse an unknown mappings key; rendered:\n%s", buf.String())
	}
	got := err.Error()
	for _, want := range []string{`"helm"`, "components", "features"} {
		if !strings.Contains(got, want) {
			t.Errorf("refusal does not name %s; got %q", want, got)
		}
	}
	if strings.Contains(buf.String(), "some-api:1.0.0") {
		t.Errorf("a refused Manifest must render no mapping content; got:\n%s", buf.String())
	}
}

// --- No-divergence / single-source proof -----------------------------------

// TestInspectAggregate_NoDivergence_TopologyMatchesBuildFunnel is the
// single-source proof: the Aggregate inspector renders the exact node set and
// uses-edge set that topology.Build produces for the same input. A future fork
// of the topology pass into the inspector would drift the inspector's output
// from the build-path funnel and fail here.
func TestInspectAggregate_NoDivergence_TopologyMatchesBuildFunnel(t *testing.T) {
	t.Parallel()

	// Build the topology independently via the shared funnel — the same call the
	// inspector composes internally.
	agg, err := resolver.LoadAggregate(fullstackAggregate)
	if err != nil {
		t.Fatalf("load aggregate: %v", err)
	}
	lookup, subLoader, _, err := aggregateTopologyInputs(resolveTempWorkspace(t, tempWorkspaceOptions{}), []string{fullstackCatalog})
	if err != nil {
		t.Fatalf("topology inputs: %v", err)
	}
	topo, err := topology.Build(topology.BuildConfig{
		ResolveResult:    &resolver.ResolveResult{Aggregate: agg},
		CatalogLookup:    lookup,
		LoadSubAggregate: subLoader,
	})
	if err != nil {
		t.Fatalf("topology.Build: %v", err)
	}

	got := inspectInstance(t, fullstackAggregate, nil)

	// Every node the funnel produced appears in the inspector's output.
	for id := range topo.Nodes.All() {
		if !strings.Contains(got, id) {
			t.Errorf("inspector output missing node %q the build funnel produced; got:\n%s", id, got)
		}
	}
	// Every edge the funnel produced appears in the dependency graph (target +
	// source both present, in the reverse-view section).
	for _, e := range topo.Edges {
		if !strings.Contains(got, e.Target) || !strings.Contains(got, e.Source) {
			t.Errorf("inspector output missing edge %s → %s the build funnel produced; got:\n%s", e.Source, e.Target, got)
		}
	}
	if len(topo.Edges) == 0 {
		t.Fatal("fixture should produce at least one edge to make the proof meaningful")
	}
}
