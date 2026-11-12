// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The output path a boundary-scope FeatureRecipe's files land at, proved through
// the shipped provider guests rather than through a stand-in assembler, because
// which directory a contributor's files sit under is the provider's own answer
// and a stand-in would be free to answer differently from the guest that ships.
//
// A provider that places each contributor in its own sub-directory is
// the case worth proving: the directory name is a path segment a reader opens,
// and the per-node key the render groups a boundary-scope attachment under is a
// marker the compiler built for its own dispatch. That marker opens with an
// angle bracket and joins with a colon — characters a path refuses on some
// platforms and a URL has to escape — so a reader must never meet it, and the
// name they meet instead is the one they wrote in the boundary's features block.
//
// A provider that places its contributors flat is here as the control. Its
// attachment output is pre-keyed at the boundary's own features position and is
// never handed to a per-contributor placement at all, so a change aimed at the
// nesting position that moved it has reached past what it was aimed at. The
// component beside the attachment is on the flat provider's other side of that
// line: a component's own artifacts DO sit under a directory named for the
// component, because the name such a file carries is the key its author wrote
// and carries nothing of the component. Pinning both in one path set is what
// holds the two apart.

// boundaryFRChart writes a minimal chart whose one template emits a ConfigMap,
// for a provider that consumes a sourced chart rather than an artifacts map.
func boundaryFRChart(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"),
		[]byte("apiVersion: v2\nname: "+name+"\nversion: 1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "values.yaml"), []byte("image: "+name+":1.0\n"), 0o644); err != nil {
		t.Fatalf("write values.yaml: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "templates"), 0o755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "templates", "configmap.yaml"),
		[]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: "+name+"\ndata:\n  marker: \"x\"\n"), 0o644); err != nil {
		t.Fatalf("write configmap.yaml: %v", err)
	}
	return dir
}

// oneInlineArtifact builds a single-entry inline artifacts map.
func oneInlineArtifact(filename, body string) types.OrderedMap[types.ArtifactEntry] {
	m := types.OrderedMap[types.ArtifactEntry]{}
	m.Set(filename, types.ArtifactEntry{Inline: body})
	return m
}

// boundaryFROutputPathCase is one provider's render of a boundary-scope
// attachment beside a component in the same boundary and under the same
// provider, so the two share a dispatch group and the group's assembler (when it
// has one) decides where each contributor's files sit.
//
// A group holding only the attachment elects no assembler, and its files would
// then be placed flat whatever the provider claims — so the component beside it
// is what makes the nesting position reachable at all, not scenery.
type boundaryFROutputPathCase struct {
	name     string
	provider string
	// sourced selects a chart source over an artifacts map for both the
	// component's Recipe and the attachment's FeatureRecipe, because a provider
	// declares which input kinds it accepts and no one shape suits all of them.
	sourced bool
	// wantPaths is the case's whole emitted path set in path order, written out
	// rather than composed from the code under test. Pinning all of it rather than
	// the attachment's share alone is what holds the component's own position and
	// the group's assembled output to account in the same comparison.
	wantPaths []string
	// wantAttachmentDir is the per-contributor directory the group's assembler
	// named for the attachment, or "" for a provider whose group names none. It is
	// a second, redundant statement of what wantPaths already implies, and
	// deliberately so: it is the one the assertion reports on, so a failure names
	// the segment rather than a path list a reader has to diff by eye.
	wantAttachmentDir string
}

func boundaryFROutputPathCases() []boundaryFROutputPathCase {
	return []boundaryFROutputPathCase{
		{
			name:     "terraform nests each contributor under its own module directory",
			provider: "terraform",
			wantPaths: []string{
				"dist/cluster/terraform/aggrega.modules.tf",
				"dist/cluster/terraform/modules/api/aggrega.provenance.tf",
				"dist/cluster/terraform/modules/api/api.tf.json",
				"dist/cluster/terraform/modules/aux/aggrega.provenance.tf",
				"dist/cluster/terraform/modules/aux/aux.tf.json",
			},
			wantAttachmentDir: "dist/cluster/terraform/modules/aux/",
		},
		{
			name:     "helm-chart nests each contributor under its own chart directory",
			provider: "helm-chart",
			sourced:  true,
			wantPaths: []string{
				"dist/cluster/helm-chart/Chart.yaml",
				"dist/cluster/helm-chart/charts/api/Chart.yaml",
				"dist/cluster/helm-chart/charts/api/templates/configmap.yaml",
				"dist/cluster/helm-chart/charts/api/values.yaml",
				"dist/cluster/helm-chart/charts/aux/Chart.yaml",
				"dist/cluster/helm-chart/charts/aux/templates/configmap.yaml",
				"dist/cluster/helm-chart/charts/aux/values.yaml",
				"dist/cluster/helm-chart/values.yaml",
			},
			wantAttachmentDir: "dist/cluster/helm-chart/charts/aux/",
		},
		{
			// The control. This provider elects no assembler and so gives no
			// contributor a position of its own: there is no directory an
			// attachment could sit in, so its instance name is encoded into the
			// filename and the file lands directly in the boundary's own
			// directory, beside what it is attached to. That is the whole point of
			// the flat shape — a consumer reading only the files sitting directly
			// in a directory picks the attachment up without being told to recurse.
			//
			// The component's artifact beside it sits under the component's name —
			// not an assembler's doing, but the artifacts placement's, which is
			// what keeps two components in one boundary from writing one path. So
			// this case also holds the two positions apart: the attachment's
			// coordinates moved into a filename and the component's artifact
			// directory did not move at all.
			name:     "helm gives no contributor a position, so the attachment's instance is encoded into its filename",
			provider: "helm",
			wantPaths: []string{
				"dist/cluster/helm/api/api-config.yaml",
				"dist/cluster/helm/aux--aux-config.yaml",
			},
			wantAttachmentDir: "",
		},
	}
}

// renderBoundaryFROutputPathCase drives RenderAll over one case and returns the
// emitted file map.
func renderBoundaryFROutputPathCase(t *testing.T, c boundaryFROutputPathCase) map[string]renderer.AssembledFile {
	t.Helper()

	const (
		boundary   = "cluster"
		component  = "api"
		frInstance = "aux"
		featType   = "core/aux:1.0.0"
	)

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(component, types.IRNode{Component: "core/api", BoundaryPath: boundary})

	boundaryFeatures := types.OrderedMap[types.BoundaryFeature]{}
	boundaryFeatures.Set(frInstance, types.BoundaryFeature{Type: featType, Infra: types.OrderedMap[any]{}})
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
	clusterBFProj.Set(frInstance, types.IRBoundaryFeature{Type: featType, Infra: types.OrderedMap[any]{}})
	bfProj := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	bfProj.Set(boundary, clusterBFProj)

	apiFV := types.OrderedMap[types.IRFeatureEntry]{}
	apiFV.Set(frInstance, types.IRFeatureEntry{Type: featType})
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set(component, apiFV)

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "boundary-fr-path-app"},
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
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "boundary-fr-path-app"}},
			Release:   &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{
				component: {BoundaryPath: boundary, Directory: "dist/cluster"},
			},
		},
	}

	componentRecipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
		Spec:     types.RecipeSpec{Capability: c.provider, Implements: "core/api"},
	}
	frRecipe := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "aux-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability:     c.provider,
			Implements:     featType,
			ExecutionScope: "boundary",
			ValuesMapping:  types.ValuesMapping{Inline: `{}`},
		},
	}
	switch {
	case c.sourced:
		componentRecipe.Spec.Source = boundaryFRChart(t, "api")
		frRecipe.Spec.Source = boundaryFRChart(t, "aux")
	case c.provider == "helm":
		componentRecipe.Spec.Artifacts = oneInlineArtifact("api-config.yaml",
			"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: api\ndata:\n  marker: \"x\"\n")
		frRecipe.Spec.Artifacts = oneInlineArtifact("aux-config.yaml",
			"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: aux\ndata:\n  marker: \"x\"\n")
	default:
		// Each artifact is a document this target actually accepts, carrying an
		// output under a name of its own. The path set below is what this file
		// asserts and is indifferent to the content — which is exactly why the
		// content has to be right here rather than merely present: a fixture
		// modelling a document the target would reject exercises a shape the
		// product cannot produce, and no assertion in this file can see it. The
		// names differ per contributor because this target merges every top-level
		// file in a directory into one namespace.
		componentRecipe.Spec.Artifacts = oneInlineArtifact("api.tf.json", "{\"output\":{\"api_marker\":{\"value\":\"api\"}}}\n")
		frRecipe.Spec.Artifacts = oneInlineArtifact("aux.tf.json", "{\"output\":{\"aux_marker\":{\"value\":\"aux\"}}}\n")
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
		t.Fatalf("case %q: RenderAll: %v", c.name, err)
	}
	return result.Files
}

// sortedEmittedPaths is the emitted file map's key set in path order.
func sortedEmittedPaths(files map[string]renderer.AssembledFile) []string {
	out := make([]string, 0, len(files))
	for p := range files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// TestBoundaryScopeFROutputPathCorpusReachesBothNestingProviders is the corpus's
// own anti-needle. The proofs below are worthless over a corpus of flat
// providers, because a flat provider never places a contributor under a
// per-contributor directory and so could never have carried the marker. It also
// runs outside the parallel subtests below, which is deliberate: a count
// accumulated inside them would be read before they finished and would report
// zero however good the corpus was.
func TestBoundaryScopeFROutputPathCorpusReachesBothNestingProviders(t *testing.T) {
	t.Parallel()

	nesting := map[string]bool{}
	flat := 0
	for _, c := range boundaryFROutputPathCases() {
		if c.wantAttachmentDir == "" {
			flat++
			continue
		}
		nesting[c.provider] = true
		// A pinned directory has to actually be the parent of the pinned
		// attachment paths, or the redundant statement is not a cross-check.
		covered := 0
		for _, p := range c.wantPaths {
			if strings.HasPrefix(p, c.wantAttachmentDir) {
				covered++
			}
		}
		if covered == 0 {
			t.Errorf("case %q pins the attachment directory %q, which no pinned path sits under",
				c.name, c.wantAttachmentDir)
		}
	}
	for _, want := range []string{"terraform", "helm-chart"} {
		if !nesting[want] {
			t.Errorf("the corpus reaches no nesting case for %q; the marker only ever appeared at a "+
				"nesting position, so a corpus without it would pass with the defect intact", want)
		}
	}
	if flat == 0 {
		t.Error("the corpus carries no flat provider, so nothing holds the untouched position to account")
	}
}

// TestBoundaryScopeFROutputPathNamesTheAttachmentInstance is the emitted-path
// proof. Each case pins its whole path set, so the assertion fails on a
// directory named anything other than the instance the boundary's features block
// declares — including the per-node dispatch key, which is what the nesting
// position carried before.
func TestBoundaryScopeFROutputPathNamesTheAttachmentInstance(t *testing.T) {
	t.Parallel()

	for _, c := range boundaryFROutputPathCases() {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := sortedEmittedPaths(renderBoundaryFROutputPathCase(t, c))
			if !slices.Equal(got, c.wantPaths) {
				t.Fatalf("emitted paths\n got: %v\nwant: %v", got, c.wantPaths)
			}
			if c.wantAttachmentDir == "" {
				return
			}
			// The attachment's files sit AT a position of their own named for the
			// instance, beside the component's rather than below anything, reported
			// on its own so a failure names the segment.
			//
			// The check is that at least one emitted path sits under it AND that
			// nothing sits deeper than one level within it. The second half is the
			// one that matters: a directory named for the instance with the files a
			// further level down is exactly the shape a nesting target does not
			// discover, and it satisfies a prefix check perfectly.
			under := 0
			for _, p := range got {
				rest, ok := strings.CutPrefix(p, c.wantAttachmentDir)
				if !ok {
					continue
				}
				under++
				if strings.Contains(rest, "/") && !strings.HasPrefix(rest, "templates/") {
					t.Errorf("attachment output %q sits at %q within its own position %q; a nesting target "+
						"discovers its contributors AT their positions, so a further directory level here is "+
						"content belonging to something else and is read by nothing", p, rest, c.wantAttachmentDir)
				}
			}
			if under == 0 {
				t.Errorf("no emitted path sits under the attachment's own position %q, so this case asserts "+
					"nothing about where the attachment went: %v", c.wantAttachmentDir, got)
			}
		})
	}
}

// TestBoundaryScopeFROutputPathCarriesNoDispatchMarker is the character-level
// assertion, stated apart from the pinned paths because it is what makes a
// pinned path re-derivable rather than merely agreed: no emitted path may carry
// a character the dispatch marker opens or joins with, whatever the pinned set
// happens to say.
//
// The four characters are the ones a path refuses on some platforms and a URL
// has to escape, and the rendered tree lands in cross-platform repositories that
// are cloned on all of them.
//
// It bounds the DIAGNOSTIC text by the same four, over the same corpus and in the
// same test, because a path and a halt are two readings of one identity and only
// one of them was ever held to account. The emitted path is built from the name an
// author wrote and so never carried the marker; a halt names the contributor, and
// naming it by the compiler's own dispatch identity puts the marker in front of a
// reader with no file to open and nothing in their own input to match it against.
// Bounding only the paths leaves that half unasserted and reads as though the
// character set were the property, when the property is that no surface a reader
// meets is spelled from the marker.
func TestBoundaryScopeFROutputPathCarriesNoDispatchMarker(t *testing.T) {
	t.Parallel()

	for _, c := range boundaryFROutputPathCases() {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			files := renderBoundaryFROutputPathCase(t, c)
			if len(files) == 0 {
				t.Fatal("the render emitted nothing, so an absence claim over it is vacuous")
			}
			for p := range files {
				assertCarriesNoDispatchMarker(t, "emitted path", p)
			}
		})
	}

	t.Run("a halt names the contributors without the dispatch marker", func(t *testing.T) {
		t.Parallel()
		text := boundaryFRCollisionHaltText(t)
		// The positive control. An absence claim over an empty string holds for every
		// character there is, so the text has to be shown to name the contributors
		// before its not naming the marker means anything.
		for _, want := range []string{
			"feature aux attached at boundary zone-a",
			"feature aux attached at boundary zone-b",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("the halt does not carry %q, so an absence claim over it is vacuous\ngot: %s", want, text)
			}
		}
		// The four are bounded over the values the halt QUOTES rather than over the
		// whole sentence, and the narrowing is the rule rather than a concession. The
		// framework quotes each value it is handed, so a quoted span is where an
		// identity appears and the prose around it is the diagnostic's own English —
		// which carries a colon after every caption. Bounding the sentence would fail
		// on that punctuation while saying nothing about the identity, so it would be
		// an assertion that cannot pass and does not test its subject.
		quoted := quotedValuesOf(text)
		if len(quoted) == 0 {
			t.Fatalf("the halt quotes no value, so there is no identity to bound\ngot: %s", text)
		}
		for _, v := range quoted {
			assertCarriesNoDispatchMarker(t, "halt value", v)
		}
	})
}

// quotedValuesOf is every double-quoted span of a diagnostic's text — the values
// the framework quoted, which is where a contributor identity appears.
func quotedValuesOf(s string) []string {
	var out []string
	for rest := s; ; {
		open := strings.Index(rest, `"`)
		if open < 0 {
			return out
		}
		rest = rest[open+1:]
		close := strings.Index(rest, `"`)
		if close < 0 {
			return out
		}
		out = append(out, rest[:close])
		rest = rest[close+1:]
	}
}

// assertCarriesNoDispatchMarker holds one string clear of every character the
// dispatch marker opens or joins with. One home for the four, so a path and a
// quoted diagnostic value are bounded by one set rather than by two that agree.
func assertCarriesNoDispatchMarker(t *testing.T, what, s string) {
	t.Helper()
	for _, bad := range []string{"<", ">", ":", "@"} {
		if strings.Contains(s, bad) {
			t.Errorf("%s %q carries %q, which a path refuses on some platforms and a URL must escape",
				what, s, bad)
		}
	}
}

// boundaryFRCollisionHaltText renders two boundaries that declare one directory and
// attach one feature under one instance name, and returns the halt.
//
// Both contributors' files land at that instance's own features position, which the
// two share because the instance name is shared — so two renders produce one path
// and the build refuses rather than letting one of them replace the other. It is
// the shortest input that puts a boundary-scope contributor's identity into a
// message a reader reads, which is what this file has to bound.
func boundaryFRCollisionHaltText(t *testing.T) string {
	t.Helper()

	const (
		frInstance = "aux"
		featType   = "core/aux:1.0.0"
		directory  = "dist/shared"
	)
	zones := []string{"zone-a", "zone-b"}

	nodes := types.OrderedMap[types.IRNode]{}
	boundaries := types.OrderedMap[types.Boundary]{}
	bfProj := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	order := ir.FeatureOrder{}
	boundaryInfra := map[string]*resolver.BoundaryInfra{}

	for _, zone := range zones {
		nodeID := "api-" + zone
		nodes.Set(nodeID, types.IRNode{Component: "core/api", BoundaryPath: zone})
		boundaryInfra[nodeID] = &resolver.BoundaryInfra{BoundaryPath: zone, Directory: directory}

		boundaryFeatures := types.OrderedMap[types.BoundaryFeature]{}
		boundaryFeatures.Set(frInstance, types.BoundaryFeature{Type: featType, Infra: types.OrderedMap[any]{}})
		proj := types.OrderedMap[types.IRBoundaryFeature]{}
		proj.Set(frInstance, types.IRBoundaryFeature{Type: featType, Infra: types.OrderedMap[any]{}})
		bfProj.Set(zone, proj)

		nodeFV := types.OrderedMap[types.IRFeatureEntry]{}
		nodeFV.Set(frInstance, types.IRFeatureEntry{Type: featType})
		fvm.Set(nodeID, nodeFV)
		order[nodeID] = []string{frInstance}

		boundary := types.Boundary{
			Directory:  directory,
			Features:   boundaryFeatures,
			Components: types.OrderedMap[types.BoundaryComponent]{},
			Boundaries: types.OrderedMap[types.Boundary]{},
		}
		boundary.Components.Set(nodeID, types.BoundaryComponent{})
		boundaries.Set(zone, boundary)
	}

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "boundary-fr-collision-app"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				InfraMapping:         types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: fvm,
				BoundaryFeatures:     bfProj,
				Provenance:           types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder: order,
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "boundary-fr-collision-app"}},
			Release:       &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}},
			BoundaryInfra: boundaryInfra,
		},
		// The rules declared over what a dispatch group assembled are invoked against
		// axes built from the graph facts, so a render handed none runs none of them —
		// and the collision this case exists to read would go unreported while the
		// output quietly held one file where two were written.
		GraphView: derivedKeyGraphFacts{},
	}

	// Every resource this fixture emits is named for the boundary it came from, so
	// the ONLY thing two contributors here arrive at together is the output path.
	// Without that, both boundaries' ConfigMaps carry one metadata name and the rule
	// over derived names refuses first — a different rule, over a different fault,
	// leaving the path collision this case exists to read unreached and the halt it
	// bounds never produced.
	configMap := func(name string) string {
		return "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: " + name + "\ndata:\n  marker: \"x\"\n"
	}
	lookup := renderer.RecipeLookup{
		Recipe: func(nodeID, componentType string) (*types.Recipe, bool) {
			if componentType != "core/api" {
				return nil, false
			}
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec: types.RecipeSpec{
					Capability: "helm", Implements: "core/api",
					Artifacts: oneInlineArtifact(nodeID+"-config.yaml", configMap(nodeID)),
				},
			}, true
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if !strings.HasPrefix(featureType, "core/aux") {
				return nil, false
			}
			return &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "aux-fr", Version: "1.0.0"},
				Spec: types.FeatureRecipeSpec{
					Capability: "helm", Implements: featType, ExecutionScope: "boundary",
					ValuesMapping: types.ValuesMapping{Inline: `{}`},
					// The file NAME is shared — that is the collision — while the
					// resource inside it is named for the boundary, so the two
					// contributors meet on the path and on nothing else.
					Artifacts: oneInlineArtifact("aux-config.yaml",
						configMap("aux-{{ .boundary.boundary_name }}")),
				},
			}, true
		},
	}

	res, err := renderer.RenderAll(context.Background(), input, lookup, wasmEngineFactory(t))
	if err == nil {
		t.Fatalf("expected a halt; emitted: %v", sortedEmittedPaths(res.Files))
	}
	return errorChainText(err)
}

// errorChainText is every frame of an error's wrap tree, joined.
//
// The whole tree rather than the outermost frame, because a categorised report puts
// its findings in the values it wraps and the top frame names only the class. An
// absence claim read off the top frame alone would be a claim about a sentence that
// names no contributor at all, and would hold however the frames beneath it were
// spelled — which is the reading this bound exists to refuse.
func errorChainText(err error) string {
	var b strings.Builder
	var walk func(e error)
	walk = func(e error) {
		if e == nil {
			return
		}
		b.WriteString(e.Error())
		b.WriteString("\n")
		if joined, isMulti := e.(interface{ Unwrap() []error }); isMulti {
			for _, m := range joined.Unwrap() {
				walk(m)
			}
			return
		}
		walk(errors.Unwrap(e))
	}
	walk(err)
	return b.String()
}
