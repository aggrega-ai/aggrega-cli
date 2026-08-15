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
