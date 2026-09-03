// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/charttest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// A component-scope attachment on a chart-nesting host, proved by what the chart
// tool RENDERS rather than by where its files landed or by the tool's verdict.
//
// # Why a path check and an exit code both stay silent here
//
// A chart file whose chart-relative name begins with neither "templates/" nor
// "charts/" is classified as inert data: it loads without error, contributes no
// manifest, and is reachable only through .Files. So an attachment placed at
// such a position is not a broken chart — it is an absent one, and every signal
// short of reading the rendered output reports success. Measured against the
// real tool over this fixture's own emitted bytes: with valid content at such a
// position `helm lint` and `helm template` both exit 0; with deliberately
// invalid content at the SAME position they both still exit 0; with that same
// invalid content in a discovered chart they exit non-zero. The verdict is a
// function of the discovered charts alone, never of the attachment's content.
//
// # What these cases control, and what they deliberately do not assert
//
// The attachment's BYTES and the host chart around them are whatever a real
// render emitted — nothing here hand-assembles a chart production computes
// differently. What the cases vary is the attachment chart's POSITION, which is
// the one axis under test, and each case constructs its own position rather than
// reading the emitted one. That is deliberate on both counts: it makes the two
// outcomes attributable to placement and nothing else, and it keeps these cases
// true across a change to where the render places an attachment.
//
// 🔴 So these cases do NOT assert that the emitted position is a discoverable
// one. They assert what discoverability BUYS, over real emitted bytes, in both
// directions. The emitted position is logged rather than pinned.
const (
	// hostChartMarker and attachmentMarker are unique to one chart each, so a
	// marker's presence in rendered output names which chart produced it and its
	// absence cannot be read as anything else.
	hostChartMarker  = "HOST-CHART-OWN-MARKER"
	attachmentMarker = "ATTACHMENT-OWN-RESOURCE-MARKER"
)

// attachmentRenderChart writes a minimal chart whose one template emits a
// ConfigMap carrying marker. The marker is baked into the template BODY, so it
// reaches rendered output only when the chart tool discovered and rendered the
// template — never merely by the file being present.
func attachmentRenderChart(t *testing.T, chartName, resourceName, marker string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"),
		[]byte("apiVersion: v2\nname: "+chartName+"\nversion: 1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "values.yaml"), []byte("image: "+chartName+":1.0\n"), 0o644); err != nil {
		t.Fatalf("write values.yaml: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "templates"), 0o755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "templates", "configmap.yaml"),
		[]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: "+resourceName+
			"\ndata:\n  marker: \""+marker+"\"\n"), 0o644); err != nil {
		t.Fatalf("write configmap.yaml: %v", err)
	}
	return dir
}

// renderComponentScopeAttachmentOnChartHost drives RenderAll over one
// chart-nesting host carrying one component-scope sourced attachment, through
// the shipped provider guests, and returns the emitted file map.
func renderComponentScopeAttachmentOnChartHost(t *testing.T) map[string]renderer.AssembledFile {
	t.Helper()

	const (
		boundary   = "cluster"
		component  = "api"
		frInstance = "guardrail"
		featType   = "core/aux:1.0.0"
	)

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(component, types.IRNode{Component: "core/api", BoundaryPath: boundary})

	clusterBoundary := types.Boundary{
		Directory:  "dist/cluster",
		Features:   types.OrderedMap[types.BoundaryFeature]{},
		Components: types.OrderedMap[types.BoundaryComponent]{},
		Boundaries: types.OrderedMap[types.Boundary]{},
	}
	clusterBoundary.Components.Set(component, types.BoundaryComponent{})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set(boundary, clusterBoundary)

	apiFV := types.OrderedMap[types.IRFeatureEntry]{}
	apiFV.Set(frInstance, types.IRFeatureEntry{Type: featType})
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set(component, apiFV)

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "attachment-render-app"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				InfraMapping:         types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: fvm,
				Provenance:           types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder: ir.FeatureOrder{component: []string{frInstance}},
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "attachment-render-app"}},
			Release:   &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{
				component: {BoundaryPath: boundary, Directory: "dist/cluster"},
			},
		},
	}

	componentRecipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Capability: "helm-chart", Implements: "core/api",
			Source: attachmentRenderChart(t, "api", "api-config", hostChartMarker),
		},
	}
	frRecipe := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "aux-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability:    "helm-chart",
			Implements:    featType,
			ValuesMapping: types.ValuesMapping{Inline: `{}`},
			Source:        attachmentRenderChart(t, "guardrail", "guardrail-config", attachmentMarker),
		},
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType == "core/api" {
				return componentRecipe, true
			}
			return nil, false
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if strings.HasPrefix(featureType, "core/aux") {
				return frRecipe, true
			}
			return nil, false
		},
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, wasmEngineFactory(t))
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	return result.Files
}

// chartRootCarrying returns the emitted directory of the nearest chart manifest
// at or above the single file whose body carries marker — the chart that would
// render that marker. It is derived from the emitted map rather than pinned, so
// it keeps naming the right chart after a change to where the render places one.
func chartRootCarrying(t *testing.T, files map[string]renderer.AssembledFile, marker string) string {
	t.Helper()

	var markerKey string
	for k, f := range files {
		if !bytes.Contains(f.Content, []byte(marker)) {
			continue
		}
		if markerKey != "" {
			t.Fatalf("marker %q appears in both %q and %q; it must name exactly one chart", marker, markerKey, k)
		}
		markerKey = k
	}
	if markerKey == "" {
		t.Fatalf("no emitted file carries the marker %q; keys: %v", marker, sortedKeys(files))
	}

	for dir := path.Dir(markerKey); dir != "." && dir != "/"; dir = path.Dir(dir) {
		if _, ok := files[path.Join(dir, "Chart.yaml")]; ok {
			return dir
		}
	}
	t.Fatalf("no chart manifest at or above %q, so its body could never render; keys: %v",
		markerKey, sortedKeys(files))
	return ""
}

// materializeWithAttachmentAt writes the emitted tree to a fresh directory,
// relocating the attachment chart rooted at fromRoot so that it sits at toRoot
// instead. Every other emitted byte is written exactly as the render produced
// it, and the attachment's own bytes are unchanged — only its position moves,
// which is the axis under test. It returns the umbrella chart's directory.
func materializeWithAttachmentAt(t *testing.T, files map[string]renderer.AssembledFile, fromRoot, toRoot, umbrellaRoot string) string {
	t.Helper()
	dest := t.TempDir()

	write := func(rel string, body []byte) {
		t.Helper()
		p := filepath.Join(dest, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir for %q: %v", rel, err)
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatalf("write %q: %v", rel, err)
		}
	}

	moved := 0
	for _, k := range sortedKeys(files) {
		body := files[k].Content
		if rel, under := strings.CutPrefix(k, fromRoot+"/"); under {
			write(path.Join(toRoot, rel), body)
			moved++
			continue
		}
		write(k, body)
	}
	if moved == 0 {
		t.Fatalf("no emitted file sat under the attachment chart root %q, so nothing was placed at %q", fromRoot, toRoot)
	}
	return filepath.Join(dest, filepath.FromSlash(umbrellaRoot))
}

// umbrellaRootOf is the emitted directory of the outermost chart manifest — the
// chart a reader would hand the tool.
func umbrellaRootOf(t *testing.T, files map[string]renderer.AssembledFile) string {
	t.Helper()
	best := ""
	for _, k := range sortedKeys(files) {
		dir, file := path.Split(k)
		if file != "Chart.yaml" {
			continue
		}
		dir = strings.TrimSuffix(dir, "/")
		if best == "" || len(strings.Split(dir, "/")) < len(strings.Split(best, "/")) {
			best = dir
		}
	}
	if best == "" {
		t.Fatalf("the render emitted no chart manifest at all; keys: %v", sortedKeys(files))
	}
	return best
}

// TestHelmChartAttachmentRendersItsOwnResources is the discriminating proof: an
// attachment's own resource must appear in what the chart tool PRODUCED, and the
// case that fails is the one whose chart sits where the tool does not look.
//
// The two subtests share one render, so both arms run over the same emitted
// bytes and differ in the attachment chart's position alone.
//
// The undiscoverable arm is what keeps the discoverable arm from being vacuous.
// Without it a green would be consistent with a marker that renders from
// anywhere, which would prove nothing about placement; with it, the same bytes
// are shown to render at one position and not at the other.
func TestHelmChartAttachmentRendersItsOwnResources(t *testing.T) {
	t.Parallel()

	files := renderComponentScopeAttachmentOnChartHost(t)

	umbrella := umbrellaRootOf(t, files)
	hostRoot := chartRootCarrying(t, files, hostChartMarker)
	attachRoot := chartRootCarrying(t, files, attachmentMarker)

	// Reported rather than pinned: this is where the render currently places a
	// component-scope attachment, and these cases stay true when it moves.
	t.Logf("emitted umbrella=%q host chart=%q attachment chart=%q", umbrella, hostRoot, attachRoot)
	t.Logf("emitted keys: %v", sortedKeys(files))

	// The relocation below is faithful only if the attachment's emitted files are
	// a chart in their own right. Asserted over the emitted bytes, and durable:
	// it is a property of what the render produced, not of where it put it.
	if _, ok := files[path.Join(attachRoot, "Chart.yaml")]; !ok {
		t.Fatalf("the attachment's emitted files carry no chart manifest at %q", attachRoot)
	}

	cases := []struct {
		name string
		// dest is the attachment chart's position, relative to the emitted tree.
		dest string
		// wantRendered is whether the attachment's own resource must appear in
		// what the tool produced from that position.
		wantRendered bool
	}{
		{
			// Under the host chart's own sub-chart directory: a position the
			// loader discovers, so the attachment's templates render.
			name:         "a discoverable position renders the attachment's own resource",
			dest:         path.Join(hostRoot, "charts", "attached"),
			wantRendered: true,
		},
		{
			// A chart-relative segment that is neither a template nor a
			// sub-chart directory. The chart still loads and the tool still
			// succeeds — the attachment's resource is simply absent.
			name:         "an undiscoverable position renders nothing and still succeeds",
			dest:         path.Join(hostRoot, "features", "attached"),
			wantRendered: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			chartDir := materializeWithAttachmentAt(t, files, attachRoot, tc.dest, umbrella)

			rendered, err := charttest.RenderThroughDependencyPipeline(chartDir)
			// Both arms must LOAD and RENDER cleanly. That is the whole point of
			// the undiscoverable arm: it is not a broken chart, which is why no
			// exit code and no path check can tell the two apart.
			if err != nil {
				t.Fatalf("the chart must render from %q, got: %v", tc.dest, err)
			}

			// The host's own resource renders in BOTH arms. It is the positive
			// control for the render itself: without it, an arm that rendered
			// nothing at all would look the same as a correctly-absent attachment.
			if !strings.Contains(rendered, hostChartMarker) {
				t.Fatalf("the host chart's own resource did not render, so this arm measures nothing:\n%s", rendered)
			}

			got := strings.Contains(rendered, attachmentMarker)
			if got == tc.wantRendered {
				return
			}
			if tc.wantRendered {
				t.Errorf("the attachment's own resource is absent from what the tool produced.\n"+
					"Its chart is at %q and its files are present there, but nothing rendered from them.\n"+
					"rendered output:\n%s", tc.dest, rendered)
				return
			}
			t.Errorf("the attachment's own resource rendered from %q, which this arm requires to be a position "+
				"the loader treats as inert data.\nThis arm is the control, so while it renders anything the "+
				"discoverable arm beside it proves nothing:\n%s", tc.dest, rendered)
		})
	}
}
