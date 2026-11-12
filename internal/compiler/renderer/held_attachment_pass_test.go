// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file drive the whole render — RenderAll, the node walk, the
// attachment pass in each host's turn and the declared pass after the walk — over
// one stub provider standing in for every node's and every attachment's guest, and
// they read what the render produced: the files, what each group handed its
// assembler, and the order the guest was asked to render in.
//
// The stub does on every artifacts crossing what a real guest does on its way
// out: it declares one derived name and reports one render fact — a list naming
// whoever rendered (the node, or the attachment's own composed identity) — so a
// placement that dropped either is visible in what the group holds, and a template
// reading the facts prints who reported them. A node whose id begins "quiet"
// reports none. Where its group assembles, it records what the group handed it, so
// the routing into the group is compared as well as the files.

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
	// crossings is whom each artifacts crossing rendered for, in the order the
	// render asked the guest — which is what tells an attachment rendered in its
	// host's turn from one rendered after every node.
	crossings []string
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
	p.crossings = append(p.crossings, who)
	if nodeCtx.DerivedKeySink != nil {
		declared := types.OrderedMap[[]string]{}
		declared.Set("probe-names", []string{"name-of-" + who})
		nodeCtx.DerivedKeySink(declared)
	}
	if nodeCtx.RenderFactSink != nil && !strings.HasPrefix(nodeCtx.NodeID, "quiet") {
		value, err := json.Marshal([]string{who})
		if err != nil {
			return nil, err
		}
		nodeCtx.RenderFactSink([]RenderFact{{Name: "emitted_by", Value: value}})
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
// FeatureRecipe it binds declares that it reads its callers' render facts
// (declares) and its targets' (declaresTargets).
type heldPassAttachment struct {
	instance        string
	declares        bool
	declaresTargets bool
}

// parityTemplate reads the host's own render facts and the connections made to the
// host — both of which an attachment reads alike in its host's turn and after the
// walk — and nothing the declaration changes, so a declared and an undeclared
// render of it render the same text.
const parityTemplate = "kind: Attached\n" +
	"host: {{ .context.instance_id }}\n" +
	"name: {{ .attachment_name }}\n" +
	"host_facts: {{ toJson .context.render_facts }}\n" +
	"callers: \"{{ range .context.used_by }}{{ .component_id }};{{ end }}\"\n"

// handingTemplate prints the host's own render facts and every connection made to
// the host whole, one line each. It names no render_facts beneath the connections,
// so the rule over an undeclared reading does not refuse it, and a caller's facts
// appear on a line exactly where the render handed them over.
const handingTemplate = "kind: Attached\n" +
	"host: {{ .context.instance_id }}\n" +
	"name: {{ .attachment_name }}\n" +
	"host_facts: {{ toJson .context.render_facts }}\n" +
	"{{- range .context.used_by }}\ncaller: {{ toJson . }}{{ end }}\n"

// connectionsHandingTemplate prints what handingTemplate prints and, after it,
// every connection the host makes whole, one line each. It names no render_facts
// beneath either end, so neither rule over an undeclared reading refuses it, and a
// caller's or a target's facts appear on a line exactly where the render handed
// them over.
const connectionsHandingTemplate = "kind: Attached\n" +
	"host: {{ .context.instance_id }}\n" +
	"name: {{ .attachment_name }}\n" +
	"host_facts: {{ toJson .context.render_facts }}\n" +
	"{{- range .context.used_by }}\ncaller: {{ toJson . }}{{ end }}" +
	"{{- range .context.uses }}\ntarget: {{ toJson . }}{{ end }}\n"

// renderHeldPass renders a graph of the given nodes, all in one boundary and all
// rendered through the stub, with one connection per pair in edges (caller first,
// then the node it connects to).
//
// Every node's Recipe places one artifact naming the node. Every attachment's
// FeatureRecipe places one artifact written from frTemplate.
func renderHeldPass(
	t *testing.T, assembly string, nodes []heldPassNode, edges [][2]string, frTemplate string,
) (*RenderResult, *heldPassProvider) {
	t.Helper()

	graphNodes := types.OrderedMap[types.IRNode]{}
	featureValues := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	order := ir.FeatureOrder{}
	declares := map[string]bool{}
	declaresTargets := map[string]bool{}
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
			declaresTargets[a.instance] = a.declaresTargets
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
			art.Set(instance+".yaml", types.ArtifactEntry{Inline: frTemplate})
			return &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: instance + "-fr", Version: "1.0.0"},
				Spec: types.FeatureRecipeSpec{
					Capability:             "probe",
					Implements:             featureType,
					Artifacts:              art,
					ReadsCallerRenderFacts: declares[instance],
					ReadsTargetRenderFacts: declaresTargets[instance],
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

// attachmentIdentityOf is the composed identity an attachment's render reports as,
// computed by the one composition the render uses — the stub answers no name-bound
// question, so the composition stands as composed.
func attachmentIdentityOf(host, instance string) string {
	return labels.ComposeComponentIdentity(labels.AttachmentScope, labels.Unbounded, host, instance)
}

// crossingIndex returns where in the guest's crossings the given one ran, failing
// the test where it ran other than exactly once — a crossing that never ran, or
// ran twice, leaves every comparison of its position meaningless.
func crossingIndex(t *testing.T, prov *heldPassProvider, who string) int {
	t.Helper()
	first := slices.Index(prov.crossings, who)
	if first < 0 || slices.Index(prov.crossings[first+1:], who) >= 0 {
		t.Fatalf("%q crossed the guest other than exactly once; crossings %v", who, prov.crossings)
	}
	return first
}

// attachedOutput returns the body of the one output file carrying the given
// attachment's artifact, failing the test where there is not exactly one.
func attachedOutput(t *testing.T, result *RenderResult, host, instance string) string {
	t.Helper()
	marker := "kind: Attached\nhost: " + host + "\nname: " + instance + "\n"
	var found []string
	for _, key := range heldPassFileKeys(result.Files) {
		if body := string(result.Files[key].Content); strings.Contains(body, marker) {
			found = append(found, body)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d output file(s) carry attachment %q on %q, want exactly one; files %v",
			len(found), instance, host, heldPassFileKeys(result.Files))
	}
	return found[0]
}

// TestHeldAttachment_IsPlacedExactlyAsInItsHostsTurn is the routing and snapshot
// parity proof. One FeatureRecipe is rendered twice over the same graph — once
// undeclared, so it renders in its host's turn, and once declaring that it reads
// its callers' render facts, so it renders after every node — and its templates
// read nothing the declaration changes. Every output file, everything the group
// hands its assembler, and every derived name the group holds must come out the
// same. The template reads the host's own render facts, which an attachment is
// handed alike at either moment, so the comparison covers them too.
//
// It runs once per kind of group, because each kind places an attachment through a
// different arm: a flat group writes its files into the host's own map, a nesting
// group places them under a position of the attachment's own, and a folding group
// hands its fragments to the assembler. The host's id sorts before its caller's and
// a node follows it in the same group, so a placement that depended on when in the
// walk it ran would show.
//
// Each row first establishes, from the order the guest was asked to render in, that
// the declared render really took the later pass and the undeclared one did not;
// without that the comparison would hold between two identical renders and prove
// nothing.
//
// Every kind of group is run once per declaration that moves an attachment into the
// later pass — the callers' key and the targets' key — because the two share one
// deferral, and a placement that depended on which key held the attachment would
// show as a difference here. The host both is called and makes a connection, and
// the template reads neither side's facts, so either declaration renders the same
// text as no declaration.
func TestHeldAttachment_IsPlacedExactlyAsInItsHostsTurn(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		assembly string
		key      string // the declaration that holds the attachment: "callers" or "targets"
	}{
		{name: "a group writing every contributor at its own position", assembly: "", key: "callers"},
		{name: "a group placing each contributor under a position of its own", assembly: "nest", key: "callers"},
		{name: "a group folding every contributor into one document", assembly: "merge", key: "callers"},
		{name: "a group writing every contributor at its own position, held by the targets' key",
			assembly: "", key: "targets"},
		{name: "a group placing each contributor under a position of its own, held by the targets' key",
			assembly: "nest", key: "targets"},
		{name: "a group folding every contributor into one document, held by the targets' key",
			assembly: "merge", key: "targets"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			graph := func(declares bool) []heldPassNode {
				guard := heldPassAttachment{instance: "guard"}
				switch tc.key {
				case "callers":
					guard.declares = declares
				case "targets":
					guard.declaresTargets = declares
				default:
					t.Fatalf("row names no declaration %q", tc.key)
				}
				return []heldPassNode{
					{id: "api", attachments: []heldPassAttachment{guard}},
					{id: "db"},
					{id: "web"},
					{id: "zz-tail"},
				}
			}
			edges := [][2]string{{"web", "api"}, {"api", "db"}}

			inTurn, inTurnProv := renderHeldPass(t, tc.assembly, graph(false), edges, parityTemplate)
			held, heldProv := renderHeldPass(t, tc.assembly, graph(true), edges, parityTemplate)

			guard := attachmentIdentityOf("api", "guard")
			if crossingIndex(t, inTurnProv, guard) > crossingIndex(t, inTurnProv, "zz-tail") {
				t.Fatalf("the undeclared attachment rendered after the walk; the comparison below would be between two held renders (crossings %v)",
					inTurnProv.crossings)
			}
			if crossingIndex(t, heldProv, guard) < crossingIndex(t, heldProv, "zz-tail") {
				t.Fatalf("the declaring attachment rendered in its host's turn; the comparison below would be between two in-turn renders (crossings %v)",
					heldProv.crossings)
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
			if !reflect.DeepEqual(inTurnProv.assembled, heldProv.assembled) {
				t.Errorf("the groups handed their assemblers different contributors:\n in turn:\n%s\n held:\n%s",
					strings.Join(inTurnProv.assembled, "---\n"), strings.Join(heldProv.assembled, "---\n"))
			}
			for _, summary := range inTurnProv.assembled {
				if strings.Contains(summary, "kind: Attached") {
					attachmentFileSeen = true
				}
			}
			if !attachmentFileSeen {
				t.Fatalf("no output carries the attachment's artifact, so the comparison covers none of its placement; files %v",
					heldPassFileKeys(inTurn.Files))
			}
			if tc.assembly != "" && len(inTurnProv.assembled) == 0 {
				t.Fatal("no group assembled, so the contributor routing was compared over nothing")
			}
			// The identity the group hands its assembler for the attachment. Folded
			// into its host's document it is the host's, the identity its render ran
			// against; placed at a position of its own it is the attachment's own
			// composed identity, so the assembled artifact addresses it apart from
			// its host. The comparison above holds either way, so this is the half
			// that pins which one each kind of group receives.
			if tc.assembly != "" {
				wantBasis := "api"
				if tc.assembly == "nest" {
					wantBasis = guard
				}
				dispatchID := attachmentIdentity{host: "api", instance: "guard"}.dispatchNodeID()
				want := fmt.Sprintf("contributor %q basis=%q ", dispatchID, wantBasis)
				for _, summary := range inTurnProv.assembled {
					if !strings.Contains(summary, want) {
						t.Errorf("the group handed its assembler the attachment under another identity; want %q in\n%s", want, summary)
					}
				}
			}
			wantHostFacts := `host_facts: {"emitted_by":["api"]}`
			hostFactsSeen := false
			for _, key := range heldPassFileKeys(held.Files) {
				if strings.Contains(string(held.Files[key].Content), wantHostFacts) {
					hostFactsSeen = true
				}
			}
			for _, summary := range heldProv.assembled {
				if strings.Contains(summary, strings.ReplaceAll(wantHostFacts, `"`, `\"`)) {
					hostFactsSeen = true
				}
			}
			if !hostFactsSeen {
				t.Errorf("the held attachment's output does not carry its host's own render facts %s", wantHostFacts)
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

// TestHeldAttachment_ReadsItsCallersFactsOnceEveryNodeHasRendered holds what a
// declaring attachment reads, by reading what it rendered.
//
// The first row is the case the walk order alone gets wrong: the host's id sorts
// before its caller's, so in its host's turn the caller has not rendered at all.
// Held for the later pass, the attachment reads, on the caller's connection, the
// caller's own facts and then those of each attachment rendered in the caller's
// turn, joined under the one name they share in the order they rendered.
//
// The second row is the property that keeps the pass free of any order among held
// attachments. The caller carries a held attachment of its own and sorts first, so
// in the later pass that attachment renders — and reports — before the host's does.
// The host's held attachment must still not read it.
//
// The third row holds a caller whose provider reports nothing: its connection
// carries the facts all the same, empty, so a template reads every caller alike.
//
// The fourth row holds what the host's own facts are: the host carries another
// attachment that renders and reports in its turn, and the declaring attachment
// still reads only what the host's own render reported.
//
// The fifth row holds the other side of the declaration: an attachment that does
// not declare it renders in its host's turn, and no caller's connection carries
// facts at all — not even an empty set.
//
// Every row also reads the host's own render facts, which every attachment is
// handed whatever it declares.
func TestHeldAttachment_ReadsItsCallersFactsOnceEveryNodeHasRendered(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name           string
		nodes          []heldPassNode
		edges          [][2]string
		host, instance string
		wantAfter      bool
		// wantHostFacts is the host's own facts as the attachment printed them.
		wantHostFacts string
		// wantCallers is each connection's caller and the render facts its line
		// carries, in the order the connections are held; "absent" where the line
		// must carry no facts at all.
		wantCallers []string
	}{
		{
			name: "a declaring attachment on a host sorting before its caller",
			nodes: []heldPassNode{
				{id: "api", attachments: []heldPassAttachment{{instance: "guard", declares: true}}},
				{id: "web", attachments: []heldPassAttachment{{instance: "tag"}, {instance: "mark"}}},
			},
			edges:         [][2]string{{"web", "api"}},
			host:          "api",
			instance:      "guard",
			wantAfter:     true,
			wantHostFacts: `{"emitted_by":["api"]}`,
			wantCallers: []string{fmt.Sprintf(`web {"emitted_by":["web",%q,%q]}`,
				attachmentIdentityOf("web", "tag"), attachmentIdentityOf("web", "mark"))},
		},
		{
			name: "a held attachment does not read what another held attachment reported",
			nodes: []heldPassNode{
				{id: "alpha", attachments: []heldPassAttachment{{instance: "probe", declares: true}}},
				{id: "beta", attachments: []heldPassAttachment{{instance: "guard", declares: true}}},
			},
			edges:         [][2]string{{"alpha", "beta"}},
			host:          "beta",
			instance:      "guard",
			wantAfter:     true,
			wantHostFacts: `{"emitted_by":["beta"]}`,
			wantCallers:   []string{`alpha {"emitted_by":["alpha"]}`},
		},
		{
			name: "a caller whose provider reports nothing carries empty facts",
			nodes: []heldPassNode{
				{id: "api", attachments: []heldPassAttachment{{instance: "guard", declares: true}}},
				{id: "quiet-worker"},
				{id: "web"},
			},
			edges:         [][2]string{{"quiet-worker", "api"}, {"web", "api"}},
			host:          "api",
			instance:      "guard",
			wantAfter:     true,
			wantHostFacts: `{"emitted_by":["api"]}`,
			wantCallers:   []string{`quiet-worker {}`, `web {"emitted_by":["web"]}`},
		},
		{
			name: "the host's own facts leave out what its other attachments reported",
			nodes: []heldPassNode{
				{id: "api", attachments: []heldPassAttachment{{instance: "tag"}, {instance: "guard", declares: true}}},
				{id: "web"},
			},
			edges:         [][2]string{{"web", "api"}},
			host:          "api",
			instance:      "guard",
			wantAfter:     true,
			wantHostFacts: `{"emitted_by":["api"]}`,
			wantCallers:   []string{`web {"emitted_by":["web"]}`},
		},
		{
			name: "an attachment that does not declare it renders in its host's turn and reads no caller's facts",
			nodes: []heldPassNode{
				{id: "api", attachments: []heldPassAttachment{{instance: "plain"}}},
				{id: "web"},
			},
			edges:         [][2]string{{"web", "api"}},
			host:          "api",
			instance:      "plain",
			wantAfter:     false,
			wantHostFacts: `{"emitted_by":["api"]}`,
			wantCallers:   []string{`web absent`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, prov := renderHeldPass(t, "", tc.nodes, tc.edges, handingTemplate)

			// When it rendered, read from the order the guest was asked: after every
			// node's own render, or before a node later in the walk.
			mine := crossingIndex(t, prov, attachmentIdentityOf(tc.host, tc.instance))
			lastNode := -1
			for _, n := range tc.nodes {
				lastNode = max(lastNode, crossingIndex(t, prov, n.id))
			}
			if after := mine > lastNode; after != tc.wantAfter {
				t.Errorf("rendered after every node = %v, want %v (crossings %v)", after, tc.wantAfter, prov.crossings)
			}

			body := attachedOutput(t, result, tc.host, tc.instance)
			if want := "host_facts: " + tc.wantHostFacts + "\n"; !strings.Contains(body, want) {
				t.Errorf("the attachment read its host's facts wrong; want %q in\n%s", want, body)
			}
			var got []string
			for _, line := range strings.Split(body, "\n") {
				raw, isCaller := strings.CutPrefix(line, "caller: ")
				if !isCaller {
					continue
				}
				var entry map[string]json.RawMessage
				if err := json.Unmarshal([]byte(raw), &entry); err != nil {
					t.Fatalf("a caller line is not the connection's JSON: %q: %v", line, err)
				}
				var caller string
				if err := json.Unmarshal(entry["component_id"], &caller); err != nil {
					t.Fatalf("a caller line names no caller: %q: %v", line, err)
				}
				facts, carried := entry["render_facts"]
				if !carried {
					got = append(got, caller+" absent")
					continue
				}
				got = append(got, caller+" "+string(facts))
			}
			if !reflect.DeepEqual(got, tc.wantCallers) {
				t.Errorf("the attachment read its callers as\n %q\nwant\n %q\nfrom\n%s", got, tc.wantCallers, body)
			}
		})
	}
}

// connectionLines reads the connection lines connectionsHandingTemplate prints
// under prefix — "caller: " or "target: " — as "<component> <facts>", where
// <component> is read from idKey on the line's JSON and <facts> is the render
// facts the line carries, or "absent" where it carries none.
func connectionLines(t *testing.T, body, prefix, idKey string) []string {
	t.Helper()
	var got []string
	for _, line := range strings.Split(body, "\n") {
		raw, isConnection := strings.CutPrefix(line, prefix)
		if !isConnection {
			continue
		}
		var entry map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			t.Fatalf("a connection line is not the connection's JSON: %q: %v", line, err)
		}
		var component string
		if err := json.Unmarshal(entry[idKey], &component); err != nil {
			t.Fatalf("a connection line names no component under %q: %q: %v", idKey, line, err)
		}
		facts, carried := entry["render_facts"]
		if !carried {
			got = append(got, component+" absent")
			continue
		}
		got = append(got, component+" "+string(facts))
	}
	return got
}

// TestHeldAttachment_ReadsItsTargetsFactsOnceEveryNodeHasRendered is the twin of the
// proof above for the components a host is wired to, read the same way: by what
// the attachment rendered.
//
// The first row is the case the walk order alone gets wrong: the target's id sorts
// after its host's, so in its host's turn the target has not rendered at all. Held
// for the later pass by the targets' key, the attachment reads, on the connection,
// the target's own facts and then those of each attachment rendered in the target's
// turn, joined under the one name they share in the order they rendered.
//
// The second row keeps the pass free of any order among held attachments: the
// target carries a held attachment of its own and sorts first, so in the later pass
// that attachment renders — and reports — before the host's does, and the host's
// must still not read it.
//
// The third row holds a target whose provider reports nothing: its connection
// carries the facts all the same, empty, so a template reads every target alike.
//
// The last four rows are the grid of the two declarations over one host that is both
// called and wired to a target, each target and caller sorting after the host: a
// key that is set puts its own side's facts on every entry of that side, a key that
// is not set leaves its side carrying none — even when the other key has moved the
// attachment into the later pass — and setting neither renders the attachment in its
// host's turn.
func TestHeldAttachment_ReadsItsTargetsFactsOnceEveryNodeHasRendered(t *testing.T) {
	t.Parallel()

	gridNodes := func(guard heldPassAttachment) []heldPassNode {
		return []heldPassNode{
			{id: "api", attachments: []heldPassAttachment{guard}},
			{id: "db"},
			{id: "web"},
		}
	}
	gridEdges := [][2]string{{"web", "api"}, {"api", "db"}}

	for _, tc := range []struct {
		name           string
		nodes          []heldPassNode
		edges          [][2]string
		host, instance string
		wantAfter      bool
		wantHostFacts  string
		// wantTargets is each connection's target and the render facts its line
		// carries, in the order the uses map is ranged — by alias; "absent" where the
		// line must carry no facts at all. wantCallers is the same for the callers.
		wantTargets []string
		wantCallers []string
	}{
		{
			name: "the targets' key on a host sorting before its target",
			nodes: []heldPassNode{
				{id: "api", attachments: []heldPassAttachment{{instance: "guard", declaresTargets: true}}},
				{id: "web", attachments: []heldPassAttachment{{instance: "tag"}, {instance: "mark"}}},
			},
			edges:         [][2]string{{"api", "web"}},
			host:          "api",
			instance:      "guard",
			wantAfter:     true,
			wantHostFacts: `{"emitted_by":["api"]}`,
			wantTargets: []string{fmt.Sprintf(`web {"emitted_by":["web",%q,%q]}`,
				attachmentIdentityOf("web", "tag"), attachmentIdentityOf("web", "mark"))},
		},
		{
			name: "a held attachment does not read what its target's held attachment reported",
			nodes: []heldPassNode{
				{id: "alpha", attachments: []heldPassAttachment{{instance: "probe", declaresTargets: true}}},
				{id: "beta", attachments: []heldPassAttachment{{instance: "guard", declaresTargets: true}}},
			},
			edges:         [][2]string{{"beta", "alpha"}},
			host:          "beta",
			instance:      "guard",
			wantAfter:     true,
			wantHostFacts: `{"emitted_by":["beta"]}`,
			wantTargets:   []string{`alpha {"emitted_by":["alpha"]}`},
		},
		{
			name: "a target whose provider reports nothing carries empty facts",
			nodes: []heldPassNode{
				{id: "api", attachments: []heldPassAttachment{{instance: "guard", declaresTargets: true}}},
				{id: "quiet-worker"},
				{id: "web"},
			},
			edges:         [][2]string{{"api", "quiet-worker"}, {"api", "web"}},
			host:          "api",
			instance:      "guard",
			wantAfter:     true,
			wantHostFacts: `{"emitted_by":["api"]}`,
			wantTargets:   []string{`quiet-worker {}`, `web {"emitted_by":["web"]}`},
		},
		{
			name:          "neither key: rendered in its host's turn, no side carries facts",
			nodes:         gridNodes(heldPassAttachment{instance: "guard"}),
			edges:         gridEdges,
			host:          "api",
			instance:      "guard",
			wantAfter:     false,
			wantHostFacts: `{"emitted_by":["api"]}`,
			wantTargets:   []string{`db absent`},
			wantCallers:   []string{`web absent`},
		},
		{
			name:          "the callers' key alone: the callers carry facts, the targets none",
			nodes:         gridNodes(heldPassAttachment{instance: "guard", declares: true}),
			edges:         gridEdges,
			host:          "api",
			instance:      "guard",
			wantAfter:     true,
			wantHostFacts: `{"emitted_by":["api"]}`,
			wantTargets:   []string{`db absent`},
			wantCallers:   []string{`web {"emitted_by":["web"]}`},
		},
		{
			name:          "the targets' key alone: the targets carry facts, the callers none",
			nodes:         gridNodes(heldPassAttachment{instance: "guard", declaresTargets: true}),
			edges:         gridEdges,
			host:          "api",
			instance:      "guard",
			wantAfter:     true,
			wantHostFacts: `{"emitted_by":["api"]}`,
			wantTargets:   []string{`db {"emitted_by":["db"]}`},
			wantCallers:   []string{`web absent`},
		},
		{
			name:          "both keys: both sides carry facts",
			nodes:         gridNodes(heldPassAttachment{instance: "guard", declares: true, declaresTargets: true}),
			edges:         gridEdges,
			host:          "api",
			instance:      "guard",
			wantAfter:     true,
			wantHostFacts: `{"emitted_by":["api"]}`,
			wantTargets:   []string{`db {"emitted_by":["db"]}`},
			wantCallers:   []string{`web {"emitted_by":["web"]}`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, prov := renderHeldPass(t, "", tc.nodes, tc.edges, connectionsHandingTemplate)

			mine := crossingIndex(t, prov, attachmentIdentityOf(tc.host, tc.instance))
			lastNode := -1
			for _, n := range tc.nodes {
				lastNode = max(lastNode, crossingIndex(t, prov, n.id))
			}
			if after := mine > lastNode; after != tc.wantAfter {
				t.Errorf("rendered after every node = %v, want %v (crossings %v)", after, tc.wantAfter, prov.crossings)
			}

			body := attachedOutput(t, result, tc.host, tc.instance)
			if want := "host_facts: " + tc.wantHostFacts + "\n"; !strings.Contains(body, want) {
				t.Errorf("the attachment read its host's facts wrong; want %q in\n%s", want, body)
			}
			if got := connectionLines(t, body, "target: ", "target"); !reflect.DeepEqual(got, tc.wantTargets) {
				t.Errorf("the attachment read its targets as\n %q\nwant\n %q\nfrom\n%s", got, tc.wantTargets, body)
			}
			if got := connectionLines(t, body, "caller: ", "component_id"); !reflect.DeepEqual(got, tc.wantCallers) {
				t.Errorf("the attachment read its callers as\n %q\nwant\n %q\nfrom\n%s", got, tc.wantCallers, body)
			}
		})
	}
}
