// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The names below are the shapes a provider actually derives, not toy strings: a
// logical id sanitised from a node id joined to a resource name, and a
// four-segment export name. The near-miss differs from the colliding name by one
// character, which is what makes the negative direction meaningful — a check
// matching on similarity rather than equality would reject it.
const (
	e2eDerivedLogicalID         = "paymentsapiQueue"
	e2eDerivedLogicalIDNearMiss = "paymentsapiQueve"
	e2eDerivedExportName        = "aggrega-prod-api-orders-db-host"
)

// derivedKeyGraphFacts stands in for the presentation the graph finalisation
// produces, which the render embeds beneath the presentations built over it.
//
// The rule under test reads one fact and it is not a graph-stage one — it is
// answered from what each group's contributors declared as they rendered — so
// this is never read. It carries no value behind the interface deliberately: a
// read panics here rather than answering a zero value, so a rule that later
// starts reading a graph-stage fact fails loudly instead of being quietly handed
// nothing.
//
// It is supplied because the presentation chain is cumulative and refuses to
// assemble with a hole in it: the render publishes no mid-render presentation
// unless the one it extends exists. Production always supplies this, from the one
// call path that renders; a fixture omitting it would leave the rule invoked zero
// times for a reason that has nothing to do with what is being proved — and a
// rule invoked zero times reports exactly what a rule that ran and approved
// reports.
type derivedKeyGraphFacts struct{ constraint.GraphView }

// derivedNameFindings returns the individual findings a derived-name halt
// carries, whichever shape the halt took.
//
// It exists because reading the halt as a string is a known-bad instrument here.
// The halt is built by a count-branching construction: one finding is returned as
// its own diagnostic with no envelope, and two or more arrive as an envelope
// wrapping the set — and an envelope's own text is its message alone, so a
// substring search over the halt reports nothing about the findings underneath
// whenever more than one was raised.
//
// The two branches are both handled rather than one asserted, because which
// branch a case takes is a property of that case's input: a single colliding name
// derived by a single repeating contributor raises one finding, and every
// two-contributor collision raises at least two.
func derivedNameFindings(t *testing.T, err error) []string {
	t.Helper()

	var envelope *cerrors.CompilerError
	if !errors.As(err, &envelope) {
		t.Fatalf("the halt is not a categorised diagnostic: %v", err)
	}
	joined, isMulti := envelope.Unwrap().(interface{ Unwrap() []error })
	if !isMulti {
		// The single-finding branch: the objection's own diagnostic, with no
		// envelope above it, so the frame itself is the finding.
		return []string{envelope.Error()}
	}
	members := joined.Unwrap()
	out := make([]string, 0, len(members))
	for _, member := range members {
		out = append(out, member.Error())
	}
	return out
}

// declaringEngine is a synthetic provider engine that declares, per node, the
// names it derived — the same thing a real provider guest does when it returns
// them and the adapter forwards them to the node's sink. It emits a per-node
// output path so no output-path collision fires, keeping the derived-name check
// the only thing these tests can trip.
type declaringEngine struct {
	// declare maps a node id to the spaces and names that node declares.
	declare map[string]map[string][]string
	// crossings is how many separate declarations each node makes, each carrying
	// the whole payload above. One is the default and models a node that declares
	// at a single render crossing; more than one models a node that declares the
	// same names again at a further crossing — a source render, a workload
	// synthesis, an artifacts placement — which is what the host sees as one
	// contributor restating itself. The host distinguishes the cases only by how
	// many separate declarations arrive, so this is the faithful fixture for the
	// distinction rather than an approximation of it.
	crossings int
}

func (e *declaringEngine) Render(_ context.Context, _ map[string]any, nodeCtx renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
	if spaces, ok := e.declare[nodeCtx.NodeID]; ok && nodeCtx.DerivedKeySink != nil {
		declared := types.OrderedMap[[]string]{}
		// Set in a fixed order so the payload this stub hands the host is a
		// function of the fixture rather than of Go's map iteration.
		for _, space := range []string{"logical-ids", "export-names"} {
			if keys, ok := spaces[space]; ok {
				declared.Set(space, keys)
			}
		}
		if declared.Len() > 0 {
			crossings := e.crossings
			if crossings < 1 {
				crossings = 1
			}
			for i := 0; i < crossings; i++ {
				nodeCtx.DerivedKeySink(declared)
			}
		}
	}
	return []renderer.RenderedFile{
		{Path: nodeCtx.NodeID + ".yaml", Content: []byte("id: " + nodeCtx.NodeID + "\n")},
	}, nil
}

func (e *declaringEngine) Contract() renderer.ProviderContract {
	return renderer.ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

// runDeclaringRender drives the full pipeline over the named component instances,
// all in one boundary and all rendered by engine.
func runDeclaringRender(instances []string, engine renderer.Provider) (*renderer.RenderResult, error) {
	nodes := types.OrderedMap[types.IRNode]{}
	for _, id := range instances {
		nodes.Set(id, types.IRNode{Component: "core/api", BoundaryPath: "default"})
	}
	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "declare-app"},
			Spec: types.IRSpec{
				Nodes:      nodes,
				Provenance: types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder:  ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{Aggregate: &types.Aggregate{}},
		GraphView:     derivedKeyGraphFacts{},
	}
	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "declare-recipe", Version: "1.0.0"},
				Spec:     types.RecipeSpec{Capability: "declare", Source: "./chart"},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}
	factory := func(_ string) (renderer.Provider, error) { return engine, nil }
	return renderer.RenderAll(context.Background(), input, lookup, factory)
}

// TestRenderAll_TwoGuestsDeclaringOneNameHalt is the end-to-end proof in the
// positive direction: two contributors declaring one name in one key space stop
// the build, and the diagnostic names the space, the name and both contributors —
// enough to act on without opening the provider.
func TestRenderAll_TwoGuestsDeclaringOneNameHalt(t *testing.T) {
	t.Parallel()

	engine := &declaringEngine{declare: map[string]map[string][]string{
		"alpha": {"logical-ids": {e2eDerivedLogicalID}},
		"beta":  {"logical-ids": {e2eDerivedLogicalID}},
	}}
	res, err := runDeclaringRender([]string{"alpha", "beta"}, engine)
	if err == nil {
		t.Fatalf("expected a halt, got %d files", len(res.Files))
	}
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("expected ErrDerivedKeyCollision, got: %v", err)
	}
	findings := derivedNameFindings(t, err)
	if len(findings) != 2 {
		t.Fatalf("expected one finding per contributor, got %d: %v", len(findings), findings)
	}
	whole := strings.Join(findings, "\n")
	for _, want := range []string{
		`key space "logical-ids"`,
		`"paymentsapiQueue"`,
		`derived by component "alpha"`,
		`derived by component "beta"`,
	} {
		if !strings.Contains(whole, want) {
			t.Errorf("the findings do not carry %q\ngot: %v", want, findings)
		}
	}
}

// TestRenderAll_TheSamePairWithoutTheCollisionRenders is the same pair in the
// negative direction: one character of one declared name differs, so nothing
// collides and the build completes. Both halves are needed — the positive
// direction alone would be satisfied by a guard that rejected every declaration.
func TestRenderAll_TheSamePairWithoutTheCollisionRenders(t *testing.T) {
	t.Parallel()

	engine := &declaringEngine{declare: map[string]map[string][]string{
		"alpha": {"logical-ids": {e2eDerivedLogicalID}},
		"beta":  {"logical-ids": {e2eDerivedLogicalIDNearMiss}},
	}}
	res, err := runDeclaringRender([]string{"alpha", "beta"}, engine)
	if err != nil {
		t.Fatalf("the same pair without a collision must render: %v", err)
	}
	if len(res.Files) == 0 {
		t.Fatal("expected rendered output")
	}
}

// TestRenderAll_OneGuestDeclaringOneNameTwiceInOneDeclarationHalts is the
// end-to-end proof for the second way the property fails. A single contributor's
// single declaration carries one name twice in one key space, so its own
// derivation is not injective and the merged output has room for only one of the
// two — the same silent loss two contributors colliding would produce, with no
// second contributor involved.
func TestRenderAll_OneGuestDeclaringOneNameTwiceInOneDeclarationHalts(t *testing.T) {
	t.Parallel()

	engine := &declaringEngine{declare: map[string]map[string][]string{
		"alpha": {"logical-ids": {e2eDerivedLogicalID, e2eDerivedLogicalID}},
	}}
	res, err := runDeclaringRender([]string{"alpha"}, engine)
	if err == nil {
		t.Fatalf("expected a halt, got %d files", len(res.Files))
	}
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("expected ErrDerivedKeyCollision, got: %v", err)
	}
	// One colliding name derived by one repeating contributor raises one finding,
	// which the halt construction returns as its own diagnostic with no envelope
	// above it. Asserting the count is what keeps the extraction honest about
	// which branch this case takes.
	findings := derivedNameFindings(t, err)
	if len(findings) != 1 {
		t.Fatalf("expected one finding naming the one contributor, got %d: %v", len(findings), findings)
	}
	for _, want := range []string{
		`key space "logical-ids"`,
		`"paymentsapiQueue"`,
		`derived more than once by component "alpha"`,
	} {
		if !strings.Contains(findings[0], want) {
			t.Errorf("the finding does not carry %q\ngot: %s", want, findings[0])
		}
	}
}

// TestRenderAll_TheSameDeclarationWithoutTheRepeatRenders is that halt's negative
// direction. The same single contributor declares two names in one space that
// differ by one character, so nothing repeats and the build completes — which the
// positive direction alone would not distinguish from a guard that refused any
// contributor declaring more than one name.
func TestRenderAll_TheSameDeclarationWithoutTheRepeatRenders(t *testing.T) {
	t.Parallel()

	engine := &declaringEngine{declare: map[string]map[string][]string{
		"alpha": {"logical-ids": {e2eDerivedLogicalID, e2eDerivedLogicalIDNearMiss}},
	}}
	res, err := runDeclaringRender([]string{"alpha"}, engine)
	if err != nil {
		t.Fatalf("one declaration carrying two distinct names must render: %v", err)
	}
	if len(res.Files) == 0 {
		t.Fatal("expected rendered output")
	}
}

// TestRenderAll_OneGuestRestatingOneNameAcrossCrossingsRenders is the bound the
// halt above must not break, proved through the whole pipeline. One contributor
// declares the same name at two separate crossings: the name describes one
// resource, the output carries it once, and the build completes. A check that
// compared the accumulated names instead of each declaration as it arrived would
// refuse this, which is why it is asserted end to end rather than at the unit
// alone.
func TestRenderAll_OneGuestRestatingOneNameAcrossCrossingsRenders(t *testing.T) {
	t.Parallel()

	engine := &declaringEngine{
		declare:   map[string]map[string][]string{"alpha": {"logical-ids": {e2eDerivedLogicalID}}},
		crossings: 3,
	}
	res, err := runDeclaringRender([]string{"alpha"}, engine)
	if err != nil {
		t.Fatalf("one contributor restating a name across crossings must render: %v", err)
	}
	if len(res.Files) == 0 {
		t.Fatal("expected rendered output")
	}
}

// TestRenderAll_RepeatDiagnosticIsStableAcrossRuns pins the halt's own
// determinism. Two contributors each repeating a name inside their own
// declaration produce two findings in one build, and identical input must report
// them identically however the accumulating walks were ordered.
func TestRenderAll_RepeatDiagnosticIsStableAcrossRuns(t *testing.T) {
	t.Parallel()

	engine := &declaringEngine{declare: map[string]map[string][]string{
		"alpha": {"logical-ids": {e2eDerivedLogicalID, e2eDerivedLogicalID}},
		"beta":  {"export-names": {e2eDerivedExportName, e2eDerivedExportName}},
	}}
	var first []string
	for i := 0; i < 40; i++ {
		_, err := runDeclaringRender([]string{"alpha", "beta"}, engine)
		if err == nil {
			t.Fatalf("iteration %d: expected a halt", i)
		}
		findings := derivedNameFindings(t, err)
		if i == 0 {
			first = findings
			continue
		}
		if !reflect.DeepEqual(findings, first) {
			t.Fatalf("the findings changed between runs of identical input\nrun 0: %v\nrun %d: %v", first, i, findings)
		}
	}
	if len(first) != 2 {
		t.Fatalf("expected one finding per repeating contributor, got %d: %v", len(first), first)
	}
	for i, want := range []string{
		`key space "export-names", derived more than once by component "beta"`,
		`key space "logical-ids", derived more than once by component "alpha"`,
	} {
		if !strings.Contains(first[i], want) {
			t.Errorf("finding %d does not carry %q\ngot: %s", i, want, first[i])
		}
	}
}

// TestRenderAll_DeclaredNamesDoNotChangeRenderedBytes is the byte-identity proof
// for the surface itself. The same inputs rendered with and without declarations
// attached must produce the same output, because declaring is an assertion about
// what the guest derived and never a change to what it emitted.
func TestRenderAll_DeclaredNamesDoNotChangeRenderedBytes(t *testing.T) {
	t.Parallel()

	instances := []string{"alpha", "beta"}
	silent := &declaringEngine{}
	declaring := &declaringEngine{declare: map[string]map[string][]string{
		"alpha": {"logical-ids": {e2eDerivedLogicalID}, "export-names": {e2eDerivedExportName}},
		"beta":  {"logical-ids": {e2eDerivedLogicalIDNearMiss}},
	}}

	withoutDecl, err := runDeclaringRender(instances, silent)
	if err != nil {
		t.Fatalf("render without declarations: %v", err)
	}
	withDecl, err := runDeclaringRender(instances, declaring)
	if err != nil {
		t.Fatalf("render with declarations: %v", err)
	}

	if len(withoutDecl.Files) != len(withDecl.Files) {
		t.Fatalf("file count changed: %d without declarations, %d with", len(withoutDecl.Files), len(withDecl.Files))
	}
	for path, want := range withoutDecl.Files {
		got, ok := withDecl.Files[path]
		if !ok {
			t.Errorf("output %q disappeared when declarations were attached", path)
			continue
		}
		if string(got.Content) != string(want.Content) {
			t.Errorf("output %q is not byte-identical with declarations attached", path)
		}
	}
}

// TestRenderAll_DerivedNameCollisionDiagnosticIsOrderIndependent pins the
// property end to end. The accumulating walks are not ordered by the input, so a
// diagnostic naming "the first declarer" would differ between runs of identical
// input.
func TestRenderAll_DerivedNameCollisionDiagnosticIsOrderIndependent(t *testing.T) {
	t.Parallel()

	engine := &declaringEngine{declare: map[string]map[string][]string{
		"alpha": {"logical-ids": {e2eDerivedLogicalID}},
		"beta":  {"logical-ids": {e2eDerivedLogicalID}},
		"gamma": {"logical-ids": {e2eDerivedLogicalID}},
	}}
	var first []string
	for i := 0; i < 40; i++ {
		_, err := runDeclaringRender([]string{"alpha", "beta", "gamma"}, engine)
		if err == nil {
			t.Fatalf("iteration %d: expected a halt", i)
		}
		findings := derivedNameFindings(t, err)
		if i == 0 {
			first = findings
			continue
		}
		if !reflect.DeepEqual(findings, first) {
			t.Fatalf("the findings changed between runs of identical input\nrun 0: %v\nrun %d: %v", first, i, findings)
		}
	}
	if len(first) != 3 {
		t.Fatalf("expected one finding per contributor, got %d: %v", len(first), first)
	}
	// Contributor order, which is what makes the report a function of the input
	// rather than of the order the accumulating walks happened to take.
	for i, want := range []string{
		`derived by component "alpha"`,
		`derived by component "beta"`,
		`derived by component "gamma"`,
	} {
		if !strings.Contains(first[i], want) {
			t.Errorf("finding %d does not carry %q\ngot: %s", i, want, first[i])
		}
	}
}

// TestRenderAll_OneGuardCoversEverySpaceAGuestDeclares is what makes a second
// provider need no second guard. Two spaces the host knows nothing about are
// checked by the same code in one build, and both collisions are reported
// together rather than one build at a time.
func TestRenderAll_OneGuardCoversEverySpaceAGuestDeclares(t *testing.T) {
	t.Parallel()

	engine := &declaringEngine{declare: map[string]map[string][]string{
		"alpha": {"logical-ids": {e2eDerivedLogicalID}, "export-names": {e2eDerivedExportName}},
		"beta":  {"logical-ids": {e2eDerivedLogicalID}, "export-names": {e2eDerivedExportName}},
	}}
	_, err := runDeclaringRender([]string{"alpha", "beta"}, engine)
	if err == nil {
		t.Fatal("expected a halt")
	}
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("expected ErrDerivedKeyCollision, got: %v", err)
	}
	findings := derivedNameFindings(t, err)
	// Two spaces, two contributors each: four findings in one build rather than
	// one space reported and the other left for a second build.
	if len(findings) != 4 {
		t.Fatalf("expected one finding per contributor per colliding name, got %d: %v", len(findings), findings)
	}
	whole := strings.Join(findings, "\n")
	for _, want := range []string{
		`"aggrega-prod-api-orders-db-host", key space "export-names"`,
		`"paymentsapiQueue", key space "logical-ids"`,
	} {
		if !strings.Contains(whole, want) {
			t.Errorf("the findings do not carry %q\ngot: %v", want, findings)
		}
	}
}
