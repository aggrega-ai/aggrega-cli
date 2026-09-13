// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	stderrors "errors"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The four annotation conditions each carry three identifiers, and this file
// renders the output each of them actually emits.
//
// Two properties are pinned that neither a unit test of the predicate nor the
// generated reference can pin on its own. The three identifiers are asserted
// together, because a condition matchable from Go but carrying no code is
// unreportable to a consumer outside Go and a code with no matchable sentinel is
// the reverse; and the emitted text is asserted in full, because the published
// reference quotes these forms and a form nobody renders is a form nobody
// notices moving.
//
// Every rendered form here is asserted to carry no category token of its own.
// A category is a property of a particular failure rather than of the condition
// behind it — it is derived from the entity kind at the reporting boundary and
// printed once, in the head — so a category word written into the text is a
// second copy of a derived value, and it renders twice wherever the head is
// composed.

// categoryTokens are the category words a diagnostic head may carry. None of
// them belongs in the text a raise site supplies.
//
// Each word is read off the declared value rather than re-spelled, so a value
// whose spelling changes moves this list with it. The enumeration is still
// written out because the values are constants and Go offers no walk over them;
// a value added beside them and omitted here leaves its occurrences uncounted,
// which reads as a clean result rather than as a gap.
var categoryTokens = []string{
	string(cerrors.DeveloperError),
	string(cerrors.OpsError),
	string(cerrors.CatalogError),
	string(cerrors.TranslationError),
	string(cerrors.FetchError),
	string(cerrors.PolicyError),
	string(cerrors.TrustError),
}

// assertIdentity pins all three identifiers of one condition on one error.
func assertIdentity(t *testing.T, err error, sentinel error, code, shortCode string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error carrying a registered condition; got nil")
	}
	if !stderrors.Is(err, sentinel) {
		t.Errorf("errors.Is against the condition's sentinel = false, want true; got %v", err)
	}
	if got, ok := cerrors.CodeOf(err); !ok || got != code {
		t.Errorf("CodeOf = %q (found=%v), want %q", got, ok, code)
	}
	if got, ok := cerrors.ShortCodeOf(err); !ok || got != shortCode {
		t.Errorf("ShortCodeOf = %q (found=%v), want %q", got, ok, shortCode)
	}
}

// assertNoCategoryToken pins that the raise site wrote no category word into
// the text it supplied.
func assertNoCategoryToken(t *testing.T, rendered string) {
	t.Helper()
	for _, token := range categoryTokens {
		if strings.Contains(rendered, token) {
			t.Errorf("rendered text carries the category token %q, which the head derives and prints; got: %s", token, rendered)
		}
	}
}

// TestAnnotationBindingTargetUnresolved_CatalogMiss_RendersItsOutput renders the
// binding-target condition met by a binding naming a Feature the catalog does
// not carry, and pins its identifiers, its derived category and its locator.
func TestAnnotationBindingTargetUnresolved_CatalogMiss_RendersItsOutput(t *testing.T) {
	t.Parallel()
	manifest := loadManifest(t, `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/missing:
          feature: core/missing:1.0.0
          maps:
            values:
              - pool
`)
	err := validateBindingTargets(manifest, mockCatalogLookup(map[string]types.Feature{}))
	assertIdentity(t, err, ErrAnnotationBindingTargetUnresolved(),
		"annotation-binding-target-unresolved", "E0253")

	// The locator names the registry's block rather than the key inside it, and
	// the key leads the detail instead. That is a property of the coordinate this
	// rule is asked at rather than of the rule: an annotation key carries a period
	// by convention, and the locator dialect stops at a segment containing one.
	const want = `annotation binding does not resolve against its target Feature ` +
		`(Manifest, at .spec.mappings.features.annotations, ` +
		`"feature.myco/missing", routes to Feature "core/missing:1.0.0", ` +
		`which this build's catalog does not hold; correct or publish the Feature named under "feature")`
	if got := err.Error(); got != want {
		t.Errorf("rendered output\n got: %s\nwant: %s", got, want)
	}
	assertNoCategoryToken(t, err.Error())

	// The category is derived from the entity kind the finding names, so it is
	// asserted through the conversion that derives it rather than read out of
	// the text.
	var re *ResolverError
	if !stderrors.As(err, &re) {
		t.Fatalf("expected a locator-bearing finding; got %T", err)
	}
	if got := re.ToCompilerError().Category(); got != cerrors.OpsError {
		t.Errorf("derived category = %v, want %v", got, cerrors.OpsError)
	}
}

// TestAnnotationBindingTargetUnresolved_PathNotDeclared_RendersItsOutput renders
// the same condition met the other way a binding can break it: a declared path
// the target Feature's schema carries no property for.
func TestAnnotationBindingTargetUnresolved_PathNotDeclared_RendersItsOutput(t *testing.T) {
	t.Parallel()
	manifest := loadManifest(t, `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/rewrite-host:
          feature: core/rewrite-host:1.0.0
          maps:
            infra:
              - nonexistent
`)
	catalog := mockCatalogLookup(map[string]types.Feature{
		"core/rewrite-host:1.0.0": featureWithSchema(t, "core/rewrite-host", `
infra:
  upstream:
    type: string
`),
	})
	err := validateBindingTargets(manifest, catalog)
	assertIdentity(t, err, ErrAnnotationBindingTargetUnresolved(),
		"annotation-binding-target-unresolved", "E0253")

	const want = `annotation binding does not resolve against its target Feature ` +
		`(Manifest, at .spec.mappings.features.annotations, ` +
		`"feature.myco/rewrite-host", routes to Feature "core/rewrite-host:1.0.0", ` +
		`at pillar "infra", and path "nonexistent", ` +
		`which that Feature does not declare a property for under "schema")`
	if got := err.Error(); got != want {
		t.Errorf("rendered output\n got: %s\nwant: %s", got, want)
	}
	assertNoCategoryToken(t, err.Error())
}

// TestAnnotationSubkeyNotConforming_RendersItsOutput renders the
// subkey-conformance condition met both ways — an undeclared subkey and a value
// the located sub-schema refuses — and pins that both carry one condition.
func TestAnnotationSubkeyNotConforming_RendersItsOutput(t *testing.T) {
	t.Parallel()
	feature := featureWithSchema(t, "core/monitoring", `
values:
  level:
    type: string
    enum: [info, warn, error]
`)
	cache := newFeatureSchemaCache(nil)

	undeclared := validateSubkeyValueAgainstFeatureSchema(cache, &feature, "values", "missing", "x")
	assertIdentity(t, undeclared, ErrAnnotationSubkeyNotConforming(),
		"annotation-subkey-not-conforming", "E0254")
	const wantUndeclared = `annotation subkey does not conform to the target Feature's schema at the routed pillar: ` +
		`annotation subkey "missing" is not declared at Feature "core/monitoring" spec.schema.values.missing`
	if got := undeclared.Error(); got != wantUndeclared {
		t.Errorf("undeclared-subkey output\n got: %s\nwant: %s", got, wantUndeclared)
	}
	assertNoCategoryToken(t, undeclared.Error())

	nonConforming := validateSubkeyValueAgainstFeatureSchema(cache, &feature, "values", "level", "bogus")
	assertIdentity(t, nonConforming, ErrAnnotationSubkeyNotConforming(),
		"annotation-subkey-not-conforming", "E0254")
	if got := nonConforming.Error(); !strings.HasPrefix(got,
		`annotation subkey does not conform to the target Feature's schema at the routed pillar: `+
			`annotation subkey "level" value bogus does not conform to Feature "core/monitoring" spec.schema.values.level: `) {
		t.Errorf("non-conforming-value output does not open with the condition and the occurrence; got: %s", got)
	}
	assertNoCategoryToken(t, nonConforming.Error())
}

// TestAnnotationSidePillarMismatch_RendersItsOutput renders the side-vs-pillar
// condition on both authoring sides, since the permitted pillar differs per
// side and the diagnostic names it.
func TestAnnotationSidePillarMismatch_RendersItsOutput(t *testing.T) {
	t.Parallel()
	aggregateSide := validateAnnotationSideCompatibility(
		sideAggregate, "feature.myco/rewrite-host", "upstream", "infra", "aggregate.yaml")
	assertIdentity(t, aggregateSide, ErrAnnotationSidePillarMismatch(),
		"annotation-side-pillar-mismatch", "E0255")
	const wantAggregate = `annotation routes through a pillar its authoring side may not use: ` +
		`annotation "feature.myco/rewrite-host" at aggregate.yaml routes through pillar "infra" at path "upstream", ` +
		`but aggregate-side annotations may only route through pillar "values"`
	if got := aggregateSide.Error(); got != wantAggregate {
		t.Errorf("Aggregate-side output\n got: %s\nwant: %s", got, wantAggregate)
	}
	assertNoCategoryToken(t, aggregateSide.Error())

	releaseSide := validateAnnotationSideCompatibility(
		sideRelease, "feature.myco/db-pool", "pool", "values", "release.yaml")
	assertIdentity(t, releaseSide, ErrAnnotationSidePillarMismatch(),
		"annotation-side-pillar-mismatch", "E0255")
	const wantRelease = `annotation routes through a pillar its authoring side may not use: ` +
		`annotation "feature.myco/db-pool" at release.yaml routes through pillar "values" at path "pool", ` +
		`but release-side annotations may only route through pillar "infra"`
	if got := releaseSide.Error(); got != wantRelease {
		t.Errorf("Release-side output\n got: %s\nwant: %s", got, wantRelease)
	}
	assertNoCategoryToken(t, releaseSide.Error())
}

// TestAnnotationBindingPillarOverlap_RendersItsOutput renders the advisory and
// pins that it is a collected finding carrying its own identity and locator
// rather than a line of text.
func TestAnnotationBindingPillarOverlap_RendersItsOutput(t *testing.T) {
	t.Parallel()
	manifest := manifestFromYAML(t, `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/db-pool:
          feature: core/db-pool:1.0.0
          maps:
            infra:
              - shared_subkey
            values:
              - shared_subkey
`)
	findings := LintAnnotationBindingMaps(manifest)
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 finding; got %d: %v", len(findings), findings)
	}
	assertIdentity(t, findings[0], ErrAnnotationBindingPillarOverlap(),
		"annotation-binding-pillar-overlap", "E0256")
	if got, want := findings[0].Field, ".spec.mappings.features.annotations.feature.myco/db-pool"; got != want {
		t.Errorf("locator = %q, want %q", got, want)
	}
	const want = `annotation binding declares one path in both pillar lists ` +
		`(Manifest, at .spec.mappings.features.annotations.feature.myco/db-pool, ` +
		`annotation binding "feature.myco/db-pool" on Feature "core/db-pool:1.0.0" declares path "shared_subkey" ` +
		`in both maps.infra and maps.values; the routing tie-break sends this subkey through the infra pillar, ` +
		`which makes it unreachable from the Aggregate side — declare two bindings under two annotation keys ` +
		`if both pillars are meant to route it)`
	if got := findings[0].Error(); got != want {
		t.Errorf("rendered output\n got: %s\nwant: %s", got, want)
	}
	assertNoCategoryToken(t, findings[0].Error())
}

// TestExpandAnnotations_SidePillarMismatch_SaysTheCategoryOnce pins the
// composition through the expansion wrapper, which is the surface an author
// actually meets: the wrapper supplies the scope the inner condition does not
// know, and the category word appears once rather than at both levels.
func TestExpandAnnotations_SidePillarMismatch_SaysTheCategoryOnce(t *testing.T) {
	t.Parallel()
	aggSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server:1.0.0
      annotations:
        feature.myco/rewrite-host: "upstream.example"
`
	rr := &ResolveResult{
		Manifest:             loadTestManifest(t),
		Aggregate:            loadTestAggregate(t, aggSrc),
		AggregatePath:        "aggregate.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	err := ExpandAnnotations(rr, CatalogLookup{})
	if err == nil {
		t.Fatal("expected the side-vs-pillar halt; got nil")
	}
	if !stderrors.Is(err, ErrAnnotationSidePillarMismatch()) {
		t.Errorf("the halt should stay matchable through the expansion wrapper; got %v", err)
	}
	if got, ok := cerrors.ShortCodeOf(err); !ok || got != "E0255" {
		t.Errorf("ShortCodeOf through the wrapper = %q (found=%v), want %q", got, ok, "E0255")
	}
	msg := err.Error()
	if n := strings.Count(msg, "OPS_ERROR"); n != 1 {
		t.Errorf("the category is said %d times, want exactly 1\nfull: %s", n, msg)
	}
	// The wrapper's own contribution is the scope the inner condition cannot
	// know, so it has to survive the raise rather than be replaced by it.
	for _, want := range []string{"annotation expansion", "api", "feature.myco/rewrite-host"} {
		if !strings.Contains(msg, want) {
			t.Errorf("composed halt missing %q\nfull: %s", want, msg)
		}
	}
}
