// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the Step-3 Feature/Secret attachment schema-conformance gate:
// validateNodeAttachments + its wiring into BuildIR. The gate validates the
// EFFECTIVE merged attachment values/infra (after the 5-Tier Onion merge)
// against each attachment's declared catalog Feature/Secret schema, via the same
// resolver.ValidatePillarConformance entry the Step-1 component gate uses.

package ir

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// conformanceFeatureCatalog returns a CatalogLookup whose Features declare
// schemas:
//
//	core/monitoring (operational):
//	  values: level (enum info|warn|error) · port (integer ≥1) ·
//	          retention (integer, default 7, required)   ← required-with-default
//	  infra:  replicas (integer ≥1)
//	core/db-creds (kind:secret):
//	  values: username (string, required, no default)
//	core/bare: no schema → attachment validation skips it.
func conformanceFeatureCatalog(t *testing.T) resolver.CatalogLookup {
	t.Helper()
	const monSchema = `values:
  level: { type: string, enum: [info, warn, error] }
  port: { type: integer, minimum: 1 }
  retention: { type: integer, default: 7, required: true }
infra:
  replicas: { type: integer, minimum: 1 }
`
	const credSchema = `values:
  username: { type: string, required: true }
`
	return resolver.CatalogLookup{
		Component: func(string) (types.Component, bool) { return types.Component{}, false },
		Feature: func(typeURI string) (types.Feature, bool) {
			switch typeURI {
			case "core/monitoring":
				return types.Feature{
					Metadata: types.FeatureMetadata{Name: "core/monitoring", Version: "1.0.0"},
					Spec:     types.FeatureSpec{Schema: schemaNode(t, monSchema)},
				}, true
			case "core/db-creds":
				labels := types.OrderedMap[string]{}
				labels.Set(types.FeatureKindLabel, types.FeatureKindSecret)
				return types.Feature{
					Metadata: types.FeatureMetadata{Name: "core/db-creds", Labels: labels},
					Spec:     types.FeatureSpec{Schema: schemaNode(t, credSchema)},
				}, true
			case "core/bare":
				return types.Feature{Metadata: types.FeatureMetadata{Name: "core/bare"}}, true
			default:
				return types.Feature{}, false
			}
		},
	}
}

func schemaNode(t *testing.T, y string) yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(y), &n); err != nil {
		t.Fatalf("schema fixture parse: %v", err)
	}
	return n
}

func vmap(pairs ...any) types.OrderedMap[any] {
	m := types.OrderedMap[any]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

// buildWithAttachments runs BuildIR end-to-end for a single node "api" carrying
// the given Feature and Secret attachments, returning the build error (nil on
// success). This exercises the real attachment-gate wiring: the builder's
// per-type schema compile cache, validateNodeAttachments, and the joined halt.
func buildWithAttachments(t *testing.T, feats []topology.FeatureAttachment, secs []topology.SecretAttachment) error {
	t.Helper()
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", &topology.TopologyNode{InstanceID: "api", ComponentType: "core/server"})
	topoResult := &topology.TopologyResult{
		Nodes:              nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{"api": feats},
		SecretAttachments:  map[string][]topology.SecretAttachment{"api": secs},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		BoundaryInfra: map[string]*resolver.BoundaryInfra{},
	}
	_, _, err := BuildIR(BuildIRConfig{
		Topology:      topoResult,
		ResolveResult: resolveResult,
		CatalogLookup: conformanceFeatureCatalog(t),
	})
	return err
}

func feat(name, ftype string, vals types.OrderedMap[any]) topology.FeatureAttachment {
	return topology.FeatureAttachment{InstanceName: name, Feature: types.FeatureInstance{Type: ftype, Values: vals}}
}

func requireConformanceHalt(t *testing.T, err error, wantFieldSubstr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an ErrSchemaConformance halt naming %q; got nil", wantFieldSubstr)
	}
	if !errors.Is(err, resolver.ErrSchemaConformance) {
		t.Fatalf("error does not wrap ErrSchemaConformance: %v", err)
	}
	if !strings.Contains(err.Error(), wantFieldSubstr) {
		t.Fatalf("halt does not name %q; got: %v", wantFieldSubstr, err)
	}
}

// --- Feature/Secret attachment values, end-to-end through BuildIR ------------

func TestAttachmentConformance_FeatureValuesViolationHalts(t *testing.T) {
	t.Parallel()
	err := buildWithAttachments(t,
		[]topology.FeatureAttachment{feat("mon", "core/monitoring", vmap("level", "bogus"))}, nil)
	requireConformanceHalt(t, err, ".spec.components.api.features.mon.values.level")
}

func TestAttachmentConformance_SecretValuesViolationHalts(t *testing.T) {
	t.Parallel()
	// username is required with no default; omitting it must halt.
	err := buildWithAttachments(t, nil,
		[]topology.SecretAttachment{{InstanceName: "db", Secret: types.SecretInstance{Type: "core/db-creds", Values: vmap()}}})
	requireConformanceHalt(t, err, ".spec.components.api.secrets.db.values")
}

func TestAttachmentConformance_SchemalessAttachmentSkips(t *testing.T) {
	t.Parallel()
	// core/bare declares no schema → its attachment is not validated.
	err := buildWithAttachments(t,
		[]topology.FeatureAttachment{feat("b", "core/bare", vmap("anything", "goes"))}, nil)
	if err != nil {
		t.Fatalf("schemaless attachment must skip validation; got %v", err)
	}
}

func TestAttachmentConformance_DefaultAwareNoFalsePositive(t *testing.T) {
	t.Parallel()
	// retention is required BUT defaulted (7); omitting it must not be flagged.
	err := buildWithAttachments(t,
		[]topology.FeatureAttachment{feat("mon", "core/monitoring", vmap("level", "info"))}, nil)
	if err != nil {
		t.Fatalf("required-with-default attachment value must not be flagged; got %v", err)
	}
}

func TestAttachmentConformance_ConformingPasses(t *testing.T) {
	t.Parallel()
	err := buildWithAttachments(t,
		[]topology.FeatureAttachment{feat("mon", "core/monitoring", vmap("level", "warn", "port", 8080, "retention", 14))}, nil)
	if err != nil {
		t.Fatalf("conforming attachment must pass; got %v", err)
	}
}

// --- Merged-result semantics: the 5-Tier merged value is validated, NOT a
// single pre-merge fragment. Two attachments of the same instance name merge
// per-key (last wins).

func TestAttachmentConformance_LaterTierOverrideToIllegalHalts(t *testing.T) {
	t.Parallel()
	// First fragment legal (info), later fragment overrides to illegal (bogus).
	// The merged value (bogus) is what halts — validating the first fragment
	// alone would pass.
	err := buildWithAttachments(t, []topology.FeatureAttachment{
		feat("mon", "core/monitoring", vmap("level", "info")),
		feat("mon", "core/monitoring", vmap("level", "bogus")),
	}, nil)
	requireConformanceHalt(t, err, ".spec.components.api.features.mon.values.level")
}

func TestAttachmentConformance_LaterTierOverrideToLegalPasses(t *testing.T) {
	t.Parallel()
	// First fragment ILLEGAL (bogus), later fragment overrides to legal (info).
	// The merged value (info) conforms — proving the gate validates the merged
	// result, not the first fragment (which alone would halt).
	err := buildWithAttachments(t, []topology.FeatureAttachment{
		feat("mon", "core/monitoring", vmap("level", "bogus")),
		feat("mon", "core/monitoring", vmap("level", "info")),
	}, nil)
	if err != nil {
		t.Fatalf("merged-result must be validated, not the illegal first fragment; got %v", err)
	}
}

// --- Feature infra (Release-tier-sourced): validated against schema.infra via
// the pass directly, since Tier-1/2 topology attachments carry no infra.

func TestAttachmentConformance_FeatureInfraViolation(t *testing.T) {
	t.Parallel()
	feats := types.OrderedMap[types.IRFeatureEntry]{}
	feats.Set("mon", types.IRFeatureEntry{Type: "core/monitoring", Infra: vmap("replicas", 0)})
	errs := validateNodeAttachments("api", feats, types.OrderedMap[types.IRSecretEntry]{},
		nil, "Aggregate test", testCompiledFor(t, conformanceFeatureCatalog(t)))
	if len(errs) == 0 {
		t.Fatal("expected an infra conformance violation for replicas below minimum")
	}
	for _, e := range errs {
		if !errors.Is(e, resolver.ErrSchemaConformance) {
			t.Errorf("error does not wrap ErrSchemaConformance: %v", e)
		}
	}
	if !strings.Contains(errs[0].Field, ".spec.components.api.features.mon.infra.replicas") {
		t.Errorf("infra violation field: %q", errs[0].Field)
	}
}

// TestAttachmentConformance_InheritedFeatureConstraintEnforced proves the
// attachment gate (the fourth effective-schema consumer) validates an
// extends-ing Feature attachment against its COMPOSED schema: a constraint
// carried only on the effective schema (here a required inherited_flag absent
// from the Feature's authored schema) is enforced on the attachment.
func TestAttachmentConformance_InheritedFeatureConstraintEnforced(t *testing.T) {
	t.Parallel()
	cl := conformanceFeatureCatalog(t)
	cl.EffectiveSchema = func(typeURI string) (compactform.Schema, bool) {
		if typeURI == "core/monitoring" {
			return compileNode(t, "values:\n  level: { type: string, enum: [info, warn, error] }\n  inherited_flag: { type: boolean, required: true }\n"), true
		}
		return nil, false
	}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", &topology.TopologyNode{InstanceID: "api", ComponentType: "core/server"})
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		// level supplied; inherited_flag (composed-required) omitted.
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {feat("mon", "core/monitoring", vmap("level", "info"))},
		},
		SecretAttachments: map[string][]topology.SecretAttachment{},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		BoundaryInfra: map[string]*resolver.BoundaryInfra{},
	}
	_, _, err := BuildIR(BuildIRConfig{Topology: topoResult, ResolveResult: resolveResult, CatalogLookup: cl})
	requireConformanceHalt(t, err, ".spec.components.api.features.mon.values")
}

// testCompiledFor mirrors the builder's per-type schema compile closure for the
// unit-level pass test.
func testCompiledFor(t *testing.T, cat resolver.CatalogLookup) func(string) (compactform.Schema, bool) {
	t.Helper()
	cache := map[string]compactform.Schema{}
	miss := map[string]bool{}
	return func(ft string) (compactform.Schema, bool) {
		if c, ok := cache[ft]; ok {
			return c, true
		}
		if miss[ft] {
			return nil, false
		}
		f, ok := cat.Feature(ft)
		if !ok || f.Spec.Schema.Kind == 0 {
			miss[ft] = true
			return nil, false
		}
		compiled, err := compactform.Compile(&f.Spec.Schema)
		if err != nil {
			miss[ft] = true
			return nil, false
		}
		cache[ft] = compiled
		return compiled, true
	}
}
