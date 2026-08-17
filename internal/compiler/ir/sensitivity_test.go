// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the derived sensitivity facts an IR attachment entry carries: the
// per-attachment roll-up and the per-delivery-key marking. Every case runs
// end-to-end through BuildIR, so the production wiring — not just the derivation
// function — is what is under test.
//
// Each case pairs the fact with its negative. A roll-up that returned true for
// everything, and a marking that marked every delivery key, would both pass a
// positive-only test while destroying the axis's meaning.

package ir

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// sensitivityCatalog returns a CatalogLookup for the sensitivity cases.
//
// Component core/server declares two attachment slots: `vault` declared
// sensitive, `plain` not.
//
// Features:
//
//	core/secret-values  values.password sensitive · values.host not
//	core/nested-values  values.auth.basic.password sensitive (nested)
//	core/plain          values.level, nothing declared sensitive
//	core/creds          provides.dsn sensitive · provides.name, provides.key not
//	core/labelled       catalog labels only, non-alphabetical
//	core/annotated      catalog labels AND annotations, each non-alphabetical
func sensitivityCatalog(t *testing.T) resolver.CatalogLookup {
	t.Helper()
	slots := types.OrderedMap[types.AttachmentSlot]{}
	slots.Set("vault", types.AttachmentSlot{Sensitive: true})
	slots.Set("plain", types.AttachmentSlot{})
	server := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/server", Version: "1.0.0"},
		Spec:     types.ComponentSpec{FeaturesSchema: slots},
	}
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "core/server" {
				return server, true
			}
			return types.Component{}, false
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			switch typeURI {
			case "core/secret-values":
				return types.Feature{
					Metadata: types.FeatureMetadata{Name: "core/secret-values", Version: "1.0.0"},
					Spec: types.FeatureSpec{Schema: schemaNode(t, `values:
  password: { type: string, sensitive: true }
  host: { type: string }
`)},
				}, true
			case "core/nested-values":
				return types.Feature{
					Metadata: types.FeatureMetadata{Name: "core/nested-values", Version: "1.0.0"},
					Spec: types.FeatureSpec{Schema: schemaNode(t, `values:
  auth:
    basic:
      password: { type: string, sensitive: true }
      username: { type: string }
`)},
				}, true
			case "core/plain":
				return types.Feature{
					Metadata: types.FeatureMetadata{Name: "core/plain", Version: "1.0.0"},
					Spec: types.FeatureSpec{Schema: schemaNode(t, `values:
  level: { type: string }
`)},
				}, true
			case "core/creds":
				return types.Feature{
					Metadata: types.FeatureMetadata{Name: "core/creds", Version: "1.0.0"},
					Spec: types.FeatureSpec{Schema: schemaNode(t, `provides:
  dsn: { type: string, sensitive: true }
  name: { type: string }
  key: { type: string }
`)},
				}, true
			case "core/labelled":
				// Labels declared in deliberately non-alphabetical order, so a
				// carry that sorted them would be visible rather than harmless.
				// Declares NO annotations, which is what makes it the control
				// for the annotations carry: labels and annotations have the
				// same shape, so only an entity carrying one and not the other
				// distinguishes a copy of the right map from a copy of either.
				return types.Feature{
					Metadata: types.FeatureMetadata{
						Name:    "core/labelled",
						Version: "1.0.0",
						Labels:  labelMap("zeta.example/second", "2", "alpha.example/first", "1"),
					},
					Spec: types.FeatureSpec{Schema: schemaNode(t, `values:
  host: { type: string }
`)},
				}, true
			case "core/annotated":
				// Both metadata maps declared, each in deliberately
				// non-alphabetical order and with disjoint keys and values, so a
				// carry that sorted either, or that filled one from the other,
				// is visible rather than harmless.
				return types.Feature{
					Metadata: types.FeatureMetadata{
						Name:        "core/annotated",
						Version:     "1.0.0",
						Labels:      labelMap("kappa.example/tier", "gold", "beta.example/region", "eu"),
						Annotations: labelMap("omega.example/reviewed", "no", "delta.example/owner", "platform-team"),
					},
					Spec: types.FeatureSpec{Schema: schemaNode(t, `values:
  host: { type: string }
`)},
				}, true
			default:
				return types.Feature{}, false
			}
		},
	}
}

// buildSensitivity runs BuildIR for one node "api" of type core/server carrying
// the given attachments, and returns the node's merged attachment map.
func buildSensitivity(t *testing.T, feats []topology.FeatureAttachment) types.OrderedMap[types.IRFeatureEntry] {
	t.Helper()
	return buildSensitivityWith(t, feats,
		types.OrderedMap[types.FeatureInstance]{},
		types.OrderedMap[types.BoundaryFeature]{})
}

// buildSensitivityWith is buildSensitivity plus the two later cascade tiers a
// merge case needs: subsystem-scope attachments (Aggregate side, after the
// component tier) and Release per-component attachments (last).
func buildSensitivityWith(
	t *testing.T,
	feats []topology.FeatureAttachment,
	subsystemFeats types.OrderedMap[types.FeatureInstance],
	releaseFeats types.OrderedMap[types.BoundaryFeature],
) types.OrderedMap[types.IRFeatureEntry] {
	t.Helper()
	graph := buildSensitivityGraph(t, feats, subsystemFeats, releaseFeats)
	attachments, ok := graph.Spec.FeatureValuesMapping.Get("api")
	if !ok {
		t.Fatalf("no attachments emitted for node api")
	}
	return attachments
}

func buildSensitivityGraph(
	t *testing.T,
	feats []topology.FeatureAttachment,
	subsystemFeats types.OrderedMap[types.FeatureInstance],
	releaseFeats types.OrderedMap[types.BoundaryFeature],
) *types.IRGraph {
	t.Helper()
	node := &topology.TopologyNode{InstanceID: "api", ComponentType: "core/server"}
	agg := &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}}
	if subsystemFeats.Len() > 0 {
		node.SubsystemPath = "sub"
		subsystems := types.OrderedMap[types.Subsystem]{}
		subsystems.Set("sub", types.Subsystem{Features: subsystemFeats})
		agg.Spec.Subsystems = subsystems
	}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", node)
	topoResult := &topology.TopologyResult{
		Nodes:              nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{"api": feats},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate:     agg,
		BoundaryInfra: map[string]*resolver.BoundaryInfra{},
	}
	if releaseFeats.Len() > 0 {
		node.BoundaryPath = "prod"
		components := types.OrderedMap[types.BoundaryComponent]{}
		components.Set("api", types.BoundaryComponent{Features: releaseFeats})
		boundaries := types.OrderedMap[types.Boundary]{}
		boundaries.Set("prod", types.Boundary{Components: components})
		resolveResult.Release = &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}}
	}
	graph, _, _, err := BuildIR(BuildIRConfig{
		Topology:      topoResult,
		ResolveResult: resolveResult,
		CatalogLookup: sensitivityCatalog(t),
	})
	if err != nil {
		t.Fatalf("BuildIR: %v", err)
	}
	return graph
}

func entry(t *testing.T, attachments types.OrderedMap[types.IRFeatureEntry], name string) types.IRFeatureEntry {
	t.Helper()
	e, ok := attachments.Get(name)
	if !ok {
		t.Fatalf("no attachment %q; have %v", name, attachments.Keys())
	}
	return e
}

// injectBlock builds a delivery block for one mechanism in the
// array-of-entries form an authored document produces. Deliveries are given
// as delivery-key, provides-key pairs and keep the order they are written in,
// so a fixture reading order out of the result reads the order it declared.
func injectBlock(mechanism string, deliveries ...string) *types.InjectBlock {
	if len(deliveries)%2 != 0 {
		panic("injectBlock: deliveries must be delivery-key, provides-key pairs")
	}
	entries := make([]any, 0, len(deliveries)/2)
	for i := 0; i < len(deliveries); i += 2 {
		entries = append(entries, map[string]any{"in": deliveries[i], "from": deliveries[i+1]})
	}
	block := &types.InjectBlock{}
	switch mechanism {
	case "env":
		block.Env = entries
	case "file":
		block.File = entries
	default:
		block.Arg = entries
	}
	return block
}

// A Feature declaring a sensitive values leaf makes its attachment sensitive.
// The sibling attachment of a Feature declaring nothing must not be.
func TestDerivedSensitivity_SensitiveLeafRollsUp(t *testing.T) {
	t.Parallel()
	attachments := buildSensitivity(t, []topology.FeatureAttachment{
		feat("plain", "core/secret-values", vmap("host", "db.internal")),
		feat("other", "core/plain", vmap("level", "info")),
	})
	if got := entry(t, attachments, "plain"); !got.Sensitive {
		t.Errorf("an attachment whose Feature declares a sensitive values leaf must be sensitive; got %+v", got)
	}
	if got := entry(t, attachments, "other"); got.Sensitive {
		t.Errorf("an attachment whose Feature declares nothing sensitive must not be sensitive; got %+v", got)
	}
}

// A nested sensitive leaf is found. The declaration is one level below a
// property, which is the depth a top-level-only reader would miss.
func TestDerivedSensitivity_NestedSensitiveLeafRollsUp(t *testing.T) {
	t.Parallel()
	attachments := buildSensitivity(t, []topology.FeatureAttachment{
		feat("other", "core/nested-values", vmap()),
	})
	if got := entry(t, attachments, "other"); !got.Sensitive {
		t.Errorf("a nested sensitive declaration must roll up; got %+v", got)
	}
}

// The case the two authored levels exist for: a slot declared sensitive whose
// fill declares NO sensitive leaf still comes out sensitive. The same Feature
// filling the un-declared slot must not.
func TestDerivedSensitivity_SensitiveSlotWithPlainFillRollsUp(t *testing.T) {
	t.Parallel()
	attachments := buildSensitivity(t, []topology.FeatureAttachment{
		feat("vault", "core/plain", vmap("level", "info")),
		feat("plain", "core/plain", vmap("level", "info")),
	})
	if got := entry(t, attachments, "vault"); !got.Sensitive {
		t.Errorf("a fill of a slot declared sensitive must be sensitive even when the Feature declares no sensitive leaf; got %+v", got)
	}
	if got := entry(t, attachments, "plain"); got.Sensitive {
		t.Errorf("the same Feature filling a slot that declares nothing must not be sensitive; got %+v", got)
	}
	// An attachment under a name no slot declares is not sensitive by default.
	free := buildSensitivity(t, []topology.FeatureAttachment{
		feat("adhoc", "core/plain", vmap("level", "info")),
	})
	if got := entry(t, free, "adhoc"); got.Sensitive {
		t.Errorf("an attachment binding no declared slot must not be sensitive; got %+v", got)
	}
}

// The marking discriminates: a delivery key referencing a sensitive provides key
// is marked and its sibling referencing a non-sensitive one is not. A marking
// that covered the whole inject block would pass the positive half alone.
func TestDerivedSensitivity_InjectMarkingDiscriminates(t *testing.T) {
	t.Parallel()
	attachments := buildSensitivity(t, []topology.FeatureAttachment{
		{InstanceName: "creds", Feature: types.FeatureInstance{
			Type: "core/creds",
			Inject: injectBlock("env",
				"DATABASE_URL", "dsn",
				"DATABASE_KEY", "key",
			),
		}},
	})
	got := entry(t, attachments, "creds")
	if !got.Sensitive {
		t.Errorf("a Feature declaring a sensitive provides leaf must roll up sensitive; got %+v", got)
	}
	env, ok := got.SensitiveInject.Get("env")
	if !ok {
		t.Fatalf("expected an env mechanism in the marking; got %v", got.SensitiveInject.Keys())
	}
	if from, ok := env.Get("DATABASE_URL"); !ok || from != "dsn" {
		t.Errorf("DATABASE_URL references a sensitive provides key and must be marked with it; got %q ok=%v", from, ok)
	}
	if _, ok := env.Get("DATABASE_KEY"); ok {
		t.Errorf("DATABASE_KEY references a provides key declaring nothing and must not be marked; got %v", env.Keys())
	}
	if env.Len() != 1 {
		t.Errorf("expected exactly one marked delivery key; got %v", env.Keys())
	}
	// The marking is a subset of the delivery mapping it derives from.
	full, _ := got.Inject.Get("env")
	if full.Len() != 2 {
		t.Fatalf("inject fixture drifted: %v", full.Keys())
	}
	for key := range env.All() {
		if _, present := full.Get(key); !present {
			t.Errorf("marked key %q is absent from inject", key)
		}
	}
}

// A `from:` naming a provides key the Feature does not declare marks nothing.
// The unfulfilled-contract judgement belongs to the contract check, not here.
func TestDerivedSensitivity_UnknownProvidesKeyMarksNothing(t *testing.T) {
	t.Parallel()
	attachments := buildSensitivity(t, []topology.FeatureAttachment{
		{InstanceName: "creds", Feature: types.FeatureInstance{
			Type:   "core/creds",
			Inject: injectBlock("env", "DATABASE_URL", "no_such_key"),
		}},
	})
	got := entry(t, attachments, "creds")
	if got.SensitiveInject.Len() != 0 {
		t.Errorf("a reference to an undeclared provides key must mark nothing; got %v", got.SensitiveInject.Keys())
	}
	if !got.Sensitive {
		t.Errorf("the roll-up still holds — the Feature declares a sensitive provides leaf; got %+v", got)
	}
}

// An attachment declaring no delivery mechanism — the shape a slot takes when
// its Recipe consumes the material internally — has no delivery key to mark.
func TestDerivedSensitivity_NoDeliveryMechanismMarksNothing(t *testing.T) {
	t.Parallel()
	attachments := buildSensitivity(t, []topology.FeatureAttachment{
		{InstanceName: "vault", Feature: types.FeatureInstance{Type: "core/creds"}},
	})
	got := entry(t, attachments, "vault")
	if got.Inject.Len() != 0 {
		t.Fatalf("fixture declares no delivery mechanism; got %v", got.Inject.Keys())
	}
	if got.SensitiveInject.Len() != 0 {
		t.Errorf("no delivery key exists, so the marking must be empty; got %v", got.SensitiveInject.Keys())
	}
	if !got.Sensitive {
		t.Errorf("the roll-up is independent of delivery; got %+v", got)
	}
}

// The roll-up is a property of the MERGED attachment. The first contribution
// names no Feature type, so a derivation running on first sight would read no
// declaration at all and answer false; the type arrives from a later tier.
func TestDerivedSensitivity_RollUpReadsTheMergedType(t *testing.T) {
	t.Parallel()
	releaseFeats := types.OrderedMap[types.BoundaryFeature]{}
	releaseFeats.Set("other", types.BoundaryFeature{Type: "core/secret-values"})

	attachments := buildSensitivityWith(t,
		[]topology.FeatureAttachment{feat("other", "", vmap("host", "db.internal"))},
		types.OrderedMap[types.FeatureInstance]{},
		releaseFeats,
	)
	got := entry(t, attachments, "other")
	if got.Type != "core/secret-values" {
		t.Fatalf("merge fixture drifted: the later tier must supply the type; got %q", got.Type)
	}
	if !got.Sensitive {
		t.Errorf("the roll-up must read the merged type, not the first fragment's; got %+v", got)
	}
}

// The marking is a property of the MERGED inject. The first contribution routes
// the delivery key to a provides key declaring nothing; a later tier repoints
// the same delivery key at the sensitive one, and the incoming contribution wins
// at the delivery key. A derivation running on first sight would answer
// not-marked.
func TestDerivedSensitivity_MarkingReadsTheMergedInject(t *testing.T) {
	t.Parallel()
	subsystemFeats := types.OrderedMap[types.FeatureInstance]{}
	subsystemFeats.Set("creds", types.FeatureInstance{
		Type:   "core/creds",
		Inject: injectBlock("env", "DATABASE_URL", "dsn"),
	})

	attachments := buildSensitivityWith(t,
		[]topology.FeatureAttachment{{InstanceName: "creds", Feature: types.FeatureInstance{
			Type:   "core/creds",
			Inject: injectBlock("env", "DATABASE_URL", "name"),
		}}},
		subsystemFeats,
		types.OrderedMap[types.BoundaryFeature]{},
	)
	got := entry(t, attachments, "creds")
	full, _ := got.Inject.Get("env")
	if from, _ := full.Get("DATABASE_URL"); from != "dsn" {
		t.Fatalf("merge fixture drifted: the later tier must win at the delivery key; got %q", from)
	}
	env, ok := got.SensitiveInject.Get("env")
	if !ok {
		t.Fatalf("expected the merged delivery key to be marked; got %v", got.SensitiveInject.Keys())
	}
	if from, ok := env.Get("DATABASE_URL"); !ok || from != "dsn" {
		t.Errorf("the marking must read the merged inject, not the first fragment's; got %q ok=%v", from, ok)
	}
}

// With nothing declared sensitive, neither sensitivity field reaches the wire.
//
// The exact-bytes assertion below pins the whole emitted entry, so it also
// records which OTHER per-attachment facts are unconditional. The declaration-
// origin marker is one: every attachment on a node whose Component resolves
// carries it, because "fills no declared slot" is a positive finding a consumer
// acts on and omitting it would make that finding indistinguishable from an
// unresolved slot set. The sensitivity fields are the opposite — they are a
// marking, absent when no declaration produces one.
func TestDerivedSensitivity_UndeclaredAttachmentCarriesNeitherKey(t *testing.T) {
	t.Parallel()
	attachments := buildSensitivity(t, []topology.FeatureAttachment{
		feat("plain", "core/plain", vmap("level", "info")),
	})
	got := entry(t, attachments, "plain")
	if got.Sensitive || got.SensitiveInject.Len() != 0 {
		t.Fatalf("nothing is declared, so both derived fields must be zero; got %+v", got)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"sensitive", "sensitive_inject"} {
		if strings.Contains(string(raw), `"`+key+`"`) {
			t.Errorf("an undeclared attachment must not emit %q; got %s", key, raw)
		}
	}
	if want := `{"type":"core/plain","resolved_version":"1.0.0","binding":"plain","values":{"level":"info"},"origin":"explicit","values_provenance":{"level":{"origin":"explicit"}}}`; string(raw) != want {
		t.Errorf("emitted bytes changed:\n got %s\nwant %s", raw, want)
	}
}

// An emitted IR carrying both derived fields must still validate against the IR
// schema. The attachment entry's schema definition is closed, so a Go field with
// no declared property makes the whole document non-conformant — the two land
// together or not at all.
func TestDerivedSensitivity_EmittedIRWithBothFieldsConformsToSchema(t *testing.T) {
	t.Parallel()
	graph := buildSensitivityGraph(t,
		[]topology.FeatureAttachment{
			feat("vault", "core/secret-values", vmap("password", "s3cret")),
			{InstanceName: "creds", Feature: types.FeatureInstance{
				Type:   "core/creds",
				Inject: injectBlock("env", "DATABASE_URL", "dsn", "DATABASE_KEY", "key"),
			}},
		},
		types.OrderedMap[types.FeatureInstance]{},
		types.OrderedMap[types.BoundaryFeature]{},
	)
	raw, err := json.Marshal(graph)
	if err != nil {
		t.Fatalf("marshal IR: %v", err)
	}
	// The fixture is only meaningful if both fields actually reached the wire.
	for _, key := range []string{`"sensitive":true`, `"sensitive_inject":`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("fixture emitted no %s; the conformance check would be vacuous:\n%s", key, raw)
		}
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatalf("re-decode IR: %v", err)
	}
	findings, err := envelope.ValidateInstance(envelope.KindIR, instance)
	if err != nil {
		t.Fatalf("IR schema engine error: %v", err)
	}
	if len(findings) > 0 {
		t.Errorf("emitted IR carrying the derived sensitivity fields violates the IR schema (%d finding(s)):\n%s\n%+v",
			len(findings), raw, findings)
	}
}
