// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file drive the whole render — RenderAll, the node walk, the
// attachment pass in each host's turn and the declared pass after the walk — over
// one stub provider standing in for every node's and every attachment's guest.
//
// The stub does on every artifacts crossing what a real guest does on its way
// out: it declares one derived name and reports one render fact, each naming
// whoever rendered (the node, or the attachment's own composed identity), so a
// placement that dropped either is visible in what the group holds. Where its
// group assembles, it records what the group handed it, so the routing into the
// group is compared as well as the files.

// heldPassProvider stands in for a provider guest on every crossing these proofs
// reach.
type heldPassProvider struct {
	stubEngine
	// assembly is the boundary-assembly claim: "" writes each contributor's files
	// at the group's position, "nest" places each under a position of its own, and
	// "merge" folds every contributor's fragments into one document.
	assembly string
	// assembled is what each group's assembly was handed, summarised.
	assembled []string
}

func (p *heldPassProvider) BoundaryAssembly() string { return p.assembly }

func (p *heldPassProvider) ArtifactTemplateDelimiters() (string, string) { return "{{", "}}" }

func (p *heldPassProvider) EmitArtifacts(
	_ context.Context, proj ArtifactsProjection, nodeCtx NodeRenderContext,
) ([]EmittedFile, error) {
	who := nodeCtx.LabelComponentID
	if who == "" {
		who = nodeCtx.NodeID
	}
	if nodeCtx.DerivedKeySink != nil {
		declared := types.OrderedMap[[]string]{}
		declared.Set("probe-names", []string{"name-of-" + who})
		nodeCtx.DerivedKeySink(declared)
	}
	if nodeCtx.RenderFactSink != nil {
		nodeCtx.RenderFactSink([]RenderFact{{Name: "emitted_by", Value: json.RawMessage(strconv.Quote(who))}})
	}
	intermediate := RenderIsIntermediate(p)
	files := make([]EmittedFile, 0, len(proj.Entries))
	for _, ent := range proj.Entries {
		files = append(files, EmittedFile{
			Name: ent.Name, Origin: EmitOriginArtifactsInline, Body: ent.Content, Intermediate: intermediate,
		})
	}
	return files, nil
}

func (p *heldPassProvider) AssembleBoundary(_ context.Context, info BoundaryInfo) (map[string]AssembledFile, error) {
	summary := summarizeAssembly(info)
	p.assembled = append(p.assembled, summary)
	return map[string]AssembledFile{"assembled.txt": {Content: []byte(summary)}}, nil
}

// summarizeAssembly writes down everything a group handed its assembler that the
// routing of a contributor decides, keyed by contributor in sorted order.
//
// The list of children a group names is sorted before it is written, and that is
// the one place this comparison is deliberately order-blind: an attachment held for
// the pass after the walk joins its group after every node of the walk, so it comes
// later in that list. The one assembler reading the list orders it by identity
// itself; what it holds must match exactly.
func summarizeAssembly(info BoundaryInfo) string {
	ids := map[string]bool{}
	for id := range info.IntermediatesByNode {
		ids[id] = true
	}
	for id := range info.PlacedByNode {
		ids[id] = true
	}
	for id := range info.NodeOutputDirectory {
		ids[id] = true
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)

	var b strings.Builder
	for _, id := range sorted {
		fmt.Fprintf(&b, "contributor %q basis=%q dir=%q wave=%d annotations=%v\n",
			id, info.NodeNameBasis[id], info.NodeOutputDirectory[id], info.NodeSyncWave[id], info.NodeAnnotations[id])
		for _, f := range info.IntermediatesByNode[id] {
			fmt.Fprintf(&b, "  fragment %s %q\n", f.Name, f.Body)
		}
		for _, f := range info.PlacedByNode[id] {
			fmt.Fprintf(&b, "  placed %s %q\n", f.Name, f.Body)
		}
	}
	children := make([]string, 0, len(info.SubCharts))
	for _, s := range info.SubCharts {
		children = append(children, fmt.Sprintf("%s values=%v", s.InstanceID, s.Values))
	}
	sort.Strings(children)
	for _, c := range children {
		fmt.Fprintf(&b, "child %s\n", c)
	}
	return b.String()
}

// heldPassNode is one component of a fixture: its id and the attachments it
// carries, in the order they cascade.
type heldPassNode struct {
	id          string
	attachments []heldPassAttachment
}

// heldPassAttachment is one attachment of a fixture node, and whether the
// FeatureRecipe it binds declares that it reads its callers' render facts.
type heldPassAttachment struct {
	instance string
	declares bool
}

// renderHeldPass renders a graph of the given nodes, all in one boundary and all
// rendered through the stub, with one connection per pair in edges (caller first,
// then the node it connects to).
//
// Every node's Recipe places one artifact naming the node. Every attachment's
// FeatureRecipe places one artifact naming its host, its own name and the
// components connecting to its host — a reading of the connections that exists
// whether or not the FeatureRecipe declares anything, so a declared and an
// undeclared render of the same FeatureRecipe render the same text.
func renderHeldPass(
	t *testing.T, assembly string, nodes []heldPassNode, edges [][2]string,
) (*RenderResult, *heldPassProvider) {
	t.Helper()

	graphNodes := types.OrderedMap[types.IRNode]{}
	featureValues := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	order := ir.FeatureOrder{}
	declares := map[string]bool{}
	for _, n := range nodes {
		graphNodes.Set(n.id, types.IRNode{
			Component: "core/probe/" + n.id, ResolvedVersion: "1.0.0", BoundaryPath: "local",
		})
		if len(n.attachments) == 0 {
			continue
		}
		entries := types.OrderedMap[types.IRFeatureEntry]{}
		for _, a := range n.attachments {
			entries.Set(a.instance, types.IRFeatureEntry{Type: "core/feature/" + a.instance})
			order[n.id] = append(order[n.id], a.instance)
			declares[a.instance] = a.declares
		}
		featureValues.Set(n.id, entries)
	}
	var graphEdges []types.IREdge
	for _, e := range edges {
		graphEdges = append(graphEdges, types.IREdge{Source: e[0], Target: e[1], Socket: "apis", Alias: "to-" + e[1]})
	}

	input := RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "held-pass"},
			Spec: types.IRSpec{
				Nodes:                graphNodes,
				Edges:                graphEdges,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				InfraMapping:         types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: featureValues,
				Provenance:           types.IRProvenance{CompilerVersion: "0.1.0", AggregateVersion: "1.0.0"},
			},
		},
		FeatureOrder: order,
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "held-pass"}},
			Release: &types.Release{
				Metadata: types.ReleaseMetadata{Name: "held-pass-release", Version: "1.0.0", Environment: "local"},
			},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
	}

	lookup := RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			id, ok := strings.CutPrefix(componentType, "core/probe/")
			if !ok {
				return nil, false
			}
			art := types.OrderedMap[types.ArtifactEntry]{}
			art.Set(id+".yaml", types.ArtifactEntry{Inline: "kind: Probe\nname: {{ .context.instance_id }}\n"})
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: id + "-recipe", Version: "1.0.0"},
				Spec:     types.RecipeSpec{Capability: "probe", Implements: componentType, Artifacts: art},
			}, true
		},
		FeatureRecipe: func(instance, featureType string) (*types.FeatureRecipe, bool) {
			if featureType != "core/feature/"+instance {
				return nil, false
			}
			art := types.OrderedMap[types.ArtifactEntry]{}
			art.Set(instance+".yaml", types.ArtifactEntry{Inline: "kind: Attached\n" +
				"host: {{ .context.instance_id }}\n" +
				"name: {{ .attachment_name }}\n" +
				"callers: \"{{ range .context.used_by }}{{ .component_id }};{{ end }}\"\n"})
			return &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: instance + "-fr", Version: "1.0.0"},
				Spec: types.FeatureRecipeSpec{
					Capability:             "probe",
					Implements:             featureType,
					Artifacts:              art,
					ReadsCallerRenderFacts: declares[instance],
				},
			}, true
		},
	}

	prov := &heldPassProvider{assembly: assembly}
	factory := func(string) (Provider, error) { return prov, nil }
	result, err := RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return result, prov
}

// heldPassFileKeys returns a render's output paths, sorted, so two renders' paths
// compare as lists.
func heldPassFileKeys(files map[string]AssembledFile) []string {
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// heldPassRecord returns what the render recorded for one attachment's render,
// failing the test where it recorded none — an attachment with no record never
// rendered, and every assertion over it would then compare nothing.
func heldPassRecord(t *testing.T, result *RenderResult, host, instance string) attachmentRenderRecord {
	t.Helper()
	if result.attachmentRenders == nil {
		t.Fatal("the render published no record of its attachment renders")
	}
	var found []attachmentRenderRecord
	for _, r := range result.attachmentRenders.records {
		if r.Host == host && r.Instance == instance {
			found = append(found, r)
		}
	}
	if len(found) != 1 {
		t.Fatalf("attachment %q on %q rendered %d time(s), want exactly once; the render recorded %+v",
			instance, host, len(found), result.attachmentRenders.records)
	}
	return found[0]
}

// TestHeldAttachment_IsPlacedExactlyAsInItsHostsTurn is the routing and snapshot
// parity proof. One FeatureRecipe is rendered twice over the same graph — once
// undeclared, so it renders in its host's turn, and once declaring that it reads
// its callers' render facts, so it renders after every node — and its templates
// read nothing the declaration changes. Every output file, everything the group
// hands its assembler, and every derived name the group holds must come out the
// same.
//
// It runs once per kind of group, because each kind places an attachment through a
// different arm: a flat group writes its files into the host's own map, a nesting
// group places them under a position of the attachment's own, and a folding group
// hands its fragments to the assembler. The host's id sorts before its caller's and
// a node follows it in the same group, so a placement that depended on when in the
// walk it ran would show.
//
// Each row first establishes that the declared render really took the later pass
// and the undeclared one did not; without that the comparison would hold between
// two identical renders and prove nothing.
func TestHeldAttachment_IsPlacedExactlyAsInItsHostsTurn(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		assembly string
	}{
		{name: "a group writing every contributor at its own position", assembly: ""},
		{name: "a group placing each contributor under a position of its own", assembly: "nest"},
		{name: "a group folding every contributor into one document", assembly: "merge"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			graph := func(declares bool) []heldPassNode {
				return []heldPassNode{
					{id: "api", attachments: []heldPassAttachment{{instance: "guard", declares: declares}}},
					{id: "web"},
					{id: "zz-tail"},
				}
			}
			edges := [][2]string{{"web", "api"}}

			inTurn, inTurnProv := renderHeldPass(t, tc.assembly, graph(false), edges)
			held, heldProv := renderHeldPass(t, tc.assembly, graph(true), edges)

			if r := heldPassRecord(t, inTurn, "api", "guard"); r.AfterTheNodes {
				t.Fatal("the undeclared attachment rendered after the walk; the comparison below would be between two held renders")
			}
			if r := heldPassRecord(t, held, "api", "guard"); !r.AfterTheNodes {
				t.Fatal("the declaring attachment rendered in its host's turn; the comparison below would be between two in-turn renders")
			}

			if !reflect.DeepEqual(heldPassFileKeys(inTurn.Files), heldPassFileKeys(held.Files)) {
				t.Fatalf("the output paths differ:\n in turn: %v\n held:    %v", heldPassFileKeys(inTurn.Files), heldPassFileKeys(held.Files))
			}
			attachmentFileSeen := false
			for _, key := range heldPassFileKeys(inTurn.Files) {
				if string(inTurn.Files[key].Content) != string(held.Files[key].Content) {
					t.Errorf("%s differs:\n in turn:\n%s\n held:\n%s", key, inTurn.Files[key].Content, held.Files[key].Content)
				}
				if strings.Contains(string(inTurn.Files[key].Content), "kind: Attached") {
					attachmentFileSeen = true
				}
			}
			if !attachmentFileSeen {
				t.Fatalf("no output carries the attachment's artifact, so the comparison covers none of its placement; files %v",
					heldPassFileKeys(inTurn.Files))
			}

			if !reflect.DeepEqual(inTurnProv.assembled, heldProv.assembled) {
				t.Errorf("the groups handed their assemblers different contributors:\n in turn:\n%s\n held:\n%s",
					strings.Join(inTurnProv.assembled, "---\n"), strings.Join(heldProv.assembled, "---\n"))
			}
			if tc.assembly != "" && len(inTurnProv.assembled) == 0 {
				t.Fatal("no group assembled, so the contributor routing was compared over nothing")
			}

			if !reflect.DeepEqual(inTurn.Contributions, held.Contributions) {
				t.Fatalf("the groups differ: in turn %+v, held %+v", inTurn.Contributions, held.Contributions)
			}
			attachmentDeclared := false
			for _, c := range inTurn.Contributions {
				a := inTurn.groupDerivedNames(c.AssemblyPath, c.Provider)
				b := held.groupDerivedNames(c.AssemblyPath, c.Provider)
				if !reflect.DeepEqual(a, b) {
					t.Errorf("group %s/%s holds different derived names:\n in turn: %+v\n held:    %+v",
						c.AssemblyPath, c.Provider, a, b)
				}
				for _, d := range b {
					if d.ContributorScope == "guard" {
						attachmentDeclared = true
					}
				}
			}
			if !attachmentDeclared {
				t.Error("no group holds a name the held attachment declared, so its declarations never reached its group")
			}
		})
	}
}

// TestHeldAttachment_IsHandedItsCallersFactsAfterEveryNodeHasRendered holds what a
// declaring attachment is handed and when it renders.
//
// The first row is the case the walk order alone gets wrong: the host's id sorts
// before its caller's, so in its host's turn the caller has not rendered at all.
// Held for the later pass, it is handed the caller's own facts and the facts of the
// attachment rendered in the caller's turn, kept apart.
//
// The second row is the property that keeps the pass free of any order among held
// attachments. The caller carries a held attachment of its own and sorts first, so
// in the later pass that attachment renders — and reports — before the host's does.
// The host's held attachment must still not be handed it.
//
// The third row holds the other side of the declaration: an attachment that does
// not declare it renders in its host's turn and is handed nothing.
func TestHeldAttachment_IsHandedItsCallersFactsAfterEveryNodeHasRendered(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name           string
		nodes          []heldPassNode
		edges          [][2]string
		host, instance string
		wantAfter      bool
		// wantCallers is each handed connection's caller and the instance names of
		// its attachments whose facts it carries; nil when nothing is handed.
		wantCallers []string
	}{
		{
			name: "a declaring attachment on a host sorting before its caller",
			nodes: []heldPassNode{
				{id: "api", attachments: []heldPassAttachment{{instance: "guard", declares: true}}},
				{id: "web", attachments: []heldPassAttachment{{instance: "tag"}}},
			},
			edges:       [][2]string{{"web", "api"}},
			host:        "api",
			instance:    "guard",
			wantAfter:   true,
			wantCallers: []string{"web own=\"web\" attachments=[tag]"},
		},
		{
			name: "a held attachment is not handed what another held attachment reported",
			nodes: []heldPassNode{
				{id: "alpha", attachments: []heldPassAttachment{{instance: "probe", declares: true}}},
				{id: "beta", attachments: []heldPassAttachment{{instance: "guard", declares: true}}},
			},
			edges:       [][2]string{{"alpha", "beta"}},
			host:        "beta",
			instance:    "guard",
			wantAfter:   true,
			wantCallers: []string{"alpha own=\"alpha\" attachments=[]"},
		},
		{
			name: "an attachment that does not declare it renders in its host's turn and is handed nothing",
			nodes: []heldPassNode{
				{id: "api", attachments: []heldPassAttachment{{instance: "plain"}}},
				{id: "web"},
			},
			edges:     [][2]string{{"web", "api"}},
			host:      "api",
			instance:  "plain",
			wantAfter: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, _ := renderHeldPass(t, "", tc.nodes, tc.edges)
			rec := heldPassRecord(t, result, tc.host, tc.instance)

			if rec.AfterTheNodes != tc.wantAfter {
				t.Errorf("rendered after every node = %v, want %v", rec.AfterTheNodes, tc.wantAfter)
			}
			var got []string
			for _, c := range rec.CallerFacts {
				own := make([]string, 0, len(c.Own))
				for _, f := range c.Own {
					own = append(own, string(f.Value))
				}
				got = append(got, fmt.Sprintf("%s own=%s attachments=%v",
					c.ComponentID, strings.Join(own, ","), c.Attachments.Keys()))
			}
			if !reflect.DeepEqual(got, tc.wantCallers) {
				t.Errorf("handed %q, want %q", got, tc.wantCallers)
			}
			// What an attachment of the caller reported is carried under that
			// attachment, never as the caller's own.
			for _, c := range rec.CallerFacts {
				for _, f := range c.Own {
					if string(f.Value) != strconv.Quote(c.ComponentID) {
						t.Errorf("caller %q's own facts carry %s, which its own render did not report", c.ComponentID, f.Value)
					}
				}
			}
		})
	}
}
