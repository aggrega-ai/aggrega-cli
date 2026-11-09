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

// The annotation conditions each carry three identifiers, and this file
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
apiVersion: aggrega.ai/v1beta1
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
apiVersion: aggrega.ai/v1beta1
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
	manifest := subkeyConformanceManifest(t)
	catalog := subkeyConformanceCatalog(t, `
values:
  level:
    type: string
    enum: [info, warn, error]
`)

	undeclared := authoredAnnotationRefusal(t, manifest,
		aggregateAnnotating(t, "feature.myco/monitoring:\n  missing: x\n"), nil, catalog)
	assertIdentity(t, undeclared, ErrAnnotationSubkeyNotConforming(),
		"annotation-subkey-not-conforming", "E0254")
	const wantUndeclared = `annotation subkey does not conform to the target Feature's schema at the routed pillar ` +
		`(Aggregate test, at .spec.components.api.annotations, ` +
		`"missing", written under annotation "feature.myco/monitoring", ` +
		`routed to Feature "core/monitoring:1.0.0", at pillar "values", ` +
		`which that Feature declares no property for under "schema")`
	if got := undeclared.Error(); got != wantUndeclared {
		t.Errorf("undeclared-subkey output\n got: %s\nwant: %s", got, wantUndeclared)
	}
	assertNoCategoryToken(t, undeclared.Error())

	nonConforming := authoredAnnotationRefusal(t, manifest,
		aggregateAnnotating(t, "feature.myco/monitoring:\n  level: bogus\n"), nil, catalog)
	assertIdentity(t, nonConforming, ErrAnnotationSubkeyNotConforming(),
		"annotation-subkey-not-conforming", "E0254")
	if got := nonConforming.Error(); !strings.HasPrefix(got,
		`annotation subkey does not conform to the target Feature's schema at the routed pillar `+
			`(Aggregate test, at .spec.components.api.annotations, `+
			`"level", written under annotation "feature.myco/monitoring", `+
			`carrying the value "bogus", routed to Feature "core/monitoring:1.0.0", `+
			`at pillar "values", which that Feature's schema refuses because `) {
		t.Errorf("non-conforming-value output does not open with the condition and the occurrence; got: %s", got)
	}
	assertNoCategoryToken(t, nonConforming.Error())
}

// TestAnnotationSubkeyNotBound_RendersItsOutput renders the unbound-subkey
// condition on both authoring sides and pins its identifiers, its emitted text and
// the category each side's finding is filed under.
func TestAnnotationSubkeyNotBound_RendersItsOutput(t *testing.T) {
	t.Parallel()
	manifest := structuredManifest(t)

	releaseSide := authoredAnnotationRefusal(t, manifest, nil,
		releaseAnnotating(t, "feature.myco/db-pool:\n  nosuch: 1\n"), CatalogLookup{})
	assertIdentity(t, releaseSide, ErrAnnotationSubkeyNotBound(),
		"annotation-subkey-not-bound", "E0363")
	const wantRelease = `annotation subkey is not routed by the annotation's binding ` +
		`(Release test, at .spec.boundaries.prod.components.api.annotations, ` +
		`"nosuch", written under annotation "feature.myco/db-pool", ` +
		`whose binding routes only "pool_size, max_connections, db_name, read_replicas")`
	if got := releaseSide.Error(); got != wantRelease {
		t.Errorf("Release-side output\n got: %s\nwant: %s", got, wantRelease)
	}
	assertNoCategoryToken(t, releaseSide.Error())

	aggregateSide := authoredAnnotationRefusal(t, manifest,
		aggregateAnnotating(t, "feature.myco/db-pool:\n  dbname: \"orders\"\n"), nil, CatalogLookup{})
	assertIdentity(t, aggregateSide, ErrAnnotationSubkeyNotBound(),
		"annotation-subkey-not-bound", "E0363")
	const wantAggregate = `annotation subkey is not routed by the annotation's binding ` +
		`(Aggregate test, at .spec.components.api.annotations, ` +
		`"dbname", written under annotation "feature.myco/db-pool", ` +
		`whose binding routes only "pool_size, max_connections, db_name, read_replicas")`
	if got := aggregateSide.Error(); got != wantAggregate {
		t.Errorf("Aggregate-side output\n got: %s\nwant: %s", got, wantAggregate)
	}
	assertNoCategoryToken(t, aggregateSide.Error())

	for _, side := range []struct {
		err  error
		want cerrors.Category
	}{
		{releaseSide, cerrors.OpsError},
		{aggregateSide, cerrors.DeveloperError},
	} {
		var re *ResolverError
		if !stderrors.As(side.err, &re) {
			t.Fatalf("the refusal carries no finding: %v", side.err)
		}
		if got := re.ToCompilerError().Category(); got != side.want {
			t.Errorf("the finding is filed under %q, want %q", got, side.want)
		}
	}
}

// TestAnnotationSidePillarMismatch_RendersItsOutput renders the side-vs-pillar
// condition on both authoring sides, since the permitted pillar differs per
// side and the diagnostic names it.
func TestAnnotationSidePillarMismatch_RendersItsOutput(t *testing.T) {
	t.Parallel()
	// One binding per pillar, so each side can be driven across the partition:
	// the Aggregate writes the infra-routed key and the Release the
	// values-routed one, which are the two crossings the condition exists for.
	manifest := loadManifest(t, `
apiVersion: aggrega.ai/v1beta1
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
              - upstream
        feature.myco/db-pool:
          feature: core/db-pool:1.0.0
          maps:
            values:
              - pool
`)

	aggregateSide := authoredAnnotationRefusal(t, manifest,
		aggregateAnnotating(t, `feature.myco/rewrite-host: "api.example.com"`+"\n"),
		nil, CatalogLookup{})
	assertIdentity(t, aggregateSide, ErrAnnotationSidePillarMismatch(),
		"annotation-side-pillar-mismatch", "E0255")
	const wantAggregate = `annotation routes through a pillar its authoring side may not use ` +
		`(Aggregate test, at .spec.components.api.annotations, ` +
		`"feature.myco/rewrite-host", which routes through pillar "infra", at path "upstream", ` +
		`while the document it is written in may only route through "values")`
	if got := aggregateSide.Error(); got != wantAggregate {
		t.Errorf("Aggregate-side output\n got: %s\nwant: %s", got, wantAggregate)
	}
	assertNoCategoryToken(t, aggregateSide.Error())

	releaseSide := authoredAnnotationRefusal(t, manifest, nil,
		releaseAnnotating(t, `feature.myco/db-pool: "shared"`+"\n"),
		CatalogLookup{})
	assertIdentity(t, releaseSide, ErrAnnotationSidePillarMismatch(),
		"annotation-side-pillar-mismatch", "E0255")
	const wantRelease = `annotation routes through a pillar its authoring side may not use ` +
		`(Release test, at .spec.boundaries.prod.components.api.annotations, ` +
		`"feature.myco/db-pool", which routes through pillar "values", at path "pool", ` +
		`while the document it is written in may only route through "infra")`
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
apiVersion: aggrega.ai/v1beta1
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
	findings := lintBindingMaps(manifest)
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 finding; got %d: %v", len(findings), findings)
	}
	assertIdentity(t, findings[0], ErrAnnotationBindingPillarOverlap(),
		"annotation-binding-pillar-overlap", "E0256")
	if got, want := findings[0].Field, ".spec.mappings.features.annotations"; got != want {
		t.Errorf("locator = %q, want %q", got, want)
	}
	const want = `annotation binding declares one path in both pillar lists ` +
		`(Manifest, at .spec.mappings.features.annotations, ` +
		`"feature.myco/db-pool", on Feature "core/db-pool:1.0.0", at path "shared_subkey", ` +
		`which the routing tie-break sends through the pillar "infra", ` +
		`declare a second binding under another annotation key to route it through "values")`
	if got := findings[0].Error(); got != want {
		t.Errorf("rendered output\n got: %s\nwant: %s", got, want)
	}
	assertNoCategoryToken(t, findings[0].Error())
}

// TestAnnotationScalarNotRoutable_RendersItsOutput renders the bare-value routing
// condition through the entry point every build asks before expansion, on both
// authoring sides, and pins its identifiers, its emitted text and the category
// each side's finding is filed under.
//
// Each refused row carries one fault — a bare value under a binding that routes
// it to no single path — and the passing rows are the controls: the same rule
// asked over a value it does route, so a refused row fails on its own fault
// rather than on anything the fixture shares. The empty-value rows pin the one
// place the rule reads the value as well as the binding: an empty value writes
// nothing, so it routes where one pillar list holds a single path.
func TestAnnotationScalarNotRoutable_RendersItsOutput(t *testing.T) {
	t.Parallel()
	// One path through infra and two through values: a bare value carrying content
	// has three places to go, while an empty one routes through the single infra
	// path.
	const mixedManifest = `
apiVersion: aggrega.ai/v1beta1
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
              - pool_size
            values:
              - db_name
              - read_replicas
`
	// One path in each document's own pillar, so each side has a bare value its
	// binding routes.
	const singlePathManifest = `
apiVersion: aggrega.ai/v1beta1
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
              - upstream
        feature.myco/db-pool:
          feature: core/db-pool:1.0.0
          maps:
            values:
              - pool
`
	const sentence = `a scalar annotation value does not route to exactly one path of its binding`
	const allFour = `whose binding routes "pool_size, max_connections, db_name, read_replicas")`

	for _, row := range []struct {
		name     string
		refusal  func(t *testing.T) error
		want     string
		category cerrors.Category
	}{
		{
			name: "a Release-side value its binding routes to several paths",
			refusal: func(t *testing.T) error {
				return authoredAnnotationRefusal(t, structuredManifest(t), nil,
					releaseAnnotating(t, `feature.myco/db-pool: "shared"`+"\n"), CatalogLookup{})
			},
			want: sentence + ` (Release test, at .spec.boundaries.prod.components.api.annotations, ` +
				`"feature.myco/db-pool", carrying the bare value "shared", ` + allFour,
			category: cerrors.OpsError,
		},
		{
			name: "an Aggregate-side value its binding routes to several paths",
			refusal: func(t *testing.T) error {
				return authoredAnnotationRefusal(t, structuredManifest(t),
					aggregateAnnotating(t, `feature.myco/db-pool: "shared"`+"\n"), nil, CatalogLookup{})
			},
			want: sentence + ` (Aggregate test, at .spec.components.api.annotations, ` +
				`"feature.myco/db-pool", carrying the bare value "shared", ` + allFour,
			category: cerrors.DeveloperError,
		},
		{
			name: "an empty value where neither pillar list holds a single path",
			refusal: func(t *testing.T) error {
				return authoredAnnotationRefusal(t, structuredManifest(t), nil,
					releaseAnnotating(t, `feature.myco/db-pool: ""`+"\n"), CatalogLookup{})
			},
			want: sentence + ` (Release test, at .spec.boundaries.prod.components.api.annotations, ` +
				`"feature.myco/db-pool", carrying the bare value "", ` + allFour,
			category: cerrors.OpsError,
		},
		{
			name: "a value carrying content where one pillar list holds a single path among several",
			refusal: func(t *testing.T) error {
				return authoredAnnotationRefusal(t, loadManifest(t, mixedManifest), nil,
					releaseAnnotating(t, `feature.myco/db-pool: "8"`+"\n"), CatalogLookup{})
			},
			want: sentence + ` (Release test, at .spec.boundaries.prod.components.api.annotations, ` +
				`"feature.myco/db-pool", carrying the bare value "8", ` +
				`whose binding routes "pool_size, db_name, read_replicas")`,
			category: cerrors.OpsError,
		},
		{
			name: "control: a Release-side value its binding routes to one path",
			refusal: func(t *testing.T) error {
				return authoredAnnotationRefusal(t, loadManifest(t, singlePathManifest), nil,
					releaseAnnotating(t, `feature.myco/rewrite-host: "api.example.com"`+"\n"), CatalogLookup{})
			},
		},
		{
			name: "control: an Aggregate-side value its binding routes to one path",
			refusal: func(t *testing.T) error {
				return authoredAnnotationRefusal(t, loadManifest(t, singlePathManifest),
					aggregateAnnotating(t, `feature.myco/db-pool: "shared"`+"\n"), nil, CatalogLookup{})
			},
		},
		{
			name: "control: an empty value where one pillar list holds a single path",
			refusal: func(t *testing.T) error {
				return authoredAnnotationRefusal(t, loadManifest(t, mixedManifest), nil,
					releaseAnnotating(t, `feature.myco/db-pool: ""`+"\n"), CatalogLookup{})
			},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			err := row.refusal(t)
			if row.want == "" {
				if err != nil {
					t.Fatalf("a bare value its binding routes was refused: %v", err)
				}
				return
			}
			assertIdentity(t, err, ErrAnnotationScalarNotRoutable(), "annotation-scalar-not-routable", "E0367")
			if got := err.Error(); got != row.want {
				t.Errorf("rendered output\n got: %s\nwant: %s", got, row.want)
			}
			assertNoCategoryToken(t, err.Error())
			var re *ResolverError
			if !stderrors.As(err, &re) {
				t.Fatalf("the refusal carries no finding: %v", err)
			}
			if got := re.ToCompilerError().Category(); got != row.category {
				t.Errorf("the finding is filed under %q, want %q", got, row.category)
			}
		})
	}
}

// TestExpandAnnotations_UnroutableRefusalsNameNoCondition drives each refusal the
// expansion still raises itself — a bare value its binding cannot place, and a
// subkey of a structured value its binding does not route — on both authoring
// sides, by calling expansion directly as a caller that skipped the declared
// rules would.
//
// Each refusal stays loud, so a value is never dropped with nothing said, and
// keeps the text the expansion composes. None of them names the registered
// condition the declared rule over the authored entry raises for the same fault:
// that rule is the condition's one raise site, and a raise here would be a second.
func TestExpandAnnotations_UnroutableRefusalsNameNoCondition(t *testing.T) {
	t.Parallel()
	const severalPaths = `binding for feature "core/db-pool:1.0.0" maps multiple subkey paths ` +
		`([pool_size max_connections db_name read_replicas]); scalar annotation value "shared" cannot route ` +
		`to multiple subkeys unambiguously. Provide separate annotation keys for each path`
	const unboundTail = `is not declared in binding map; declared paths: ` +
		`[pool_size max_connections db_name read_replicas]`

	for _, row := range []struct {
		name string
		rr   func(t *testing.T) *ResolveResult
		tail string
	}{
		{
			name: "a Release-side scalar its binding routes to several paths",
			rr: func(t *testing.T) *ResolveResult {
				return &ResolveResult{Manifest: structuredManifest(t),
					Release: releaseAnnotating(t, `feature.myco/db-pool: "shared"`+"\n")}
			},
			tail: severalPaths,
		},
		{
			name: "an Aggregate-side scalar its binding routes to several paths",
			rr: func(t *testing.T) *ResolveResult {
				return &ResolveResult{Manifest: structuredManifest(t),
					Aggregate: aggregateAnnotating(t, `feature.myco/db-pool: "shared"`+"\n")}
			},
			tail: severalPaths,
		},
		{
			name: "a Release-side subkey its binding does not route",
			rr: func(t *testing.T) *ResolveResult {
				return &ResolveResult{Manifest: structuredManifest(t),
					Release: releaseAnnotating(t, "feature.myco/db-pool:\n  nosuch: 1\n")}
			},
			tail: unboundTail,
		},
		{
			name: "an Aggregate-side subkey its binding does not route",
			rr: func(t *testing.T) *ResolveResult {
				return &ResolveResult{Manifest: structuredManifest(t),
					Aggregate: aggregateAnnotating(t, "feature.myco/db-pool:\n  nosuch: 1\n")}
			},
			tail: unboundTail,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			err := ExpandAnnotations(row.rr(t), CatalogLookup{})
			if err == nil {
				t.Fatal("expansion placed or dropped a value its binding does not route; want a refusal")
			}
			chain := cerrors.CauseChain(err)
			if !strings.HasSuffix(chain, row.tail) {
				t.Errorf("the flattened refusal does not end with the expansion's own text\n got: %s\nwant suffix: %s",
					chain, row.tail)
			}
			for _, condition := range []error{ErrAnnotationScalarNotRoutable(), ErrAnnotationSubkeyNotBound()} {
				if stderrors.Is(err, condition) {
					t.Errorf("the expansion's refusal names %v, which the declared rule over the authored "+
						"entry raises; got %v", condition, err)
				}
			}
			if code, ok := cerrors.CodeOf(err); ok {
				t.Errorf("the expansion's refusal carries the code %q; want none", code)
			}
		})
	}

	// A binding declaring no path is refused by the Manifest schema before
	// expansion runs, so it is reached here directly; it keeps its own text and
	// names no condition either.
	_, _, err := resolveBindingTarget(types.AnnotationBinding{Feature: "core/db-pool:1.0.0"}, "shared")
	if err == nil {
		t.Fatal("a binding declaring no path placed a bare value; want a refusal")
	}
	const wantNoPaths = `binding for feature "core/db-pool:1.0.0" declares no paths in maps; ` +
		`scalar annotation value cannot route to any subkey`
	if got := err.Error(); got != wantNoPaths {
		t.Errorf("no-path refusal\n got: %s\nwant: %s", got, wantNoPaths)
	}
	if stderrors.Is(err, ErrAnnotationScalarNotRoutable()) {
		t.Errorf("the no-path refusal names the condition the declared rule raises; got %v", err)
	}
}

// TestAnnotationSidePillarMismatch_SaysTheCategoryOnce pins the composition an
// author actually meets: the finding names its authoring document once, and the
// category word appears once rather than at two levels.
//
// The annotation value is authored in the Aggregate, so the category named is
// the developer's — derived at the reporting boundary from the entity kind the
// finding names. The operator's domain is asserted absent for the same reason
// the developer's is counted rather than merely found: the value is named once,
// by the surface it was written on, and never alongside a second one.
func TestAnnotationSidePillarMismatch_SaysTheCategoryOnce(t *testing.T) {
	t.Parallel()
	aggSrc := `
apiVersion: aggrega.ai/v1beta1
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
	err := ValidateAuthoredAnnotations(nil, rr, CatalogLookup{})
	if err == nil {
		t.Fatal("expected the side-vs-pillar halt; got nil")
	}
	if !stderrors.Is(err, ErrAnnotationSidePillarMismatch()) {
		t.Errorf("the halt should stay matchable through the entry point; got %v", err)
	}
	if got, ok := cerrors.ShortCodeOf(err); !ok || got != "E0255" {
		t.Errorf("ShortCodeOf = %q (found=%v), want %q", got, ok, "E0255")
	}
	msg := err.Error()
	// The category is derived from the entity kind the finding names rather than
	// written into the text, so it is asserted through the conversion that derives
	// it and asserted ABSENT from the text — which is this file's standing
	// property for every condition it renders.
	assertNoCategoryToken(t, msg)
	var re *ResolverError
	if !stderrors.As(err, &re) {
		t.Fatalf("expected a locator-bearing finding; got %T", err)
	}
	if got := re.ToCompilerError().Category(); got != cerrors.DeveloperError {
		t.Errorf("derived category = %v, want %v", got, cerrors.DeveloperError)
	}
	// The subject the rule cannot know — which document, and where inside it —
	// is supplied by the framework and by the conversion, so both survive.
	for _, want := range []string{
		"Aggregate test", ".spec.components.api.annotations", "feature.myco/rewrite-host",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("composed halt missing %q\nfull: %s", want, msg)
		}
	}
}
