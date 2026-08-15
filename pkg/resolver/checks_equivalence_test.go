// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Each rule below is stated twice in this package: once as a declared value the
// framework invokes, and once as a hand-written function beside it. The tests
// here hold the two readings of one rule side by side over one input and record
// where they agree and where they do not.
//
// 🔴 Each test calls the hand-written function DIRECTLY, and that is the whole
// point rather than an incidental choice. Nothing in this package routes these
// rules through the framework yet, so comparing against a resolve's findings
// would compare the hand-written reading with itself and pass forever while
// saying nothing about the declared one.
//
// The hand-written functions are therefore deliberate near-duplicates, kept as
// independent cross-checks. They must not be folded into the declared values or
// re-expressed in terms of them: a cross-check sharing an implementation with
// what it checks cannot see the two drift apart. What the two legitimately share
// is the corpus — both walk the same component instances — and what is compared
// is their conclusions.
//
// Every row states both sequences in full rather than deriving one from the
// other, so an ordering difference and a locator-spelling difference are pinned
// here instead of rediscovered later. Coverage — which components and which
// attachments each reading reports about — is the claim that matters, so each row
// also declares whether the two agree on it, and a row that agrees carries an
// empty-set control so the comparison cannot pass over nothing.

// dottedFieldPath rewrites a bracketed key into the dot-separated spelling, so
// the two readings' locators can be compared as coverage.
//
// It is used for the coverage comparison ONLY. Each side's raw sequence is
// asserted verbatim against its own written expectation, so this rewrite cannot
// hide a difference between them — it exists because the two readings spell one
// authored path two ways, and coverage is a question about which key was
// reported rather than about how it was spelled.
func dottedFieldPath(field string) string {
	return strings.NewReplacer(`["`, ".", `"]`, "").Replace(field)
}

// locatorsOf returns each objection's locator, in the order raised.
func locatorsOf(objections []constraint.Objection) []string {
	out := make([]string, 0, len(objections))
	for _, o := range objections {
		out = append(out, o.Locator())
	}
	return out
}

// dottedCopy returns a sorted copy of fields with every bracketed key rewritten,
// which is the canonical form the coverage comparison runs over.
func dottedCopy(fields []string) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, dottedFieldPath(f))
	}
	return sortedCopy(out)
}

// ------------------------------------------------------------------ the rule
// about a component type the catalog holds no entry for

// handWrittenCatalogEntryReading is the hand-written reading of the rule about a
// component type resolved as a catalog identity that the catalog holds no entry
// for.
//
// It reproduces the shape the guards apply — the catalog miss decided at the
// call site, the classification delegated to the one shared classifier — over the
// component walk that reaches every instance, which is the widest of the sites
// that ask it. The classifier itself is called directly and is not restated.
//
// The nil-lookup skip is part of what is being cross-checked rather than an
// omission: every guard that reaches the classifier returns early when the
// catalog offers no component lookup, so a reading without it would not be the
// hand-written reading.
func handWrittenCatalogEntryReading(
	na types.NormalizedAggregate,
	catalog CatalogLookup,
	entity string,
) []*ResolverError {
	if catalog.Component == nil {
		return nil
	}
	var errs []*ResolverError
	for _, p := range collectComponentInstances(na) {
		if _, found := catalog.Component(p.inst.Type); found {
			continue
		}
		if verr := guardEntityViewInconsistency(
			entity, componentFieldPath(p.subsystemPath, p.name, p.synthesized), p.inst.Type,
		); verr != nil {
			errs = append(errs, verr)
		}
	}
	return errs
}

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

func TestTheCatalogEntryRuleAgreesWithItsHandWrittenReading(t *testing.T) {
	t.Parallel()

	const absentType = "core/absent:1.0.0"
	const knownType = "core/known:1.0.0"
	const localType = "./local-variant"

	for _, tc := range []struct {
		name       string
		catalog    CatalogLookup
		components []namedType
		// declared is the sequence the declared rule raises: one locator per
		// component it reports, in the order the node walk reaches them.
		declared []string
		// handWritten is the sequence the hand-written reading reports, spelled
		// the way that reading spells an authored key.
		handWritten []string
		// agree records whether the two cover the same components. It is
		// asserted rather than trusted, so a row cannot change class in silence.
		agree bool
	}{
		{
			// One component of each kind, so the two skips are exercised by the
			// absence of those components from both expectations rather than by
			// a row that reports nothing at all.
			name:    "a catalog identity with no entry is reported; a local path and a held type are not",
			catalog: presentCatalog(knownType),
			components: []namedType{
				{"web", absentType},
				{"cache", knownType},
				{"sidecar", localType},
			},
			declared:    []string{".spec.components.web"},
			handWritten: []string{`.spec.components["web"]`},
			agree:       true,
		},
		{
			// Two reports, authored against their alphabetical order. With a
			// single finding every ordering agrees, which is why the row above
			// cannot carry the ordering claim and this one can.
			name:    "several reports come back in instance-name order on both sides",
			catalog: presentCatalog(),
			components: []namedType{
				{"zeta", absentType},
				{"alpha", absentType},
			},
			declared:    []string{".spec.components.alpha", ".spec.components.zeta"},
			handWritten: []string{`.spec.components["alpha"]`, `.spec.components["zeta"]`},
			agree:       true,
		},
		{
			// 🔴 A measured disagreement, recorded rather than reconciled.
			//
			// A build that consults no catalog at all offers no component
			// lookup. The hand-written reading treats that as nothing to check
			// and reports none; the declared rule reads the absent lookup as an
			// absent entry and reports every component addressed as an identity.
			//
			// The consequence is the whole reason this row is written out: on
			// the day the declared rule becomes the live path, a build with no
			// catalog stops resolving and halts on every component, where today
			// it proceeds.
			name:        "a build consulting no catalog is nothing to check on one side and every component on the other",
			catalog:     CatalogLookup{},
			components:  []namedType{{"web", absentType}},
			declared:    []string{".spec.components.web"},
			handWritten: nil,
			agree:       false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.declared) == 0 && len(tc.handWritten) == 0 {
				t.Fatal("this row expects nothing from either reading, so it cannot tell them " +
					"apart and would pass however either one behaved")
			}
			if tc.agree && len(tc.handWritten) == 0 {
				t.Fatal("an agreement row with no hand-written finding would compare two empty " +
					"sets and report agreement without either reading having read anything")
			}

			agg := catalogEntryAggregate("catalog-entry", tc.components)
			na := normalized(t, agg)
			view := newNormalizedView(na, nil, tc.catalog, nil)

			objections, err := constraint.RunOnNodes[constraint.NormalizedView](
				nil, newCoordinateSet(view), view, componentTypeResolvesToACatalogEntry)
			if err != nil {
				t.Fatalf("the node walk returned an error: %v", err)
			}
			fromDeclared := locatorsOf(objections)
			fromHandWritten := fieldsOf(
				handWrittenCatalogEntryReading(na, tc.catalog, "Aggregate catalog-entry"))

			if !equalStrings(fromDeclared, tc.declared) {
				t.Errorf("declared sequence = %v; want %v", fromDeclared, tc.declared)
			}
			if !equalStrings(fromHandWritten, tc.handWritten) {
				t.Errorf("hand-written sequence = %v; want %v", fromHandWritten, tc.handWritten)
			}

			declaredSet := dottedCopy(fromDeclared)
			handWrittenSet := dottedCopy(fromHandWritten)
			if got := equalStrings(declaredSet, handWrittenSet); got != tc.agree {
				t.Errorf("the two readings cover the same components = %t; want %t:\n"+
					"  declared:     %v\n  hand-written: %v",
					got, tc.agree, declaredSet, handWrittenSet)
			}
		})
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

// boundaryDecl is one boundary of the deployment and the attachment types it
// declares against the one component, in author order.
type boundaryDecl struct {
	name        string
	attachments []namedType
}

// mergeRelease builds a deployment whose named boundaries each write against the
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

func TestTheAttachmentTypeAgreementRuleAgreesWithItsHandWrittenReading(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// authored is the attachment block the application declares, in author
		// order.
		authored []namedType
		// boundaries is what the deployment writes against the same component.
		boundaries  []boundaryDecl
		declared    []string
		handWritten []string
		agree       bool
	}{
		{
			// Every legitimate shape sits beside the one conflict, so the skips
			// are exercised by their absence from both expectations.
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
			declared:    []string{".spec.components.web.features.conflicting"},
			handWritten: []string{`.spec.components["web"].features["conflicting"]`},
			agree:       true,
		},
		{
			// Two conflicts, authored against their alphabetical order, so the
			// row can tell an author-order sequence from a sorted one.
			name: "several conflicts come back in author order and in sorted order respectively",
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
			declared: []string{
				".spec.components.web.features.zebra",
				".spec.components.web.features.antelope",
			},
			handWritten: []string{
				`.spec.components["web"].features["antelope"]`,
				`.spec.components["web"].features["zebra"]`,
			},
			agree: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.declared) == 0 && len(tc.handWritten) == 0 {
				t.Fatal("this row expects nothing from either reading, so it cannot tell them apart")
			}
			if tc.agree && len(tc.handWritten) == 0 {
				t.Fatal("an agreement row with no hand-written finding would compare two empty sets")
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
			fromDeclared := locatorsOf(objections)
			fromHandWritten := fieldsOf(guardFeatureMerge(na, release))

			if !equalStrings(fromDeclared, tc.declared) {
				t.Errorf("declared sequence = %v; want %v", fromDeclared, tc.declared)
			}
			if !equalStrings(fromHandWritten, tc.handWritten) {
				t.Errorf("hand-written sequence = %v; want %v", fromHandWritten, tc.handWritten)
			}

			declaredSet := dottedCopy(fromDeclared)
			handWrittenSet := dottedCopy(fromHandWritten)
			if got := equalStrings(declaredSet, handWrittenSet); got != tc.agree {
				t.Errorf("the two readings cover the same attachments = %t; want %t:\n"+
					"  declared:     %v\n  hand-written: %v",
					got, tc.agree, declaredSet, handWrittenSet)
			}
		})
	}
}

// The two readings of the attachment-type rule fold the deployment's attachments
// at DIFFERENT granularity, and they are equivalent only because a document that
// would tell them apart is refused when it is read.
//
// The hand-written reading merges a component's attachments per attachment name
// across every boundary that names the component. The presentation the declared
// rule reads keeps one boundary's whole attachment block per component, last
// write winning. Those differ exactly when one component is written against from
// more than one boundary — and that document does not load: placing one component
// in two boundaries is refused at read time, so no such Release ever reaches
// either reading.
//
// 🔴 This test exists because a cross-check that fabricated such a Release would
// be asserting a difference over a shape the product cannot produce, and would
// hand the next reader a divergence to design around that does not exist. The
// honest statement is not "the two agree" but "the two agree because of this
// gate", so the gate is what is asserted. If it is ever relaxed, this test turns
// red and the granularity difference becomes a real one to settle.
func TestOneComponentInTwoBoundariesIsRefusedWhenTheDocumentIsRead(t *testing.T) {
	t.Parallel()

	// Both documents below are complete and well-formed apart from the one fault
	// under test, so the refusal is attributable to the second placement rather
	// than to an earlier gate rejecting something else.
	const twoBoundaries = `apiVersion: aggrega.ai/v1alpha1
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
		t.Fatal("a deployment placing one component in two boundaries was accepted.\n" +
			"INVARIANT: each component is declared in exactly one boundary, refused at read time.\n" +
			"CONSEQUENCE: the two readings of the attachment-type rule fold that component's " +
			"attachments at different granularity, so they would report different conflicts " +
			"for one document and neither cross-check above would see it.")
	}
	if !errors.Is(err, ErrDuplicateBoundaryComponent()) {
		t.Fatalf("the refusal does not carry the rule's identity: %v", err)
	}

	// The positive control: the same document with the second placement removed
	// must load, or the refusal above would be reporting some other fault and
	// this test would pass for the wrong reason.
	const oneBoundary = `apiVersion: aggrega.ai/v1alpha1
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

// attachmentSiteOf truncates a dotted locator at the attachment it names.
//
// The two readings of this rule report at different depths — one names the
// attachment, the other the delivery key inside it that offends — so coverage
// here is the question of WHICH attachment is at fault, and that is what this
// key expresses. Each side's full locator is asserted verbatim beside the
// comparison, so the truncation cannot hide a difference in what is reported;
// it only stops a depth difference being read as a coverage difference.
//
// A locator naming no attachment is returned unchanged, so a finding somewhere
// else entirely cannot be folded onto an attachment's key.
func attachmentSiteOf(dotted string) string {
	const marker = ".features."
	at := strings.Index(dotted, marker)
	if at < 0 {
		return dotted
	}
	rest := dotted[at+len(marker):]
	if end := strings.Index(rest, "."); end >= 0 {
		return dotted[:at+len(marker)+end]
	}
	return dotted
}

// attachmentSiteSet returns the sorted attachment sites a reading reported.
func attachmentSiteSet(fields []string) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, attachmentSiteOf(dottedFieldPath(f)))
	}
	return sortedCopy(out)
}

func TestTheSlotPermittedDeliveryRuleAgreesWithItsHandWrittenReading(t *testing.T) {
	t.Parallel()

	// The catalog declares one slot reserving delivery to the Recipe and
	// requiring the fill to name its Feature, and one slot permitting env and
	// file. Both are already exercised by the hand-written reading's own tests.
	catalog := slotShapeCatalog()

	// A fill dictating a mechanism the product does not name as one of its own.
	// Both readings must see it: the block carries such a key alongside the ones
	// it types, and a reading that walked only the typed ones would miss it.
	customMechanismFill := types.FeatureInstance{Type: "core/secret-value"}
	customMechanismFill.Inject = &types.InjectBlock{}
	customMechanismFill.Inject.Additional.Set("flag", []any{map[string]any{"in": "PW", "from": "value"}})

	for _, tc := range []struct {
		name string
		// attachments is the block the application declares, in author order.
		attachments []namedFill
		declared    []string
		handWritten []string
		agree       bool
	}{
		{
			// The free attachment sits beside the violating fill so the open
			// outer object is exercised by its absence from both expectations
			// rather than by a row that reports nothing.
			name: "a mechanism outside the slot's allowlist is reported; a free attachment dictating the same one is not",
			attachments: []namedFill{
				{"user_password", fill("core/secret-value", false, true, "")},
				{"some_monitoring", fill("core/prometheus-exporter", false, true, "")},
			},
			declared:    []string{".spec.components.db.features.user_password"},
			handWritten: []string{`.spec.components["db"].features.user_password.inject.arg`},
			agree:       true,
		},
		{
			// The allowlisted, documented fill sits beside the reserved slot's
			// violating one, for the same reason.
			name: "a slot reserving delivery to the Recipe reports any delivery; an allowlisted documented fill is reported by neither",
			attachments: []namedFill{
				{"root_password", fill("core/secret-value", true, false, "")},
				{"user_password", fill("core/secret-value", true, false, "why this credential is here")},
			},
			declared:    []string{".spec.components.db.features.root_password"},
			handWritten: []string{`.spec.components["db"].features.root_password.inject`},
			agree:       true,
		},
		{
			name: "a mechanism the product does not name as one of its own is reported by both",
			attachments: []namedFill{
				{"user_password", customMechanismFill},
			},
			declared:    []string{".spec.components.db.features.user_password"},
			handWritten: []string{`.spec.components["db"].features.user_password.inject.flag`},
			agree:       true,
		},
		{
			// Two reports, authored against their alphabetical order, so this
			// row can tell an author-order sequence from a sorted one. The rows
			// above each carry a single report, where every ordering agrees.
			name: "several reports come back in author order and in sorted order respectively",
			attachments: []namedFill{
				{"user_password", fill("core/secret-value", false, true, "")},
				{"root_password", fill("core/secret-value", true, false, "")},
			},
			declared: []string{
				".spec.components.db.features.user_password",
				".spec.components.db.features.root_password",
			},
			handWritten: []string{
				`.spec.components["db"].features.root_password.inject`,
				`.spec.components["db"].features.user_password.inject.arg`,
			},
			agree: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.declared) == 0 && len(tc.handWritten) == 0 {
				t.Fatal("this row expects nothing from either reading, so it cannot tell them apart")
			}
			if tc.agree && len(tc.handWritten) == 0 {
				t.Fatal("an agreement row with no hand-written finding would compare two empty sets")
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
			fromDeclared := locatorsOf(objections)
			fromHandWritten := fieldsOf(guardAttachmentSlotShape(na, catalog))

			if !equalStrings(fromDeclared, tc.declared) {
				t.Errorf("declared sequence = %v; want %v", fromDeclared, tc.declared)
			}
			if !equalStrings(fromHandWritten, tc.handWritten) {
				t.Errorf("hand-written sequence = %v; want %v", fromHandWritten, tc.handWritten)
			}

			declaredSet := attachmentSiteSet(fromDeclared)
			handWrittenSet := attachmentSiteSet(fromHandWritten)
			if got := equalStrings(declaredSet, handWrittenSet); got != tc.agree {
				t.Errorf("the two readings report about the same attachments = %t; want %t:\n"+
					"  declared:     %v\n  hand-written: %v",
					got, tc.agree, declaredSet, handWrittenSet)
			}
		})
	}
}

// ------------------------------------------------------------------ the rules
// about a socket name an attachment or a deployment edge wires

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

// declaredWiringPaths returns, per objection, the authored site the rule named
// joined with the socket wired there — the full dotted path of the offending
// entry, which is what the hand-written reading spells directly in its own
// locator. It is the coverage key wherever the two readings name the same
// document's authoring site.
func declaredWiringPaths(objections []constraint.Objection) []string {
	out := make([]string, 0, len(objections))
	for _, o := range objections {
		out = append(out, o.Locator()+".uses."+wiredSocketOfObjection(o))
	}
	return sortedCopy(out)
}

// attachmentWiringOf truncates a dotted locator to the attachment it names and
// the socket wired under it — "features.<attachment>.uses.<socket>".
//
// It is the coverage key for the two deployment-side attachment rules, where the
// site prefix cannot be compared at all: the two readings name DIFFERENT
// documents' authoring sites for one fault. The declared side names the
// component's site on the application document; the hand-written side names the
// deployment's boundary site. Each side's full locator is asserted verbatim
// beside the comparison, so the truncation cannot hide a difference in what is
// reported; it only stops a document difference being read as a coverage one.
//
// A locator naming no attachment is returned unchanged, so a finding somewhere
// else entirely cannot be folded onto an attachment's key.
func attachmentWiringOf(dotted string) string {
	const marker = ".features."
	at := strings.LastIndex(dotted, marker)
	if at < 0 {
		return dotted
	}
	return dotted[at+1:]
}

// declaredAttachmentWiring returns the attachment-and-socket key per objection.
func declaredAttachmentWiring(objections []constraint.Objection) []string {
	out := make([]string, 0, len(objections))
	for _, o := range objections {
		out = append(out, attachmentWiringOf(o.Locator()+".uses."+wiredSocketOfObjection(o)))
	}
	return sortedCopy(out)
}

// handWrittenAttachmentWiring returns the same key from the hand-written side's
// own locators.
func handWrittenAttachmentWiring(fields []string) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, attachmentWiringOf(dottedFieldPath(f)))
	}
	return sortedCopy(out)
}

// wiredSocketOfField returns the socket a dotted locator's trailing dependency
// -block entry names, or the locator itself when it names no such entry.
func wiredSocketOfField(dotted string) string {
	const marker = ".uses."
	at := strings.LastIndex(dotted, marker)
	if at < 0 {
		return dotted
	}
	return dotted[at+len(marker):]
}

// uniqueSorted returns the distinct values of in, sorted.
//
// It is used only where the two readings legitimately report at different
// MULTIPLICITY for one fault — one raises per socket, the other per entry under
// it — so that a multiplicity difference is not read as a coverage difference.
// The raw sequences are asserted separately, which is what pins the multiplicity
// itself.
func uniqueSorted(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return sortedCopy(out)
}

// wiredAttachment is one attachment of an application document: the Feature it
// attaches and the sockets it wires, in author order.
func wiredAttachment(featureType string, sockets ...string) types.FeatureInstance {
	fi := types.FeatureInstance{Type: featureType}
	for _, s := range sockets {
		fi.Uses.Set(s, types.UsesSocketValue{Compact: []string{"anything"}})
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

// releaseDocument wraps a boundary block in the envelope every deployment
// document carries, so a row writes only the part under test.
//
// The envelope matters to the rows rather than being scaffolding: the document
// is read through the real loader below, and a row carrying a second fault in
// the envelope would be refused before the rule under test was ever reached.
func releaseDocument(boundaries string) string {
	return `apiVersion: aggrega.ai/v1alpha1
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

// loadRelease reads a deployment document through the real loader.
//
// 🔴 The rows below build their deployment side this way rather than assembling
// the value directly, and the reason is measured rather than stylistic: the
// loader folds a top-level component surface into the synthesized boundary and
// refuses a document placing one component in two boundaries, so a value
// assembled beside it can encode a shape no deployment reaching either reading
// ever has. A divergence found only in such a value is not a divergence.
func loadRelease(t *testing.T, body string) *types.Release {
	t.Helper()
	release, err := LoadReleaseFromBytes([]byte(body), "release.yaml")
	if err != nil {
		t.Fatalf("the deployment document this row is written against does not load, "+
			"so the row would be testing a shape the product refuses: %v", err)
	}
	return release
}

// The rule over an attachment written on a component, and the hand-written
// reading of the same rule.
//
// The hand-written side is the whole of the walk that reaches every `uses`
// surface, called directly. Each row authors on ONE surface only, so what that
// walk returns is that surface's reading and no partition of its output is
// needed — which is what keeps the comparison from depending on a rule about
// locator shapes that this test would then be asserting rather than measuring.
func TestTheComponentAttachmentSocketRuleAgreesWithItsHandWrittenReading(t *testing.T) {
	t.Parallel()

	catalog := socketNameRuleCatalog()

	for _, tc := range []struct {
		name        string
		attachments []namedFill
		declared    []string
		handWritten []string
		agree       bool
	}{
		{
			// Every legitimate shape sits beside the one fault, so the skips are
			// exercised by their absence from both expectations rather than by a
			// row that reports nothing at all.
			name: "a socket the attached Feature does not declare is reported; one it declares, an attachment wiring nothing, and one whose Feature the catalog holds no entry for are not",
			attachments: []namedFill{
				{"audit", wiredAttachment(socketAuditType, "sink", "ghost")},
				{"quiet", wiredAttachment(socketAuditType)},
				{"mystery", wiredAttachment(socketAbsentFeature, "whatever")},
			},
			declared:    []string{".spec.components.web.features.audit"},
			handWritten: []string{`.spec.components["web"].features["audit"].uses["ghost"]`},
			agree:       true,
		},
		{
			// A Feature declaring no dependency socket at all is the most
			// constrained answer rather than an absent one, so every socket wired
			// on it is refused. Both readings express that as the same comparison.
			name: "a Feature declaring no dependency socket refuses every socket wired on it",
			attachments: []namedFill{
				{"plain", wiredAttachment(socketPlainType, "anything")},
			},
			declared:    []string{".spec.components.web.features.plain"},
			handWritten: []string{`.spec.components["web"].features["plain"].uses["anything"]`},
			agree:       true,
		},
		{
			// Two reports, authored against their alphabetical order, so this row
			// can tell an author-order sequence from a sorted one. The rows above
			// each carry a single report, where every ordering agrees.
			name: "several reports come back in author order and in sorted order respectively",
			attachments: []namedFill{
				{"zulu", wiredAttachment(socketAuditType, "ghost")},
				{"alpha", wiredAttachment(socketAuditType, "ghost")},
			},
			declared: []string{
				".spec.components.web.features.zulu",
				".spec.components.web.features.alpha",
			},
			handWritten: []string{
				`.spec.components["web"].features["alpha"].uses["ghost"]`,
				`.spec.components["web"].features["zulu"].uses["ghost"]`,
			},
			agree: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.declared) == 0 && len(tc.handWritten) == 0 {
				t.Fatal("this row expects nothing from either reading, so it cannot tell them apart")
			}
			if tc.agree && len(tc.handWritten) == 0 {
				t.Fatal("an agreement row with no hand-written finding would compare two empty sets")
			}

			na := normalized(t, socketAggregate(attachmentBlock(tc.attachments...)))
			view := newNormalizedView(na, nil, catalog, nil)
			coordinates := newCoordinateSet(view)

			objections, err := constraint.RunOnNodeAttachments[constraint.NormalizedView](
				nil, coordinates, coordinates, view, componentAttachmentSocketDeclared)
			if err != nil {
				t.Fatalf("the attachment walk returned an error: %v", err)
			}
			fromDeclared := locatorsOf(objections)
			fromHandWritten := fieldsOf(guardUsesSocket(na, nil, catalog))

			if !equalStrings(fromDeclared, tc.declared) {
				t.Errorf("declared sequence = %v; want %v", fromDeclared, tc.declared)
			}
			if !equalStrings(fromHandWritten, tc.handWritten) {
				t.Errorf("hand-written sequence = %v; want %v", fromHandWritten, tc.handWritten)
			}

			declaredSet := declaredWiringPaths(objections)
			handWrittenSet := dottedCopy(fromHandWritten)
			if got := equalStrings(declaredSet, handWrittenSet); got != tc.agree {
				t.Errorf("the two readings cover the same wired sockets = %t; want %t:\n"+
					"  declared:     %v\n  hand-written: %v",
					got, tc.agree, declaredSet, handWrittenSet)
			}
		})
	}
}

// The rule over an attachment the deployment writes against a named component.
//
// 🔴 The two readings do NOT cover the same attachments, and the row that shows
// it is the reason this cross-check exists. The declared rule is invoked once
// per attachment name the APPLICATION document declares on the component, so an
// attachment only the deployment declares is never presented to it — while the
// rule's own contract is that adding such an attachment is supported and must
// therefore be asked of the deployment's declaration. The hand-written reading
// walks the deployment's own block and reports it.
func TestTheDeploymentComponentAttachmentSocketRuleAgreesWithItsHandWrittenReading(t *testing.T) {
	t.Parallel()

	catalog := socketNameRuleCatalog()

	for _, tc := range []struct {
		name string
		// authored is the attachment block the application declares. Its entries
		// wire nothing, so every report below comes from the deployment's block.
		authored []namedFill
		// deployed is the boundary block of the deployment document.
		deployed    string
		declared    []string
		handWritten []string
		agree       bool
	}{
		{
			name: "an attachment both documents declare is reported by both when the deployment wires a socket the Feature does not declare",
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
			declared:    []string{".spec.components.web.features.audit"},
			handWritten: []string{`.spec.boundaries["edge"].components["web"].features["audit"].uses["ghost"]`},
			agree:       true,
		},
		{
			// 🔴 The measured divergence, written out rather than reconciled.
			//
			// The application declares no attachment under this name, so the
			// coordinate the declared rule is invoked per never carries it and the
			// rule is not asked at all. Silence from a rule never asked is
			// indistinguishable from approval, which is what makes this the shape
			// worth pinning: no build goes red, and the enforcement is simply gone.
			name:     "an attachment only the deployment declares is reported by the hand-written reading and by nothing on the declared side",
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
			declared:    nil,
			handWritten: []string{`.spec.boundaries["edge"].components["web"].features["ops_audit"].uses["ghost"]`},
			agree:       false,
		},
		{
			// Two reports, authored against their alphabetical order.
			name: "several reports come back in author order and in sorted order respectively",
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
			declared: []string{
				".spec.components.web.features.zulu",
				".spec.components.web.features.alpha",
			},
			handWritten: []string{
				`.spec.boundaries["edge"].components["web"].features["alpha"].uses["ghost"]`,
				`.spec.boundaries["edge"].components["web"].features["zulu"].uses["ghost"]`,
			},
			agree: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.declared) == 0 && len(tc.handWritten) == 0 {
				t.Fatal("this row expects nothing from either reading, so it cannot tell them apart")
			}
			if tc.agree && len(tc.handWritten) == 0 {
				t.Fatal("an agreement row with no hand-written finding would compare two empty sets")
			}

			na := normalized(t, socketAggregate(attachmentBlock(tc.authored...)))
			release := loadRelease(t, releaseDocument(tc.deployed))
			view := newNormalizedView(na, release, catalog, nil)
			coordinates := newCoordinateSet(view)

			objections, err := constraint.RunOnNodeAttachments[constraint.NormalizedView](
				nil, coordinates, coordinates, view, releaseComponentAttachmentSocketDeclared)
			if err != nil {
				t.Fatalf("the attachment walk returned an error: %v", err)
			}
			fromDeclared := locatorsOf(objections)
			fromHandWritten := fieldsOf(guardUsesSocket(na, release, catalog))

			if !equalStrings(fromDeclared, tc.declared) {
				t.Errorf("declared sequence = %v; want %v", fromDeclared, tc.declared)
			}
			if !equalStrings(fromHandWritten, tc.handWritten) {
				t.Errorf("hand-written sequence = %v; want %v", fromHandWritten, tc.handWritten)
			}

			declaredSet := declaredAttachmentWiring(objections)
			handWrittenSet := handWrittenAttachmentWiring(fromHandWritten)
			if got := equalStrings(declaredSet, handWrittenSet); got != tc.agree {
				t.Errorf("the two readings cover the same wired sockets = %t; want %t:\n"+
					"  declared:     %v\n  hand-written: %v",
					got, tc.agree, declaredSet, handWrittenSet)
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

// scopeDecl is one declared scope of the application document: the nesting the
// author wrote it under, and the attachments it carries.
type scopeDecl struct {
	levels      []string
	attachments []namedFill
}

// scopedAggregate builds an application document declaring the supplied scopes,
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

// The rule over an attachment written at a scope of the application document.
//
// The two readings name the same authoring site here, nesting included: both
// repeat the container's own key between the levels of a nested scope, so the
// full dotted path is the coverage key.
func TestTheScopeAttachmentSocketRuleAgreesWithItsHandWrittenReading(t *testing.T) {
	t.Parallel()

	catalog := socketNameRuleCatalog()

	for _, tc := range []struct {
		name        string
		scopes      []scopeDecl
		declared    []string
		handWritten []string
		agree       bool
	}{
		{
			name: "a socket the attached Feature does not declare is reported; one it declares is not",
			scopes: []scopeDecl{{
				levels: []string{"data"},
				attachments: []namedFill{
					{"audit", wiredAttachment(socketAuditType, "sink", "ghost")},
				},
			}},
			declared:    []string{".spec.subsystems.data.features.audit"},
			handWritten: []string{`.spec.subsystems.data.features["audit"].uses["ghost"]`},
			agree:       true,
		},
		{
			// A nested scope names every level under the container's own key, on
			// both sides. This row is what makes that agreement measured rather
			// than assumed — a flat scope cannot tell a repeated container key
			// from a joined path.
			name: "a scope nested inside another names every level on both sides",
			scopes: []scopeDecl{{
				levels: []string{"data", "warm"},
				attachments: []namedFill{
					{"audit", wiredAttachment(socketAuditType, "ghost")},
				},
			}},
			declared:    []string{".spec.subsystems.data.subsystems.warm.features.audit"},
			handWritten: []string{`.spec.subsystems.data.subsystems.warm.features["audit"].uses["ghost"]`},
			agree:       true,
		},
		{
			// Two scopes, authored against their alphabetical order.
			name: "several reports come back in author order and in sorted order respectively",
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
			declared: []string{
				".spec.subsystems.zulu.features.audit",
				".spec.subsystems.alpha.features.audit",
			},
			handWritten: []string{
				`.spec.subsystems.alpha.features["audit"].uses["ghost"]`,
				`.spec.subsystems.zulu.features["audit"].uses["ghost"]`,
			},
			agree: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.declared) == 0 && len(tc.handWritten) == 0 {
				t.Fatal("this row expects nothing from either reading, so it cannot tell them apart")
			}
			if tc.agree && len(tc.handWritten) == 0 {
				t.Fatal("an agreement row with no hand-written finding would compare two empty sets")
			}

			na := normalized(t, scopedAggregate(tc.scopes))
			view := newNormalizedView(na, nil, catalog, nil)
			coordinates := newCoordinateSet(view)

			objections, err := constraint.RunOnSubsystemAttachments[constraint.NormalizedView](
				nil, coordinates, coordinates, view, subsystemAttachmentSocketDeclared)
			if err != nil {
				t.Fatalf("the scope walk returned an error: %v", err)
			}
			fromDeclared := locatorsOf(objections)
			fromHandWritten := fieldsOf(guardUsesSocket(na, nil, catalog))

			if !equalStrings(fromDeclared, tc.declared) {
				t.Errorf("declared sequence = %v; want %v", fromDeclared, tc.declared)
			}
			if !equalStrings(fromHandWritten, tc.handWritten) {
				t.Errorf("hand-written sequence = %v; want %v", fromHandWritten, tc.handWritten)
			}

			declaredSet := declaredWiringPaths(objections)
			handWrittenSet := dottedCopy(fromHandWritten)
			if got := equalStrings(declaredSet, handWrittenSet); got != tc.agree {
				t.Errorf("the two readings cover the same wired sockets = %t; want %t:\n"+
					"  declared:     %v\n  hand-written: %v",
					got, tc.agree, declaredSet, handWrittenSet)
			}
		})
	}
}

// The rule over an attachment written at a scope of the deployment document.
//
// 🔴 The two readings spell a NESTED scope's site differently, and the row below
// pins it: the declared side repeats the container's own key between the levels,
// naming a path the document contains, while the hand-written side joins the
// levels with the separator the compiler composes the path with and emits a key
// no file has. The coverage key is therefore the attachment and the socket,
// which both express, and each side's full locator is asserted verbatim beside
// it.
func TestTheDeploymentScopeAttachmentSocketRuleAgreesWithItsHandWrittenReading(t *testing.T) {
	t.Parallel()

	catalog := socketNameRuleCatalog()

	for _, tc := range []struct {
		name        string
		deployed    string
		declared    []string
		handWritten []string
		agree       bool
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
			declared:    []string{".spec.boundaries.edge.features.audit"},
			handWritten: []string{`.spec.boundaries["edge"].features["audit"].uses["ghost"]`},
			agree:       true,
		},
		{
			name: "a scope nested inside another is named level by level on one side and as a joined path on the other",
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
			declared:    []string{".spec.boundaries.edge.boundaries.inner.features.audit"},
			handWritten: []string{`.spec.boundaries["edge/inner"].features["audit"].uses["ghost"]`},
			agree:       true,
		},
		{
			// Two scopes, authored against their alphabetical order.
			name: "several reports come back in author order and in sorted order respectively",
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
			declared: []string{
				".spec.boundaries.zulu.features.audit",
				".spec.boundaries.alpha.features.tracing",
			},
			handWritten: []string{
				`.spec.boundaries["alpha"].features["tracing"].uses["ghost"]`,
				`.spec.boundaries["zulu"].features["audit"].uses["ghost"]`,
			},
			agree: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.declared) == 0 && len(tc.handWritten) == 0 {
				t.Fatal("this row expects nothing from either reading, so it cannot tell them apart")
			}
			if tc.agree && len(tc.handWritten) == 0 {
				t.Fatal("an agreement row with no hand-written finding would compare two empty sets")
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
			fromDeclared := locatorsOf(objections)
			fromHandWritten := fieldsOf(guardUsesSocket(na, release, catalog))

			if !equalStrings(fromDeclared, tc.declared) {
				t.Errorf("declared sequence = %v; want %v", fromDeclared, tc.declared)
			}
			if !equalStrings(fromHandWritten, tc.handWritten) {
				t.Errorf("hand-written sequence = %v; want %v", fromHandWritten, tc.handWritten)
			}

			declaredSet := declaredAttachmentWiring(objections)
			handWrittenSet := handWrittenAttachmentWiring(fromHandWritten)
			if got := equalStrings(declaredSet, handWrittenSet); got != tc.agree {
				t.Errorf("the two readings cover the same wired sockets = %t; want %t:\n"+
					"  declared:     %v\n  hand-written: %v",
					got, tc.agree, declaredSet, handWrittenSet)
			}
		})
	}
}

// edgeAggregate builds the two components the deployment-edge rows wire between.
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

// declaredSocketsOf returns the socket each objection names, in the order raised.
func declaredSocketsOf(objections []constraint.Objection) []string {
	out := make([]string, 0, len(objections))
	for _, o := range objections {
		out = append(out, wiredSocketOfObjection(o))
	}
	return out
}

// handWrittenSocketsOf returns the socket each hand-written locator names.
func handWrittenSocketsOf(fields []string) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, wiredSocketOfField(dottedFieldPath(f)))
	}
	return out
}

// The rule over a socket the deployment's own edge surface wires.
//
// 🔴 Two differences are pinned here rather than reconciled, and both are
// properties of the rule rather than slips. The two readings raise at different
// MULTIPLICITY — the declared rule once per offending socket, the hand-written
// reading once per entry written under it — and they name different documents'
// authoring sites, the declared one the component's site on the application
// document and the hand-written one the deployment's boundary site. So the
// declared side's locator sequence is degenerate for this rule (every objection
// names the same component), which is why the socket sequence is asserted
// beside it: without that, an ordering change would be invisible.
func TestTheDeploymentEdgeSocketRuleAgreesWithItsHandWrittenReading(t *testing.T) {
	t.Parallel()

	catalog := socketNameRuleCatalog()

	for _, tc := range []struct {
		name     string
		deployed string
		// declaredLocators is the site the declared rule names, once per
		// objection. It is the component, not the socket.
		declaredLocators []string
		// declaredSockets is the socket each objection carries, in the order
		// raised — the sequence the locators cannot express.
		declaredSockets []string
		handWritten     []string
		agree           bool
	}{
		{
			// The declared socket and the component the application does not
			// declare sit beside the one fault, so both skips are exercised by
			// their absence from the expectations.
			name: "a socket the consuming component's type does not declare is reported; a declared one and a component the application never declared are not",
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
			declaredLocators: []string{".spec.components.web"},
			declaredSockets:  []string{"ghost"},
			handWritten:      []string{`.spec.boundaries["edge"].components["web"].uses["ghost"]`},
			agree:            true,
		},
		{
			// The multiplicity difference, written out. Two entries under one
			// undeclared socket: one objection on the declared side, one finding
			// per entry on the hand-written side — and the two hand-written
			// locators are identical, because that locator names the socket and
			// not the entry.
			name: "two entries under one undeclared socket draw one report on the declared side and one per entry on the other",
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
			declaredLocators: []string{".spec.components.web"},
			declaredSockets:  []string{"ghost"},
			handWritten: []string{
				`.spec.boundaries["edge"].components["web"].uses["ghost"]`,
				`.spec.boundaries["edge"].components["web"].uses["ghost"]`,
			},
			agree: true,
		},
		{
			// Two sockets, authored against their alphabetical order.
			name: "several reports come back in author order and in sorted order respectively",
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
			declaredLocators: []string{".spec.components.web", ".spec.components.web"},
			declaredSockets:  []string{"zulu", "alpha"},
			handWritten: []string{
				`.spec.boundaries["edge"].components["web"].uses["alpha"]`,
				`.spec.boundaries["edge"].components["web"].uses["zulu"]`,
			},
			agree: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.declaredLocators) == 0 && len(tc.handWritten) == 0 {
				t.Fatal("this row expects nothing from either reading, so it cannot tell them apart")
			}
			if tc.agree && len(tc.handWritten) == 0 {
				t.Fatal("an agreement row with no hand-written finding would compare two empty sets")
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
			fromDeclared := locatorsOf(objections)
			fromHandWritten := fieldsOf(guardReleaseUsesSocket(na, release, catalog))

			if !equalStrings(fromDeclared, tc.declaredLocators) {
				t.Errorf("declared locator sequence = %v; want %v", fromDeclared, tc.declaredLocators)
			}
			if got := declaredSocketsOf(objections); !equalStrings(got, tc.declaredSockets) {
				t.Errorf("declared socket sequence = %v; want %v", got, tc.declaredSockets)
			}
			if !equalStrings(fromHandWritten, tc.handWritten) {
				t.Errorf("hand-written sequence = %v; want %v", fromHandWritten, tc.handWritten)
			}

			declaredSet := uniqueSorted(declaredSocketsOf(objections))
			handWrittenSet := uniqueSorted(handWrittenSocketsOf(fromHandWritten))
			if got := equalStrings(declaredSet, handWrittenSet); got != tc.agree {
				t.Errorf("the two readings cover the same sockets = %t; want %t:\n"+
					"  declared:     %v\n  hand-written: %v",
					got, tc.agree, declaredSet, handWrittenSet)
			}
		})
	}
}

// A deployment wiring an edge on a component whose type the catalog holds no
// entry for is where the two readings part, and they part because they are not
// answering the same question.
//
// The declared rule ends there: a type nobody found declares an empty socket
// set, and refusing every socket wired on it would report the wrong thing. The
// hand-written function does not end there — it is the invocation site of a
// SECOND rule as well, the one about a type resolved as a catalog identity the
// catalog holds no entry for, and it raises that rule's identity from inside
// this walk.
//
// 🔴 This is written as its own test rather than as a row because the claim is
// about which identity is raised, and the rows above compare locators. The
// second rule's own declared value is cross-checked separately; what is measured
// here is that the finding this walk emits carries that rule's identity and not
// the socket rule's, so whoever collapses these two invocation paths into one
// can route the finding rather than lose it.
func TestTheDeploymentEdgeReadingsPartWhenTheCatalogHoldsNoEntryForTheConsumer(t *testing.T) {
	t.Parallel()

	catalog := socketNameRuleCatalog()
	na := normalized(t, edgeAggregate(socketAbsentComponent))
	release := loadRelease(t, releaseDocument(`    edge:
      directory: ./output
      components:
        web:
          uses:
            ghost:
              spare:
                target: cache
`))
	view := newNormalizedView(na, release, catalog, nil)
	coordinates := newCoordinateSet(view)

	objections, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, coordinates, view, releaseEdgeSocketDeclared)
	if err != nil {
		t.Fatalf("the node walk returned an error: %v", err)
	}
	if len(objections) != 0 {
		t.Errorf("the declared rule reported on a type the catalog holds no entry for: %v",
			locatorsOf(objections))
	}

	handWritten := guardReleaseUsesSocket(na, release, catalog)
	if len(handWritten) != 1 {
		t.Fatalf("hand-written findings = %d (%v); want exactly 1", len(handWritten), fieldsOf(handWritten))
	}
	if errors.Is(handWritten[0], ErrUnknownUsesSocket()) {
		t.Error("the hand-written finding carries the socket rule's identity, so the two readings " +
			"are reporting the same rule and this test's premise is wrong")
	}
	if !errors.Is(handWritten[0], ErrGuardEntityViewInconsistency()) {
		t.Errorf("the hand-written finding carries neither rule's identity: %v", handWritten[0])
	}
	if want := `.spec.boundaries["edge"].components["web"]`; handWritten[0].Field != want {
		t.Errorf("hand-written locator = %q; want %q, the deployment's own site", handWritten[0].Field, want)
	}

	// The positive control: the same document against a type the catalog holds
	// must report the socket rule instead, or the silence above would be the
	// walk finding nothing at all rather than parting from the declared rule.
	held := normalized(t, edgeAggregate(socketWebType))
	heldView := newNormalizedView(held, release, catalog, nil)
	heldCoordinates := newCoordinateSet(heldView)
	control, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, heldCoordinates, heldView, releaseEdgeSocketDeclared)
	if err != nil {
		t.Fatalf("the control node walk returned an error: %v", err)
	}
	if len(control) != 1 {
		t.Fatalf("control objections = %d; want 1, or the input above reports nothing for a "+
			"reason other than the catalog", len(control))
	}
}

// ------------------------------------------------------------------ the rules
// about a delivery entry reading from a key a component type does not publish

// injectCatalog is the catalog the delivery-reference rows consult: one
// component type publishing two keys and one publishing a single key.
func injectCatalog(t *testing.T) CatalogLookup {
	t.Helper()
	consumer := componentWithProvides(t, "host", "port")
	target := componentWithProvides(t, "password")
	return CatalogLookup{Component: func(uri string) (types.Component, bool) {
		switch uri {
		case socketWebType:
			return consumer, true
		case socketCacheType:
			return target, true
		}
		return types.Component{}, false
	}}
}

// verboseDelivery is one delivery entry in the array authoring form: the key
// delivered, and the published key it reads from. An empty reference writes a
// template instead, which is nothing to resolve and must be skipped by both
// readings.
func verboseDelivery(deliveryKey, reference string) map[string]any {
	if reference == "" {
		return map[string]any{"in": deliveryKey, "value": "a template"}
	}
	return map[string]any{"in": deliveryKey, "from": reference}
}

// deliveryBlock is what one injection block delivers, per mechanism. The array
// authoring form is used throughout because it is the one shape carrying the
// author's own order — a mapping form carries none and is read back
// alphabetically, so a row written against one could not tell an answer that
// preserved the author's order from an answer that sorted it.
type deliveryBlock struct {
	env []any
	arg []any
}

// injectBlockOf assembles an injection block, or none where nothing is written.
func injectBlockOf(b deliveryBlock) *types.InjectBlock {
	if b.env == nil && b.arg == nil {
		return nil
	}
	out := &types.InjectBlock{}
	if b.env != nil {
		out.Env = b.env
	}
	if b.arg != nil {
		out.Arg = b.arg
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
//
// 🔴 The two readings spell one locator IDENTICALLY here once the bracketed keys
// are rewritten, which makes the locator itself the coverage key — the strongest
// agreement in this family. That is worth saying because the hand-written
// function ALSO writes a locator that is not a path at all, on a different arm:
// the arm reached only when the catalog holds no entry for the resolved type.
// That arm is measured in its own test below, and it is the only place where the
// locator is unusable as a key.
func TestTheSelfDeliveryReferenceRuleAgreesWithItsHandWrittenReading(t *testing.T) {
	t.Parallel()

	catalog := injectCatalog(t)

	for _, tc := range []struct {
		name        string
		block       deliveryBlock
		declared    []string
		handWritten []string
		agree       bool
	}{
		{
			// The entry reading a published key and the entry carrying a template
			// sit beside the one fault, so both skips are exercised by their
			// absence from the expectations.
			name: "an entry reading from a key the type does not publish is reported; one reading a published key and one carrying a template are not",
			block: deliveryBlock{env: []any{
				verboseDelivery("DB_HOST", "host"),
				verboseDelivery("BAD", "nope"),
				verboseDelivery("LITERAL", ""),
			}},
			declared:    []string{".spec.components.web.inject.env.BAD"},
			handWritten: []string{`.spec.components["web"].inject.env["BAD"]`},
			agree:       true,
		},
		{
			// Two reports, authored against their alphabetical order.
			name: "several reports come back in author order and in sorted order respectively",
			block: deliveryBlock{env: []any{
				verboseDelivery("ZED", "nope"),
				verboseDelivery("ALPHA", "nope"),
			}},
			declared: []string{
				".spec.components.web.inject.env.ZED",
				".spec.components.web.inject.env.ALPHA",
			},
			handWritten: []string{
				`.spec.components["web"].inject.env["ALPHA"]`,
				`.spec.components["web"].inject.env["ZED"]`,
			},
			agree: true,
		},
		{
			// Two mechanisms, so the row can tell the mechanism enumeration order
			// from the sort the hand-written reading applies at the end.
			name: "two mechanisms come back in the order the block declares them and in sorted order respectively",
			block: deliveryBlock{
				env: []any{verboseDelivery("E", "nope")},
				arg: []any{verboseDelivery("A", "nope")},
			},
			declared: []string{
				".spec.components.web.inject.env.E",
				".spec.components.web.inject.arg.A",
			},
			handWritten: []string{
				`.spec.components["web"].inject.arg["A"]`,
				`.spec.components["web"].inject.env["E"]`,
			},
			agree: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.declared) == 0 && len(tc.handWritten) == 0 {
				t.Fatal("this row expects nothing from either reading, so it cannot tell them apart")
			}
			if tc.agree && len(tc.handWritten) == 0 {
				t.Fatal("an agreement row with no hand-written finding would compare two empty sets")
			}

			na := normalized(t, selfInjectAggregate(socketWebType, tc.block))
			view := newNormalizedView(na, nil, catalog, nil)
			coordinates := newCoordinateSet(view)

			objections, err := constraint.RunOnNodeMechanismDeliveryKeys[constraint.NormalizedView](
				nil, coordinates, coordinates, coordinates, view, selfInjectReferenceIsPublished)
			if err != nil {
				t.Fatalf("the delivery-key walk returned an error: %v", err)
			}
			fromDeclared := locatorsOf(objections)
			fromHandWritten := fieldsOf(guardInjectKeys(na, catalog, view))

			if !equalStrings(fromDeclared, tc.declared) {
				t.Errorf("declared sequence = %v; want %v", fromDeclared, tc.declared)
			}
			if !equalStrings(fromHandWritten, tc.handWritten) {
				t.Errorf("hand-written sequence = %v; want %v", fromHandWritten, tc.handWritten)
			}

			declaredSet := sortedCopy(fromDeclared)
			handWrittenSet := dottedCopy(fromHandWritten)
			if got := equalStrings(declaredSet, handWrittenSet); got != tc.agree {
				t.Errorf("the two readings cover the same delivery entries = %t; want %t:\n"+
					"  declared:     %v\n  hand-written: %v",
					got, tc.agree, declaredSet, handWrittenSet)
			}
		})
	}
}

// A component whose type the catalog holds no entry for is where the delivery
// -reference readings part, and the shape of the parting is the thing to record.
//
// The declared rule ends there, because the published set reads empty for such a
// type exactly as it does for a type publishing nothing. The hand-written
// function raises a SECOND rule's identity instead — the one about a type
// resolved as a catalog identity nobody found — and it raises it ONCE PER TYPE
// rather than once per component, with a locator that is not a path at all.
//
// 🔴 Both halves are measured here rather than predicted, because both survive
// any collapse of the two readings into one: the per-type fold changes how many
// diagnostics one input draws, and a locator that is not a path cannot be
// positioned at all by the consumer that reads these locators.
func TestTheSelfDeliveryReadingsPartWhenTheCatalogHoldsNoEntryForTheComponent(t *testing.T) {
	t.Parallel()

	const otherAbsent = "core/absent-other:1.0.0"
	catalog := injectCatalog(t)

	// Three components: two of one type the catalog holds no entry for, one of
	// another. Each writes one delivery entry reading a key nothing publishes.
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
		t.Errorf("the declared rule reported against a type the catalog holds no entry for: %v",
			locatorsOf(objections))
	}

	handWritten := guardInjectKeys(na, catalog, view)
	// Three components, two types: one finding per TYPE is the claim.
	want := []string{
		`inject resolution for type "core/absent-component:1.0.0"`,
		`inject resolution for type "core/absent-other:1.0.0"`,
	}
	if got := fieldsOf(handWritten); !equalStrings(got, want) {
		t.Errorf("hand-written locators = %v; want %v — one per type, not one per component", got, want)
	}
	for _, finding := range handWritten {
		if errors.Is(finding, ErrInvalidInjectKey()) {
			t.Error("the hand-written finding carries the delivery-reference rule's identity, so " +
				"this test's premise is wrong")
		}
		if !errors.Is(finding, ErrGuardEntityViewInconsistency()) {
			t.Errorf("the hand-written finding carries neither rule's identity: %v", finding)
		}
		if strings.HasPrefix(finding.Field, ".") {
			t.Errorf("locator %q reads as an authored path; the claim under test is that it is "+
				"not one", finding.Field)
		}
	}

	// The positive control: the same three components against a type the catalog
	// holds must draw one delivery-reference finding each, or the silence above
	// is the walk reading nothing rather than the two readings parting.
	held := types.OrderedMap[types.ComponentInstance]{}
	held.Set("alpha", types.ComponentInstance{Type: socketWebType, Inject: injectBlockOf(block)})
	held.Set("beta", types.ComponentInstance{Type: socketWebType, Inject: injectBlockOf(block)})
	held.Set("gamma", types.ComponentInstance{Type: socketWebType, Inject: injectBlockOf(block)})
	control := normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "delivery-references"},
		Spec:     types.AggregateSpec{Components: held},
	})
	controlView := newNormalizedView(control, nil, catalog, nil)
	if got := len(guardInjectKeys(control, catalog, controlView)); got != 3 {
		t.Fatalf("control findings = %d; want 3, one per component", got)
	}
	// 🔴 The declared side needs its own control, and the reason is that without
	// one this test's first assertion is unfalsifiable: a rule that raised
	// nothing whatever the input would satisfy "reported nothing here" forever.
	controlCoordinates := newCoordinateSet(controlView)
	controlObjections, err := constraint.RunOnNodeMechanismDeliveryKeys[constraint.NormalizedView](
		nil, controlCoordinates, controlCoordinates, controlCoordinates, controlView,
		selfInjectReferenceIsPublished)
	if err != nil {
		t.Fatalf("the control delivery-key walk returned an error: %v", err)
	}
	if len(controlObjections) != 3 {
		t.Fatalf("control objections = %d; want 3, or the silence above is the rule reading "+
			"nothing rather than the two readings parting", len(controlObjections))
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
// path of the offending entry, which is what the hand-written reading spells
// directly in its own locator.
//
// The declared rule raises every objection for one connection at that
// connection's locator, so the locator sequence alone cannot express which
// entries were reported or in what order. This is the sequence that can.
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
// The two readings spell one locator identically once the bracketed keys are
// rewritten, so the full delivery path is the coverage key. What differs is
// ORDER, and the difference is not the one the rest of this family shows: this
// declared rule sorts a connection's entries by delivery key rather than raising
// in author order, because only one of the shapes an author may write the
// payload in carries an order at all.
func TestTheConnectionDeliveryReferenceRuleAgreesWithItsHandWrittenReading(t *testing.T) {
	t.Parallel()

	catalog := injectCatalog(t)

	for _, tc := range []struct {
		name        string
		connections []namedConnection
		// declaredLocators is the connection each objection names, once per
		// objection — degenerate where one connection draws several.
		declaredLocators []string
		// declaredDeliveries is the entry each objection names, in the order
		// raised.
		declaredDeliveries []string
		handWritten        []string
		agree              bool
	}{
		{
			// The entry reading a published key, the connection dictating no
			// delivery at all, and the connection naming a component the
			// application does not declare all sit beside the one fault.
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
			declaredLocators:   []string{".spec.components.web.uses.db.primary"},
			declaredDeliveries: []string{".spec.components.web.uses.db.primary.inject.env.BAD"},
			handWritten:        []string{`.spec.components["web"].uses["db"]["primary"].inject.env["BAD"]`},
			agree:              true,
		},
		{
			// An entry naming no component falls back to the alias on both sides,
			// which is the same fallback that draws the edge.
			name: "a connection naming no component resolves against its own alias on both sides",
			connections: []namedConnection{
				{alias: "cache", block: deliveryBlock{env: []any{verboseDelivery("BAD", "nope")}}},
			},
			declaredLocators:   []string{".spec.components.web.uses.db.cache"},
			declaredDeliveries: []string{".spec.components.web.uses.db.cache.inject.env.BAD"},
			handWritten:        []string{`.spec.components["web"].uses["db"]["cache"].inject.env["BAD"]`},
			agree:              true,
		},
		{
			// Two entries of one mechanism, authored against their alphabetical
			// order. Both sides report alphabetically — the declared rule because
			// it sorts a connection's entries by delivery key, the hand-written
			// one because it sorts its findings at the end. This row is what makes
			// that measured: it is the only rule in this family whose declared
			// side does NOT raise in author order.
			name: "entries of one mechanism come back in delivery-key order on both sides, not in author order",
			connections: []namedConnection{
				{alias: "primary", target: "cache", block: deliveryBlock{env: []any{
					verboseDelivery("ZED", "nope"),
					verboseDelivery("ALPHA", "nope"),
				}}},
			},
			declaredLocators: []string{
				".spec.components.web.uses.db.primary",
				".spec.components.web.uses.db.primary",
			},
			declaredDeliveries: []string{
				".spec.components.web.uses.db.primary.inject.env.ALPHA",
				".spec.components.web.uses.db.primary.inject.env.ZED",
			},
			handWritten: []string{
				`.spec.components["web"].uses["db"]["primary"].inject.env["ALPHA"]`,
				`.spec.components["web"].uses["db"]["primary"].inject.env["ZED"]`,
			},
			agree: true,
		},
		{
			// Two mechanisms, so the row can tell the mechanism enumeration order
			// from the sort applied at the end.
			name: "two mechanisms come back in the order the block declares them and in sorted order respectively",
			connections: []namedConnection{
				{alias: "primary", target: "cache", block: deliveryBlock{
					env: []any{verboseDelivery("E", "nope")},
					arg: []any{verboseDelivery("A", "nope")},
				}},
			},
			declaredLocators: []string{
				".spec.components.web.uses.db.primary",
				".spec.components.web.uses.db.primary",
			},
			declaredDeliveries: []string{
				".spec.components.web.uses.db.primary.inject.env.E",
				".spec.components.web.uses.db.primary.inject.arg.A",
			},
			handWritten: []string{
				`.spec.components["web"].uses["db"]["primary"].inject.arg["A"]`,
				`.spec.components["web"].uses["db"]["primary"].inject.env["E"]`,
			},
			agree: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.declaredDeliveries) == 0 && len(tc.handWritten) == 0 {
				t.Fatal("this row expects nothing from either reading, so it cannot tell them apart")
			}
			if tc.agree && len(tc.handWritten) == 0 {
				t.Fatal("an agreement row with no hand-written finding would compare two empty sets")
			}

			na := normalized(t, connectionAggregate(tc.connections))
			view := newNormalizedView(na, nil, catalog, nil)
			coordinates := newCoordinateSet(view)

			objections, err := constraint.RunOnEdges[constraint.NormalizedView](
				nil, coordinates, view, connectionInjectReferenceIsPublished)
			if err != nil {
				t.Fatalf("the connection walk returned an error: %v", err)
			}
			fromDeclared := locatorsOf(objections)
			fromHandWritten := fieldsOf(guardInjectKeys(na, catalog, view))

			if !equalStrings(fromDeclared, tc.declaredLocators) {
				t.Errorf("declared locator sequence = %v; want %v", fromDeclared, tc.declaredLocators)
			}
			if got := declaredDeliveryPaths(objections); !equalStrings(got, tc.declaredDeliveries) {
				t.Errorf("declared entry sequence = %v; want %v", got, tc.declaredDeliveries)
			}
			if !equalStrings(fromHandWritten, tc.handWritten) {
				t.Errorf("hand-written sequence = %v; want %v", fromHandWritten, tc.handWritten)
			}

			declaredSet := sortedCopy(declaredDeliveryPaths(objections))
			handWrittenSet := dottedCopy(fromHandWritten)
			if got := equalStrings(declaredSet, handWrittenSet); got != tc.agree {
				t.Errorf("the two readings cover the same delivery entries = %t; want %t:\n"+
					"  declared:     %v\n  hand-written: %v",
					got, tc.agree, declaredSet, handWrittenSet)
			}
		})
	}
}
