// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Each rule the pre-flatten guard suite asks is a declared value, and the tests
// here pin what one of them reports over an authored document: which sites it
// names, in which order, and how many findings one input draws.
//
// 🔴 Every rule is read through the framework's own traversal rather than by
// calling its predicate, and that is the whole point rather than an incidental
// choice. A predicate called directly is asked about a coordinate the test chose;
// a rule reached through its traversal is asked about the coordinates the document
// actually presents, which is what the suite does in production. A rule that
// stopped being presented with a coordinate would still pass a direct call and
// would report nothing here.
//
// Each row states the sequence in full rather than a count, so an ordering change
// and a locator change are pinned here instead of rediscovered later. Where a row
// exercises a skip, the skipped site sits in the same document beside a site that
// IS reported — so the skip is proven by its absence from a non-empty expectation
// rather than by a row that reports nothing and would pass however the rule
// behaved.

// locatorsOf returns each objection's locator, in the order raised.
func locatorsOf(objections []constraint.Objection) []string {
	out := make([]string, 0, len(objections))
	for _, o := range objections {
		out = append(out, o.Locator())
	}
	return out
}

// ------------------------------------------------------------------ the rule
// about a component type the catalog holds no entry for

// namedType pairs an authored name with the type it declares. The rows below use
// an ordered slice rather than a map, so a fixture's author order is the order it
// is written in and no test has to sort a map to become deterministic.
type namedType struct {
	name string
	typ  string
}

// catalogEntryAggregate builds one Aggregate whose components carry the supplied
// types, in the order given.
func catalogEntryAggregate(name string, components []namedType) *types.Aggregate {
	comps := types.OrderedMap[types.ComponentInstance]{}
	for _, c := range components {
		comps.Set(c.name, types.ComponentInstance{Type: c.typ})
	}
	return &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: name},
		Spec:     types.AggregateSpec{Components: comps},
	}
}

// presentCatalog holds entries for exactly the types named.
func presentCatalog(held ...string) CatalogLookup {
	set := map[string]bool{}
	for _, h := range held {
		set[h] = true
	}
	return CatalogLookup{Component: func(uri string) (types.Component, bool) {
		if !set[uri] {
			return types.Component{}, false
		}
		comp := types.Component{}
		comp.Metadata.Name = uri
		return comp, true
	}}
}

func TestTheCatalogEntryRuleReportsEachComponentAddressedAsAnAbsentIdentity(t *testing.T) {
	t.Parallel()

	const absentType = "core/absent:1.0.0"
	const knownType = "core/known:1.0.0"
	const localType = "./local-variant"

	for _, tc := range []struct {
		name       string
		catalog    CatalogLookup
		components []namedType
		// raised is the sequence the rule reports: one locator per component it
		// names, in the order the node walk reaches them.
		raised []string
	}{
		{
			// One component of each kind, so the skip is exercised by the absence
			// of the held type's component from the expectation rather than by a
			// row that reports nothing at all, and a path-spelled type is reported
			// beside the absent identity: it names no catalog entity either.
			name:    "a catalog identity with no entry and a path-spelled type are reported; a held type is not",
			catalog: presentCatalog(knownType),
			components: []namedType{
				{"web", absentType},
				{"cache", knownType},
				{"sidecar", localType},
			},
			raised: []string{".spec.components.sidecar", ".spec.components.web"},
		},
		{
			// Two reports, authored against their alphabetical order. With a
			// single finding every ordering agrees, which is why the row above
			// cannot carry the ordering claim and this one can.
			name:    "several reports come back in instance-name order",
			catalog: presentCatalog(),
			components: []namedType{
				{"zeta", absentType},
				{"alpha", absentType},
			},
			raised: []string{".spec.components.alpha", ".spec.components.zeta"},
		},
		{
			// A build that consults no catalog at all offers no component lookup.
			// The rule reads the absent lookup as an absent entry and reports
			// every component addressed as an identity, so such a build halts on
			// every one of them.
			//
			// 🔴 Recorded because it looks like an oversight and is not: adding a
			// condition on whether a lookup exists would make the rule read the
			// absence of tooling rather than a fact about the document, and no
			// shipping build produces this input — every constructor of the lookup
			// populates it.
			name:       "a build consulting no catalog reports every component addressed as an identity",
			catalog:    CatalogLookup{},
			components: []namedType{{"web", absentType}},
			raised:     []string{".spec.components.web"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.raised) == 0 {
				t.Fatal("this row expects nothing, so it cannot tell a working rule from one " +
					"that reports nothing whatever it is given")
			}

			agg := catalogEntryAggregate("catalog-entry", tc.components)
			na := normalized(t, agg)
			view := newNormalizedView(na, nil, tc.catalog, nil)

			objections, err := constraint.RunOnNodes[constraint.NormalizedView](
				nil, newCoordinateSet(view), view, componentTypeResolvesToACatalogEntry)
			if err != nil {
				t.Fatalf("the node walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, tc.raised) {
				t.Errorf("raised sequence = %v; want %v", got, tc.raised)
			}
		})
	}
}

// TestTheCatalogEntryRuleReportsOnceHoweverManySurfacesTheComponentAuthored is
// the multiplicity claim, and it is the one a reader most needs because the
// enforcement it replaced was spread across every surface that consulted the
// catalog.
//
// The classification used to be reached from each authoring surface in turn — the
// injection block, a delivery reference, an attachment, a configuration pillar, a
// wired socket — so one component of an absent type drew one finding per surface
// it happened to author. The rule is keyed on the COMPONENT, so it reports once
// however many surfaces are written.
//
// The control beside it is a component of the same absent type authoring nothing
// but its type, which must also draw exactly one: without it, a rule that had
// somehow become keyed on a surface could still satisfy the count above by
// reporting once for one of them.
func TestTheCatalogEntryRuleReportsOnceHoweverManySurfacesTheComponentAuthored(t *testing.T) {
	t.Parallel()

	const absentType = "core/absent:1.0.0"

	loud := types.ComponentInstance{Type: absentType}
	loud.Inject = &types.InjectBlock{Env: []any{
		map[string]any{"in": "DB_HOST", "from": "nope"},
	}}
	loud.Uses.Set("db", types.UsesSocketValue{Compact: []string{"quiet"}})
	loud.Features.Set("audit", types.FeatureInstance{Type: "core/audit:1.0.0"})
	loud.Values.Set("replicas", "many")

	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set("loud", loud)
	comps.Set("quiet", types.ComponentInstance{Type: absentType})
	na := normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "catalog-entry"},
		Spec:     types.AggregateSpec{Components: comps},
	})

	view := newNormalizedView(na, nil, presentCatalog(), nil)
	objections, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, newCoordinateSet(view), view, componentTypeResolvesToACatalogEntry)
	if err != nil {
		t.Fatalf("the node walk returned an error: %v", err)
	}

	want := []string{".spec.components.loud", ".spec.components.quiet"}
	if got := locatorsOf(objections); !equalStrings(got, want) {
		t.Errorf("raised sequence = %v; want %v — one report per component, whatever it authored", got, want)
	}
}

// ------------------------------------------------------------------ the rule
// about an attachment both documents declare under one name

// mergeAggregate builds one component carrying the supplied attachment block.
func mergeAggregate(features types.OrderedMap[types.FeatureInstance]) *types.Aggregate {
	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set("web", types.ComponentInstance{Type: "core/web:1.0.0", Features: features})
	return &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "attachment-merge"},
		Spec:     types.AggregateSpec{Components: comps},
	}
}

// boundaryDecl is one boundary of the Release and the attachment types it
// declares against the one component, in author order.
type boundaryDecl struct {
	name        string
	attachments []namedType
}

// mergeRelease builds a Release whose named boundaries each write against the
// one component, carrying the attachment types each boundary declares for it.
func mergeRelease(boundaries []boundaryDecl) *types.Release {
	declared := types.OrderedMap[types.Boundary]{}
	for _, b := range boundaries {
		feats := types.OrderedMap[types.BoundaryFeature]{}
		for _, a := range b.attachments {
			feats.Set(a.name, types.BoundaryFeature{Type: a.typ})
		}
		comps := types.OrderedMap[types.BoundaryComponent]{}
		comps.Set("web", types.BoundaryComponent{Features: feats})
		declared.Set(b.name, types.Boundary{Components: comps})
	}
	return &types.Release{
		Metadata: types.ReleaseMetadata{Name: "attachment-merge"},
		Spec:     types.ReleaseSpec{Boundaries: declared},
	}
}

func TestTheAttachmentTypeAgreementRuleReportsOnlyANameBothDocumentsType(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// authored is the attachment block the Aggregate declares, in author
		// order.
		authored []namedType
		// boundaries is what the Release writes against the same component.
		boundaries []boundaryDecl
		raised     []string
	}{
		{
			// Every legitimate shape sits beside the one conflict, so the skips
			// are exercised by their absence from the expectation.
			name: "only a name both documents type, with the two types differing, is reported",
			authored: []namedType{
				{"conflicting", "core/audit:1.0.0"},
				{"agreeing", "core/metrics:1.0.0"},
				{"untyped", ""},
				{"application_only", "core/tracing:1.0.0"},
			},
			boundaries: []boundaryDecl{{
				name: "edge",
				attachments: []namedType{
					{"conflicting", "core/audit:2.0.0"},
					{"agreeing", "core/metrics:1.0.0"},
					{"untyped", "core/whatever:1.0.0"},
					{"deployment_only", "core/backup:1.0.0"},
				},
			}},
			raised: []string{".spec.components.web.features.conflicting"},
		},
		{
			// Two conflicts, authored against their alphabetical order, so the
			// row can tell an author-order sequence from a sorted one.
			name: "several conflicts come back in author order",
			authored: []namedType{
				{"zebra", "core/z:1.0.0"},
				{"antelope", "core/a:1.0.0"},
			},
			boundaries: []boundaryDecl{{
				name: "edge",
				attachments: []namedType{
					{"zebra", "core/z:2.0.0"},
					{"antelope", "core/a:2.0.0"},
				},
			}},
			raised: []string{
				".spec.components.web.features.zebra",
				".spec.components.web.features.antelope",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.raised) == 0 {
				t.Fatal("this row expects nothing, so it cannot tell a working rule from one " +
					"that reports nothing whatever it is given")
			}

			features := types.OrderedMap[types.FeatureInstance]{}
			for _, a := range tc.authored {
				features.Set(a.name, types.FeatureInstance{Type: a.typ})
			}
			na := normalized(t, mergeAggregate(features))
			release := mergeRelease(tc.boundaries)
			view := newNormalizedView(na, release, CatalogLookup{}, nil)
			coordinates := newCoordinateSet(view)

			objections, err := constraint.RunOnNodeAttachments[constraint.NormalizedView](
				nil, coordinates, coordinates, view, attachmentTypeAgreesAcrossDocuments)
			if err != nil {
				t.Fatalf("the attachment walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, tc.raised) {
				t.Errorf("raised sequence = %v; want %v", got, tc.raised)
			}
		})
	}
}

// One component may be declared in exactly one boundary, and the Release
// document is refused at read time when it is declared in two.
//
// 🔴 The rule above reads one boundary's whole attachment block per component,
// last write winning. That reading is complete only because no document reaching
// it names one component from two boundaries — so what makes the rule correct is
// this gate rather than anything in the rule, and the gate is what is asserted. If
// it is ever relaxed, this test turns red and the rule's fold becomes a real
// question to settle.
func TestOneComponentInTwoBoundariesIsRefusedWhenTheDocumentIsRead(t *testing.T) {
	t.Parallel()

	// Both documents below are complete and well-formed apart from the one fault
	// under test, so the refusal is attributable to the second placement rather
	// than to an earlier gate rejecting something else.
	const twoBoundaries = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: attachment-merge
  environment: prod
  version: "1.0.0"
spec:
  aggregate: attachment-merge
  manifest: attachment-merge-manifest
  boundaries:
    edge:
      directory: ./output
      components:
        web:
          features:
            audit:
              type: core/audit:2.0.0
    core:
      directory: ./output
      components:
        web:
          features:
            metrics:
              type: core/metrics:2.0.0
`

	_, err := LoadReleaseFromBytes([]byte(twoBoundaries), "release.yaml")
	if err == nil {
		t.Fatal("a Release placing one component in two boundaries was accepted.\n" +
			"INVARIANT: each component is declared in exactly one boundary, refused at read time.\n" +
			"CONSEQUENCE: the attachment-type rule folds that component's attachments to one " +
			"boundary's block, so it would read only one of the two and report no conflict.")
	}
	if !errors.Is(err, ErrDuplicateBoundaryComponent()) {
		t.Fatalf("the refusal does not carry the rule's identity: %v", err)
	}

	// The positive control: the same document with the second placement removed
	// must load, or the refusal above would be reporting some other fault and
	// this test would pass for the wrong reason.
	const oneBoundary = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: attachment-merge
  environment: prod
  version: "1.0.0"
spec:
  aggregate: attachment-merge
  manifest: attachment-merge-manifest
  boundaries:
    edge:
      directory: ./output
      components:
        web:
          features:
            audit:
              type: core/audit:2.0.0
            metrics:
              type: core/metrics:2.0.0
`
	if _, err := LoadReleaseFromBytes([]byte(oneBoundary), "release.yaml"); err != nil {
		t.Fatalf("the same content in one boundary must load, or the refusal above is not "+
			"attributable to the second placement: %v", err)
	}
}

// ------------------------------------------------------------------ the rule
// about a delivery an attachment slot does not permit

// namedFill pairs an attachment name with the fill authored under it, in author
// order.
type namedFill struct {
	name string
	fill types.FeatureInstance
}

// slotShapeCatalog returns a CatalogLookup whose one Component declares a slot
// reserving delivery to the Recipe (an empty allowlist, which is the most
// constrained answer rather than an absent one) and a slot permitting env and
// file.
func slotShapeCatalog() CatalogLookup {
	slots := types.OrderedMap[types.AttachmentSlot]{}
	slots.Set("root_password", types.AttachmentSlot{Required: true, Inject: []string{}})
	slots.Set("user_password", types.AttachmentSlot{Inject: []string{"env", "file"}})
	comp := types.Component{}
	comp.Metadata.Name = "core/datastore/db"
	comp.Spec.FeaturesSchema = slots
	return CatalogLookup{
		Component: func(uri string) (types.Component, bool) {
			if strings.HasPrefix(uri, "core/datastore/db") {
				return comp, true
			}
			return types.Component{}, false
		},
	}
}

// slotShapeAggregate wraps one component instance carrying the supplied
// attachment block.
func slotShapeAggregate(features types.OrderedMap[types.FeatureInstance]) types.NormalizedAggregate {
	inst := types.ComponentInstance{Type: "core/datastore/db:1.0.0", Features: features}
	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set("db", inst)
	agg := &types.Aggregate{}
	agg.Metadata.Name = "slot-shape"
	agg.Spec.Components = comps
	return types.Normalize(agg)
}

// fill builds one attachment: the Feature it names, the delivery it dictates, and
// the description an author may write on any attachment.
func fill(t string, injectEnv, injectArg bool, description string) types.FeatureInstance {
	fi := types.FeatureInstance{Type: t, Description: description}
	if injectEnv || injectArg {
		fi.Inject = &types.InjectBlock{}
		entry := []any{map[string]any{"in": "PW", "from": "value"}}
		if injectEnv {
			fi.Inject.Env = entry
		}
		if injectArg {
			fi.Inject.Arg = entry
		}
	}
	return fi
}

func TestTheSlotPermittedDeliveryRuleReportsEachMechanismTheSlotRefuses(t *testing.T) {
	t.Parallel()

	catalog := slotShapeCatalog()

	// A fill dictating a mechanism the product does not name as one of its own.
	// The block carries such a key alongside the ones it types, and a reading that
	// walked only the typed ones would miss it.
	customMechanismFill := types.FeatureInstance{Type: "core/secret-value"}
	customMechanismFill.Inject = &types.InjectBlock{}
	customMechanismFill.Inject.Additional.Set("flag", []any{map[string]any{"in": "PW", "from": "value"}})

	for _, tc := range []struct {
		name string
		// attachments is the block the Aggregate declares, in author order.
		attachments []namedFill
		raised      []string
	}{
		{
			// The free attachment sits beside the violating fill so the open
			// outer object is exercised by its absence from the expectation
			// rather than by a row that reports nothing.
			name: "a mechanism outside the slot's allowlist is reported; a free attachment dictating the same one is not",
			attachments: []namedFill{
				{"user_password", fill("core/secret-value", false, true, "")},
				{"some_monitoring", fill("core/prometheus-exporter", false, true, "")},
			},
			raised: []string{".spec.components.db.features.user_password"},
		},
		{
			// The allowlisted, documented fill sits beside the reserved slot's
			// violating one, for the same reason.
			name: "a slot reserving delivery to the Recipe reports any delivery; an allowlisted documented fill is not",
			attachments: []namedFill{
				{"root_password", fill("core/secret-value", true, false, "")},
				{"user_password", fill("core/secret-value", true, false, "why this credential is here")},
			},
			raised: []string{".spec.components.db.features.root_password"},
		},
		{
			name: "a mechanism the product does not name as one of its own is reported",
			attachments: []namedFill{
				{"user_password", customMechanismFill},
			},
			raised: []string{".spec.components.db.features.user_password"},
		},
		{
			// A fill on the reserved slot that dictates NO delivery is what that
			// slot's own contract expects, so it is accepted — and it sits beside
			// the violating one rather than alone, so the acceptance is proven by
			// its absence from a non-empty expectation.
			//
			// The description written on it is legal on any attachment, so a shape
			// check narrower than the envelope would reject a document the entity
			// schemas admit.
			name: "a documented fill dictating no delivery is accepted on the slot that reserves delivery",
			attachments: []namedFill{
				{"root_password", fill("core/secret-value", false, false, "why this credential is here")},
				{"user_password", fill("core/secret-value", false, true, "")},
			},
			raised: []string{".spec.components.db.features.user_password"},
		},
		{
			// Two reports, authored against their alphabetical order, so this
			// row can tell an author-order sequence from a sorted one. The rows
			// above each carry a single report, where every ordering agrees.
			name: "several reports come back in author order",
			attachments: []namedFill{
				{"user_password", fill("core/secret-value", false, true, "")},
				{"root_password", fill("core/secret-value", true, false, "")},
			},
			raised: []string{
				".spec.components.db.features.user_password",
				".spec.components.db.features.root_password",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.raised) == 0 {
				t.Fatal("this row expects nothing, so it cannot tell a working rule from one " +
					"that reports nothing whatever it is given")
			}

			features := types.OrderedMap[types.FeatureInstance]{}
			for _, a := range tc.attachments {
				features.Set(a.name, a.fill)
			}
			na := slotShapeAggregate(features)
			view := newNormalizedView(na, nil, catalog, nil)
			coordinates := newCoordinateSet(view)

			objections, err := constraint.RunOnNodeAttachments[constraint.NormalizedView](
				nil, coordinates, coordinates, view, attachmentDeliveryIsSlotPermitted)
			if err != nil {
				t.Fatalf("the attachment walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, tc.raised) {
				t.Errorf("raised sequence = %v; want %v", got, tc.raised)
			}
		})
	}
}

// TestTheSlotPermittedDeliveryRuleIsSilentAboutSlotsNobodyFilled pins what this
// rule is NOT about, and it needs saying because the slot it stays silent about
// is declared required.
//
// Presence is not this rule's question, structurally: the rule is asked once per
// attachment the component's document declares, so a slot nothing filled presents
// no coordinate and the rule is never asked. That is correct rather than a gap —
// an attachment contributed at a broader scope binds the slot while leaving this
// block empty, so a presence claim here would reject the shared form. The
// requirement is evaluated over the finalised attachment set instead.
//
// The positive control is the same document's other attachment, which does
// violate: without it the silence below would be the rule reading nothing at all.
func TestTheSlotPermittedDeliveryRuleIsSilentAboutSlotsNobodyFilled(t *testing.T) {
	t.Parallel()

	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("user_password", fill("core/secret-value", false, true, ""))

	na := slotShapeAggregate(features)
	view := newNormalizedView(na, nil, slotShapeCatalog(), nil)
	coordinates := newCoordinateSet(view)

	objections, err := constraint.RunOnNodeAttachments[constraint.NormalizedView](
		nil, coordinates, coordinates, view, attachmentDeliveryIsSlotPermitted)
	if err != nil {
		t.Fatalf("the attachment walk returned an error: %v", err)
	}
	want := []string{".spec.components.db.features.user_password"}
	if got := locatorsOf(objections); !equalStrings(got, want) {
		t.Fatalf("raised sequence = %v; want %v — the control that keeps the silence below "+
			"attributable", got, want)
	}
	for _, o := range objections {
		if strings.Contains(o.Locator(), "root_password") {
			t.Errorf("the rule reported the required slot %q that nothing filled: %v",
				"root_password", o.Locator())
		}
	}
}

// TestTheSlotPermittedDeliveryRuleEndsWhereNoSlotIsDeclared pins the two inputs
// that leave the rule with no slot to measure against: an entity declaring no
// slots at all, and a build consulting no catalog.
//
// Both are stated because they read alike and arrive differently — the first is a
// document about an entity that constrains nothing, the second an input no
// shipping build produces. The positive control is the same attachment block
// against the entity that DOES declare the slot, so neither silence can be the
// walk finding nothing to ask about.
func TestTheSlotPermittedDeliveryRuleEndsWhereNoSlotIsDeclared(t *testing.T) {
	t.Parallel()

	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("root_password", fill("core/secret-value", true, false, ""))
	na := slotShapeAggregate(features)

	ask := func(t *testing.T, catalog CatalogLookup) []string {
		t.Helper()
		view := newNormalizedView(na, nil, catalog, nil)
		coordinates := newCoordinateSet(view)
		objections, err := constraint.RunOnNodeAttachments[constraint.NormalizedView](
			nil, coordinates, coordinates, view, attachmentDeliveryIsSlotPermitted)
		if err != nil {
			t.Fatalf("the attachment walk returned an error: %v", err)
		}
		return locatorsOf(objections)
	}

	want := []string{".spec.components.db.features.root_password"}
	if got := ask(t, slotShapeCatalog()); !equalStrings(got, want) {
		t.Fatalf("control sequence = %v; want %v — without it the silences below are the walk "+
			"reading nothing", got, want)
	}

	slotless := CatalogLookup{Component: func(string) (types.Component, bool) {
		return types.Component{}, true
	}}
	if got := ask(t, slotless); len(got) != 0 {
		t.Errorf("an entity declaring no slots constrains nothing; got %v", got)
	}
	if got := ask(t, CatalogLookup{}); len(got) != 0 {
		t.Errorf("a build consulting no catalog has no slot declaration to measure against; got %v", got)
	}
}

// ------------------------------------------------------------------ the rules
// about a socket name an attachment or a Release edge wires

// The types the socket-name rows author against. Two component types and two
// Feature types the catalog holds, and one of each it does not, so a row
// exercises the catalog skip by naming a type rather than by emptying the
// catalog — which is a different input and one no shipping build produces.
const (
	socketWebType         = "core/web:1.0.0"
	socketCacheType       = "core/cache:1.0.0"
	socketAuditType       = "core/audit:1.0.0"
	socketPlainType       = "core/plain:1.0.0"
	socketAbsentFeature   = "core/absent-feature:1.0.0"
	socketAbsentComponent = "core/absent-component:1.0.0"
)

// socketNameRuleCatalog is the catalog the socket-name rows consult: one component type
// declaring one dependency socket, one Feature declaring one, and one Feature
// declaring none at all — which is the most constrained answer rather than an
// absent one, so every socket wired on it is refused.
func socketNameRuleCatalog() CatalogLookup {
	componentSockets := types.OrderedMap[types.UsesSchemaEntry]{}
	componentSockets.Set("db", types.UsesSchemaEntry{})
	web := types.Component{}
	web.Metadata.Name = "core/web"
	web.Spec.UsesSchema = componentSockets

	cache := types.Component{}
	cache.Metadata.Name = "core/cache"

	attachmentSockets := types.OrderedMap[types.UsesSchemaEntry]{}
	attachmentSockets.Set("sink", types.UsesSchemaEntry{})
	audit := types.Feature{}
	audit.Metadata.Name = "core/audit"
	audit.Spec.UsesSchema = attachmentSockets

	plain := types.Feature{}
	plain.Metadata.Name = "core/plain"

	return CatalogLookup{
		Component: func(uri string) (types.Component, bool) {
			switch uri {
			case socketWebType:
				return web, true
			case socketCacheType:
				return cache, true
			}
			return types.Component{}, false
		},
		Feature: func(uri string) (types.Feature, bool) {
			switch uri {
			case socketAuditType:
				return audit, true
			case socketPlainType:
				return plain, true
			}
			return types.Feature{}, false
		},
	}
}

// wiredSocketOfObjection returns the socket name a socket-name rule carried in
// the objection it raised.
//
// Every declaration of that rule raises with the attached type first and the
// offending socket second, which is the position read here. A detail shorter
// than that yields the empty string rather than a plausible key, so a malformed
// objection cannot be folded onto a real one's coverage.
func wiredSocketOfObjection(o constraint.Objection) string {
	detail := o.Detail()
	if len(detail) < 2 {
		return ""
	}
	return detail[1]
}

// declaredSocketsOf returns the socket each objection names, in the order raised.
func declaredSocketsOf(objections []constraint.Objection) []string {
	out := make([]string, 0, len(objections))
	for _, o := range objections {
		out = append(out, wiredSocketOfObjection(o))
	}
	return out
}

// wiredAttachment is one attachment of an Aggregate document: the Feature it
// attaches and the sockets it wires, in author order.
func wiredAttachment(featureType string, sockets ...string) types.FeatureInstance {
	fi := types.FeatureInstance{Type: featureType}
	for _, s := range sockets {
		fi.Uses.Set(s, types.FeatureUsesSocketValue{Compact: []string{"anything"}})
	}
	return fi
}

// attachmentBlock assembles an attachment block in the order its entries are
// written, so a fixture's author order is the order it reads in.
func attachmentBlock(entries ...namedFill) types.OrderedMap[types.FeatureInstance] {
	out := types.OrderedMap[types.FeatureInstance]{}
	for _, e := range entries {
		out.Set(e.name, e.fill)
	}
	return out
}

// socketAggregate builds one component carrying the supplied attachment block.
func socketAggregate(features types.OrderedMap[types.FeatureInstance]) *types.Aggregate {
	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set("web", types.ComponentInstance{Type: socketWebType, Features: features})
	return &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "socket-names"},
		Spec:     types.AggregateSpec{Components: comps},
	}
}

// releaseDocument wraps a boundary block in the envelope every Release
// document carries, so a row writes only the part under test.
//
// The envelope matters to the rows rather than being scaffolding: the document
// is read through the real loader below, and a row carrying a second fault in
// the envelope would be refused before the rule under test was ever reached.
func releaseDocument(boundaries string) string {
	return `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: socket-names
  environment: prod
  version: "1.0.0"
spec:
  aggregate: socket-names
  manifest: socket-names-manifest
  boundaries:
` + boundaries
}

// loadRelease reads a Release document through the real loader.
//
// 🔴 The rows below build their Release side this way rather than assembling
// the value directly, and the reason is measured rather than stylistic: the
// loader folds a top-level component surface into the synthesized boundary and
// refuses a document placing one component in two boundaries, so a value
// assembled beside it can encode a shape no Release reaching the rule ever
// has. A behaviour observed only in such a value is not the product's behaviour.
func loadRelease(t *testing.T, body string) *types.Release {
	t.Helper()
	release, err := LoadReleaseFromBytes([]byte(body), "release.yaml")
	if err != nil {
		t.Fatalf("the Release document this row is written against does not load, "+
			"so the row would be testing a shape the product refuses: %v", err)
	}
	return release
}

// The rule over an attachment written on a component.
func TestTheComponentAttachmentSocketRuleReportsASocketTheFeatureDoesNotDeclare(t *testing.T) {
	t.Parallel()

	catalog := socketNameRuleCatalog()

	for _, tc := range []struct {
		name        string
		attachments []namedFill
		raised      []string
		// sockets is the socket each objection carried as a detail value, in the
		// order raised. The locator now names the socket too, and the two are
		// independent channels rather than one restated: the framework assembles
		// the locator from the coordinate it invoked the rule for, while the detail
		// is what the rule itself raised about. Asserting both is what pins them to
		// the same socket, so a rule reading one coordinate and reporting another
		// cannot satisfy the row.
		sockets []string
	}{
		{
			// Every legitimate shape sits beside the one fault, so the skips are
			// exercised by their absence from the expectation rather than by a
			// row that reports nothing at all.
			name: "a socket the attached Feature does not declare is reported; one it declares, an attachment wiring nothing, and one whose Feature the catalog holds no entry for are not",
			attachments: []namedFill{
				{"audit", wiredAttachment(socketAuditType, "sink", "ghost")},
				{"quiet", wiredAttachment(socketAuditType)},
				{"mystery", wiredAttachment(socketAbsentFeature, "whatever")},
			},
			raised:  []string{".spec.components.web.features.audit.uses.ghost"},
			sockets: []string{"ghost"},
		},
		{
			// A Feature declaring no dependency socket at all is the most
			// constrained answer rather than an absent one, so every socket wired
			// on it is refused.
			name: "a Feature declaring no dependency socket refuses every socket wired on it",
			attachments: []namedFill{
				{"plain", wiredAttachment(socketPlainType, "anything")},
			},
			raised:  []string{".spec.components.web.features.plain.uses.anything"},
			sockets: []string{"anything"},
		},
		{
			// Two reports, authored against their alphabetical order, so this row
			// can tell an author-order sequence from a sorted one. The rows above
			// each carry a single report, where every ordering agrees.
			name: "several reports come back in author order",
			attachments: []namedFill{
				{"zulu", wiredAttachment(socketAuditType, "ghost")},
				{"alpha", wiredAttachment(socketAuditType, "ghost")},
			},
			raised: []string{
				".spec.components.web.features.zulu.uses.ghost",
				".spec.components.web.features.alpha.uses.ghost",
			},
			sockets: []string{"ghost", "ghost"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.raised) == 0 {
				t.Fatal("this row expects nothing, so it cannot tell a working rule from one " +
					"that reports nothing whatever it is given")
			}

			na := normalized(t, socketAggregate(attachmentBlock(tc.attachments...)))
			view := newNormalizedView(na, nil, catalog, nil)
			coordinates := newCoordinateSet(view)

			objections, err := constraint.RunOnNodeAttachmentSockets[constraint.NormalizedView](
				nil, coordinates, coordinates, coordinates, view,
				componentAttachmentSocketDeclared)
			if err != nil {
				t.Fatalf("the attachment socket walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, tc.raised) {
				t.Errorf("raised sequence = %v; want %v", got, tc.raised)
			}
			if got := declaredSocketsOf(objections); !equalStrings(got, tc.sockets) {
				t.Errorf("socket sequence = %v; want %v", got, tc.sockets)
			}
		})
	}
}

// The rule over an attachment the Release writes against a named component.
//
// 🔴 It is asked about every attachment name EITHER document declares on the
// component, and about every socket EITHER document wires under that name. The
// row for a name only the Release declares pins both, since the socket it
// reports exists only in the Release. A rule asked only about the
// Aggregate's names, or walked only over the Aggregate's sockets, would never
// be presented with such a subject, and silence from a rule never asked is
// indistinguishable from approval: no build goes red, and the enforcement is
// simply gone.
func TestTheReleaseComponentAttachmentSocketRuleReadsBothDocumentsNames(t *testing.T) {
	t.Parallel()

	catalog := socketNameRuleCatalog()

	for _, tc := range []struct {
		name string
		// authored is the attachment block the Aggregate declares. Its entries
		// wire nothing, so every report below comes from the Release's block.
		authored []namedFill
		// deployed is the boundary block of the Release document.
		deployed string
		raised   []string
		sockets  []string
	}{
		{
			name: "an attachment both documents declare is reported when the Release wires a socket the Feature does not declare",
			authored: []namedFill{
				{"audit", wiredAttachment(socketAuditType)},
			},
			deployed: `    edge:
      directory: ./output
      components:
        web:
          features:
            audit:
              type: core/audit:1.0.0
              uses:
                ghost: [anything]
`,
			raised:  []string{".spec.boundaries.edge.components.web.features.audit.uses.ghost"},
			sockets: []string{"ghost"},
		},
		{
			// 🔴 An attachment only the Release declares. The rule's own
			// contract is that adding such an attachment is supported, so it has
			// to be asked of the Release's declaration rather than inferred
			// from the Aggregate's — and the coordinate it is asked per names
			// the Aggregate's attachments first and then any the Release
			// declares that the Aggregate does not.
			//
			// The locator is rooted at the Release's own entry for the
			// component, in the boundary it sits in, because that is where the
			// offending text is: the rule is invoked over the set that positions
			// every attachment at the Release's declaration of it.
			name:     "an attachment only the Release declares is reported",
			authored: nil,
			deployed: `    edge:
      directory: ./output
      components:
        web:
          features:
            ops_audit:
              type: core/audit:1.0.0
              uses:
                ghost: [anything]
`,
			raised:  []string{".spec.boundaries.edge.components.web.features.ops_audit.uses.ghost"},
			sockets: []string{"ghost"},
		},
		{
			// Two reports, authored against their alphabetical order.
			name: "several reports come back in author order",
			authored: []namedFill{
				{"zulu", wiredAttachment(socketAuditType)},
				{"alpha", wiredAttachment(socketAuditType)},
			},
			deployed: `    edge:
      directory: ./output
      components:
        web:
          features:
            zulu:
              type: core/audit:1.0.0
              uses:
                ghost: [anything]
            alpha:
              type: core/audit:1.0.0
              uses:
                ghost: [anything]
`,
			raised: []string{
				".spec.boundaries.edge.components.web.features.zulu.uses.ghost",
				".spec.boundaries.edge.components.web.features.alpha.uses.ghost",
			},
			sockets: []string{"ghost", "ghost"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.raised) == 0 {
				t.Fatal("this row expects nothing, so it cannot tell a working rule from one " +
					"that reports nothing whatever it is given")
			}

			na := normalized(t, socketAggregate(attachmentBlock(tc.authored...)))
			release := loadRelease(t, releaseDocument(tc.deployed))
			view := newNormalizedView(na, release, catalog, nil)
			coordinates := newCoordinateSet(view)

			// Invoked over the set the resolve hands the Release's own rules, so the
			// row positions its findings exactly as a resolve does.
			objections, err := constraint.RunOnNodeAttachmentSockets[constraint.NormalizedView](
				nil, coordinates, coordinates.inRelease(), coordinates, view,
				releaseComponentAttachmentSocketDeclared)
			if err != nil {
				t.Fatalf("the attachment socket walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, tc.raised) {
				t.Errorf("raised sequence = %v; want %v", got, tc.raised)
			}
			if got := declaredSocketsOf(objections); !equalStrings(got, tc.sockets) {
				t.Errorf("socket sequence = %v; want %v", got, tc.sockets)
			}
		})
	}
}

// nestedScope builds one declared scope carrying an attachment block, wrapped in
// the outer scopes named before it, and returns the outermost scope's own name
// beside it.
func nestedScope(
	attachments types.OrderedMap[types.FeatureInstance], levels ...string,
) (string, types.Subsystem) {
	sub := types.Subsystem{Features: attachments}
	for i := len(levels) - 1; i > 0; i-- {
		inner := types.OrderedMap[types.Subsystem]{}
		inner.Set(levels[i], sub)
		sub = types.Subsystem{Subsystems: inner}
	}
	return levels[0], sub
}

// scopeDecl is one declared scope of the Aggregate document: the nesting the
// author wrote it under, and the attachments it carries.
type scopeDecl struct {
	levels      []string
	attachments []namedFill
}

// scopedAggregate builds an Aggregate document declaring the supplied scopes,
// in the order they are written.
func scopedAggregate(scopes []scopeDecl) *types.Aggregate {
	subs := types.OrderedMap[types.Subsystem]{}
	for _, s := range scopes {
		name, sub := nestedScope(attachmentBlock(s.attachments...), s.levels...)
		subs.Set(name, sub)
	}
	return &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "socket-names"},
		Spec:     types.AggregateSpec{Subsystems: subs},
	}
}

// The rule over an attachment written at a scope of the Aggregate document.
func TestTheScopeAttachmentSocketRuleNamesEveryLevelOfANestedScope(t *testing.T) {
	t.Parallel()

	catalog := socketNameRuleCatalog()

	for _, tc := range []struct {
		name    string
		scopes  []scopeDecl
		raised  []string
		sockets []string
	}{
		{
			name: "a socket the attached Feature does not declare is reported; one it declares is not",
			scopes: []scopeDecl{{
				levels: []string{"data"},
				attachments: []namedFill{
					{"audit", wiredAttachment(socketAuditType, "sink", "ghost")},
				},
			}},
			raised:  []string{".spec.subsystems.data.features.audit"},
			sockets: []string{"ghost"},
		},
		{
			// A nested scope repeats the container's own key between the levels,
			// naming a path the document contains. A flat scope cannot tell a
			// repeated container key from a joined path, which is why this row
			// carries the claim and the one above cannot.
			name: "a scope nested inside another names every level",
			scopes: []scopeDecl{{
				levels: []string{"data", "warm"},
				attachments: []namedFill{
					{"audit", wiredAttachment(socketAuditType, "ghost")},
				},
			}},
			raised:  []string{".spec.subsystems.data.subsystems.warm.features.audit"},
			sockets: []string{"ghost"},
		},
		{
			// Two scopes, authored against their alphabetical order.
			name: "several reports come back in author order",
			scopes: []scopeDecl{
				{
					levels:      []string{"zulu"},
					attachments: []namedFill{{"audit", wiredAttachment(socketAuditType, "ghost")}},
				},
				{
					levels:      []string{"alpha"},
					attachments: []namedFill{{"audit", wiredAttachment(socketAuditType, "ghost")}},
				},
			},
			raised: []string{
				".spec.subsystems.zulu.features.audit",
				".spec.subsystems.alpha.features.audit",
			},
			sockets: []string{"ghost", "ghost"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.raised) == 0 {
				t.Fatal("this row expects nothing, so it cannot tell a working rule from one " +
					"that reports nothing whatever it is given")
			}

			na := normalized(t, scopedAggregate(tc.scopes))
			view := newNormalizedView(na, nil, catalog, nil)
			coordinates := newCoordinateSet(view)

			objections, err := constraint.RunOnSubsystemAttachments[constraint.NormalizedView](
				nil, coordinates, coordinates, view, subsystemAttachmentSocketDeclared)
			if err != nil {
				t.Fatalf("the scope walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, tc.raised) {
				t.Errorf("raised sequence = %v; want %v", got, tc.raised)
			}
			if got := declaredSocketsOf(objections); !equalStrings(got, tc.sockets) {
				t.Errorf("socket sequence = %v; want %v", got, tc.sockets)
			}
		})
	}
}

// The rule over an attachment written at a scope of the Release document.
func TestTheReleaseScopeAttachmentSocketRuleNamesEveryLevelOfANestedScope(t *testing.T) {
	t.Parallel()

	catalog := socketNameRuleCatalog()

	for _, tc := range []struct {
		name     string
		deployed string
		raised   []string
		sockets  []string
	}{
		{
			name: "a socket the attached Feature does not declare is reported; one it declares is not",
			deployed: `    edge:
      directory: ./output
      features:
        audit:
          type: core/audit:1.0.0
          uses:
            sink: [anything]
            ghost: [anything]
`,
			raised:  []string{".spec.boundaries.edge.features.audit"},
			sockets: []string{"ghost"},
		},
		{
			// The nested scope names a path the document contains, level by
			// level, rather than the separator-joined key the compiler composes
			// a scope path with — which is a key no file has.
			name: "a scope nested inside another names every level",
			deployed: `    edge:
      directory: ./output
      boundaries:
        inner:
          directory: ./output
          features:
            audit:
              type: core/audit:1.0.0
              uses:
                ghost: [anything]
`,
			raised:  []string{".spec.boundaries.edge.boundaries.inner.features.audit"},
			sockets: []string{"ghost"},
		},
		{
			// Two scopes, authored against their alphabetical order.
			name: "several reports come back in author order",
			deployed: `    zulu:
      directory: ./output
      features:
        audit:
          type: core/audit:1.0.0
          uses:
            ghost: [anything]
    alpha:
      directory: ./output
      features:
        tracing:
          type: core/audit:1.0.0
          uses:
            ghost: [anything]
`,
			raised: []string{
				".spec.boundaries.zulu.features.audit",
				".spec.boundaries.alpha.features.tracing",
			},
			sockets: []string{"ghost", "ghost"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.raised) == 0 {
				t.Fatal("this row expects nothing, so it cannot tell a working rule from one " +
					"that reports nothing whatever it is given")
			}

			na := normalized(t, socketAggregate(types.OrderedMap[types.FeatureInstance]{}))
			release := loadRelease(t, releaseDocument(tc.deployed))
			view := newNormalizedView(na, release, catalog, nil)
			coordinates := newCoordinateSet(view)

			objections, err := constraint.RunOnBoundaryAttachments[constraint.NormalizedView](
				nil, coordinates, coordinates, view, boundaryAttachmentSocketDeclared)
			if err != nil {
				t.Fatalf("the scope walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, tc.raised) {
				t.Errorf("raised sequence = %v; want %v", got, tc.raised)
			}
			if got := declaredSocketsOf(objections); !equalStrings(got, tc.sockets) {
				t.Errorf("socket sequence = %v; want %v", got, tc.sockets)
			}
		})
	}
}

// edgeAggregate builds the two components the Release-edge rows wire between.
// The consuming component's type is supplied so a row can name one the catalog
// holds no entry for.
func edgeAggregate(consumerType string) *types.Aggregate {
	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set("web", types.ComponentInstance{Type: consumerType})
	comps.Set("cache", types.ComponentInstance{Type: socketCacheType})
	return &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "socket-names"},
		Spec:     types.AggregateSpec{Components: comps},
	}
}

// The rule over a socket the Release's own edge surface wires.
//
// 🔴 The rule raises once per offending SOCKET rather than once per entry written
// under it, so its locator sequence is degenerate — every objection names the same
// component. The socket sequence is asserted beside it: without that, an ordering
// change would be invisible.
func TestTheReleaseEdgeSocketRuleReportsOncePerOffendingSocket(t *testing.T) {
	t.Parallel()

	catalog := socketNameRuleCatalog()

	for _, tc := range []struct {
		name     string
		deployed string
		// raised is the site the rule names, once per objection. It is the
		// component, not the socket.
		raised []string
		// sockets is the socket each objection carries, in the order raised —
		// the sequence the locators cannot express.
		sockets []string
	}{
		{
			// The declared socket and the component the Aggregate does not
			// declare sit beside the one fault, so both skips are exercised by
			// their absence from the expectation.
			name: "a socket the consuming component's type does not declare is reported; a declared one and a component the Aggregate never declared are not",
			deployed: `    edge:
      directory: ./output
      components:
        web:
          uses:
            db:
              primary:
                target: cache
            ghost:
              spare:
                target: cache
        stranger:
          uses:
            whatever:
              x:
                target: cache
`,
			raised:  []string{".spec.components.web"},
			sockets: []string{"ghost"},
		},
		{
			// The multiplicity claim, written out: two entries under one
			// undeclared socket draw ONE objection, because the socket is what is
			// undeclared and the entries under it are not each a separate fault.
			name: "two entries under one undeclared socket draw one report",
			deployed: `    edge:
      directory: ./output
      components:
        web:
          uses:
            ghost:
              first:
                target: cache
              second:
                target: cache
`,
			raised:  []string{".spec.components.web"},
			sockets: []string{"ghost"},
		},
		{
			// Two sockets, authored against their alphabetical order.
			name: "several reports come back in author order",
			deployed: `    edge:
      directory: ./output
      components:
        web:
          uses:
            zulu:
              a:
                target: cache
            alpha:
              b:
                target: cache
`,
			raised:  []string{".spec.components.web", ".spec.components.web"},
			sockets: []string{"zulu", "alpha"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.raised) == 0 {
				t.Fatal("this row expects nothing, so it cannot tell a working rule from one " +
					"that reports nothing whatever it is given")
			}

			na := normalized(t, edgeAggregate(socketWebType))
			release := loadRelease(t, releaseDocument(tc.deployed))
			view := newNormalizedView(na, release, catalog, nil)
			coordinates := newCoordinateSet(view)

			objections, err := constraint.RunOnNodes[constraint.NormalizedView](
				nil, coordinates, view, releaseEdgeSocketDeclared)
			if err != nil {
				t.Fatalf("the node walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, tc.raised) {
				t.Errorf("raised locator sequence = %v; want %v", got, tc.raised)
			}
			if got := declaredSocketsOf(objections); !equalStrings(got, tc.sockets) {
				t.Errorf("raised socket sequence = %v; want %v", got, tc.sockets)
			}
		})
	}
}

// A Release wiring an edge on a component whose type the catalog holds no
// entry for ends this rule: a type nobody found declares an empty socket set, and
// refusing every socket wired on it would report the wrong thing. The rule about
// a type resolved as a catalog identity nobody found reports that fault instead,
// once per component.
//
// The control is the same document against a type the catalog holds, so the
// silence is attributable to the catalog rather than to the walk finding nothing.
func TestTheReleaseEdgeSocketRuleEndsWhereTheCatalogHoldsNoEntryForTheConsumer(t *testing.T) {
	t.Parallel()

	catalog := socketNameRuleCatalog()
	release := loadRelease(t, releaseDocument(`    edge:
      directory: ./output
      components:
        web:
          uses:
            ghost:
              spare:
                target: cache
`))

	na := normalized(t, edgeAggregate(socketAbsentComponent))
	view := newNormalizedView(na, release, catalog, nil)
	coordinates := newCoordinateSet(view)
	objections, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, coordinates, view, releaseEdgeSocketDeclared)
	if err != nil {
		t.Fatalf("the node walk returned an error: %v", err)
	}
	if len(objections) != 0 {
		t.Errorf("the socket rule reported on a type the catalog holds no entry for: %v",
			locatorsOf(objections))
	}

	// The fault that IS reported for that document, by the rule whose subject it
	// is — one report for the consuming component, and one for the component the
	// document also declares of a type the catalog does hold no entry for.
	absent, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, coordinates, view, componentTypeResolvesToACatalogEntry)
	if err != nil {
		t.Fatalf("the node walk returned an error: %v", err)
	}
	if want := []string{".spec.components.web"}; !equalStrings(locatorsOf(absent), want) {
		t.Errorf("the catalog-entry rule raised %v; want %v", locatorsOf(absent), want)
	}

	held := normalized(t, edgeAggregate(socketWebType))
	heldView := newNormalizedView(held, release, catalog, nil)
	control, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, newCoordinateSet(heldView), heldView, releaseEdgeSocketDeclared)
	if err != nil {
		t.Fatalf("the control node walk returned an error: %v", err)
	}
	if len(control) != 1 {
		t.Fatalf("control objections = %d; want 1, or the input above reports nothing for a "+
			"reason other than the catalog", len(control))
	}
}

// TestTheAttachmentSocketRuleReportsInAStableOrderOverRepeatedReads holds the
// enumeration to Invariant 8 across reads of ONE input, which a single-read
// sequence assertion structurally cannot: a walk that iterated a Go map would
// satisfy any one read and report a different sequence on the next.
//
// 🔴 The two axes this rule is invoked across order differently, and the fixture
// is built so that the expectation states both. COMPONENTS come back in
// instance-name order, so the document authors them against that order and a walk
// preserving the author's would be visible. The ATTACHMENTS a component carries
// come back in the order it declares them, which the per-rule table above pins —
// so this fixture gives each component one attachment and carries the component
// claim alone.
func TestTheAttachmentSocketRuleReportsInAStableOrderOverRepeatedReads(t *testing.T) {
	t.Parallel()

	comps := types.OrderedMap[types.ComponentInstance]{}
	for _, name := range []string{"zebra", "alpha", "mid"} {
		comps.Set(name, types.ComponentInstance{
			Type:     socketWebType,
			Features: attachmentBlock(namedFill{"audit", wiredAttachment(socketPlainType, "anything")}),
		})
	}
	na := normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "socket-names"},
		Spec:     types.AggregateSpec{Components: comps},
	})
	catalog := socketNameRuleCatalog()

	want := []string{
		".spec.components.alpha.features.audit.uses.anything",
		".spec.components.mid.features.audit.uses.anything",
		".spec.components.zebra.features.audit.uses.anything",
	}
	for i := 0; i < 10; i++ {
		view := newNormalizedView(na, nil, catalog, nil)
		coordinates := newCoordinateSet(view)
		objections, err := constraint.RunOnNodeAttachmentSockets[constraint.NormalizedView](
			nil, coordinates, coordinates, coordinates, view,
			componentAttachmentSocketDeclared)
		if err != nil {
			t.Fatalf("read %d: the attachment socket walk returned an error: %v", i, err)
		}
		if got := locatorsOf(objections); !equalStrings(got, want) {
			t.Fatalf("read %d raised %v; want %v — identical input must yield an identical "+
				"sequence on every read", i, got, want)
		}
	}
}

// ------------------------------------------------------------------ the rules
// about a delivery entry reading from a key a component type does not publish

// componentWithProvides builds a catalog Component whose contract publishes the
// given keys (each a string-typed compact-form property).
func componentWithProvides(t *testing.T, keys ...string) types.Component {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("provides:\n")
	if len(keys) == 0 {
		sb.WriteString("  {}\n")
	}
	for _, k := range keys {
		sb.WriteString("  " + k + ":\n    type: string\n")
	}
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(sb.String()), &node); err != nil {
		t.Fatalf("building provides schema: %v", err)
	}
	comp := types.Component{}
	comp.Spec.Schema = node
	return comp
}

// injectCatalog is the catalog the delivery-reference rows consult: one
// component type publishing two keys, one publishing a single key, and one
// publishing none at all — the empty answer being the boundary case, so every
// reference against that type is refused.
func injectCatalog(t *testing.T) CatalogLookup {
	t.Helper()
	consumer := componentWithProvides(t, "host", "port")
	target := componentWithProvides(t, "password")
	opaque := componentWithProvides(t)
	return CatalogLookup{Component: func(uri string) (types.Component, bool) {
		switch uri {
		case socketWebType:
			return consumer, true
		case socketCacheType:
			return target, true
		case socketPlainType:
			return opaque, true
		}
		return types.Component{}, false
	}}
}

// verboseDelivery is one delivery entry in the array authoring form: the key
// delivered, and the published key it reads from. An empty reference writes a
// template instead, which is nothing to resolve and must be skipped.
func verboseDelivery(deliveryKey, reference string) map[string]any {
	if reference == "" {
		return map[string]any{"in": deliveryKey, "value": "a template"}
	}
	return map[string]any{"in": deliveryKey, "from": reference}
}

// deliveryBlock is what one injection block delivers, per mechanism, as the list
// of entries the inject_delivery definition describes. A list carries the
// author's own order, so a row can tell an answer that preserved it from an
// answer that sorted it.
//
// custom carries the entries of a mechanism the product does not name as one of
// its own, under the name given.
type deliveryBlock struct {
	env        []any
	arg        []any
	custom     []any
	customName string
}

// injectBlockOf assembles an injection block, or none where nothing is written.
func injectBlockOf(b deliveryBlock) *types.InjectBlock {
	if b.env == nil && b.arg == nil && b.custom == nil {
		return nil
	}
	out := &types.InjectBlock{}
	if b.env != nil {
		out.Env = b.env
	}
	if b.arg != nil {
		out.Arg = b.arg
	}
	if b.custom != nil {
		out.Additional.Set(b.customName, b.custom)
	}
	return out
}

// selfInjectAggregate builds one component whose own injection block delivers
// what the row wrote.
func selfInjectAggregate(componentType string, block deliveryBlock) *types.Aggregate {
	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set("web", types.ComponentInstance{Type: componentType, Inject: injectBlockOf(block)})
	return &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "delivery-references"},
		Spec:     types.AggregateSpec{Components: comps},
	}
}

// The rule over a delivery entry in a component's own injection block.
func TestTheSelfDeliveryReferenceRuleReportsAnEntryReadingAnUnpublishedKey(t *testing.T) {
	t.Parallel()

	catalog := injectCatalog(t)

	for _, tc := range []struct {
		name          string
		componentType string
		block         deliveryBlock
		raised        []string
	}{
		{
			// The entry reading a published key and the entry carrying a template
			// sit beside the one fault, so both skips are exercised by their
			// absence from the expectation.
			name:          "an entry reading from a key the type does not publish is reported; one reading a published key and one carrying a template are not",
			componentType: socketWebType,
			block: deliveryBlock{env: []any{
				verboseDelivery("DB_HOST", "host"),
				verboseDelivery("BAD", "nope"),
				verboseDelivery("LITERAL", ""),
			}},
			raised: []string{".spec.components.web.inject.env.BAD"},
		},
		{
			// Two reports, authored against their alphabetical order.
			name:          "several reports come back in author order",
			componentType: socketWebType,
			block: deliveryBlock{env: []any{
				verboseDelivery("ZED", "nope"),
				verboseDelivery("ALPHA", "nope"),
			}},
			raised: []string{
				".spec.components.web.inject.env.ZED",
				".spec.components.web.inject.env.ALPHA",
			},
		},
		{
			// Two mechanisms, so the row can tell the mechanism enumeration order
			// from a sort applied at the end.
			name:          "two mechanisms come back in the order the block declares them",
			componentType: socketWebType,
			block: deliveryBlock{
				env: []any{verboseDelivery("E", "nope")},
				arg: []any{verboseDelivery("A", "nope")},
			},
			raised: []string{
				".spec.components.web.inject.env.E",
				".spec.components.web.inject.arg.A",
			},
		},
		{
			// A mechanism the product does not name as one of its own carries
			// delivery entries the same way, and the rule reads them the same way
			// — a walk over the typed mechanisms alone would report nothing here.
			// The typed mechanism beside it is what keeps that attributable.
			name:          "an entry under a mechanism the product does not name as one of its own is reported",
			componentType: socketWebType,
			block: deliveryBlock{
				env:        []any{verboseDelivery("E", "nope")},
				custom:     []any{verboseDelivery("PW", "nope")},
				customName: "ssm_param",
			},
			raised: []string{
				".spec.components.web.inject.env.E",
				".spec.components.web.inject.ssm_param.PW",
			},
		},
		{
			// A type publishing nothing at all is the boundary case the rows
			// above cannot state: it is the most constrained answer rather than
			// an absent one, so every reference against it is refused.
			name:          "a type publishing nothing refuses every entry that reads from it",
			componentType: socketPlainType,
			block: deliveryBlock{env: []any{
				verboseDelivery("A", "host"),
				verboseDelivery("B", "port"),
			}},
			raised: []string{
				".spec.components.web.inject.env.A",
				".spec.components.web.inject.env.B",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.raised) == 0 {
				t.Fatal("this row expects nothing, so it cannot tell a working rule from one " +
					"that reports nothing whatever it is given")
			}

			na := normalized(t, selfInjectAggregate(tc.componentType, tc.block))
			view := newNormalizedView(na, nil, catalog, nil)
			coordinates := newCoordinateSet(view)

			objections, err := constraint.RunOnNodeMechanismDeliveryKeys[constraint.NormalizedView](
				nil, coordinates, coordinates, coordinates, view, selfInjectReferenceIsPublished)
			if err != nil {
				t.Fatalf("the delivery-key walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, tc.raised) {
				t.Errorf("raised sequence = %v; want %v", got, tc.raised)
			}
		})
	}
}

// A component whose type the catalog holds no entry for ends this rule: the
// published set reads empty for such a type exactly as it does for a type
// publishing nothing, and the rule about a type resolved as a catalog identity
// nobody found reports that fault instead.
//
// The control is the same three components against a type the catalog holds, so
// the silence is attributable to the catalog rather than to the walk reading
// nothing.
func TestTheSelfDeliveryReferenceRuleEndsWhereTheCatalogHoldsNoEntryForTheComponent(t *testing.T) {
	t.Parallel()

	const otherAbsent = "core/absent-other:1.0.0"
	catalog := injectCatalog(t)

	block := deliveryBlock{env: []any{verboseDelivery("K", "nope")}}
	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set("alpha", types.ComponentInstance{Type: socketAbsentComponent, Inject: injectBlockOf(block)})
	comps.Set("beta", types.ComponentInstance{Type: socketAbsentComponent, Inject: injectBlockOf(block)})
	comps.Set("gamma", types.ComponentInstance{Type: otherAbsent, Inject: injectBlockOf(block)})
	na := normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "delivery-references"},
		Spec:     types.AggregateSpec{Components: comps},
	})

	view := newNormalizedView(na, nil, catalog, nil)
	coordinates := newCoordinateSet(view)
	objections, err := constraint.RunOnNodeMechanismDeliveryKeys[constraint.NormalizedView](
		nil, coordinates, coordinates, coordinates, view, selfInjectReferenceIsPublished)
	if err != nil {
		t.Fatalf("the delivery-key walk returned an error: %v", err)
	}
	if len(objections) != 0 {
		t.Errorf("the delivery-reference rule reported against a type the catalog holds no "+
			"entry for: %v", locatorsOf(objections))
	}

	// The fault that IS reported, by the rule whose subject it is: once per
	// COMPONENT rather than once per type, which is the multiplicity a reader
	// needs because the absent type is shared by two of the three.
	absent, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, coordinates, view, componentTypeResolvesToACatalogEntry)
	if err != nil {
		t.Fatalf("the node walk returned an error: %v", err)
	}
	want := []string{
		".spec.components.alpha",
		".spec.components.beta",
		".spec.components.gamma",
	}
	if got := locatorsOf(absent); !equalStrings(got, want) {
		t.Errorf("the catalog-entry rule raised %v; want %v — one per component", got, want)
	}

	// The control. Without it the silence above is unfalsifiable: a rule that
	// raised nothing whatever the input would satisfy it forever.
	held := types.OrderedMap[types.ComponentInstance]{}
	held.Set("alpha", types.ComponentInstance{Type: socketWebType, Inject: injectBlockOf(block)})
	held.Set("beta", types.ComponentInstance{Type: socketWebType, Inject: injectBlockOf(block)})
	held.Set("gamma", types.ComponentInstance{Type: socketWebType, Inject: injectBlockOf(block)})
	control := normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "delivery-references"},
		Spec:     types.AggregateSpec{Components: held},
	})
	controlView := newNormalizedView(control, nil, catalog, nil)
	controlCoordinates := newCoordinateSet(controlView)
	controlObjections, err := constraint.RunOnNodeMechanismDeliveryKeys[constraint.NormalizedView](
		nil, controlCoordinates, controlCoordinates, controlCoordinates, controlView,
		selfInjectReferenceIsPublished)
	if err != nil {
		t.Fatalf("the control delivery-key walk returned an error: %v", err)
	}
	if len(controlObjections) != 3 {
		t.Fatalf("control objections = %d; want 3, or the silence above is the rule reading "+
			"nothing rather than the catalog ending it", len(controlObjections))
	}
}

// namedConnection is one connection of a component's own dependency block: the
// alias it was written under, the component it names (empty to leave the alias
// to supply it), and what it dictates for delivery.
type namedConnection struct {
	alias  string
	target string
	block  deliveryBlock
}

// connectionAggregate builds a consuming component wiring one dependency socket,
// beside the component its connections point at.
func connectionAggregate(connections []namedConnection) *types.Aggregate {
	socket := types.UsesSocketValue{}
	for _, c := range connections {
		socket.Verbose.Set(c.alias, types.VerboseUsesEntry{
			Target: c.target,
			Inject: injectBlockOf(c.block),
		})
	}
	web := types.ComponentInstance{Type: socketWebType}
	web.Uses.Set("db", socket)

	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set("web", web)
	comps.Set("cache", types.ComponentInstance{Type: socketCacheType})
	return &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "delivery-references"},
		Spec:     types.AggregateSpec{Components: comps},
	}
}

// declaredDeliveryPaths returns, per objection, the connection the rule named
// joined with the mechanism and the delivery key it carried — the full dotted
// path of the offending entry.
//
// The rule raises every objection for one connection at that connection's
// locator, so the locator sequence alone cannot express which entries were
// reported or in what order. This is the sequence that can.
func declaredDeliveryPaths(objections []constraint.Objection) []string {
	out := make([]string, 0, len(objections))
	for _, o := range objections {
		detail := o.Detail()
		if len(detail) < 2 {
			out = append(out, o.Locator())
			continue
		}
		out = append(out, o.Locator()+".inject."+detail[0]+"."+detail[1])
	}
	return out
}

// The rule over a delivery entry dictated on one connection.
//
// It sorts a connection's entries by delivery key rather than raising in author
// order, because only one of the shapes an author may write the payload in
// carries an order at all.
func TestTheConnectionDeliveryReferenceRuleReportsAnEntryReadingAnUnpublishedKey(t *testing.T) {
	t.Parallel()

	catalog := injectCatalog(t)

	for _, tc := range []struct {
		name        string
		connections []namedConnection
		// raised is the connection each objection names, once per objection —
		// degenerate where one connection draws several.
		raised []string
		// deliveries is the entry each objection names, in the order raised.
		deliveries []string
	}{
		{
			// The entry reading a published key, the connection dictating no
			// delivery at all, and the connection naming a component the
			// Aggregate does not declare all sit beside the one fault.
			name: "an entry reading from a key the target does not publish is reported; a published one, a connection dictating nothing and one naming no declared component are not",
			connections: []namedConnection{
				{alias: "primary", target: "cache", block: deliveryBlock{env: []any{
					verboseDelivery("PW", "password"),
					verboseDelivery("BAD", "nope"),
				}}},
				{alias: "spare", target: "cache"},
				{alias: "nowhere", target: "absent-node", block: deliveryBlock{env: []any{
					verboseDelivery("X", "nope"),
				}}},
			},
			raised:     []string{".spec.components.web.uses.db.primary"},
			deliveries: []string{".spec.components.web.uses.db.primary.inject.env.BAD"},
		},
		{
			// An entry naming no component falls back to the alias, which is the
			// same fallback that draws the edge.
			name: "a connection naming no component resolves against its own alias",
			connections: []namedConnection{
				{alias: "cache", block: deliveryBlock{env: []any{verboseDelivery("BAD", "nope")}}},
			},
			raised:     []string{".spec.components.web.uses.db.cache"},
			deliveries: []string{".spec.components.web.uses.db.cache.inject.env.BAD"},
		},
		{
			// Two entries of one mechanism, authored against their alphabetical
			// order. This is the only rule in this family that does NOT raise in
			// author order, and this row is what makes that measured.
			name: "entries of one mechanism come back in delivery-key order, not in author order",
			connections: []namedConnection{
				{alias: "primary", target: "cache", block: deliveryBlock{env: []any{
					verboseDelivery("ZED", "nope"),
					verboseDelivery("ALPHA", "nope"),
				}}},
			},
			raised: []string{
				".spec.components.web.uses.db.primary",
				".spec.components.web.uses.db.primary",
			},
			deliveries: []string{
				".spec.components.web.uses.db.primary.inject.env.ALPHA",
				".spec.components.web.uses.db.primary.inject.env.ZED",
			},
		},
		{
			// Two mechanisms, so the row can tell the mechanism enumeration order
			// from a sort over the whole set.
			name: "two mechanisms come back in the order the block declares them",
			connections: []namedConnection{
				{alias: "primary", target: "cache", block: deliveryBlock{
					env: []any{verboseDelivery("E", "nope")},
					arg: []any{verboseDelivery("A", "nope")},
				}},
			},
			raised: []string{
				".spec.components.web.uses.db.primary",
				".spec.components.web.uses.db.primary",
			},
			deliveries: []string{
				".spec.components.web.uses.db.primary.inject.env.E",
				".spec.components.web.uses.db.primary.inject.arg.A",
			},
		},
		{
			// A mechanism the product does not name as one of its own, on the
			// connection surface. The typed mechanism beside it keeps the report
			// attributable to the walk rather than to the mechanism's name.
			name: "an entry under a mechanism the product does not name as one of its own is reported",
			connections: []namedConnection{
				{alias: "primary", target: "cache", block: deliveryBlock{
					env:        []any{verboseDelivery("E", "nope")},
					custom:     []any{verboseDelivery("PW", "nope")},
					customName: "ssm_param",
				}},
			},
			raised: []string{
				".spec.components.web.uses.db.primary",
				".spec.components.web.uses.db.primary",
			},
			deliveries: []string{
				".spec.components.web.uses.db.primary.inject.env.E",
				".spec.components.web.uses.db.primary.inject.ssm_param.PW",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.deliveries) == 0 {
				t.Fatal("this row expects nothing, so it cannot tell a working rule from one " +
					"that reports nothing whatever it is given")
			}

			na := normalized(t, connectionAggregate(tc.connections))
			view := newNormalizedView(na, nil, catalog, nil)
			coordinates := newCoordinateSet(view)

			objections, err := constraint.RunOnEdges[constraint.NormalizedView](
				nil, coordinates, view, connectionInjectReferenceIsPublished)
			if err != nil {
				t.Fatalf("the connection walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, tc.raised) {
				t.Errorf("raised locator sequence = %v; want %v", got, tc.raised)
			}
			if got := declaredDeliveryPaths(objections); !equalStrings(got, tc.deliveries) {
				t.Errorf("raised entry sequence = %v; want %v", got, tc.deliveries)
			}
		})
	}
}

// ------------------------------------------------------------------ the rules
// about a component's own injection block: what its type permits it to dictate,
// and what its connections already claim

// The types the self-delivery rows author against: one component type permitting
// two mechanisms, one permitting a single mechanism whose name sorts against the
// order a block enumerates its mechanisms in, and one permitting none at all —
// the empty allowlist being the most constrained answer rather than an absent
// one, so every mechanism dictated on that type is refused. The type the catalog
// holds no entry for is the one the socket rows already name, reused so the two
// families cannot drift about which identity is absent.
const (
	selfDeliveryOpenType     = "core/open-delivery:1.0.0"
	selfDeliveryFileOnlyType = "core/file-delivery:1.0.0"
	selfDeliveryClosedType   = "core/closed-delivery:1.0.0"
)

// selfDeliveryRuleCatalog holds entries for the three types above and for
// nothing else.
func selfDeliveryRuleCatalog() CatalogLookup {
	open := types.Component{}
	open.Metadata.Name = "core/open-delivery"
	open.Spec.Inject = []string{"env", "arg"}

	fileOnly := types.Component{}
	fileOnly.Metadata.Name = "core/file-delivery"
	fileOnly.Spec.Inject = []string{"file"}

	closed := types.Component{}
	closed.Metadata.Name = "core/closed-delivery"

	return CatalogLookup{Component: func(uri string) (types.Component, bool) {
		switch uri {
		case selfDeliveryOpenType:
			return open, true
		case selfDeliveryFileOnlyType:
			return fileOnly, true
		case selfDeliveryClosedType:
			return closed, true
		}
		return types.Component{}, false
	}}
}

// mechanismDelivery is what one mechanism of an injection block delivers: the
// mechanism the author named, and the keys under it in the order written.
type mechanismDelivery struct {
	name string
	keys []string
}

// writtenBlock assembles an injection block the author wrote, whatever it
// carries — including one written and left empty, which is a different document
// from one not written at all and the two require opposite verdicts.
//
// Each mechanism is written as the list the inject_delivery definition
// describes, which carries the author's own order, so a row can tell an answer
// preserving that order from an answer that sorted it. Every entry carries a
// literal rather than a reference, so no row here also states a fault the rule
// about a delivery entry reading an unpublished key would report.
func writtenBlock(mechanisms ...mechanismDelivery) *types.InjectBlock {
	out := &types.InjectBlock{}
	for _, m := range mechanisms {
		entries := make([]any, 0, len(m.keys))
		for _, k := range m.keys {
			entries = append(entries, map[string]any{"in": k, "value": "a literal"})
		}
		switch m.name {
		case "env":
			out.Env = entries
		case "file":
			out.File = entries
		case "arg":
			out.Arg = entries
		default:
			out.Additional.Set(m.name, entries)
		}
	}
	return out
}

// selfDeliveryComponent is one component of a self-delivery row: the type it
// declares, the injection block it wrote (none where it wrote none), and the
// connections its own dependency block carries under one socket.
type selfDeliveryComponent struct {
	name        string
	typ         string
	block       *types.InjectBlock
	socket      string
	connections []namedConnection
}

// selfDeliveryAggregate builds the components a row declares, in the order given.
func selfDeliveryAggregate(components ...selfDeliveryComponent) *types.Aggregate {
	comps := types.OrderedMap[types.ComponentInstance]{}
	for _, c := range components {
		inst := types.ComponentInstance{Type: c.typ, Inject: c.block}
		if len(c.connections) > 0 {
			socket := types.UsesSocketValue{}
			for _, conn := range c.connections {
				socket.Verbose.Set(conn.alias, types.VerboseUsesEntry{
					Target: conn.target,
					Inject: injectBlockOf(conn.block),
				})
			}
			inst.Uses.Set(c.socket, socket)
		}
		comps.Set(c.name, inst)
	}
	return &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "self-delivery"},
		Spec:     types.AggregateSpec{Components: comps},
	}
}

// The rule about an injection block written on a component type that permits no
// delivery at all.
//
// Order is by instance name, which is the order the one projection of the
// component instances walks them in.
func TestTheSelfDeliveryBlockPermittedRuleReportsABlockWrittenOnAClosedType(t *testing.T) {
	t.Parallel()

	catalog := selfDeliveryRuleCatalog()

	for _, tc := range []struct {
		name       string
		components []selfDeliveryComponent
		// raised is the sequence the rule reports, one locator per component it
		// names, in the order the node walk reaches them.
		raised []string
	}{
		{
			// One component of each skipping kind sits beside the one fault, so
			// every skip is exercised by its absence from the expectation rather
			// than by a row that reports nothing at all. The reported component
			// wrote its block and left it EMPTY, which is the document the rule
			// exists to refuse and the one a rule reading mechanisms would accept.
			name: "a block written on a type permitting nothing is reported; a block on a type permitting delivery, a type permitting nothing with no block, and a type the catalog holds no entry for are not",
			components: []selfDeliveryComponent{
				{name: "web", typ: selfDeliveryClosedType, block: writtenBlock()},
				{name: "api", typ: selfDeliveryOpenType, block: writtenBlock(
					mechanismDelivery{name: "env", keys: []string{"PORT"}})},
				{name: "cache", typ: selfDeliveryClosedType},
				{name: "ghost", typ: socketAbsentComponent, block: writtenBlock()},
			},
			raised: []string{".spec.components.web.inject"},
		},
		{
			// Two reports, authored against their alphabetical order. With a single
			// finding every ordering agrees, which is why the row above cannot carry
			// the ordering claim and this one can.
			name: "several reports come back in instance-name order",
			components: []selfDeliveryComponent{
				{name: "zeta", typ: selfDeliveryClosedType, block: writtenBlock()},
				{name: "alpha", typ: selfDeliveryClosedType, block: writtenBlock()},
			},
			raised: []string{".spec.components.alpha.inject", ".spec.components.zeta.inject"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.raised) == 0 {
				t.Fatal("this row expects nothing, so it cannot tell a working rule from one " +
					"that reports nothing whatever it is given")
			}

			na := normalized(t, selfDeliveryAggregate(tc.components...))
			view := newNormalizedView(na, nil, catalog, nil)

			objections, err := constraint.RunOnNodes[constraint.NormalizedView](
				nil, newCoordinateSet(view), view, selfInjectBlockAllowed)
			if err != nil {
				t.Fatalf("the node walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, tc.raised) {
				t.Errorf("raised sequence = %v; want %v", got, tc.raised)
			}
		})
	}
}

// The rule about a delivery mechanism an injection block dictates that the
// component's type does not permit.
//
// It is invoked once per mechanism in the order the block enumerates them — the
// mechanisms named as fields first, in that fixed order, then the ones named
// beside them in author order. A row whose refused mechanisms are enumerated
// against their alphabetical order is what tells that apart from a sort.
func TestTheSelfDeliveryMechanismPermittedRuleReportsEachRefusedMechanism(t *testing.T) {
	t.Parallel()

	catalog := selfDeliveryRuleCatalog()

	for _, tc := range []struct {
		name       string
		components []selfDeliveryComponent
		raised     []string
	}{
		{
			// The permitted mechanism sits beside the one fault, so the accept path
			// is exercised by what the expectation does not contain.
			name: "a mechanism outside the allowlist is reported; a permitted one is not",
			components: []selfDeliveryComponent{
				{name: "web", typ: selfDeliveryOpenType, block: writtenBlock(
					mechanismDelivery{name: "env", keys: []string{"PORT"}},
					mechanismDelivery{name: "file", keys: []string{"CONF"}})},
			},
			raised: []string{".spec.components.web.inject.file"},
		},
		{
			// A mechanism the product does not name among its own is dictated the
			// same way and refused the same way, and it is enumerated after the
			// three that are named as fields.
			name: "a mechanism the product does not name among its own is reported alongside a field-named one",
			components: []selfDeliveryComponent{
				{name: "web", typ: selfDeliveryOpenType, block: writtenBlock(
					mechanismDelivery{name: "arg", keys: []string{"FLAG"}},
					mechanismDelivery{name: "file", keys: []string{"CONF"}},
					mechanismDelivery{name: "secret_store", keys: []string{"PW"}})},
			},
			raised: []string{
				".spec.components.web.inject.file",
				".spec.components.web.inject.secret_store",
			},
		},
		{
			// The ordering row. Both mechanisms are refused, and the block
			// enumerates them in the order it names its own fields — which is the
			// reverse of alphabetical.
			name: "two refused mechanisms come back in the block's own enumeration order",
			components: []selfDeliveryComponent{
				{name: "web", typ: selfDeliveryFileOnlyType, block: writtenBlock(
					mechanismDelivery{name: "env", keys: []string{"PORT"}},
					mechanismDelivery{name: "arg", keys: []string{"FLAG"}})},
			},
			raised: []string{
				".spec.components.web.inject.env",
				".spec.components.web.inject.arg",
			},
		},
		{
			// A type permitting nothing refuses every mechanism the block
			// dictates, so this rule reports one per mechanism rather than once
			// about the block — the block itself is the other rule's subject.
			name: "a type permitting nothing draws one report per mechanism the block dictates",
			components: []selfDeliveryComponent{
				{name: "web", typ: selfDeliveryClosedType, block: writtenBlock(
					mechanismDelivery{name: "env", keys: []string{"PORT"}},
					mechanismDelivery{name: "arg", keys: []string{"FLAG"}},
					mechanismDelivery{name: "secret_store", keys: []string{"PW"}})},
			},
			raised: []string{
				".spec.components.web.inject.env",
				".spec.components.web.inject.arg",
				".spec.components.web.inject.secret_store",
			},
		},
		{
			// A type the catalog holds no entry for ends this rule, exactly as it
			// ends the block rule. The component reported beside it is the control
			// that keeps the silence attributable.
			name: "a type the catalog holds no entry for is not reported, beside a component that is",
			components: []selfDeliveryComponent{
				{name: "ghost", typ: socketAbsentComponent, block: writtenBlock(
					mechanismDelivery{name: "file", keys: []string{"CONF"}})},
				{name: "web", typ: selfDeliveryOpenType, block: writtenBlock(
					mechanismDelivery{name: "file", keys: []string{"CONF"}})},
			},
			raised: []string{".spec.components.web.inject.file"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.raised) == 0 {
				t.Fatal("this row expects nothing, so it cannot tell a working rule from one " +
					"that reports nothing whatever it is given")
			}

			na := normalized(t, selfDeliveryAggregate(tc.components...))
			view := newNormalizedView(na, nil, catalog, nil)
			coordinates := newCoordinateSet(view)

			objections, err := constraint.RunOnNodeMechanisms[constraint.NormalizedView](
				nil, coordinates, coordinates, view, selfInjectMechanismAllowed)
			if err != nil {
				t.Fatalf("the mechanism walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, tc.raised) {
				t.Errorf("raised sequence = %v; want %v", got, tc.raised)
			}
		})
	}
}

// The rule about a delivery key a component claims both in its own injection
// block and on one of its connections.
//
// It raises per key in the order the block enumerates its mechanisms and then its
// keys. It carries no condition on what the component's type permits or on
// whether the catalog holds an entry for it: a key claimed twice is a
// contradiction in the document itself, which is why the last two rows report
// where a reading conditioned on the type would be silent.
func TestTheSelfDeliveryKeyAgainstAConnectionRuleReportsAKeyClaimedTwice(t *testing.T) {
	t.Parallel()

	catalog := selfDeliveryRuleCatalog()
	claimed := func(keys ...string) deliveryBlock {
		entries := make([]any, 0, len(keys))
		for _, k := range keys {
			entries = append(entries, verboseDelivery(k, ""))
		}
		return deliveryBlock{env: entries}
	}

	for _, tc := range []struct {
		name       string
		components []selfDeliveryComponent
		raised     []string
	}{
		{
			// The key only the block claims and the key only the connection claims
			// sit beside the one contradiction, so both skips are exercised by their
			// absence from the expectation.
			name: "a key claimed by both the block and a connection is reported; a key claimed by only one of them is not",
			components: []selfDeliveryComponent{{
				name: "web", typ: selfDeliveryOpenType, socket: "db",
				block: writtenBlock(mechanismDelivery{
					name: "env", keys: []string{"OWN", "SHARED"}}),
				connections: []namedConnection{
					{alias: "primary", target: "cache", block: claimed("SHARED", "THEIRS")},
				},
			}},
			raised: []string{".spec.components.web.inject.env.SHARED"},
		},
		{
			// Two connections claiming one key is ONE contradiction: the correction
			// is to the pair rather than to a connection, so the report is not
			// multiplied by the connections that participate.
			name: "two connections claiming one key draw one report",
			components: []selfDeliveryComponent{{
				name: "web", typ: selfDeliveryOpenType, socket: "db",
				block: writtenBlock(mechanismDelivery{name: "env", keys: []string{"SHARED"}}),
				connections: []namedConnection{
					{alias: "primary", target: "cache", block: claimed("SHARED")},
					{alias: "spare", target: "cache", block: claimed("SHARED")},
				},
			}},
			raised: []string{".spec.components.web.inject.env.SHARED"},
		},
		{
			// The ordering row: two contradictions on one mechanism, authored against
			// their alphabetical order.
			name: "several reports come back in the block's key order",
			components: []selfDeliveryComponent{{
				name: "web", typ: selfDeliveryOpenType, socket: "db",
				block: writtenBlock(mechanismDelivery{name: "env", keys: []string{"ZED", "ALPHA"}}),
				connections: []namedConnection{
					{alias: "primary", target: "cache", block: claimed("ZED", "ALPHA")},
				},
			}},
			raised: []string{
				".spec.components.web.inject.env.ZED",
				".spec.components.web.inject.env.ALPHA",
			},
		},
		{
			// 🔴 A type permitting no delivery at all. The block itself is refused
			// by another rule, and this one still reports the contradiction: a key
			// claimed twice is defective input whatever the type permits, so
			// conditioning this rule on the allowlist would silence a real fault.
			name: "a contradiction on a type permitting nothing is reported",
			components: []selfDeliveryComponent{{
				name: "web", typ: selfDeliveryClosedType, socket: "db",
				block: writtenBlock(mechanismDelivery{name: "env", keys: []string{"SHARED"}}),
				connections: []namedConnection{
					{alias: "primary", target: "cache", block: claimed("SHARED")},
				},
			}},
			raised: []string{".spec.components.web.inject.env.SHARED"},
		},
		{
			// 🔴 The same, on a type the catalog holds no entry for: the
			// contradiction is between two things the document itself says, so
			// nothing about the catalog bears on it.
			name: "a contradiction on a type the catalog holds no entry for is reported",
			components: []selfDeliveryComponent{{
				name: "web", typ: socketAbsentComponent, socket: "db",
				block: writtenBlock(mechanismDelivery{name: "env", keys: []string{"SHARED"}}),
				connections: []namedConnection{
					{alias: "primary", target: "cache", block: claimed("SHARED")},
				},
			}},
			raised: []string{".spec.components.web.inject.env.SHARED"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.raised) == 0 {
				t.Fatal("this row expects nothing, so it cannot tell a working rule from one " +
					"that reports nothing whatever it is given")
			}

			na := normalized(t, selfDeliveryAggregate(tc.components...))
			view := newNormalizedView(na, nil, catalog, nil)
			coordinates := newCoordinateSet(view)

			objections, err := constraint.RunOnNodeMechanismDeliveryKeys[constraint.NormalizedView](
				nil, coordinates, coordinates, coordinates, view, selfInjectKeyNotAlsoOnAConnection)
			if err != nil {
				t.Fatalf("the delivery-key walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, tc.raised) {
				t.Errorf("raised sequence = %v; want %v", got, tc.raised)
			}
		})
	}
}

// TestTheCollisionRuleWalksOrderedMapEntriesToo holds the delivery-key fact to
// both shapes a list entry can arrive in rather than to the plain-map entries
// every row above writes.
//
// An entry decoded through a path that records key order arrives as an ordered
// map. What this pins is that the fact RECOGNISES such an entry rather than
// answering with no keys for it — a reading that returned nothing would approve a
// component it never examined, which is the failure a collision rule exists to
// prevent, and no row over plain-map entries can see it.
//
// The payload is constructed here rather than parsed, so what the test proves is a
// property of the walk and never of what parsing produces.
func TestTheCollisionRuleWalksOrderedMapEntriesToo(t *testing.T) {
	t.Parallel()

	ordered := types.NewOrderedMap[any]()
	ordered.Set("in", "SHARED")
	ordered.Set("value", "localhost")
	block := &types.InjectBlock{Env: []any{ordered}}

	connectionEntry := types.NewOrderedMap[any]()
	connectionEntry.Set("in", "SHARED")
	connectionEntry.Set("from", "host")
	connection := types.OrderedMap[types.VerboseUsesEntry]{}
	connection.Set("primary", types.VerboseUsesEntry{
		Target: "cache",
		Inject: &types.InjectBlock{Env: []any{connectionEntry}},
	})
	web := types.ComponentInstance{Type: selfDeliveryOpenType, Inject: block}
	web.Uses.Set("db", types.UsesSocketValue{Verbose: connection})

	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set("web", web)
	comps.Set("cache", types.ComponentInstance{Type: selfDeliveryOpenType})
	na := normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "self-delivery"},
		Spec:     types.AggregateSpec{Components: comps},
	})

	view := newNormalizedView(na, nil, selfDeliveryRuleCatalog(), nil)
	coordinates := newCoordinateSet(view)

	// The fact first, because a rule reporting nothing here would otherwise be
	// indistinguishable from a fact that read no keys.
	if got := view.AuthoredInjectDeliveryKeys("web", "env"); !equalStrings(got, []string{"SHARED"}) {
		t.Fatalf("the delivery-key fact read %v from a list of ordered-map entries; want [SHARED].\n"+
			"INVARIANT: the fact recognises every shape a list entry arrives in.\n"+
			"CONSEQUENCE: a rule over it approves a component whose keys were never read.", got)
	}

	objections, err := constraint.RunOnNodeMechanismDeliveryKeys[constraint.NormalizedView](
		nil, coordinates, coordinates, coordinates, view, selfInjectKeyNotAlsoOnAConnection)
	if err != nil {
		t.Fatalf("the delivery-key walk returned an error: %v", err)
	}
	want := []string{".spec.components.web.inject.env.SHARED"}
	if got := locatorsOf(objections); !equalStrings(got, want) {
		t.Errorf("raised sequence = %v; want %v", got, want)
	}
}

// The rule about a delivery key an injection block claims under more than one
// mechanism.
//
// One report per participating mechanism is the claim, so an author correcting the
// contradiction sees every site that states it.
func TestTheSelfDeliveryKeyAcrossMechanismsRuleReportsOncePerMechanism(t *testing.T) {
	t.Parallel()

	catalog := selfDeliveryRuleCatalog()

	for _, tc := range []struct {
		name       string
		components []selfDeliveryComponent
		raised     []string
	}{
		{
			// The key claimed under one mechanism only sits beside the one
			// contradiction, so the skip is exercised by its absence from the
			// expectation. Both participating mechanisms are reported, which is what
			// makes the multiplicity claim measured rather than assumed.
			name: "a key claimed under two mechanisms is reported once per mechanism; a key claimed under one is not reported",
			components: []selfDeliveryComponent{{
				name: "web", typ: selfDeliveryOpenType,
				block: writtenBlock(
					mechanismDelivery{name: "env", keys: []string{"SHARED", "OWN"}},
					mechanismDelivery{name: "arg", keys: []string{"SHARED"}}),
			}},
			raised: []string{
				".spec.components.web.inject.env.SHARED",
				".spec.components.web.inject.arg.SHARED",
			},
		},
		{
			// Three mechanisms claiming one key is three reports, not one and not
			// three-choose-two: the report is per site that states the contradiction.
			name: "three mechanisms claiming one key draw three reports",
			components: []selfDeliveryComponent{{
				name: "web", typ: selfDeliveryOpenType,
				block: writtenBlock(
					mechanismDelivery{name: "env", keys: []string{"SHARED"}},
					mechanismDelivery{name: "arg", keys: []string{"SHARED"}},
					mechanismDelivery{name: "secret_store", keys: []string{"SHARED"}}),
			}},
			raised: []string{
				".spec.components.web.inject.env.SHARED",
				".spec.components.web.inject.arg.SHARED",
				".spec.components.web.inject.secret_store.SHARED",
			},
		},
		{
			// 🔴 The same independence the rule beside this one has, on the same two
			// inputs and for the same reason: the contradiction is between two things
			// the document says, so neither what the type permits nor whether the
			// catalog holds it bears on the report.
			name: "a contradiction on a type permitting nothing is reported",
			components: []selfDeliveryComponent{{
				name: "web", typ: selfDeliveryClosedType,
				block: writtenBlock(
					mechanismDelivery{name: "env", keys: []string{"SHARED"}},
					mechanismDelivery{name: "arg", keys: []string{"SHARED"}}),
			}},
			raised: []string{
				".spec.components.web.inject.env.SHARED",
				".spec.components.web.inject.arg.SHARED",
			},
		},
		{
			name: "a contradiction on a type the catalog holds no entry for is reported",
			components: []selfDeliveryComponent{{
				name: "web", typ: socketAbsentComponent,
				block: writtenBlock(
					mechanismDelivery{name: "env", keys: []string{"SHARED"}},
					mechanismDelivery{name: "arg", keys: []string{"SHARED"}}),
			}},
			raised: []string{
				".spec.components.web.inject.env.SHARED",
				".spec.components.web.inject.arg.SHARED",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.raised) == 0 {
				t.Fatal("this row expects nothing, so it cannot tell a working rule from one " +
					"that reports nothing whatever it is given")
			}

			na := normalized(t, selfDeliveryAggregate(tc.components...))
			view := newNormalizedView(na, nil, catalog, nil)
			coordinates := newCoordinateSet(view)

			objections, err := constraint.RunOnNodeMechanismDeliveryKeys[constraint.NormalizedView](
				nil, coordinates, coordinates, coordinates, view, selfInjectKeyNotOnTwoMechanisms)
			if err != nil {
				t.Fatalf("the delivery-key walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, tc.raised) {
				t.Errorf("raised sequence = %v; want %v", got, tc.raised)
			}
		})
	}
}

// How many diagnostics one document draws from the four self-delivery rules
// together.
//
// It is written as its own test because no per-rule table can state it: each of
// those holds ONE rule over its own document, while a build asks all four at once.
// The document below is the worst case on purpose — a component type permitting
// nothing, a block dictating three mechanisms, one key claimed under two of them
// and one of those also claimed by a connection — so every rule in the family has
// something to say about it.
func TestTheSelfDeliveryRulesReportOneDocumentAtAKnownMultiplicity(t *testing.T) {
	t.Parallel()

	catalog := selfDeliveryRuleCatalog()
	na := normalized(t, selfDeliveryAggregate(selfDeliveryComponent{
		name: "web", typ: selfDeliveryClosedType, socket: "db",
		block: writtenBlock(
			mechanismDelivery{name: "env", keys: []string{"SHARED"}},
			mechanismDelivery{name: "arg", keys: []string{"SHARED"}},
			mechanismDelivery{name: "secret_store", keys: []string{"PW"}}),
		connections: []namedConnection{
			{alias: "primary", target: "cache", block: deliveryBlock{
				env: []any{verboseDelivery("SHARED", "")}}},
		},
	}))
	view := newNormalizedView(na, nil, catalog, nil)
	coordinates := newCoordinateSet(view)

	nodes, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, coordinates, view, selfInjectBlockAllowed)
	if err != nil {
		t.Fatalf("the node walk returned an error: %v", err)
	}
	mechanisms, err := constraint.RunOnNodeMechanisms[constraint.NormalizedView](
		nil, coordinates, coordinates, view, selfInjectMechanismAllowed)
	if err != nil {
		t.Fatalf("the mechanism walk returned an error: %v", err)
	}
	againstConnection, err := constraint.RunOnNodeMechanismDeliveryKeys[constraint.NormalizedView](
		nil, coordinates, coordinates, coordinates, view, selfInjectKeyNotAlsoOnAConnection)
	if err != nil {
		t.Fatalf("the delivery-key walk returned an error: %v", err)
	}
	acrossMechanisms, err := constraint.RunOnNodeMechanismDeliveryKeys[constraint.NormalizedView](
		nil, coordinates, coordinates, coordinates, view, selfInjectKeyNotOnTwoMechanisms)
	if err != nil {
		t.Fatalf("the second delivery-key walk returned an error: %v", err)
	}

	for _, got := range []struct {
		rule       string
		locators   []string
		wantRaised []string
	}{
		// The four positions below descend by exactly one authored segment per
		// rule — the block, a mechanism the block dictates, a key that mechanism
		// delivers — so the family's positions read as one ladder over the
		// document rather than as four independently chosen depths.
		{"the block rule", locatorsOf(nodes), []string{".spec.components.web.inject"}},
		{"the mechanism rule", locatorsOf(mechanisms), []string{
			".spec.components.web.inject.env",
			".spec.components.web.inject.arg",
			".spec.components.web.inject.secret_store",
		}},
		{"the rule against a connection", locatorsOf(againstConnection), []string{
			".spec.components.web.inject.env.SHARED",
		}},
		{"the rule across mechanisms", locatorsOf(acrossMechanisms), []string{
			".spec.components.web.inject.env.SHARED",
			".spec.components.web.inject.arg.SHARED",
		}},
	} {
		if !equalStrings(got.locators, got.wantRaised) {
			t.Errorf("%s raised %v; want %v", got.rule, got.locators, got.wantRaised)
		}
	}

	raised := len(nodes) + len(mechanisms) + len(againstConnection) + len(acrossMechanisms)
	if raised != 7 {
		t.Errorf("the four rules raised %d objections; want 7", raised)
	}
}

// The order an injection block enumerates its mechanisms in, read from a real
// authoring document rather than from a hand-built value.
//
// It matters to the rules above because two of them are invoked once per
// mechanism, so this order IS the order their diagnostics come back in. A
// hand-built block cannot establish it: assigning the fields in Go fixes the
// order by construction, and the question is what the AUTHOR's own order does to
// it. So the block below is unmarshalled from the text an author writes.
//
// The order is by shape, not by authored position: the three mechanisms the block
// names as its own fields come first, in the order it names them, and the
// mechanisms named beside those follow in the order the author wrote them. Every
// row authors its mechanisms against that order, so no row can pass by accident
// of having written them in it.
func TestAnInjectionBlockEnumeratesTheMechanismsItNamesAsFieldsFirst(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		document string
		want     []string
	}{
		{
			name:     "a field-named mechanism written second is enumerated first",
			document: "arg:\n  FLAG: x\nenv:\n  PORT: \"1\"\n",
			want:     []string{"env", "arg"},
		},
		{
			// Each row below pairs mechanisms from a DIFFERENT position of the
			// block's own field order, so no single reordering of those fields can
			// leave the whole table passing. A table whose rows all omit one field
			// cannot see that field move, and reports the omission as agreement.
			name:     "the first two field-named mechanisms hold their relative order against the author's",
			document: "file:\n  CONF: c\nenv:\n  PORT: \"1\"\n",
			want:     []string{"env", "file"},
		},
		{
			name:     "a mechanism named beside the fields is enumerated after them however early it is written",
			document: "secret_store:\n  PW: y\nenv:\n  PORT: \"1\"\n",
			want:     []string{"env", "secret_store"},
		},
		{
			name:     "the two orderings compose: every field in the block's own order, then the rest in the author's",
			document: "vault:\n  T: t\narg:\n  FLAG: x\nfile:\n  CONF: c\nsecret_store:\n  PW: y\nenv:\n  PORT: \"1\"\n",
			want:     []string{"env", "file", "arg", "vault", "secret_store"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var block types.InjectBlock
			if err := yaml.Unmarshal([]byte(tc.document), &block); err != nil {
				t.Fatalf("reading the document: %v", err)
			}
			// Without this the row would pass over a block that decoded only part of
			// the document, which is what an unmarshalling bypassing the block's own
			// reader produces: the mechanisms named beside the fields vanish and the
			// remaining order still looks correct.
			if got := len(InjectMechanismNames(&block)); got != len(tc.want) {
				t.Fatalf("the document yielded %d mechanisms (%v); want %d — the block was not "+
					"read as the author wrote it", got, InjectMechanismNames(&block), len(tc.want))
			}
			if got := InjectMechanismNames(&block); !equalStrings(got, tc.want) {
				t.Errorf("mechanism enumeration = %v; want %v", got, tc.want)
			}
		})
	}
}

// ------------------------------------------------------------------ the rules
// about a payload measured against the contract declared over it

// The types the conformance rows author against: one declaring a contract over
// each of its two configuration pillars and over each of the two pillars a
// connection into its dependency socket may carry, and one whose catalog entry
// declares no contract at all. The type the catalog holds no entry for is the one
// the socket rows already name, reused so the two families cannot drift about
// which identity is absent.
const (
	conformanceContractedType = "core/contracted:1.0.0"
	conformanceBareType       = "core/uncontracted:1.0.0"
	conformanceSocket         = "store"
)

// conformanceSchemaNode reads a contract fixture into the node shape a catalog
// entity carries a contract in.
//
// The document wrapper is unwrapped because that is the shape reading a real
// entity produces: a contract is the value of a key in the entity's own document
// and never a document of its own, and the socket-contract compile wraps the node
// it is handed as a mapping value — which a document node cannot be.
func conformanceSchemaNode(t *testing.T, body string) yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("the contract fixture does not parse: %v", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		t.Fatalf("the contract fixture is not one document carrying one node")
	}
	return *doc.Content[0]
}

// conformanceRuleCatalog holds entries for the two types above and for nothing
// else.
//
// Every contract declares one REQUIRED key that carries no default, and that is
// what makes the rules' treatment of an absent payload measurable rather than
// merely documented: a payload nobody wrote is the maximal violation of such a
// key, so a rule that measures absence reports on it and a rule that skipped it
// would be silent on the one input the key was declared for.
func conformanceRuleCatalog(t *testing.T) CatalogLookup {
	t.Helper()

	contracted := types.Component{}
	contracted.Metadata.Name = "core/contracted"
	contracted.Spec.Schema = conformanceSchemaNode(t, `values:
  replicas: { type: integer, minimum: 1, required: true }
infra:
  size: { type: string, required: true }
`)
	socket := types.UsesSchemaEntry{}
	socket.ValueSchema = conformanceSchemaNode(t,
		"pool: { type: integer, minimum: 1, required: true }\n")
	socket.InfraSchema = conformanceSchemaNode(t,
		"tier: { type: string, required: true }\n")
	sockets := types.OrderedMap[types.UsesSchemaEntry]{}
	sockets.Set(conformanceSocket, socket)
	contracted.Spec.UsesSchema = sockets

	bare := types.Component{}
	bare.Metadata.Name = "core/uncontracted"

	return CatalogLookup{Component: func(uri string) (types.Component, bool) {
		switch uri {
		case conformanceContractedType:
			return contracted, true
		case conformanceBareType:
			return bare, true
		}
		return types.Component{}, false
	}}
}

// conformancePayload assembles one pillar's payload in the order written.
func conformancePayload(pairs ...any) types.OrderedMap[any] {
	out := types.OrderedMap[any]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		out.Set(pairs[i].(string), pairs[i+1])
	}
	return out
}

// conformanceConnection is one connection a component's own dependency block
// carries: the alias naming it, the component it points at, and the
// configuration written on it.
type conformanceConnection struct {
	alias  string
	target string
	values types.OrderedMap[any]
}

// conformanceComponent is one component of a conformance row: the type it
// declares, the configuration its own document wrote, and the connections its
// dependency block carries — either as aliases, or as the short form listing
// targets, never both, because a socket is written in one form or the other.
type conformanceComponent struct {
	name        string
	typ         string
	values      types.OrderedMap[any]
	connections []conformanceConnection
	compact     []string
}

// conformanceAggregate builds the components a row declares, in the order given.
func conformanceAggregate(components ...conformanceComponent) *types.Aggregate {
	comps := types.OrderedMap[types.ComponentInstance]{}
	for _, c := range components {
		inst := types.ComponentInstance{Type: c.typ, Values: c.values}
		switch {
		case len(c.compact) > 0:
			inst.Uses.Set(conformanceSocket, types.UsesSocketValue{Compact: c.compact})
		case len(c.connections) > 0:
			socket := types.UsesSocketValue{}
			for _, conn := range c.connections {
				socket.Verbose.Set(conn.alias, types.VerboseUsesEntry{
					Target: conn.target,
					Values: conn.values,
				})
			}
			inst.Uses.Set(conformanceSocket, socket)
		}
		comps.Set(c.name, inst)
	}
	return &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "conformance"},
		Spec:     types.AggregateSpec{Components: comps},
	}
}

func TestTheComponentConfigurationConformanceRuleMeasuresTheAuthoredPillar(t *testing.T) {
	t.Parallel()

	catalog := conformanceRuleCatalog(t)

	for _, tc := range []struct {
		name       string
		components []conformanceComponent
		// raised is the locator sequence the rule reports: the whole dotted path
		// of the offending leaf, one entry per violation. This rule positions
		// INSIDE the site carrying the payload, at the key the contract measured.
		raised []string
	}{
		{
			// One component of each skipping kind sits beside the one fault, so
			// every skip is exercised by its absence from the expectation rather
			// than by a row reporting nothing at all.
			name: "a configuration violating the contract is reported; a type declaring no contract and a type the catalog holds no entry for are not",
			components: []conformanceComponent{
				{name: "web", typ: conformanceContractedType,
					values: conformancePayload("replicas", "many")},
				{name: "api", typ: conformanceBareType,
					values: conformancePayload("anything", 1)},
				{name: "ghost", typ: socketAbsentComponent,
					values: conformancePayload("replicas", "many")},
			},
			raised: []string{".spec.components.web.values.replicas"},
		},
		{
			// The empty payload is the row that separates this rule from its
			// sizing counterpart: an empty configuration is measured here, and
			// the counterpart ends before measuring the same emptiness.
			name: "a component that wrote no configuration at all is measured against a key the contract declares required",
			components: []conformanceComponent{
				{name: "web", typ: conformanceContractedType},
			},
			raised: []string{".spec.components.web.values.replicas"},
		},
		{
			// Two reports, authored against their alphabetical order. With a
			// single finding every ordering agrees, which is why the rows above
			// cannot carry the ordering claim and this one can.
			name: "several reports come back in instance-name order",
			components: []conformanceComponent{
				{name: "zeta", typ: conformanceContractedType,
					values: conformancePayload("replicas", "many")},
				{name: "alpha", typ: conformanceContractedType,
					values: conformancePayload("replicas", 0)},
			},
			raised: []string{
				".spec.components.alpha.values.replicas",
				".spec.components.zeta.values.replicas",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.raised) == 0 {
				t.Fatal("this row expects nothing, so it cannot tell a working rule from one " +
					"that reports nothing whatever it is given")
			}

			na := normalized(t, conformanceAggregate(tc.components...))
			view := newNormalizedView(na, nil, catalog, nil)

			objections, err := constraint.RunOnNodes[constraint.NormalizedView](
				nil, newCoordinateSet(view), view, componentValuesConformToTheType)
			if err != nil {
				t.Fatalf("the node walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, tc.raised) {
				t.Errorf("raised sequence = %v; want %v", got, tc.raised)
			}
		})
	}
}

// The rule about the physical sizing a Release wrote for a component.
//
// One row authors the component at DEPTH in the boundary tree, so a derivation
// that stopped at the outer scope would be visible rather than merely possible.
func TestTheComponentSizingConformanceRuleMeasuresWhatTheReleaseWrote(t *testing.T) {
	t.Parallel()

	catalog := conformanceRuleCatalog(t)

	for _, tc := range []struct {
		name       string
		components []conformanceComponent
		// deployed is the boundary block of the Release document.
		deployed string
		raised   []string
	}{
		{
			// The Release writes a violating payload for one component and no
			// sizing block at all for the other. Both are reported: the contract is
			// opted into on the component type's side, so the component nobody wrote
			// sizing for is measured against it exactly as its sibling is, and the
			// required key it omits is the maximal violation of that contract.
			name: "sizing violating the contract is reported, and so is the component the Release wrote no sizing for",
			components: []conformanceComponent{
				{name: "api", typ: conformanceContractedType,
					values: conformancePayload("replicas", 1)},
				{name: "web", typ: conformanceContractedType,
					values: conformancePayload("replicas", 1)},
			},
			deployed: `    prod:
      directory: ./output
      components:
        web:
          infra:
            size: 3
        api: {}
`,
			raised: []string{
				".spec.components.api.infra.size",
				".spec.components.web.infra.size",
			},
		},
		{
			// The component sits inside a boundary nested in another, which is the
			// shape a walk that descends only one level would miss.
			name: "sizing written on a component at depth in the boundary tree is reported",
			components: []conformanceComponent{
				{name: "web", typ: conformanceContractedType,
					values: conformancePayload("replicas", 1)},
			},
			deployed: `    prod:
      directory: ./output
      boundaries:
        inner:
          components:
            web:
              infra:
                size: 3
`,
			raised: []string{".spec.components.web.infra.size"},
		},
		{
			// The counterpart of the empty-payload row on the configuration rule, and
			// it reads the same way there: written-and-empty is measured against a
			// required key rather than passed over. The positive control beside it is
			// the component that violates on written content, so the row cannot pass
			// over nothing.
			name: "a component the Release wrote an empty sizing block for is reported, beside its sibling that wrote a violating one",
			components: []conformanceComponent{
				{name: "api", typ: conformanceContractedType,
					values: conformancePayload("replicas", 1)},
				{name: "web", typ: conformanceContractedType,
					values: conformancePayload("replicas", 1)},
			},
			deployed: `    prod:
      directory: ./output
      components:
        web:
          infra:
            size: 3
        api:
          infra: {}
`,
			raised: []string{
				".spec.components.api.infra.size",
				".spec.components.web.infra.size",
			},
		},
		{
			// Both legs of a required sizing key in one row: the component that wrote
			// a payload satisfying it is silent, and the component beside it that
			// wrote no sizing block is reported. The silent leg is what keeps the
			// reporting leg from being a rule that objects to everything, and the
			// reporting leg is what keeps the silent one from being a rule that
			// objects to nothing.
			name: "a conforming sizing payload is not reported, while the component beside it that wrote none is",
			components: []conformanceComponent{
				{name: "api", typ: conformanceContractedType,
					values: conformancePayload("replicas", 1)},
				{name: "web", typ: conformanceContractedType,
					values: conformancePayload("replicas", 1)},
			},
			deployed: `    prod:
      directory: ./output
      components:
        web:
          infra:
            size: "3"
        api: {}
`,
			raised: []string{".spec.components.api.infra.size"},
		},
		{
			// Authorship, which is the one thing separating the two components in
			// this row: both read the same empty sizing, and only the one the
			// Release wrote an entry against is the Release's to answer for.
			// The other is placed nowhere, and a component placed nowhere is
			// reported once the composition is flattened, at a key its author can
			// act on — so measuring its absent sizing here would replace that
			// report with one asking for a key under an entry that is not there.
			name: "a component the Release wrote no entry against is not reported, beside the one it named and left without sizing",
			components: []conformanceComponent{
				{name: "api", typ: conformanceContractedType,
					values: conformancePayload("replicas", 1)},
				{name: "web", typ: conformanceContractedType,
					values: conformancePayload("replicas", 1)},
			},
			deployed: `    prod:
      directory: ./output
      components:
        api: {}
`,
			raised: []string{".spec.components.api.infra.size"},
		},
		{
			// A type whose catalog entry declares no contract over the sizing
			// pillar is not measured, whatever the Release wrote for it. The
			// contracted sibling beside it keeps the silence attributable.
			name: "a type declaring no sizing contract is not measured, beside a contracted sibling that is",
			components: []conformanceComponent{
				{name: "api", typ: conformanceBareType},
				{name: "web", typ: conformanceContractedType,
					values: conformancePayload("replicas", 1)},
			},
			deployed: `    prod:
      directory: ./output
      components:
        web:
          infra:
            size: 3
        api:
          infra:
            anything: goes
`,
			raised: []string{".spec.components.web.infra.size"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.raised) == 0 {
				t.Fatal("this row expects nothing, so it cannot tell a working rule from one " +
					"that reports nothing whatever it is given")
			}

			na := normalized(t, conformanceAggregate(tc.components...))
			release := loadRelease(t, releaseDocument(tc.deployed))
			view := newNormalizedView(na, release, catalog, nil)

			objections, err := constraint.RunOnNodes[constraint.NormalizedView](
				nil, newCoordinateSet(view), view, componentInfraConformsToTheType)
			if err != nil {
				t.Fatalf("the node walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, tc.raised) {
				t.Errorf("raised sequence = %v; want %v", got, tc.raised)
			}
		})
	}
}

// TestTheReleaseSizingFactAndTheWiredBoundaryContextCarryOneAnswer measures
// the two derivations of one fact directly, rather than inferring their agreement
// from the rows above passing.
//
// 🔴 The Release's sizing is derived twice from one tree by two walks that
// share no symbol: the presentation derives it for the rule, and the render path
// is handed a pre-computed per-component map. Two walks with no shared symbol is
// the shape that agrees until the day one of them changes, and nothing compiles
// wrongly when it does — so the agreement is asserted here, over a document
// carrying a component at each depth the tree has.
func TestTheReleaseSizingFactAndTheWiredBoundaryContextCarryOneAnswer(t *testing.T) {
	t.Parallel()

	release := loadRelease(t, releaseDocument(`    prod:
      directory: ./output
      components:
        web:
          infra:
            size: outer
      boundaries:
        inner:
          components:
            api:
              infra:
                size: nested
        deeper:
          boundaries:
            deepest:
              components:
                worker:
                  infra:
                    size: deepest
`))
	wired, _ := WireBoundaryInfra(types.NormalizeRelease(release))
	if len(wired) != 3 {
		t.Fatalf("the wired contexts name %d components; want 3 — the document authors one "+
			"at each depth and a walk reaching fewer would make every comparison below "+
			"vacuous", len(wired))
	}

	view := newNormalizedView(
		normalized(t, conformanceAggregate(
			conformanceComponent{name: "api", typ: conformanceContractedType},
			conformanceComponent{name: "web", typ: conformanceContractedType},
			conformanceComponent{name: "worker", typ: conformanceContractedType},
		)),
		release, conformanceRuleCatalog(t), nil)

	for node, bi := range wired {
		fromPresentation := view.ReleaseComponentInfra(node)
		if !equalStrings(fromPresentation.Keys(), bi.Infra.Keys()) {
			t.Errorf("component %q: the view names sizing keys %v; the wired context "+
				"names %v", node, fromPresentation.Keys(), bi.Infra.Keys())
			continue
		}
		for key, want := range bi.Infra.All() {
			got, ok := fromPresentation.Get(key)
			if !ok || got != want {
				t.Errorf("component %q sizing key %q: the view reads %v (present %t); "+
					"the wired context reads %v", node, key, got, ok, want)
			}
		}
	}
}

func TestTheConnectionConfigurationConformanceRuleMeasuresTheAuthoredEntry(t *testing.T) {
	t.Parallel()

	catalog := conformanceRuleCatalog(t)

	for _, tc := range []struct {
		name       string
		components []conformanceComponent
		raised     []string
	}{
		{
			name: "configuration violating the socket's contract is reported; a type declaring no contract on the socket and a type the catalog holds no entry for are not",
			components: []conformanceComponent{
				{name: "web", typ: conformanceContractedType,
					values: conformancePayload("replicas", 1),
					connections: []conformanceConnection{
						{alias: "primary", target: "db",
							values: conformancePayload("pool", "many")},
					}},
				{name: "api", typ: conformanceBareType,
					connections: []conformanceConnection{
						{alias: "primary", target: "db",
							values: conformancePayload("pool", "many")},
					}},
				{name: "ghost", typ: socketAbsentComponent,
					connections: []conformanceConnection{
						{alias: "primary", target: "db",
							values: conformancePayload("pool", "many")},
					}},
			},
			raised: []string{".spec.components.web.uses.store.primary.values.pool"},
		},
		{
			// A connection that wrote nothing is the maximal violation of a key
			// the socket's contract declares required, not an exemption from it.
			name: "a connection that wrote no configuration is measured against a key the socket's contract declares required",
			components: []conformanceComponent{
				{name: "web", typ: conformanceContractedType,
					values: conformancePayload("replicas", 1),
					connections: []conformanceConnection{
						{alias: "primary", target: "db"},
					}},
			},
			raised: []string{".spec.components.web.uses.store.primary.values.pool"},
		},
		{
			// 🔴 The short authoring form. It lists targets and gives each
			// connection the target's own name as its alias, so no key in the file
			// spells that alias — and the locator names none, positioning at the
			// socket the author did write.
			name: "a connection written in the short form is reported at the socket, which is the deepest key the file contains",
			components: []conformanceComponent{
				{name: "web", typ: conformanceContractedType,
					values:  conformancePayload("replicas", 1),
					compact: []string{"db"}},
			},
			raised: []string{".spec.components.web.uses.store.values.pool"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.raised) == 0 {
				t.Fatal("this row expects nothing, so it cannot tell a working rule from one " +
					"that reports nothing whatever it is given")
			}

			na := normalized(t, conformanceAggregate(tc.components...))
			view := newNormalizedView(na, nil, catalog, nil)

			objections, err := constraint.RunOnEdges[constraint.NormalizedView](
				nil, newCoordinateSet(view), view, connectionValuesConformToTheSocket)
			if err != nil {
				t.Fatalf("the connection walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, tc.raised) {
				t.Errorf("raised sequence = %v; want %v", got, tc.raised)
			}
		})
	}
}

// TestTheConnectionSizingConformanceRuleMeasuresWhatTheReleaseWrote proves the
// operator-side per-edge rule is asked of each entry the Release wrote and
// positions its finding inside that entry, in the boundary the Release placed the
// component in — the document and the key the operator edits to fix it.
func TestTheConnectionSizingConformanceRuleMeasuresWhatTheReleaseWrote(t *testing.T) {
	t.Parallel()

	catalog := conformanceRuleCatalog(t)

	for _, tc := range []struct {
		name       string
		components []conformanceComponent
		deployed   string
		raised     []string
	}{
		{
			// The Release configures a connection the Aggregate declared,
			// which is the ordinary shape: the operator supplies sizing for an
			// edge the developer's architecture already draws.
			name: "sizing violating the socket's contract on a connection the Aggregate declared is reported",
			components: []conformanceComponent{
				{name: "web", typ: conformanceContractedType,
					values: conformancePayload("replicas", 1),
					connections: []conformanceConnection{
						{alias: "primary", target: "db",
							values: conformancePayload("pool", 1)},
					}},
			},
			deployed: `    prod:
      directory: ./output
      components:
        web:
          uses:
            store:
              primary:
                infra:
                  tier: 7
`,
			raised: []string{".spec.boundaries.prod.components.web.uses.store.primary.infra.tier"},
		},
		{
			// The Release wires a connection the Aggregate did not declare,
			// which the edge surface permits within a declared socket. The
			// finding is positioned at the entry that wired it, in the Release —
			// the Aggregate's own dependency block holds no such key at all.
			name: "sizing on a connection only the Release wired is reported at the Release's entry",
			components: []conformanceComponent{
				{name: "db", typ: conformanceBareType},
				{name: "web", typ: conformanceContractedType,
					values: conformancePayload("replicas", 1)},
			},
			deployed: `    prod:
      directory: ./output
      components:
        web:
          uses:
            store:
              primary:
                target: db
                infra:
                  tier: 7
`,
			raised: []string{".spec.boundaries.prod.components.web.uses.store.primary.infra.tier"},
		},
		{
			// A connection the Release wrote an entry for but no sizing on is
			// the maximal violation of a key the socket's contract declares
			// required, on the same terms as its configuration counterpart.
			name: "a connection the Release wrote no sizing on is measured against a key the socket's contract declares required",
			components: []conformanceComponent{
				{name: "web", typ: conformanceContractedType,
					values: conformancePayload("replicas", 1),
					connections: []conformanceConnection{
						{alias: "primary", target: "db",
							values: conformancePayload("pool", 1)},
					}},
			},
			deployed: `    prod:
      directory: ./output
      components:
        web:
          uses:
            store:
              primary: {}
`,
			raised: []string{".spec.boundaries.prod.components.web.uses.store.primary.infra.tier"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.raised) == 0 {
				t.Fatal("this row expects nothing, so it cannot tell a working rule from one " +
					"that reports nothing whatever it is given")
			}

			na := normalized(t, conformanceAggregate(tc.components...))
			release := loadRelease(t, releaseDocument(tc.deployed))
			view := newNormalizedView(na, release, catalog, nil)

			objections, err := constraint.RunOnReleaseEdgeConfigurations[constraint.NormalizedView](
				nil, newCoordinateSet(view).withReleaseSource("release.yaml"), view, connectionInfraConformsToTheSocket)
			if err != nil {
				t.Fatalf("the per-edge walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, tc.raised) {
				t.Errorf("raised sequence = %v; want %v", got, tc.raised)
			}
			// The finding names the Release's file as well as the path inside it,
			// so a surface placing it in an open document places it in the
			// Release.
			for _, o := range objections {
				if o.SourceFile() != "release.yaml" {
					t.Errorf("finding at %s names file %q; want the Release's, %q",
						o.Locator(), o.SourceFile(), "release.yaml")
				}
			}
		})
	}
}

// TestTheConformanceRulesReportOneDocumentAtAKnownMultiplicity measures the four
// rules together on one document carrying a fault on every surface at once.
//
// The per-rule tests above each partition to one rule. This one does not: it is
// the number the family contributes as a whole, which is what a reader comparing
// enforcement paths needs and what no per-rule row can state.
func TestTheConformanceRulesReportOneDocumentAtAKnownMultiplicity(t *testing.T) {
	t.Parallel()

	catalog := conformanceRuleCatalog(t)
	agg := conformanceAggregate(conformanceComponent{
		name: "web", typ: conformanceContractedType,
		values: conformancePayload("replicas", "many"),
		connections: []conformanceConnection{
			{alias: "primary", target: "db", values: conformancePayload("pool", "many")},
		},
	})
	na := normalized(t, agg)
	release := loadRelease(t, releaseDocument(`    prod:
      directory: ./output
      components:
        web:
          infra:
            size: 3
          uses:
            store:
              primary:
                infra:
                  tier: 7
`))
	view := newNormalizedView(na, release, catalog, nil)
	coordinates := newCoordinateSet(view)

	var raised int
	for _, check := range []constraint.Check[constraint.NormalizedView, constraint.NodeSubject]{
		componentValuesConformToTheType,
		componentInfraConformsToTheType,
	} {
		objections, err := constraint.RunOnNodes[constraint.NormalizedView](
			nil, coordinates, view, check)
		if err != nil {
			t.Fatalf("the node walk returned an error: %v", err)
		}
		raised += len(objections)
	}
	configuration, err := constraint.RunOnEdges[constraint.NormalizedView](
		nil, coordinates, view, connectionValuesConformToTheSocket)
	if err != nil {
		t.Fatalf("the connection walk returned an error: %v", err)
	}
	raised += len(configuration)
	sizing, err := constraint.RunOnReleaseEdgeConfigurations[constraint.NormalizedView](
		nil, coordinates, view, connectionInfraConformsToTheSocket)
	if err != nil {
		t.Fatalf("the per-edge walk returned an error: %v", err)
	}
	raised += len(sizing)

	const want = 4
	if raised != want {
		t.Errorf("one document faulting on all four surfaces yields %d objections; want %d — "+
			"one per surface", raised, want)
	}
}
