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
// # What these cases control
//
// The attachment's BYTES and the host chart around them are whatever a real
// render emitted — nothing here hand-assembles a chart production computes
// differently. The first arm hands the tool the emitted tree UNTOUCHED, so it
// asserts the emitted position itself; the other two relocate the attachment out
// of it, which is what makes the first arm's outcome attributable to placement
// rather than to a marker that would render from anywhere.
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
// instead, and optionally dropping the umbrella's dependency entry for it.
// Every other emitted byte is written exactly as the render produced it, and the
// attachment's own bytes are unchanged. It returns the umbrella chart's
// directory.
//
// toRoot equal to fromRoot relocates nothing, which is how an arm hands the tool
// the emitted tree untouched.
//
// # Why dropping the entry is a separate axis rather than part of relocating
//
// Once the umbrella DECLARES the attachment as a child, relocating its chart out
// of the discovered directory makes the tool refuse outright rather than quietly
// render less — a declared child with no chart present is an error. So a
// two-outcome test could no longer separate "the tool did not look there" from
// "the tool refused the tree": two different causes reaching one red. Dropping
// the entry alongside the relocation restores the original silent shape — nothing
// declared, nothing found, nothing said — and keeping it is the guardrail arm.
// One cause per arm is the whole reason the axis is here.
func materializeWithAttachmentAt(t *testing.T, files map[string]renderer.AssembledFile, fromRoot, toRoot, umbrellaRoot string, dropDependency bool) string {
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

	umbrellaManifest := path.Join(umbrellaRoot, "Chart.yaml")
	moved := 0
	for _, k := range sortedKeys(files) {
		body := files[k].Content
		if dropDependency && k == umbrellaManifest {
			body = withoutDependencyOn(t, body, path.Base(fromRoot), files, fromRoot)
		}
		if rel, under := strings.CutPrefix(k, fromRoot+"/"); under && toRoot != fromRoot {
			write(path.Join(toRoot, rel), body)
			moved++
			continue
		}
		write(k, body)
	}
	if moved == 0 && toRoot != fromRoot {
		t.Fatalf("no emitted file sat under the attachment chart root %q, so nothing was placed at %q", fromRoot, toRoot)
	}
	return filepath.Join(dest, filepath.FromSlash(umbrellaRoot))
}

// withoutDependencyOn removes the umbrella's dependency entry for the chart
// rooted at chartRoot, identified by the NAME that chart's own manifest declares
// — which is the value a dependency binds by, and is a different string from the
// directory it sits in.
//
// It fails rather than returning the manifest unchanged when it removes nothing.
// An arm whose whole premise is that the entry is gone would otherwise run
// against a manifest that still declares it, and would then be measuring the
// guardrail it exists to be the control for.
func withoutDependencyOn(t *testing.T, manifest []byte, _ string, files map[string]renderer.AssembledFile, chartRoot string) []byte {
	t.Helper()

	placed, ok := files[path.Join(chartRoot, "Chart.yaml")]
	if !ok {
		t.Fatalf("the chart at %q declares no manifest, so no dependency name can be read from it", chartRoot)
	}
	var header struct {
		Name string `json:"name" yaml:"name"`
	}
	for _, line := range strings.Split(string(placed.Content), "\n") {
		if rest, cut := strings.CutPrefix(line, "name:"); cut {
			header.Name = strings.Trim(strings.TrimSpace(rest), `"'`)
			break
		}
	}
	if header.Name == "" {
		t.Fatalf("the chart at %q declares no name, so no dependency entry can be identified", chartRoot)
	}

	lines := strings.Split(string(manifest), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	inEntry := false
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "  - name: "):
			inEntry = strings.TrimSpace(strings.TrimPrefix(line, "  - name:")) == header.Name
			if inEntry {
				dropped++
				continue
			}
		case inEntry && strings.HasPrefix(line, "    "):
			continue
		default:
			inEntry = false
		}
		kept = append(kept, line)
	}
	if dropped != 1 {
		t.Fatalf("expected to remove exactly one dependency entry naming %q, removed %d; this arm's premise "+
			"is that the entry is gone, so it would otherwise measure the guardrail rather than control for it:\n%s",
			header.Name, dropped, manifest)
	}
	return []byte(strings.Join(kept, "\n"))
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

// TestHelmChartAttachmentIsEmittedWhereTheToolDiscoversIt pins the emitted
// position against the discovery rule, structurally rather than as a literal.
//
// The rule is ONE level: the tool discovers a child chart at <chart>/charts/*
// and does not walk further down. So the property that matters is not what the
// directory is called but how deep it is — a chart two or three levels below
// that directory is not a child of anything, it is bytes inside another child's
// directory. Asserting the depth is asserting the rule; asserting only the name
// would pass for a name at any depth.
func TestHelmChartAttachmentIsEmittedWhereTheToolDiscoversIt(t *testing.T) {
	t.Parallel()

	files := renderComponentScopeAttachmentOnChartHost(t)
	umbrella := umbrellaRootOf(t, files)
	hostRoot := chartRootCarrying(t, files, hostChartMarker)
	attachRoot := chartRootCarrying(t, files, attachmentMarker)

	if got, want := path.Dir(attachRoot), path.Join(umbrella, "charts"); got != want {
		t.Errorf("the attachment's chart sits at %q, whose parent is %q, want a child of %q.\n"+
			"The tool discovers a child chart one level under the umbrella's charts directory and "+
			"walks no further, so a chart anywhere else renders nothing and reports success:\n%v",
			attachRoot, got, want, sortedKeys(files))
	}
	// And BESIDE the host rather than inside it. This is the half the depth check
	// above cannot see on its own: the host's own chart is also a child of that
	// directory, so a chart nested inside the host is still "under charts".
	if strings.HasPrefix(attachRoot, hostRoot+"/") {
		t.Errorf("the attachment's chart at %q sits inside the host's own chart at %q; a child chart's own "+
			"subdirectories belong to that child, so nothing discovers this one", attachRoot, hostRoot)
	}
	// The directory carries the attachment's two coordinates joined once, so one
	// directory per contributor and each carrying its identity a single time.
	if got, want := path.Base(attachRoot), "api--guardrail"; got != want {
		t.Errorf("the attachment's directory is %q, want %q — the host and the instance joined through the "+
			"shared segment encoder, each coordinate appearing once", got, want)
	}
	// A directory the tool can load at all. A child directory with no manifest
	// makes the whole umbrella refuse, so this is a precondition of the position
	// being usable rather than a restatement of the assertions above.
	if _, ok := files[path.Join(attachRoot, "Chart.yaml")]; !ok {
		t.Fatalf("the attachment's emitted files carry no chart manifest at %q, so a child directory sits "+
			"under charts with no metadata and the umbrella will not load", attachRoot)
	}
}

// TestHelmChartAttachmentRendersItsOwnResources is the discriminating proof: an
// attachment's own resource must appear in what the chart tool PRODUCED.
//
// The three arms share one render, so all of them run over the same emitted
// bytes. 🔴 They carry ONE CAUSE EACH, and that is the whole construction:
//
//   - emitted, declared — the tree as the render produced it. Renders.
//   - relocated AND undeclared — the original silent defect. Renders nothing
//     from the attachment, reports success, says nothing.
//   - relocated but still declared — the guardrail the dependency entry buys.
//     Refused loudly.
//
// A two-arm version cannot exist any more. Once the umbrella declares the
// attachment as a child, relocating its chart makes the tool refuse rather than
// render less, so "not discovered" and "chart refused" would arrive as one red
// from two different causes — and a reader could not tell which arm had caught
// what. The undeclared arm is what keeps the first arm from being vacuous, and
// the declared-but-relocated arm is what shows the entry is load-bearing rather
// than decoration.
func TestHelmChartAttachmentRendersItsOwnResources(t *testing.T) {
	t.Parallel()

	files := renderComponentScopeAttachmentOnChartHost(t)

	umbrella := umbrellaRootOf(t, files)
	hostRoot := chartRootCarrying(t, files, hostChartMarker)
	attachRoot := chartRootCarrying(t, files, attachmentMarker)
	elsewhere := path.Join(hostRoot, "features", "attached")

	cases := []struct {
		name string
		// dest is the attachment chart's position in the materialised tree;
		// equal to its emitted root means the tree is handed over untouched.
		dest string
		// dropDependency removes the umbrella's entry naming the attachment.
		dropDependency bool
		// wantRendered is whether the attachment's own resource must appear in
		// what the tool produced. Read only where wantRefused is false.
		wantRendered bool
		// wantRefused is whether the tool must refuse the tree outright.
		wantRefused bool
	}{
		{
			name:         "the emitted position renders the attachment's own resource",
			dest:         attachRoot,
			wantRendered: true,
		},
		{
			name:           "relocated and undeclared renders nothing and still succeeds",
			dest:           elsewhere,
			dropDependency: true,
			wantRendered:   false,
		},
		{
			name:        "relocated while still declared is refused outright",
			dest:        elsewhere,
			wantRefused: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			chartDir := materializeWithAttachmentAt(t, files, attachRoot, tc.dest, umbrella, tc.dropDependency)
			rendered, err := charttest.RenderThroughDependencyPipeline(chartDir)

			if tc.wantRefused {
				if err == nil {
					t.Errorf("the tool accepted a tree declaring the attachment as a child while its chart "+
						"sits at %q; this arm is what shows the dependency entry is load-bearing, so while it "+
						"passes silently the entry could be dropped with nothing objecting:\n%s", tc.dest, rendered)
				}
				return
			}
			// Both surviving arms must LOAD and RENDER cleanly. That is the point
			// of the undeclared arm: it is not a broken chart, which is why no
			// exit code and no path check can tell it from the emitted one.
			if err != nil {
				t.Fatalf("the chart must render from %q, got: %v", tc.dest, err)
			}

			// The host's own resource renders in both. It is the positive control
			// for the render itself: without it, an arm that rendered nothing at
			// all would look the same as a correctly-absent attachment.
			if !strings.Contains(rendered, hostChartMarker) {
				t.Fatalf("the host chart's own resource did not render, so this arm measures nothing:\n%s", rendered)
			}

			if got := strings.Contains(rendered, attachmentMarker); got != tc.wantRendered {
				if tc.wantRendered {
					t.Errorf("the attachment's own resource is absent from what the tool produced.\n"+
						"Its chart is at %q and its files are present there, but nothing rendered from them.\n"+
						"rendered output:\n%s", tc.dest, rendered)
					return
				}
				t.Errorf("the attachment's own resource rendered from %q, which this arm requires to be a "+
					"position the loader treats as inert data.\nThis arm is the control, so while it renders "+
					"anything the emitted-position arm beside it proves nothing:\n%s", tc.dest, rendered)
			}
		})
	}
}

// TestHelmChartAttachmentDefectAtTheEmittedPositionIsCaught is the falsifiability
// demonstration, and it asserts a property none of the arms above do.
//
// 🔴 Those arms vary POSITION over fixed bytes. Every one of them could pass
// against a tool that never reads the attachment's content at all — a tool that
// merely listed the files would satisfy them. So they do not establish that the
// verdict is a function of what the attachment CONTAINS, which is exactly the
// property the emitted position was moved to buy.
//
// This plants a defect in the attachment's own template at the emitted position
// and requires the tool to refuse. Its control is the arm above that renders the
// same bytes cleanly from the same position: together they show the verdict
// moving with the content and nothing else.
//
// Measured before this position existed: the same defect at the old nested
// position left both `helm lint` and `helm template` at exit 0, because a chart
// nothing discovers contributes nothing to any verdict.
func TestHelmChartAttachmentDefectAtTheEmittedPositionIsCaught(t *testing.T) {
	t.Parallel()

	files := renderComponentScopeAttachmentOnChartHost(t)
	umbrella := umbrellaRootOf(t, files)
	attachRoot := chartRootCarrying(t, files, attachmentMarker)

	// The template carrying the attachment's marker: the one file whose body the
	// tool must have read for that marker to reach rendered output.
	var markerRel string
	for _, k := range sortedKeys(files) {
		if bytes.Contains(files[k].Content, []byte(attachmentMarker)) {
			markerRel = k
			break
		}
	}
	if markerRel == "" {
		t.Fatalf("no emitted file carries %q, so there is nothing to plant a defect in", attachmentMarker)
	}

	chartDir := materializeWithAttachmentAt(t, files, attachRoot, attachRoot, umbrella, false)
	// The materialised tree's root, recovered from the umbrella directory the
	// helper returned — the emitted keys are relative to it, so every emitted
	// path composes from it directly.
	treeRoot := strings.TrimSuffix(chartDir, string(filepath.Separator)+filepath.FromSlash(umbrella))
	planted := filepath.Join(treeRoot, filepath.FromSlash(markerRel))

	if _, err := os.Stat(planted); err != nil {
		t.Fatalf("the file to plant a defect in is not where the materialised tree put it (%q): %v", planted, err)
	}
	// A template action referring to a function that does not exist: the chart
	// still parses as a file and fails when the tool RENDERS it, which is the
	// stage that only a discovered chart reaches.
	if err := os.WriteFile(planted, []byte("{{ thisFunctionDoesNotExist }}\n"), 0o644); err != nil {
		t.Fatalf("planting the defect: %v", err)
	}

	rendered, err := charttest.RenderThroughDependencyPipeline(chartDir)
	if err == nil {
		t.Errorf("a defect planted inside the attachment's own chart at its emitted position %q did not "+
			"make the tool fail, so the verdict is not a function of that chart's content and the "+
			"position-varying arms beside this one prove only that the files were listed:\n%s",
			attachRoot, rendered)
	}
}
