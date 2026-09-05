// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for materialising an attachment's schema-declared pillar defaults into
// the graph, on both surfaces an attachment reaches the graph by: the per-node
// five-tier cascade, and the boundary-wide projection the boundary-scope
// dispatch reads.
//
// The two surfaces are covered together because they are two write paths for one
// authoring concept, and a fix applied to one leaves the other rendering an
// empty value. A boundary-wide attachment is folded into the per-node entry as
// well, for a different purpose, which is what makes covering only the cascade
// look sufficient — the attachment IS present there, just not on the path that
// renders it at boundary scope.

package ir

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// defaultsFeatureCatalog returns a CatalogLookup whose one Feature declares a
// default on both pillars and, on each, a sibling with no default:
//
//	core/settings
//	  values: level     (string, enum info|warn|error)      — no default
//	          retention (integer, default 7, required)       — required-with-default
//	  infra:  tier      (string, default standard, required) — required-with-default
//	          replicas  (integer, minimum 1)                 — no default
//	          limits.cpu    (string, default 250m)            — nested default
//	          limits.memory (string, default 512Mi)           — nested default
//
// Each pillar carries a required-with-default key AND an undefaulted sibling on
// purpose. The required-with-default key is what makes the trap observable: it
// satisfies the conformance gate while nothing renders. The undefaulted sibling
// is the control that keeps a fill from being mistaken for the pillar simply
// being replaced wholesale.
//
// core/plain declares no schema at all, so nothing can be materialised for it.
func defaultsFeatureCatalog(t *testing.T) resolver.CatalogLookup {
	t.Helper()
	const settingsSchema = `values:
  level: { type: string, enum: [info, warn, error] }
  retention: { type: integer, default: 7, required: true }
infra:
  tier: { type: string, default: standard, required: true }
  replicas: { type: integer, minimum: 1 }
  limits:
    cpu: { type: string, default: 250m }
    memory: { type: string, default: 512Mi }
`
	return resolver.CatalogLookup{
		Component: func(string) (types.Component, bool) { return types.Component{}, false },
		Feature: func(typeURI string) (types.Feature, bool) {
			switch typeURI {
			case "core/settings":
				return types.Feature{
					Metadata: types.FeatureMetadata{Name: "core/settings", Version: "1.0.0"},
					Spec:     types.FeatureSpec{Schema: defaultsSchemaNode(t, settingsSchema)},
				}, true
			case "core/plain":
				return types.Feature{Metadata: types.FeatureMetadata{Name: "core/plain"}}, true
			default:
				return types.Feature{}, false
			}
		},
	}
}

func defaultsSchemaNode(t *testing.T, y string) yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(y), &n); err != nil {
		t.Fatalf("schema fixture parse: %v", err)
	}
	return n
}

// defaultsNode returns a topology node placed in one boundary, so the Release
// tiers are reachable.
func defaultsNode() *topology.TopologyNode {
	return &topology.TopologyNode{InstanceID: "api", ComponentType: "core/server", BoundaryPath: "prod"}
}

// collectWithTiers folds one node's attachments through the real cascade, with
// the Aggregate-side contribution supplied as a component-tier attachment and
// the two Release-side contributions as the boundary-wide and per-component
// tiers. Passing nil for a tier omits that tier entirely, which is what lets a
// row assert the difference between "authored nothing" and "authored a value".
func collectWithTiers(
	t *testing.T,
	featureType string,
	aggregateValues types.OrderedMap[any],
	boundaryWideInfra, componentInfra *types.OrderedMap[any],
) *featureResult {
	t.Helper()
	node := defaultsNode()
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", node)

	topoResult := &topology.TopologyResult{Nodes: nodes}
	if aggregateValues.Len() > 0 {
		topoResult.FeatureAttachments = map[string][]topology.FeatureAttachment{
			"api": {{InstanceName: "settings", Feature: types.FeatureInstance{Type: featureType, Values: aggregateValues}}},
		}
	}

	prod := types.Boundary{}
	if boundaryWideInfra != nil {
		prod.Features.Set("settings", types.BoundaryFeature{Type: featureType, Infra: *boundaryWideInfra})
	}
	if componentInfra != nil {
		comp := types.BoundaryComponent{}
		comp.Features.Set("settings", types.BoundaryFeature{Type: featureType, Infra: *componentInfra})
		prod.Components.Set("api", comp)
	}
	release := &types.Release{}
	release.Spec.Boundaries.Set("prod", prod)

	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		Release:   release,
	}

	result, err := collectNodeFeatures("api", node, topoResult, resolveResult, defaultsFeatureCatalog(t))
	if err != nil {
		t.Fatalf("collectNodeFeatures: %v", err)
	}
	return result
}

func settingsEntry(t *testing.T, result *featureResult) types.IRFeatureEntry {
	t.Helper()
	entry, ok := result.Features.Get("settings")
	if !ok {
		t.Fatalf("the cascade produced no %q attachment; it holds %v", "settings", result.Features.Keys())
	}
	return entry
}

// TestAttachmentDefaults_AuthoredWinsAndOmittedSiblingIsFilled pins the merge
// order on both pillars in one build: one key authored, its sibling omitted.
//
// Both halves are needed and neither implies the other. A fill that replaced the
// pillar wholesale would pass an assertion that only checked the omitted key,
// and a no-op would pass one that only checked the authored key.
func TestAttachmentDefaults_AuthoredWinsAndOmittedSiblingIsFilled(t *testing.T) {
	t.Parallel()
	authoredInfra := valuesMap("replicas", 3)
	result := collectWithTiers(t, "core/settings", valuesMap("level", "warn"), nil, &authoredInfra)
	entry := settingsEntry(t, result)

	for _, tc := range []struct {
		pillar string
		got    types.OrderedMap[any]
		key    string
		want   any
		why    string
	}{
		{"values", entry.Values, "level", "warn", "authored, and the schema declares no default for it"},
		{"values", entry.Values, "retention", 7, "omitted, so the declared default fills it"},
		{"infra", entry.Infra, "replicas", 3, "authored, and the schema declares no default for it"},
		{"infra", entry.Infra, "tier", "standard", "omitted, so the declared default fills it"},
	} {
		t.Run(tc.pillar+"."+tc.key, func(t *testing.T) {
			got, present := tc.got.Get(tc.key)
			if !present {
				t.Fatalf("%s.%s absent; the pillar holds %v", tc.pillar, tc.key, tc.got.Keys())
			}
			if got != tc.want {
				t.Errorf("%s.%s = %#v, want %#v — %s", tc.pillar, tc.key, got, tc.want, tc.why)
			}
		})
	}
}

// TestAttachmentDefaults_NestedOmittedSiblingIsFilled pins the nested case: a
// partially-authored group keeps what the author wrote and gains the siblings
// they did not.
//
// A nested group is where a wholesale replacement hides most easily, because
// the group is present in both the authored and the merged pillar and only its
// contents differ.
func TestAttachmentDefaults_NestedOmittedSiblingIsFilled(t *testing.T) {
	t.Parallel()
	authoredInfra := valuesMap("limits", valuesMap("cpu", "1000m"))
	result := collectWithTiers(t, "core/settings", types.OrderedMap[any]{}, nil, &authoredInfra)
	entry := settingsEntry(t, result)

	rawLimits, present := entry.Infra.Get("limits")
	if !present {
		t.Fatalf("infra.limits absent; infra holds %v", entry.Infra.Keys())
	}
	limits, nested := rawLimits.(types.OrderedMap[any])
	if !nested {
		t.Fatalf("infra.limits is not a nested map; got %#v", rawLimits)
	}
	if cpu, _ := limits.Get("cpu"); cpu != "1000m" {
		t.Errorf("infra.limits.cpu = %#v, want %q — the author wrote it and an author's value wins", cpu, "1000m")
	}
	if mem, _ := limits.Get("memory"); mem != "512Mi" {
		t.Errorf("infra.limits.memory = %#v, want %q — the author omitted it, so the declared default fills it beside the sibling they wrote", mem, "512Mi")
	}
}

// TestAttachmentDefaults_ALaterTierSupplyingTheKeyIsNotOverwritten is the
// clause that keeps this change from being a worse defect than the one it
// fixes.
//
// The infra pillar is contested between exactly two tiers — the boundary-wide
// attachment and the per-component one — so the four combinations of "supplies"
// and "omits" across those two are the whole grid, and every cell is asserted.
// The two cells where either tier supplies a value are what would fail if the
// default were folded in as a contribution rather than applied over the
// completed merge: it would be ranked against the tiers instead of filling what
// they left absent.
func TestAttachmentDefaults_ALaterTierSupplyingTheKeyIsNotOverwritten(t *testing.T) {
	t.Parallel()
	gold := valuesMap("tier", "gold")
	silver := valuesMap("tier", "silver")
	empty := types.OrderedMap[any]{}

	// Every row attaches the instance from at least one tier. That is a
	// precondition rather than a convenience: a declared default fills a leaf of
	// an attachment the deployment made, and it does not conjure the attachment
	// itself. A node nothing attached to carries no attachment for a default to
	// land on, which is the correct answer and is asserted separately below.
	for _, tc := range []struct {
		name            string
		aggregateValues types.OrderedMap[any]
		boundaryWide    *types.OrderedMap[any]
		perComponent    *types.OrderedMap[any]
		wantTier        string
		why             string
	}{
		{
			name:            "no Release tier contributes at all",
			aggregateValues: valuesMap("level", "info"),
			wantTier:        "standard",
			why:             "the attachment exists from the Aggregate side and no Release tier supplied the key, so the declared default is what stands",
		},
		{
			name:         "both tiers attach and neither supplies the key",
			boundaryWide: &empty,
			perComponent: &empty,
			wantTier:     "standard",
			why:          "an attachment carrying no infra is still nothing supplied, so the default stands",
		},
		{
			name:         "the earlier tier supplies it",
			boundaryWide: &gold,
			wantTier:     "gold",
			why:          "the boundary-wide tier supplied the key and a default fills only what nothing supplied",
		},
		{
			name:         "the later tier supplies it",
			perComponent: &silver,
			wantTier:     "silver",
			why:          "the per-component tier supplied the key and a default fills only what nothing supplied",
		},
		{
			name:         "both supply it and the narrower scope wins",
			boundaryWide: &gold,
			perComponent: &silver,
			wantTier:     "silver",
			why:          "the per-component tier is the narrower scope, and the default must not displace the winner of that contest",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := collectWithTiers(t, "core/settings", tc.aggregateValues, tc.boundaryWide, tc.perComponent)
			entry := settingsEntry(t, result)
			got, present := entry.Infra.Get("tier")
			if !present {
				t.Fatalf("infra.tier absent; infra holds %v", entry.Infra.Keys())
			}
			if got != tc.wantTier {
				t.Errorf("infra.tier = %#v, want %q — %s", got, tc.wantTier, tc.why)
			}
		})
	}
}

// TestAttachmentDefaults_NoAttachmentMeansNothingToFill states the boundary of
// the fill, because the alternative is a defect rather than a nicety: a declared
// default fills a leaf of an attachment a deployment made, and it must not bring
// the attachment into existence.
//
// A Feature declaring defaults is attached to nothing here. The node comes out
// carrying no attachment at all — not one populated from the schema — which is
// what keeps a catalog entity's declarations from adding an overlay nobody asked
// for.
func TestAttachmentDefaults_NoAttachmentMeansNothingToFill(t *testing.T) {
	t.Parallel()
	result := collectWithTiers(t, "core/settings", types.OrderedMap[any]{}, nil, nil)
	if result.Features.Len() != 0 {
		t.Errorf("the node carries attachment(s) %v, but nothing attached one; a declared default fills a leaf of an attachment a deployment made and never creates the attachment",
			result.Features.Keys())
	}
}

// TestAttachmentDefaults_TrapBothHalvesThroughTheRealBuild asserts the two
// halves of the trap in one build through BuildIRContext: a required key carrying a
// default PASSES conformance, and its value ARRIVES.
//
// Either half alone leaves the trap in place. Asserting only that the build
// passes describes the state this change was made to end — the gate that exists
// to catch a missing required value being the reason nobody notices it is
// missing. Asserting only that the value arrives would not show that the gate
// admitted the document in the first place, so a regression turning the omission
// into a halt would read as this test still passing on a build that never ran.
func TestAttachmentDefaults_TrapBothHalvesThroughTheRealBuild(t *testing.T) {
	t.Parallel()
	node := defaultsNode()
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", node)

	// Both required-with-default keys omitted, on both pillars.
	prod := types.Boundary{}
	prod.Features.Set("settings", types.BoundaryFeature{Type: "core/settings"})
	release := &types.Release{}
	release.Spec.Boundaries.Set("prod", prod)

	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {{InstanceName: "settings", Feature: types.FeatureInstance{
				Type:   "core/settings",
				Values: valuesMap("level", "info"),
			}}},
		},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		Release:       release,
		BoundaryInfra: map[string]*resolver.BoundaryInfra{},
	}

	graph, _, _, err := buildIR(BuildIRConfig{
		Topology:      topoResult,
		ResolveResult: resolveResult,
		CatalogLookup: defaultsFeatureCatalog(t),
	})

	// Half one: the gate admits it, because the default satisfies the
	// requirement.
	if err != nil {
		t.Fatalf("a required attachment key carrying a default must not be flagged as missing; got %v", err)
	}

	// Half two: the value the gate accepted on is the value the graph carries.
	feats, ok := graph.Spec.FeatureValuesMapping.Get("api")
	if !ok {
		t.Fatalf("the graph carries no attachments for %q", "api")
	}
	entry, ok := feats.Get("settings")
	if !ok {
		t.Fatalf("the graph carries no %q attachment; it holds %v", "settings", feats.Keys())
	}
	if retention, _ := entry.Values.Get("retention"); retention != 7 {
		t.Errorf("values.retention = %#v, want 7 — the gate accepted the document because this default satisfies the requirement, so the graph has to carry it too", retention)
	}
	if tier, _ := entry.Infra.Get("tier"); tier != "standard" {
		t.Errorf("infra.tier = %#v, want %q — the gate accepted the document because this default satisfies the requirement, so the graph has to carry it too", tier, "standard")
	}
}

// TestAttachmentDefaults_FilledLeafCarriesNoProvenance pins the deliberate
// answer rather than leaving it to be rediscovered.
//
// The provenance record answers which authoring act produced a leaf, and its
// vocabulary is explicit authoring versus annotation-driven expansion. A schema
// default is neither, so a filled leaf carries no record; every reader of these
// maps already treats an absent record as naming no authoring act. Changing this
// is a change to what the record means, which is why it is asserted here.
func TestAttachmentDefaults_FilledLeafCarriesNoProvenance(t *testing.T) {
	t.Parallel()
	result := collectWithTiers(t, "core/settings", valuesMap("level", "warn"), nil, nil)
	entry := settingsEntry(t, result)

	if _, present := entry.Values.Get("retention"); !present {
		t.Fatal("values.retention was not filled, so this test is no longer asserting anything about a filled leaf")
	}
	if _, recorded := entry.ValuesProvenance.Get("retention"); recorded {
		t.Errorf("values.retention carries a provenance record; a schema default is neither an explicit authoring act nor an annotation expansion, and stamping either asserts something nobody wrote. provenance holds %v",
			provenanceKeys(entry.ValuesProvenance))
	}
	if _, recorded := entry.ValuesProvenance.Get("level"); !recorded {
		t.Error("values.level carries no provenance record, but it WAS authored — the absence above has to distinguish a filled leaf from an authored one to mean anything")
	}
}

// TestAttachmentDefaults_InheritedDefaultMaterialises pins the claim the
// language reference makes about an extending Feature: a default declared on a
// parent reaches a child's attachments.
//
// The property is not incidental — it holds because the fill reads the same
// composed contract the conformance gate reads, so a change that compiled an
// attachment's AUTHORED schema directly would keep every other test in this
// file green while silently making the documented behaviour false. The
// composed contract here declares a key the authored one does not, so only a
// fill that consulted it can produce the value.
func TestAttachmentDefaults_InheritedDefaultMaterialises(t *testing.T) {
	t.Parallel()
	cl := defaultsFeatureCatalog(t)
	cl.EffectiveSchema = func(typeURI string) (compactform.Schema, bool) {
		if typeURI == "core/settings" {
			// A composed contract carrying a key absent from the authored one.
			return compileNode(t, "infra:\n  region: { type: string, default: eu-west-1 }\n"), true
		}
		return nil, false
	}

	node := defaultsNode()
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", node)
	prod := types.Boundary{}
	prod.Features.Set("settings", types.BoundaryFeature{Type: "core/settings"})
	release := &types.Release{}
	release.Spec.Boundaries.Set("prod", prod)

	result, err := collectNodeFeatures("api", node,
		&topology.TopologyResult{Nodes: nodes},
		&resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
			Release:   release,
		}, cl)
	if err != nil {
		t.Fatalf("collectNodeFeatures: %v", err)
	}
	entry := settingsEntry(t, result)

	if region, _ := entry.Infra.Get("region"); region != "eu-west-1" {
		t.Errorf("infra.region = %#v, want %q — the key is declared only on the COMPOSED contract, so this fails unless the fill reads the composed schema rather than the authored one. infra holds %v",
			region, "eu-west-1", entry.Infra.Keys())
	}
	// The authored contract's own key must NOT appear: the composed contract
	// replaces it wholesale here, and asserting its absence is what keeps this
	// test measuring which schema was consulted rather than merely that some
	// default arrived.
	if _, present := entry.Infra.Get("tier"); present {
		t.Error("infra.tier is present, but the composed contract this fixture supplies declares no such key — the fill read the authored schema as well as, or instead of, the composed one")
	}
}

// TestAttachmentDefaults_SchemalessAttachmentIsUntouched keeps the change
// additive: a Feature declaring no contract has nothing to materialise, and its
// attachment must come out of the cascade exactly as authored.
func TestAttachmentDefaults_SchemalessAttachmentIsUntouched(t *testing.T) {
	t.Parallel()
	result := collectWithTiers(t, "core/plain", valuesMap("anything", "goes"), nil, nil)
	entry := settingsEntry(t, result)

	if entry.Values.Len() != 1 {
		t.Errorf("values holds %v, want only the authored key — a Feature declaring no contract has no default to fill", entry.Values.Keys())
	}
	if got, _ := entry.Values.Get("anything"); got != "goes" {
		t.Errorf("values.anything = %#v, want %q", got, "goes")
	}
	if entry.Infra.Len() != 0 {
		t.Errorf("infra holds %v, want nothing", entry.Infra.Keys())
	}
}

// TestAttachmentDefaults_KeyOrderIsStableAndAuthorFirst pins the ordering the
// Deterministic Output invariant rests on for a partially-authored pillar.
//
// The order is asserted as an exact sequence rather than as a set, because a
// default-fill that appended through an unordered map would satisfy every
// membership check and still move rendered bytes between builds. Author keys
// come first in the order the author wrote them, then the filled keys; that is
// what makes the sequence predictable rather than merely repeatable.
func TestAttachmentDefaults_KeyOrderIsStableAndAuthorFirst(t *testing.T) {
	t.Parallel()
	// Authored in an order that is NOT the sorted one, so an implementation
	// re-sorting the whole pillar is distinguishable from one appending fills.
	authoredInfra := valuesMap("replicas", 3)
	want := []string{"replicas", "limits", "tier"}

	var first []string
	for run := 0; run < 2; run++ {
		result := collectWithTiers(t, "core/settings", types.OrderedMap[any]{}, nil, &authoredInfra)
		entry := settingsEntry(t, result)
		got := entry.Infra.Keys()
		if run == 0 {
			first = got
			if len(got) != len(want) {
				t.Fatalf("infra key order = %v, want %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("infra key order = %v, want %v — author keys in author order, then filled keys", got, want)
				}
			}
			continue
		}
		if len(got) != len(first) {
			t.Fatalf("second fold produced %v, first produced %v", got, first)
		}
		for i := range first {
			if got[i] != first[i] {
				t.Fatalf("second fold produced %v, first produced %v — the fill is not order-stable across folds of one input", got, first)
			}
		}
	}
}

// TestBoundaryFeaturesProjection_DefaultMaterialises covers the SECOND write
// path an attachment reaches the graph by.
//
// A boundary-wide attachment does not render from the per-node cascade: it is
// projected by this walk and read straight from the projection by the
// boundary-scope dispatch. So a default filled only on the cascade leaves this
// path rendering an empty value, and the two rows here are what separate the two
// paths — the projection is asked directly rather than through a node.
func TestBoundaryFeaturesProjection_DefaultMaterialises(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		authored types.OrderedMap[any]
		wantTier string
		why      string
	}{
		{
			name:     "nothing authored",
			wantTier: "standard",
			why:      "the boundary attached the Feature and authored no infra, so the declared default is what the projection carries",
		},
		{
			name:     "authored",
			authored: valuesMap("tier", "gold"),
			wantTier: "gold",
			why:      "an authored value wins over the declared default here exactly as it does on every other pillar",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			feats := types.OrderedMap[types.BoundaryFeature]{}
			feats.Set("settings", types.BoundaryFeature{Type: "core/settings", Infra: tc.authored})
			prod := types.Boundary{Features: feats}
			boundaries := types.OrderedMap[types.Boundary]{}
			boundaries.Set("prod", prod)

			out := buildBoundaryFeaturesProjection(boundaries, defaultsFeatureCatalog(t))

			inner, ok := out.Get("prod")
			if !ok {
				t.Fatalf("the projection carries no boundary %q; it holds %v", "prod", out.Keys())
			}
			entry, ok := inner.Get("settings")
			if !ok {
				t.Fatalf("the projection carries no %q attachment; it holds %v", "settings", inner.Keys())
			}
			got, present := entry.Infra.Get("tier")
			if !present {
				t.Fatalf("infra.tier absent from the projection; it holds %v", entry.Infra.Keys())
			}
			if got != tc.wantTier {
				t.Errorf("projected infra.tier = %#v, want %q — %s", got, tc.wantTier, tc.why)
			}
			// The undefaulted sibling stays absent: a fill adds what the schema
			// declares and never invents a key it does not.
			if _, present := entry.Infra.Get("replicas"); present {
				t.Error("infra.replicas is present, but the schema declares no default for it and nothing authored it")
			}
		})
	}
}
