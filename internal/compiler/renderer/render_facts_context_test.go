// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// factsOf builds one report of render facts from name/value pairs.
func factsOf(pairs ...string) []RenderFact {
	out := make([]RenderFact, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, RenderFact{Name: pairs[i], Value: json.RawMessage(pairs[i+1])})
	}
	return out
}

// contextFactsJSON writes facts keyed for a context back out as JSON, keys and
// entries in the order the context holds them, so a row states what a template is
// handed as one string.
func contextFactsJSON(t *testing.T, facts *types.OrderedMap[[]any]) string {
	t.Helper()
	if facts == nil {
		return "nil"
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		t.Fatalf("encode the context's facts: %v", err)
	}
	return string(raw)
}

// TestRenderFactsOnContext_KeysByNameAndJoinsInCallOrder holds what a template is
// handed of the reported facts: keyed by name in the order the names were first
// reported, each name's lists joined into one in the order the reports ran, and
// every entry decoded with the order of its keys kept as the guest wrote it.
//
// The rows are the shapes a template meets: nothing reported (an empty set, never
// nil, so a template reading a name nobody reported reads nothing rather than
// failing); one report; one name reported by more than one call of a node's own
// render; and a caller's own render followed by its attachments, which is the order
// the callers' facts are joined in. A value that is not a list is refused.
func TestRenderFactsOnContext_KeysByNameAndJoinsInCallOrder(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		reports [][]RenderFact
		want    string
		wantErr string
	}{
		{name: "nothing reported", reports: nil, want: `{}`},
		{name: "one empty report", reports: [][]RenderFact{nil}, want: `{}`},
		{
			name:    "one report, names in the guest's order",
			reports: [][]RenderFact{factsOf("zeta", `["z"]`, "alpha", `["a1","a2"]`)},
			want:    `{"zeta":["z"],"alpha":["a1","a2"]}`,
		},
		{
			name: "one name reported by two calls of a node's own render",
			reports: [][]RenderFact{factsOf(
				"pods", `[{"kind":"Deployment","name":"api"}]`,
				"ports", `[80]`,
				"pods", `[{"kind":"Job","name":"migrate"}]`,
			)},
			want: `{"pods":[{"kind":"Deployment","name":"api"},{"kind":"Job","name":"migrate"}],"ports":[80]}`,
		},
		{
			name: "a caller's own render, then its attachments in the order they rendered",
			reports: [][]RenderFact{
				factsOf("pods", `["own-1"]`),
				factsOf("pods", `["first-attachment"]`, "extra", `[]`),
				factsOf("pods", `["second-attachment"]`),
			},
			want: `{"pods":["own-1","first-attachment","second-attachment"],"extra":[]}`,
		},
		{
			name:    "the keys of an entry stay in the guest's order",
			reports: [][]RenderFact{factsOf("labels", `[{"tier":"web","app":"api","zone":{"z":1,"a":2}}]`)},
			want:    `{"labels":[{"tier":"web","app":"api","zone":{"z":1,"a":2}}]}`,
		},
		{
			name:    "a number keeps the text the guest wrote",
			reports: [][]RenderFact{factsOf("sizes", `[12345678901234567890, 1.50]`)},
			want:    `{"sizes":[12345678901234567890,1.50]}`,
		},
		{
			name:    "a value that is not a list",
			reports: [][]RenderFact{factsOf("pods", `["ok"]`), factsOf("labels", `{"app":"api"}`)},
			wantErr: `render fact "labels": its value is not a list`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := renderFactsOnContext(tc.reports...)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want one saying %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("renderFactsOnContext: %v", err)
			}
			if got == nil {
				t.Fatal("the facts are nil; a context that promises them carries an empty set instead")
			}
			if js := contextFactsJSON(t, got); js != tc.want {
				t.Errorf("handed %s, want %s", js, tc.want)
			}
		})
	}
}

// TestRenderFactsOnContext_AnEntryIsAnOrderedValueNotAMap pins the shape behind the
// order: an object inside a list is held as an ordered map, never as a Go map whose
// iteration order is random, so anything that writes the context out — and the
// template boundary, which is the one place it becomes a plain map — starts from the
// guest's order.
func TestRenderFactsOnContext_AnEntryIsAnOrderedValueNotAMap(t *testing.T) {
	t.Parallel()
	facts, err := renderFactsOnContext(factsOf("labels", `[{"tier":"web","app":"api"}]`))
	if err != nil {
		t.Fatalf("renderFactsOnContext: %v", err)
	}
	entries, _ := facts.Get("labels")
	if len(entries) != 1 {
		t.Fatalf("labels holds %d entries, want 1", len(entries))
	}
	entry, ordered := entries[0].(*types.OrderedMap[any])
	if !ordered {
		t.Fatalf("an entry is a %T, want an ordered map", entries[0])
	}
	if keys := entry.Keys(); !reflect.DeepEqual(keys, []string{"tier", "app"}) {
		t.Errorf("the entry's keys are %v, want the guest's order [tier app]", keys)
	}
}

// TestUsedByWithCallerFacts_PutsEachCallersFactsOnItsOwnEntryOfACopy holds the
// handing itself: each connection's entry carries its own caller's facts, joined
// own-then-attachments; a caller reporting nothing carries an empty set; and the
// list handed over is a copy, so the host's own list — the one every other
// attachment of the host reads — never carries them. A gathered list that does not
// line up with the connections is refused rather than paired by position.
func TestUsedByWithCallerFacts_PutsEachCallersFactsOnItsOwnEntryOfACopy(t *testing.T) {
	t.Parallel()

	inbound := []types.InboundEdge{
		{ComponentID: "web", Socket: "apis"},
		{ComponentID: "quiet", Socket: "apis"},
	}
	var webAttachments types.OrderedMap[[]RenderFact]
	webAttachments.Set("tag", factsOf("pods", `["web-tag"]`))
	callers := []callerRenderFacts{
		{ComponentID: "web", Own: factsOf("pods", `["web"]`), Attachments: webAttachments},
		{ComponentID: "quiet"},
	}

	got, err := usedByWithCallerFacts(inbound, callers)
	if err != nil {
		t.Fatalf("usedByWithCallerFacts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("handed %d entries, want 2", len(got))
	}
	if js := contextFactsJSON(t, got[0].RenderFacts); js != `{"pods":["web","web-tag"]}` {
		t.Errorf("web's entry carries %s, want its own facts then its attachment's", js)
	}
	if js := contextFactsJSON(t, got[1].RenderFacts); js != `{}` {
		t.Errorf("a caller reporting nothing carries %s, want an empty set", js)
	}
	for i, edge := range inbound {
		if edge.RenderFacts != nil {
			t.Errorf("the host's own entry %d was written: it carries %s", i, contextFactsJSON(t, edge.RenderFacts))
		}
	}

	if _, err := usedByWithCallerFacts(inbound, callers[:1]); err == nil {
		t.Error("a gathered list shorter than the connections was paired by position")
	}
	swapped := []callerRenderFacts{callers[1], callers[0]}
	if _, err := usedByWithCallerFacts(inbound, swapped); err == nil {
		t.Error("a gathered list in another order was paired by position with the wrong callers")
	}
}

// TestUsesWithTargetFacts_PutsEachTargetsFactsOnItsOwnEntryOfACopy is the twin of
// the proof above for the other end of a connection: each connection's entry
// carries its own target's facts, joined own-then-attachments exactly as a caller's
// are; a target reporting nothing — or holding no record at all — carries an empty
// set; and the map handed over is rebuilt, so the host's own map, which its Recipe
// rendered against and every other attachment of the host reads, never carries
// them. A gathered set that does not line up with the connections is refused
// rather than paired with the wrong target.
func TestUsesWithTargetFacts_PutsEachTargetsFactsOnItsOwnEntryOfACopy(t *testing.T) {
	t.Parallel()

	var uses types.OrderedMap[types.EnrichedEdge]
	uses.Set("primary", types.EnrichedEdge{Target: "db", Socket: "databases"})
	uses.Set("quiet", types.EnrichedEdge{Target: "quiet-cache", Socket: "caches"})
	uses.Set("gone", types.EnrichedEdge{Target: "unrecorded", Socket: "apis"})

	var dbAttachments types.OrderedMap[[]RenderFact]
	dbAttachments.Set("backup", factsOf("pods", `["db-backup"]`))
	recorded := map[string]renderFactsRecord{
		"db":          {own: factsOf("pods", `["db"]`), attachments: dbAttachments},
		"quiet-cache": {},
	}

	targets := gatherTargetRenderFacts(uses, recorded)
	if keys := targets.Keys(); !reflect.DeepEqual(keys, []string{"primary", "quiet", "gone"}) {
		t.Fatalf("gathered %v, want one entry per connection in the uses map's order", keys)
	}
	got, err := usesWithTargetFacts(uses, targets)
	if err != nil {
		t.Fatalf("usesWithTargetFacts: %v", err)
	}
	for _, tc := range []struct {
		alias string
		want  string
	}{
		{alias: "primary", want: `{"pods":["db","db-backup"]}`},
		{alias: "quiet", want: `{}`},
		{alias: "gone", want: `{}`},
	} {
		entry, ok := got.Get(tc.alias)
		if !ok {
			t.Errorf("the connection %q was dropped from the map handed over", tc.alias)
			continue
		}
		if js := contextFactsJSON(t, entry.RenderFacts); js != tc.want {
			t.Errorf("the connection %q carries %s, want %s", tc.alias, js, tc.want)
		}
	}
	if keys := got.Keys(); !reflect.DeepEqual(keys, uses.Keys()) {
		t.Errorf("handed the connections as %v, want the host's own order %v", keys, uses.Keys())
	}
	for alias, edge := range uses.All() {
		if edge.RenderFacts != nil {
			t.Errorf("the host's own entry %q was written: it carries %s", alias, contextFactsJSON(t, edge.RenderFacts))
		}
	}

	var short types.OrderedMap[renderFactsRecord]
	short.Set("primary", recorded["db"])
	if _, err := usesWithTargetFacts(uses, short); err == nil {
		t.Error("a gathered set shorter than the connections was paired with them")
	}
	var renamed types.OrderedMap[renderFactsRecord]
	renamed.Set("primary", recorded["db"])
	renamed.Set("quiet", recorded["quiet-cache"])
	renamed.Set("elsewhere", renderFactsRecord{})
	if _, err := usesWithTargetFacts(uses, renamed); err == nil {
		t.Error("a gathered set naming another connection was paired with the wrong targets")
	}
}

// TestHeldAttachment_TheStepsContextCarriesNoTargetsFacts holds where a target's
// facts travel, and where they do not, for an attachment whose FeatureRecipe
// declares it reads them: onto the context its own artifacts render against, and
// not onto the context its pipeline steps read — those keep the connections the
// host makes, as the host's own Recipe reads them, without any target's facts — and
// not back onto the host's own context.
//
// The attachment is held by phaseB6 and rendered through the later pass's own entry,
// handed the targets' facts as that pass gathers them. The host provider is an
// assembler, so the attachment's pipeline is deferred and the context it would run
// against is returned rather than executed.
func TestHeldAttachment_TheStepsContextCarriesNoTargetsFacts(t *testing.T) {
	t.Parallel()

	var uses types.OrderedMap[types.EnrichedEdge]
	uses.Set("primary", types.EnrichedEdge{Target: "db", Socket: "databases"})
	hostCtx := &types.TemplateContext{
		Context: types.ContextBlock{InstanceID: "api", Uses: uses},
		Values:  types.OrderedMap[any]{},
		Infra:   types.OrderedMap[any]{},
	}
	hostRecipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
		Spec:     types.RecipeSpec{Capability: "compose", Implements: "core/api"},
	}
	state := NewPhaseBState(hostRecipe, "api", "local", "")

	art := types.OrderedMap[types.ArtifactEntry]{}
	art.Set("facts.yaml", types.ArtifactEntry{
		Inline: "target_facts: {{ toJson .context.uses.primary.render_facts }}\n",
	})
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "target-reader", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability:             "helm",
			Implements:             "core/target-reader",
			Artifacts:              art,
			ReadsTargetRenderFacts: true,
			Pipeline: []types.PipelineStep{{
				ID: "stamp", Plugin: "mutator/stamp", Function: "mutate",
				With: `{{ range .context.render_facts.pods }}{{ . }}{{ end }}`,
			}},
		},
	}
	emitter := &stubArtifactsEmitter{}
	factory := func(string) (Provider, error) { return emitter, nil }

	inTurn, held, err := phaseB6(context.Background(), hostCtx, state,
		[]FeatureRecipeRef{{InstanceName: "reader", FeatureRecipe: fr}}, factory,
		NodeProvides{}, RecipeLookup{}, NodeRenderContext{NodeID: "api", BoundaryPath: "local"},
		nil, nil, nil, nil, RendererInput{}, nil, true, "compose", false, renderctx.RenderContext{})
	if err != nil {
		t.Fatalf("phaseB6: %v", err)
	}
	if held == nil || len(held.items) != 1 {
		t.Fatalf("the declaring attachment was not held for the pass after the walk; held %+v", held)
	}
	if len(inTurn.files) != 0 {
		t.Fatalf("the declaring attachment also rendered in its host's turn: %v", inTurn.files)
	}

	recorded := map[string]renderFactsRecord{"db": {own: factsOf("pods", `["db"]`)}}
	out, err := held.render(context.Background(), nil, gatherTargetRenderFacts(hostCtx.Context.Uses, recorded))
	if err != nil {
		t.Fatalf("the later pass: %v", err)
	}

	var body string
	for name, content := range out.files {
		if strings.HasSuffix(name, "facts.yaml") {
			body = string(content)
		}
	}
	if want := `target_facts: {"pods":["db"]}` + "\n"; !strings.Contains(body, want) {
		t.Errorf("the attachment's artifact read its target's facts as %q, want %q in it", body, want)
	}
	if entry, _ := hostCtx.Context.Uses.Get("primary"); entry.RenderFacts != nil {
		t.Errorf("the later pass wrote the target's facts back onto the host's own map: %s",
			contextFactsJSON(t, entry.RenderFacts))
	}
	if len(out.deferred) != 1 || out.deferred[0].WithCtx == nil {
		t.Fatalf("want the attachment's pipeline deferred once with its context, got %d record(s)", len(out.deferred))
	}
	stepUses := out.deferred[0].WithCtx.Context.Uses
	entry, ok := stepUses.Get("primary")
	if !ok {
		t.Fatalf("the pipeline context lost the connection the host makes; it holds %v", stepUses.Keys())
	}
	if entry.RenderFacts != nil {
		t.Errorf("the pipeline context carries the target's facts: %s", contextFactsJSON(t, entry.RenderFacts))
	}
}

// TestPhaseB6_TheHostsOwnFactsReachEveryTemplateOfAnAttachment holds where the
// host's own render facts travel in the attachment render: onto the context the
// attachment's artifacts render against and onto the context its pipeline steps
// read — they come from the host's own render, so nothing reading them makes the
// host's bytes depend on who connects — and not back onto the host's own context.
// The connections made to the host stay off the pipeline's context as before.
//
// The host provider is an assembler, so the attachment's pipeline is deferred and
// the context it would run against is returned rather than executed. The second row
// is a host whose provider reported nothing: the facts are there, empty.
func TestPhaseB6_TheHostsOwnFactsReachEveryTemplateOfAnAttachment(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		hostFacts [][]RenderFact
		want      string
	}{
		{
			name:      "control: two calls of the host's render, one name joined",
			hostFacts: [][]RenderFact{factsOf("pods", `["chart"]`), factsOf("pods", `["artifact"]`, "ports", `[80]`)},
			want:      `{"pods":["chart","artifact"],"ports":[80]}`,
		},
		{name: "the host's provider reported nothing", hostFacts: nil, want: `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			hostCtx := &types.TemplateContext{
				Context: types.ContextBlock{InstanceID: "api"},
				Values:  types.OrderedMap[any]{},
				Infra:   types.OrderedMap[any]{},
			}
			hostRecipe := &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec:     types.RecipeSpec{Capability: "compose", Implements: "core/api"},
			}
			state := NewPhaseBState(hostRecipe, "api", "local", "")
			for _, report := range tc.hostFacts {
				state.recordRenderFacts(report)
			}
			state.InboundEdges = []types.InboundEdge{{ComponentID: "web", Socket: "apis"}}

			art := types.OrderedMap[types.ArtifactEntry]{}
			art.Set("facts.yaml", types.ArtifactEntry{Inline: "host_facts: {{ toJson .context.render_facts }}\n"})
			fr := &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "fact-reader", Version: "1.0.0"},
				Spec: types.FeatureRecipeSpec{
					Capability: "helm",
					Implements: "core/fact-reader",
					Artifacts:  art,
					Pipeline: []types.PipelineStep{{
						ID: "stamp", Plugin: "mutator/stamp", Function: "mutate",
						With: `{{ range .context.render_facts.pods }}{{ . }}{{ end }}`,
					}},
				},
			}
			emitter := &stubArtifactsEmitter{}
			factory := func(string) (Provider, error) { return emitter, nil }

			b6, _, err := phaseB6(context.Background(), hostCtx, state,
				[]FeatureRecipeRef{{InstanceName: "reader", FeatureRecipe: fr}}, factory,
				NodeProvides{}, RecipeLookup{}, NodeRenderContext{NodeID: "api", BoundaryPath: "local"},
				nil, nil, nil, nil, RendererInput{}, nil, true, "compose", false, renderctx.RenderContext{})
			if err != nil {
				t.Fatalf("phaseB6: %v", err)
			}

			var body string
			for name, content := range b6.files {
				if strings.HasSuffix(name, "facts.yaml") {
					body = string(content)
				}
			}
			if want := "host_facts: " + tc.want + "\n"; !strings.Contains(body, want) {
				t.Errorf("the attachment's artifact read its host's facts as %q, want %q in it", body, want)
			}
			if hostCtx.Context.RenderFacts != nil {
				t.Errorf("the attachment render wrote the facts back onto the host's own context: %s",
					contextFactsJSON(t, hostCtx.Context.RenderFacts))
			}
			deferred := b6.deferred
			if len(deferred) != 1 || deferred[0].WithCtx == nil {
				t.Fatalf("want the attachment's pipeline deferred once with its context, got %d record(s)", len(deferred))
			}
			if js := contextFactsJSON(t, deferred[0].WithCtx.Context.RenderFacts); js != tc.want {
				t.Errorf("the pipeline context carries the host's facts as %s, want %s", js, tc.want)
			}
			if got := deferred[0].WithCtx.Context.UsedBy; len(got) != 0 {
				t.Errorf("the pipeline context carries the connections made to the host: %+v", got)
			}
		})
	}
}
