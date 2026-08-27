// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// namingAssemblerEngine is a synthetic provider that folds its dispatch group's
// per-node output into one artifact and names that artifact from a boundary
// annotation, requiring every boundary feeding the group to resolve the annotation
// to one value.
//
// It genuinely assembles — the merged document is produced below — so a group it
// serves reaches the naming rule rather than being stopped earlier by the rule
// about a provider whose plugin offers no assembly.
type namingAssemblerEngine struct {
	annotation string
	mustAgree  bool
}

func (namingAssemblerEngine) Render(_ context.Context, _ map[string]any, nodeCtx renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
	return []renderer.RenderedFile{{
		Path:    nodeCtx.NodeID + ".yaml",
		Content: []byte("instance: " + nodeCtx.NodeID + "\n"),
	}}, nil
}

func (namingAssemblerEngine) Contract() renderer.ProviderContract {
	return renderer.ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

func (namingAssemblerEngine) BoundaryAssembly() string { return "nest" }

func (e namingAssemblerEngine) AssemblyNameAnnotation() (string, bool) {
	return e.annotation, e.mustAgree
}

func (namingAssemblerEngine) AssembleBoundary(_ context.Context, info renderer.BoundaryInfo) (map[string]renderer.AssembledFile, renderer.BoundaryNesting, error) {
	var b strings.Builder
	b.WriteString("assembly: " + info.ChartName + "\n")
	for _, bp := range info.ContributingBoundaries {
		b.WriteString("- " + bp + "\n")
	}
	return map[string]renderer.AssembledFile{
		"assembly.yaml": {Content: []byte(b.String()), Format: "yaml"},
	}, renderer.BoundaryNesting{PerNode: true}, nil
}

// namingGraphFacts stands in for the presentation the graph finalisation produces,
// which the render embeds beneath the presentations built over it.
//
// The rule under test reads two facts and neither is a graph-stage one — both are
// answered from what the grouping recorded about each group's elected engine — so
// this is never read. It carries no value behind the interface deliberately: a read
// panics here rather than answering a zero value, so a rule that later starts
// reading a graph-stage fact fails loudly instead of being quietly handed nothing.
//
// It is supplied because the presentation chain is cumulative and refuses to
// assemble with a hole in it: the render publishes no mid-render presentation
// unless the one it extends exists. Production always supplies this, from the one
// call path that renders; a fixture omitting it would leave the rule invoked zero
// times for a reason that has nothing to do with what is being proved — and a rule
// invoked zero times reports exactly what a rule that ran and approved reports.
type namingGraphFacts struct{ constraint.GraphView }

// namingCase is one render over a group several boundaries feed: what each of them
// resolved the assembly-name annotation to, and whether the provider requires them
// to agree.
type namingCase struct {
	name       string
	annotation string
	mustAgree  bool
	// names maps boundary path to the value that boundary resolved the annotation
	// to. A boundary present with the empty string resolved it to nothing, which is
	// a different input from a boundary absent from the cascade altogether.
	names map[string]string
	// absent lists boundaries the cascade holds no annotations for at all.
	absent []string
}

// runNamingCase renders one case, putting every boundary on one evaluated path
// under one provider so they form a single group.
//
// One provider throughout, so the collision rule at the same stage stays silent and
// a halt here is attributable to the naming rule alone.
func runNamingCase(t *testing.T, c namingCase) error {
	t.Helper()

	boundaries := make([]string, 0, len(c.names)+len(c.absent))
	for _, bp := range append(orderedNamingBoundaries(c), c.absent...) {
		boundaries = append(boundaries, bp)
	}

	nodes := types.OrderedMap[types.IRNode]{}
	infra := map[string]*resolver.BoundaryInfra{}
	annotations := map[string]types.OrderedMap[string]{}
	for i, bp := range boundaries {
		node := "svc" + string(rune('a'+i))
		nodes.Set(node, types.IRNode{Component: "core/api", BoundaryPath: bp})
		infra[node] = &resolver.BoundaryInfra{BoundaryPath: bp, Directory: "dist/shared"}
		if value, held := c.names[bp]; held {
			var resolved types.OrderedMap[string]
			resolved.Set(c.annotation, value)
			annotations[bp] = resolved
		}
	}

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "naming-app"},
			Spec: types.IRSpec{
				Nodes:      nodes,
				Provenance: types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder: ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:           &types.Aggregate{},
			BoundaryInfra:       infra,
			BoundaryAnnotations: annotations,
		},
		GraphView: namingGraphFacts{},
	}
	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "naming-recipe", Version: "1.0.0"},
				Spec:     types.RecipeSpec{Capability: "namingprov", Source: "./chart"},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}
	engine := namingAssemblerEngine{annotation: c.annotation, mustAgree: c.mustAgree}
	factory := func(string) (renderer.Provider, error) { return engine, nil }

	_, err := renderer.RenderAll(context.Background(), input, lookup, factory)
	return err
}

// orderedNamingBoundaries returns the case's named boundaries in a fixed order, so
// the group hears from them identically on every run and the objection sequence a
// halt carries is the same each time.
func orderedNamingBoundaries(c namingCase) []string {
	order := []string{"billing", "payments", "ledger"}
	out := make([]string, 0, len(c.names))
	for _, bp := range order {
		if _, held := c.names[bp]; held {
			out = append(out, bp)
		}
	}
	if len(out) != len(c.names) {
		panic("naming case names a boundary outside the fixed order")
	}
	return out
}

// TestRenderAll_MergedGroupWithDisagreeingAssemblyNames_Halts is the refusal row.
// Two boundaries merge into one group under a provider that names the assembly
// from an annotation and requires one value, and they resolved it to two — so the
// build halts rather than publishing one name and discarding the other.
func TestRenderAll_MergedGroupWithDisagreeingAssemblyNames_Halts(t *testing.T) {
	t.Parallel()

	err := runNamingCase(t, namingCase{
		name:       "two boundaries disagree",
		annotation: "aggrega.ai/assembly-name",
		mustAgree:  true,
		names:      map[string]string{"billing": "alpha", "payments": "beta"},
	})
	if err == nil {
		t.Fatal("RenderAll succeeded; expected a loud halt for a merged group whose boundaries disagree on its name")
	}
	if !errors.Is(err, renderer.ErrChartNameMismatch()) {
		t.Fatalf("RenderAll error = %v; want ErrChartNameMismatch", err)
	}
}

// TestRenderAll_MergedGroupNamingStaysSilentWhereItMust is the row a corpus of
// refusals cannot see: every input the rule must let through.
//
// Without it a predicate that refused every merged group — or every group at all —
// would satisfy the refusal row above while breaking every build that merges
// boundaries legitimately.
func TestRenderAll_MergedGroupNamingStaysSilentWhereItMust(t *testing.T) {
	t.Parallel()

	for _, c := range []namingCase{
		{
			name:       "boundaries agree on the name",
			annotation: "aggrega.ai/assembly-name",
			mustAgree:  true,
			names:      map[string]string{"billing": "shared", "payments": "shared"},
		},
		{
			name:       "three boundaries agree on the name",
			annotation: "aggrega.ai/assembly-name",
			mustAgree:  true,
			names:      map[string]string{"billing": "shared", "payments": "shared", "ledger": "shared"},
		},
		{
			// The provider names its assembly from the annotation and is
			// indifferent to disagreement, so a disagreement is not a fault. A rule
			// reading the resolved names alone would refuse this.
			name:       "provider requires no agreement",
			annotation: "aggrega.ai/assembly-name",
			mustAgree:  false,
			names:      map[string]string{"billing": "alpha", "payments": "beta"},
		},
		{
			// One boundary has nothing to disagree with.
			name:       "single boundary",
			annotation: "aggrega.ai/assembly-name",
			mustAgree:  true,
			names:      map[string]string{"billing": "alpha"},
		},
		{
			// Every boundary resolved the annotation to nothing, so they agree: the
			// assembly is unnamed from every side and the provider falls back to
			// naming it from the group's path.
			name:       "every boundary resolved it to nothing",
			annotation: "aggrega.ai/assembly-name",
			mustAgree:  true,
			names:      map[string]string{"billing": "", "payments": ""},
		},
		{
			// Nothing declared the annotation anywhere in either boundary's
			// ancestry, so the cascade holds no entry for either. That is the
			// ordinary shape of a deployment that never names its assemblies.
			name:       "annotation declared nowhere",
			annotation: "aggrega.ai/assembly-name",
			mustAgree:  true,
			absent:     []string{"billing", "payments"},
		},
	} {
		if err := runNamingCase(t, c); err != nil {
			t.Errorf("case %q: RenderAll halted; the rule must stay silent here: %v", c.name, err)
		}
	}
}

// TestMergedGroupNamingRuleIsReachedAtAll is this file's own anti-needle, and it
// reads the halt structurally rather than as a string.
//
// Every silent row above passes on a rule invoked zero times exactly as it passes
// on a rule that ran and approved, so silence proves nothing on its own. This
// establishes the rule is reached in this harness by making it refuse and then
// reading what the refusal carries: one finding per boundary feeding the group,
// each naming that boundary's own resolved value alongside the annotation, the
// provider and the path.
//
// The envelope's own text is its message alone — a string search over the halt
// reports nothing whichever shape the chain has — so the findings are reached
// through the joined set the envelope wraps.
func TestMergedGroupNamingRuleIsReachedAtAll(t *testing.T) {
	t.Parallel()

	err := runNamingCase(t, namingCase{
		name:       "two boundaries disagree",
		annotation: "aggrega.ai/assembly-name",
		mustAgree:  true,
		names:      map[string]string{"billing": "alpha", "payments": "beta"},
	})
	if err == nil {
		t.Fatal("the disagreement row did not halt, so every silent row above proves nothing")
	}

	var envelope *cerrors.CompilerError
	if !errors.As(err, &envelope) {
		t.Fatalf("the halt is not a categorised diagnostic: %v", err)
	}
	joined, isMulti := envelope.Wrap.(interface{ Unwrap() []error })
	if !isMulti {
		t.Fatalf("the envelope wraps a single error rather than the set the rule raised: %T", envelope.Wrap)
	}
	members := joined.Unwrap()
	if got, want := len(members), 2; got != want {
		t.Fatalf("the halt carries %d finding(s); want %d — one per boundary feeding the group", got, want)
	}

	texts := make([]string, 0, len(members))
	for _, member := range members {
		texts = append(texts, member.Error())
	}
	whole := strings.Join(texts, "\n")
	for _, want := range []string{
		"aggrega.ai/assembly-name",
		"billing",
		"payments",
		"alpha",
		"beta",
		"namingprov",
		"dist/shared",
	} {
		if !strings.Contains(whole, want) {
			t.Errorf("the findings do not name %q; the rule's own detail is what carries it\nfindings:\n%s", want, whole)
		}
	}

	// Each finding names its own boundary and its own value, so a reader is sent to
	// one editable site rather than handed the group's whole disagreement twice.
	for _, pair := range [][2]string{{"billing", "alpha"}, {"payments", "beta"}} {
		found := false
		for _, text := range texts {
			if strings.Contains(text, pair[0]) && strings.Contains(text, pair[1]) {
				found = true
			}
		}
		if !found {
			t.Errorf("no finding names boundary %q beside the value it resolved, %q\nfindings:\n%s",
				pair[0], pair[1], whole)
		}
	}
}
