// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/charttest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// An attachment brings a chart its author shipped, vendored in verbatim. Nothing
// writes the values that chart is deployed with into those bytes, so the only
// surface that can carry them is the umbrella assembled around it — and a chart
// whose values never arrive renders the defaults its author shipped and reports
// success, which is indistinguishable from working unless the default is a string
// no real value could be.
//
// That is what these constants are for: each names exactly one origin, so a
// rendered occurrence says which one arrived rather than merely that something
// did.
const (
	// vendoredValuesDefault is the value the attachment's own chart ships in its
	// values.yaml. It is deliberately not a plausible value: an assertion that
	// merely required SOME value would pass over it, and the whole failure being
	// proved against is one where a default is mistaken for a success.
	vendoredValuesDefault = "DEFAULT-NOT-A-REAL-VALUE-8f3a1c"
	// authoredAttachmentValue is what the Aggregate authored on the attachment
	// and what its FeatureRecipe's own valuesMapping resolves to. It reaches the
	// rendered output only through the umbrella's per-contributor values block.
	authoredAttachmentValue = "AUTHORED-ON-THE-ATTACHMENT-6b21ef"
	// hostValuesMarker is the host component's own rendered marker, the positive
	// control: without it an arm that rendered nothing at all would look the same
	// as one whose attachment values were correctly absent.
	hostValuesMarker = "HOST-CHART-VALUES-CONTROL"
)

// valuesReadingChart writes a chart whose one template renders a value rather
// than a literal, so what reaches the rendered output names where the value came
// from. Its own values.yaml supplies fallback as the default for that key.
func valuesReadingChart(t *testing.T, chartName, resourceName, fallback string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"),
		[]byte("apiVersion: v2\nname: "+chartName+"\nversion: 1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "values.yaml"), []byte("marker: "+fallback+"\n"), 0o644); err != nil {
		t.Fatalf("write values.yaml: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "templates"), 0o755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "templates", "configmap.yaml"),
		[]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: "+resourceName+
			"\ndata:\n  marker: \"{{ .Values.marker }}\"\n"), 0o644); err != nil {
		t.Fatalf("write configmap.yaml: %v", err)
	}
	return dir
}

// renderComponentScopeValuedAttachment drives RenderAll over one chart-nesting
// host carrying one component-scope sourced attachment whose FeatureRecipe
// resolves a value of its own, through the shipped provider guests.
//
// The attachment's value is authored on the attachment in the IR and read by the
// FeatureRecipe's own valuesMapping, rather than being a constant in that
// mapping. Both routes would put a value in the same place, and the authored one
// is the one a build actually takes — so a mapping that ignored its context
// entirely would satisfy a constant fixture and fail a real one.
func renderComponentScopeValuedAttachment(t *testing.T) map[string]renderer.AssembledFile {
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

	authored := types.OrderedMap[any]{}
	authored.Set("marker", authoredAttachmentValue)
	apiFV := types.OrderedMap[types.IRFeatureEntry]{}
	apiFV.Set(frInstance, types.IRFeatureEntry{Type: featType, Values: authored})
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set(component, apiFV)

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "attachment-values-app"},
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
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "attachment-values-app"}},
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
			Source: valuesReadingChart(t, "api", "api-config", hostValuesMarker),
		},
	}
	frRecipe := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "aux-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability:    "helm-chart",
			Implements:    featType,
			ValuesMapping: types.ValuesMapping{Inline: `marker: "{{ .values.marker }}"`},
			Source:        valuesReadingChart(t, "guardrail", "guardrail-config", vendoredValuesDefault),
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

// renderBoundaryScopeValuedAttachment is the same proof one scope out: a
// boundary-scope attachment beside a component in the same boundary and under the
// same provider, so the two share a dispatch group and the group's assembler
// decides what each contributor is supplied with.
//
// A boundary-scope FeatureRecipe evaluates against an empty values pillar by the
// Release pillar split, so the value it resolves is authored on the boundary
// attachment's infra and read from there by its own valuesMapping. That is the
// authored route at this scope, and it is why the two helpers differ here rather
// than sharing one fixture.
func renderBoundaryScopeValuedAttachment(t *testing.T) map[string]renderer.AssembledFile {
	t.Helper()

	const (
		boundary   = "cluster"
		component  = "api"
		frInstance = "aux"
		featType   = "core/aux:1.0.0"
	)

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(component, types.IRNode{Component: "core/api", BoundaryPath: boundary})

	authoredInfra := types.OrderedMap[any]{}
	authoredInfra.Set("marker", authoredAttachmentValue)

	boundaryFeatures := types.OrderedMap[types.BoundaryFeature]{}
	boundaryFeatures.Set(frInstance, types.BoundaryFeature{Type: featType, Infra: authoredInfra})
	clusterBoundary := types.Boundary{
		Directory:  "dist/cluster",
		Features:   boundaryFeatures,
		Components: types.OrderedMap[types.BoundaryComponent]{},
		Boundaries: types.OrderedMap[types.Boundary]{},
	}
	clusterBoundary.Components.Set(component, types.BoundaryComponent{})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set(boundary, clusterBoundary)

	clusterBFProj := types.OrderedMap[types.IRBoundaryFeature]{}
	clusterBFProj.Set(frInstance, types.IRBoundaryFeature{Type: featType, Infra: authoredInfra})
	bfProj := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	bfProj.Set(boundary, clusterBFProj)

	apiFV := types.OrderedMap[types.IRFeatureEntry]{}
	apiFV.Set(frInstance, types.IRFeatureEntry{Type: featType})
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set(component, apiFV)

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "boundary-attachment-values-app"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				InfraMapping:         types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: fvm,
				BoundaryFeatures:     bfProj,
				Provenance:           types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder: ir.FeatureOrder{component: []string{frInstance}},
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "boundary-attachment-values-app"}},
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
			Source: valuesReadingChart(t, "api", "api-config", hostValuesMarker),
		},
	}
	frRecipe := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "aux-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability:     "helm-chart",
			Implements:     featType,
			ExecutionScope: "boundary",
			ValuesMapping:  types.ValuesMapping{Inline: `marker: "{{ .infra.marker }}"`},
			Source:         valuesReadingChart(t, "aux", "aux-config", vendoredValuesDefault),
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

// materializeEmitted writes the emitted tree to a fresh directory, applying
// rewrite to each file's bytes first, and returns the umbrella chart's directory
// inside it. A nil rewrite writes every byte exactly as the render produced it,
// which is how an arm hands the tool the emitted tree untouched.
func materializeEmitted(t *testing.T, files map[string]renderer.AssembledFile, rewrite func(string, []byte) []byte) string {
	t.Helper()
	dest := t.TempDir()
	for _, k := range sortedKeys(files) {
		body := files[k].Content
		if rewrite != nil {
			body = rewrite(k, body)
		}
		p := filepath.Join(dest, filepath.FromSlash(k))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir for %q: %v", k, err)
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatalf("write %q: %v", k, err)
		}
	}
	return filepath.Join(dest, filepath.FromSlash(umbrellaRootOf(t, files)))
}

// rekeyedUmbrellaValues returns the umbrella's values document with the block
// scoped under from moved to to, decoded and re-encoded rather than edited as
// text so the result is a document that genuinely carries the other key.
//
// It fails when the block it is asked to move is not there. An arm whose whole
// premise is that the values now sit at a different address would otherwise run
// against a document that still carries the original one, and would then be
// measuring the case it exists to be the control for.
func rekeyedUmbrellaValues(t *testing.T, body []byte, from, to string) []byte {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("the umbrella values document does not decode as YAML: %v\n%s", err, body)
	}
	block, ok := doc[from]
	if !ok {
		t.Fatalf("the umbrella values document carries no block under %q, so there is nothing to move:\n%s", from, body)
	}
	delete(doc, from)
	doc[to] = block
	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("re-encoding the umbrella values document: %v", err)
	}
	return out
}

// placedChartNameAt is the name the chart manifest emitted at chartRoot
// declares — the value a dependency binds by and the key the umbrella scopes that
// contributor's values under, which is a different string from the directory the
// chart sits in. The decode is the one this package already single-sources.
func placedChartNameAt(t *testing.T, files map[string]renderer.AssembledFile, chartRoot string) string {
	t.Helper()
	manifest, ok := files[path.Join(chartRoot, "Chart.yaml")]
	if !ok {
		t.Fatalf("no chart manifest at %q, so it declares no name", chartRoot)
	}
	return declaredChartName(t, manifest.Content)
}

// attachmentValuesCase is one attachment scope's render, and the two scopes are
// both here because an attachment reaches an assembled artifact by two separate
// routes — one per scope — and a fix applied to one of them leaves the other
// rendering defaults with every signal still reporting success.
type attachmentValuesCase struct {
	name   string
	render func(*testing.T) map[string]renderer.AssembledFile
}

func attachmentValuesCases() []attachmentValuesCase {
	return []attachmentValuesCase{
		{name: "a component-scope attachment", render: renderComponentScopeValuedAttachment},
		{name: "a boundary-scope attachment", render: renderBoundaryScopeValuedAttachment},
	}
}

// chartRootShipping is the emitted directory of the chart whose OWN values
// document ships fallback as its default. Each fixture chart ships a different
// one, so the value names exactly one chart — and it is derived from the emitted
// map rather than pinned, so it keeps naming the right chart after a change to
// where the render places one. The two scopes place their attachment under
// different directories, which a pinned pair would have to restate.
//
// It reads the values document rather than the template because these charts
// render a VALUE rather than a literal: their template bodies carry the template
// action and no marker at all, which is the whole point of the fixture.
func chartRootShipping(t *testing.T, files map[string]renderer.AssembledFile, fallback string) string {
	t.Helper()
	found := ""
	for _, k := range sortedKeys(files) {
		if path.Base(k) != "values.yaml" || !strings.Contains(string(files[k].Content), fallback) {
			continue
		}
		if found != "" {
			t.Fatalf("the default %q appears in both %q and %q; it must name exactly one chart", fallback, found, k)
		}
		found = path.Dir(k)
	}
	if found == "" {
		t.Fatalf("no emitted values document ships the default %q, so the chart that declares it was not "+
			"placed verbatim; keys: %v", fallback, sortedKeys(files))
	}
	return found
}

// attachmentChartRoot is the emitted directory of the chart the ATTACHMENT
// brought.
func attachmentChartRoot(t *testing.T, files map[string]renderer.AssembledFile) string {
	t.Helper()
	return chartRootShipping(t, files, vendoredValuesDefault)
}

// requireUmbrellaScopes fails unless the emitted umbrella scopes the authored
// value under alias, and it is a precondition of both proofs below rather than an
// assertion belonging to one of them.
//
// 🔴 The relocating arm needs it as much as the direct one, and for the sharper
// reason: that arm moves the block to another address and requires the default to
// render, which is exactly what an EMPTY block produces too. Without this check it
// would pass over an umbrella that had no value to move, reporting a discriminating
// result while discriminating nothing.
func requireUmbrellaScopes(t *testing.T, files map[string]renderer.AssembledFile, alias string) {
	t.Helper()
	key := path.Join(umbrellaRootOf(t, files), "values.yaml")
	umbrella, ok := files[key]
	if !ok {
		t.Fatalf("the render emitted no umbrella values document; keys: %v", sortedKeys(files))
	}
	var doc map[string]any
	if err := yaml.Unmarshal(umbrella.Content, &doc); err != nil {
		t.Fatalf("the umbrella values document does not decode as YAML: %v\n%s", err, umbrella.Content)
	}
	block, held := doc[alias].(map[string]any)
	if !held {
		t.Fatalf("the umbrella scopes no values under %q, the name the attachment's placed chart declares, so "+
			"the values authored on that attachment have no address to arrive at:\n%s", alias, umbrella.Content)
	}
	if got := block["marker"]; got != authoredAttachmentValue {
		t.Fatalf("the umbrella scopes %v under %q, want %q — the value authored on the attachment and resolved "+
			"by its own valuesMapping:\n%s", got, alias, authoredAttachmentValue, umbrella.Content)
	}
}

// TestAttachmentValuesReachTheChartTheAttachmentBrought is the discriminating
// proof: the value a Aggregate authored on an attachment has to appear in what
// the chart tool PRODUCED, and the value the vendored chart ships as its own
// default has to be absent from it.
//
// 🔴 Both halves are the assertion. A chart whose values are unaddressable
// renders its own defaults and reports success, so an assertion that only
// required SOME value would pass over exactly the failure being proved against —
// which is why the vendored default is a string no real value would be rather
// than a plausible one.
//
// The host component's own marker is the positive control: without it an arm that
// rendered nothing at all would look the same as one whose values arrived.
func TestAttachmentValuesReachTheChartTheAttachmentBrought(t *testing.T) {
	t.Parallel()

	for _, c := range attachmentValuesCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			files := c.render(t)
			alias := placedChartNameAt(t, files, attachmentChartRoot(t, files))

			// The umbrella's own bytes first, because they are what a reader diffs
			// and what every later stage reads. An assertion only on the rendered
			// output would pass on an umbrella that reached the same result by some
			// other route, and would say nothing about the document under review.
			requireUmbrellaScopes(t, files, alias)

			rendered, err := charttest.RenderThroughDependencyPipeline(materializeEmitted(t, files, nil))
			if err != nil {
				t.Fatalf("the emitted tree must render: %v", err)
			}
			if !strings.Contains(rendered, hostValuesMarker) {
				t.Fatalf("the host chart's own resource did not render, so this case measures nothing:\n%s", rendered)
			}
			if !strings.Contains(rendered, authoredAttachmentValue) {
				t.Errorf("the value authored on the attachment is absent from what the tool produced, so the "+
					"author's mapping reached nothing:\n%s", rendered)
			}
			if strings.Contains(rendered, vendoredValuesDefault) {
				t.Errorf("the attachment's chart rendered the default its author shipped, which is what an "+
					"unaddressable value looks like — the render succeeds and the deployment is wrong:\n%s", rendered)
			}
		})
	}
}

// TestAttachmentValuesAreAddressedByTheDeclaredNameNotTheDirectory is what keeps
// the proof above from being vacuous.
//
// 🔴 That proof could pass against a tool that scoped the umbrella's values by
// any key at all — by the directory, or by every key at once — because it varies
// nothing. This moves the block to the one other address a reader would guess,
// the directory the chart sits in, and requires the render to fall back to the
// vendored default. Together the two show the rendered value moving with the
// address and with nothing else.
//
// The tool still succeeds here, and that is the finding rather than an aside: a
// value written at an address nothing reads is not an error at any stage.
func TestAttachmentValuesAreAddressedByTheDeclaredNameNotTheDirectory(t *testing.T) {
	t.Parallel()

	for _, c := range attachmentValuesCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			files := c.render(t)
			attachRoot := attachmentChartRoot(t, files)
			alias := placedChartNameAt(t, files, attachRoot)
			dirKey := path.Base(attachRoot)

			// The arm is vacuous where the two addresses are one string, and it would
			// pass silently in that state: every assertion below would still agree.
			if alias == dirKey {
				t.Fatalf("the placed chart declares %q and sits in a directory of the same name, so moving the "+
					"values from one to the other moves nothing and this arm measures nothing", alias)
			}
			// And there has to BE a value at the declared name to move, or the
			// default rendering below says nothing about the address.
			requireUmbrellaScopes(t, files, alias)

			umbrellaValues := path.Join(umbrellaRootOf(t, files), "values.yaml")
			dir := materializeEmitted(t, files, func(key string, body []byte) []byte {
				if key != umbrellaValues {
					return body
				}
				return rekeyedUmbrellaValues(t, body, alias, dirKey)
			})

			rendered, err := charttest.RenderThroughDependencyPipeline(dir)
			if err != nil {
				t.Fatalf("a tree whose values sit at an address nothing reads is not a broken tree and must "+
					"still render; that it does is the whole reason this failure is silent: %v", err)
			}
			if !strings.Contains(rendered, hostValuesMarker) {
				t.Fatalf("the host chart's own resource did not render, so this arm measures nothing:\n%s", rendered)
			}
			if !strings.Contains(rendered, vendoredValuesDefault) {
				t.Errorf("with the values scoped under the attachment's DIRECTORY %q rather than the name its "+
					"placed chart declares (%q), the chart must fall back to the default its author shipped. It "+
					"did not, so the directory is reaching the chart too and the proof beside this one does not "+
					"establish which address carried the value:\n%s", dirKey, alias, rendered)
			}
			if strings.Contains(rendered, authoredAttachmentValue) {
				t.Errorf("the authored value arrived through the directory key %q; the two addresses are "+
					"therefore not distinguishable and neither arm proves anything about which one the "+
					"umbrella has to write:\n%s", dirKey, rendered)
			}
		})
	}
}

// TestAttachmentValuesProofToolRefusesAPlantedDefect is the falsifiability
// demonstration for the instrument itself.
//
// 🔴 Every arm above reads the tool's OUTPUT and none of them reads its verdict,
// so none establishes that the instrument can refuse anything at all. A helper
// that returned the same string whatever it was handed would satisfy all of them.
// This plants a defect in the host component's own template — a file the tool
// unambiguously reads, since the arms above depend on its marker rendering — and
// requires a refusal.
func TestAttachmentValuesProofToolRefusesAPlantedDefect(t *testing.T) {
	t.Parallel()

	for _, c := range attachmentValuesCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			files := c.render(t)

			// A template of the HOST component's own chart: the arms above require
			// the tool to have rendered one, so it is unambiguously a file it reads.
			hostRoot := chartRootShipping(t, files, hostValuesMarker)
			hostTemplate := ""
			for _, k := range sortedKeys(files) {
				if strings.HasPrefix(k, path.Join(hostRoot, "templates")+"/") {
					hostTemplate = k
					break
				}
			}
			if hostTemplate == "" {
				t.Fatalf("the host chart at %q emitted no template, so there is nothing to plant a defect in; "+
					"keys: %v", hostRoot, sortedKeys(files))
			}

			// A template action naming a function that does not exist: the file still
			// parses and fails when the tool RENDERS it, which is the stage only a
			// discovered chart reaches.
			dir := materializeEmitted(t, files, func(key string, body []byte) []byte {
				if key != hostTemplate {
					return body
				}
				return []byte("{{ thisFunctionDoesNotExist }}\n")
			})

			rendered, err := charttest.RenderThroughDependencyPipeline(dir)
			if err == nil {
				t.Errorf("a defect planted in %q did not make the tool refuse, so its verdict is not a function "+
					"of what it rendered and the greens beside this prove only that it tolerated the tree:\n%s",
					hostTemplate, rendered)
			}
		})
	}
}
