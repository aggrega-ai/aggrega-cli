// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The tests of the rules over what an author wrote under one delivery
// mechanism. Each is driven two ways: through its traversal, where the subject's
// position and detail are read directly, and through the suite a resolve asks,
// where what a build halts with is read as a finding.

// deliveryEntry builds one list entry of a delivery mechanism, as the decoder
// hands an authored entry over.
func deliveryEntry(in, value, from string) map[string]any {
	e := map[string]any{"in": in}
	if value != "" {
		e["value"] = value
	}
	if from != "" {
		e["from"] = from
	}
	return e
}

// mapShapedDelivery is an injection block whose env delivery is a mapping rather
// than a list.
func mapShapedDelivery() *types.InjectBlock {
	return &types.InjectBlock{Env: map[string]any{"DB_URL": "host"}}
}

// aggregateWithOwnBlock is an Aggregate named app declaring one top-level
// component, api, whose own injection block is block.
func aggregateWithOwnBlock(block *types.InjectBlock) *types.Aggregate {
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server:1.0.0", Inject: block})
	return &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "app"},
		Spec:     types.AggregateSpec{Components: components},
	}
}

// emptyAggregate is an Aggregate declaring nothing, beside which a Release is
// resolved.
func emptyAggregate() *types.Aggregate {
	return &types.Aggregate{Metadata: types.AggregateMetadata{Name: "app"}}
}

// deliveryObjections asks one rule over the delivery axis of the two documents,
// through its traversal, with the Release read from release.yaml.
func deliveryObjections(
	t *testing.T,
	agg *types.Aggregate,
	rel *types.Release,
	check constraint.Check[constraint.NormalizedView, constraint.InjectDeliverySubject],
) []constraint.Objection {
	t.Helper()
	view := newNormalizedView(normalized(t, agg), rel, testCatalog(), nil)
	objections, err := constraint.RunOnInjectDeliveries[constraint.NormalizedView](
		nil, newCoordinateSet(view).withReleaseSource("release.yaml"), view, check)
	if err != nil {
		t.Fatalf("the delivery walk returned an error over a walk nothing cancelled: %v", err)
	}
	return objections
}

// deliveryFindings asks the whole suite a resolve asks, and returns the findings
// carrying sentinel.
func deliveryFindings(t *testing.T, agg *types.Aggregate, rel *types.Release, sentinel error) []*ResolverError {
	t.Helper()
	errs, err := RunGuards(nil, normalized(t, agg), rel, "release.yaml", testCatalog(), nil, nil)
	if err != nil {
		t.Fatalf("the suite returned an error: %v", err)
	}
	var out []*ResolverError
	for _, e := range errs {
		if errors.Is(e, sentinel) {
			out = append(out, e)
		}
	}
	return out
}

// TestInjectDeliveryShape_MapShapedEnvIsRefusedWithItsMessageAtItsMechanism pins
// the refusal as a build halts with it: a component's own block carrying a
// map-shaped env yields one finding, filed under the Aggregate, positioned at the
// mechanism, whose whole message is the registered sentence followed by the
// subject and the mechanism's name.
func TestInjectDeliveryShape_MapShapedEnvIsRefusedWithItsMessageAtItsMechanism(t *testing.T) {
	t.Parallel()

	findings := deliveryFindings(t, aggregateWithOwnBlock(mapShapedDelivery()), nil, ErrInjectDeliveryShape())
	if len(findings) != 1 {
		t.Fatalf("a map-shaped env must yield exactly one shape finding; got %d: %v", len(findings), findings)
	}
	f := findings[0]
	if f.Entity != "Aggregate app" || f.Field != ".spec.components.api.inject.env" {
		t.Errorf("finding filed under %q at %q; want %q at %q",
			f.Entity, f.Field, "Aggregate app", ".spec.components.api.inject.env")
	}
	const want = "inject delivery: shape must be a list of entries, each `in:` with exactly one of `from:` or `value:` " +
		`(Aggregate app, at .spec.components.api.inject.env, "env")`
	if got := f.Error(); got != want {
		t.Errorf("message\n got: %s\nwant: %s", got, want)
	}
	if ambiguous := deliveryFindings(t, aggregateWithOwnBlock(mapShapedDelivery()), nil, ErrInjectEntryAmbiguous()); len(ambiguous) != 0 {
		t.Errorf("a shape refusal must not also report an ambiguous entry: %v", ambiguous)
	}
}

// TestInjectDeliveryShape_EveryNonListShapeIsRefusedOnEveryMechanism pins that the
// rule reads the shape alone — a mapping in any of its decoded forms, a scalar —
// and reaches every mechanism, the three the block names and one an author names
// beside them, each at its own position in the order the block holds them.
func TestInjectDeliveryShape_EveryNonListShapeIsRefusedOnEveryMechanism(t *testing.T) {
	t.Parallel()

	ordered := types.NewOrderedMap[any]()
	ordered.Set("DB_URL", "connection.url")

	for _, tc := range []struct {
		name  string
		value any
	}{
		{"plain map", map[string]any{"DB_URL": "connection.url"}},
		{"string map", map[string]string{"DB_URL": "connection.url"}},
		{"ordered map", ordered},
		{"scalar", "connection.url"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			block := &types.InjectBlock{Env: tc.value, File: tc.value, Arg: tc.value}
			block.Additional.Set("ssm_param", tc.value)

			objections := deliveryObjections(t, aggregateWithOwnBlock(block), nil, injectDeliveryIsAList)
			want := []string{
				".spec.components.api.inject.env",
				".spec.components.api.inject.file",
				".spec.components.api.inject.arg",
				".spec.components.api.inject.ssm_param",
			}
			if got := locatorsOf(objections); !equalStrings(got, want) {
				t.Errorf("raised at %v; want %v", got, want)
			}
		})
	}
}

// TestInjectDeliveryShape_ListFormPasses pins the other side of the refusal: a
// list on every mechanism — entries as plain or ordered maps, an empty list — and
// a mechanism carrying no value at all pass both rules.
func TestInjectDeliveryShape_ListFormPasses(t *testing.T) {
	t.Parallel()

	ordered := types.NewOrderedMap[any]()
	ordered.Set("in", "CERT")
	ordered.Set("value", "/etc/cert.pem")

	block := &types.InjectBlock{
		Env:  []any{deliveryEntry("DB_URL", "", "connection.url"), deliveryEntry("PORT", "8080", "")},
		File: []any{ordered},
		Arg:  []any{},
	}
	block.Additional.Set("ssm_param", []any{deliveryEntry("API_KEY", "", "secret.path")})
	block.Additional.Set("unset", nil)

	for name, check := range map[string]constraint.Check[constraint.NormalizedView, constraint.InjectDeliverySubject]{
		"shape":     injectDeliveryIsAList,
		"ambiguity": injectDeliveryEntryIsUnambiguous,
	} {
		if got := deliveryObjections(t, aggregateWithOwnBlock(block), nil, check); len(got) != 0 {
			t.Errorf("the %s rule must pass the list form; raised at %v", name, locatorsOf(got))
		}
	}
}

// TestInjectEntryAmbiguous_OneFindingPerMechanismNamingEachEntry pins the
// ambiguity rule: one finding per mechanism holding an entry with both `value:`
// and `from:`, positioned at the mechanism, its detail the mechanism and then the
// `in:` of each offending entry in list order. An entry carrying one of the two,
// and an entry the decoder cannot read as one, are not offending.
func TestInjectEntryAmbiguous_OneFindingPerMechanismNamingEachEntry(t *testing.T) {
	t.Parallel()

	block := &types.InjectBlock{
		Env: []any{
			deliveryEntry("FIRST", "literal", "ref.first"),
			deliveryEntry("FINE", "", "ref.fine"),
			deliveryEntry("SECOND", "literal", "ref.second"),
		},
		Arg: []any{
			map[string]any{"value": "missing-in", "from": "ref"},
			"not-a-map",
			deliveryEntry("PORT", "8080", ""),
		},
	}
	block.Additional.Set("ssm_param", []any{deliveryEntry("API_KEY", "literal", "secret.path")})

	objections := deliveryObjections(t, aggregateWithOwnBlock(block), nil, injectDeliveryEntryIsUnambiguous)
	wantAt := []string{".spec.components.api.inject.env", ".spec.components.api.inject.ssm_param"}
	if got := locatorsOf(objections); !equalStrings(got, wantAt) {
		t.Fatalf("raised at %v; want %v", got, wantAt)
	}
	wantDetail := []string{"env,FIRST,SECOND", "ssm_param,API_KEY"}
	if got := detailsOf(objections); !equalStrings(got, wantDetail) {
		t.Errorf("detail %v; want %v", got, wantDetail)
	}
	for _, o := range objections {
		if !errors.Is(o.Sentinel(), ErrInjectEntryAmbiguous()) {
			t.Errorf("objection at %s carries %v; want the ambiguity condition", o.Locator(), o.Sentinel())
		}
	}
}

// TestInjectDeliveryRules_ShapeAndAmbiguityAreBothReported pins that a block
// faulting both ways halts with both conditions, the shape refusal first.
func TestInjectDeliveryRules_ShapeAndAmbiguityAreBothReported(t *testing.T) {
	t.Parallel()

	block := &types.InjectBlock{
		Env: map[string]any{"DB_URL": "connection.url"},
		Arg: []any{deliveryEntry("FLAG", "v", "ref")},
	}
	errs, err := RunGuards(nil, normalized(t, aggregateWithOwnBlock(block)), nil, "", testCatalog(), nil, nil)
	if err != nil {
		t.Fatalf("the suite returned an error: %v", err)
	}
	shapeAt, ambiguousAt := -1, -1
	for i, e := range errs {
		switch {
		case errors.Is(e, ErrInjectDeliveryShape()) && e.Field == ".spec.components.api.inject.env":
			shapeAt = i
		case errors.Is(e, ErrInjectEntryAmbiguous()) && e.Field == ".spec.components.api.inject.arg":
			ambiguousAt = i
		}
	}
	if shapeAt < 0 || ambiguousAt < 0 || shapeAt > ambiguousAt {
		t.Errorf("want the shape finding at .inject.env before the ambiguity finding at .inject.arg; got %v", errs)
	}
}

// TestInjectDeliveryRules_ReachEveryAggregateBlock pins the Aggregate's side of
// the axis: a block on a component, on one of its connections, on an attachment
// it carries and on a connection that attachment draws, on a scope-wide
// attachment, and on a component inside a nested scope, each refused at the key
// path its author wrote.
func TestInjectDeliveryRules_ReachEveryAggregateBlock(t *testing.T) {
	t.Parallel()

	connection := func(block *types.InjectBlock) types.OrderedMap[types.UsesSocketValue] {
		verbose := types.OrderedMap[types.VerboseUsesEntry]{}
		verbose.Set("primary", types.VerboseUsesEntry{Target: "db", Inject: block})
		uses := types.OrderedMap[types.UsesSocketValue]{}
		uses.Set("databases", types.UsesSocketValue{Verbose: verbose})
		return uses
	}
	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("creds", types.FeatureInstance{Inject: mapShapedDelivery()})
	features.Set("metrics", types.FeatureInstance{Uses: deliveryAttachmentUses(mapShapedDelivery())})

	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{
		Inject:   mapShapedDelivery(),
		Uses:     connection(mapShapedDelivery()),
		Features: features,
	})

	inner := types.OrderedMap[types.ComponentInstance]{}
	inner.Set("worker", types.ComponentInstance{Inject: mapShapedDelivery()})
	nested := types.OrderedMap[types.Subsystem]{}
	nested.Set("inner", types.Subsystem{Components: inner})
	scopeFeatures := types.OrderedMap[types.FeatureInstance]{}
	scopeFeatures.Set("audit", types.FeatureInstance{Inject: mapShapedDelivery()})

	subsystems := types.OrderedMap[types.Subsystem]{}
	subsystems.Set("web", types.Subsystem{Components: components})
	subsystems.Set("outer", types.Subsystem{Features: scopeFeatures, Subsystems: nested})

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "app"},
		Spec:     types.AggregateSpec{Subsystems: subsystems},
	}

	want := []string{
		".spec.subsystems.web.components.api.inject.env",
		".spec.subsystems.web.components.api.uses.databases.primary.inject.env",
		".spec.subsystems.web.components.api.features.creds.inject.env",
		".spec.subsystems.web.components.api.features.metrics.uses.backend.primary.inject.env",
		".spec.subsystems.outer.features.audit.inject.env",
		".spec.subsystems.outer.subsystems.inner.components.worker.inject.env",
	}
	if got := locatorsOf(deliveryObjections(t, agg, nil, injectDeliveryIsAList)); !equalStrings(got, want) {
		t.Errorf("raised at\n got: %v\nwant: %v", got, want)
	}
}

// deliveryAttachmentUses is an attachment's dependency block drawing one
// connection, backend/primary, that carries block.
func deliveryAttachmentUses(block *types.InjectBlock) types.OrderedMap[types.FeatureUsesSocketValue] {
	verbose := types.OrderedMap[types.FeatureUsesEntry]{}
	verbose.Set("primary", types.FeatureUsesEntry{Target: "db", Inject: block})
	uses := types.OrderedMap[types.FeatureUsesSocketValue]{}
	uses.Set("backend", types.FeatureUsesSocketValue{Verbose: verbose})
	return uses
}

// releaseWithDeliveryOnAttachment places one Release attachment, metrics, carrying
// block on its backend/primary connection, at the position named by where, and
// returns the Release with the locator the attachment's connection is named at.
func releaseWithDeliveryOnAttachment(where string, block *types.InjectBlock) (*types.Release, string) {
	attachment := types.OrderedMap[types.BoundaryFeature]{}
	attachment.Set("metrics", types.BoundaryFeature{Type: "core/metrics:1.0.0", Uses: deliveryAttachmentUses(block)})
	component := types.OrderedMap[types.BoundaryComponent]{}
	component.Set("api", types.BoundaryComponent{Features: attachment})

	rel := &types.Release{Metadata: types.ReleaseMetadata{Name: "rel"}}
	boundaries := types.OrderedMap[types.Boundary]{}
	const connection = ".features.metrics.uses.backend.primary"
	switch where {
	case "boundary-wide":
		boundaries.Set("prod", types.Boundary{Features: attachment})
		rel.Spec.Boundaries = boundaries
		return rel, ".spec.boundaries.prod" + connection
	case "on a component":
		boundaries.Set("prod", types.Boundary{Components: component})
		rel.Spec.Boundaries = boundaries
		return rel, ".spec.boundaries.prod.components.api" + connection
	case "in a nested boundary":
		inner := types.OrderedMap[types.Boundary]{}
		inner.Set("eks", types.Boundary{Features: attachment})
		boundaries.Set("aws", types.Boundary{Boundaries: inner})
		rel.Spec.Boundaries = boundaries
		return rel, ".spec.boundaries.aws.boundaries.eks" + connection
	case "on a routed component":
		routed := types.OrderedMap[types.AggregateRouting]{}
		routed.Set("data", types.AggregateRouting{Components: component})
		boundaries.Set("prod", types.Boundary{Aggregates: routed})
		rel.Spec.Boundaries = boundaries
		return rel, ".spec.boundaries.prod.aggregates.data.components.api" + connection
	case "on a top-level component":
		rel.Spec.Components = component
		return rel, ".spec.components.api" + connection
	}
	panic("releaseWithDeliveryOnAttachment: unknown position " + where)
}

// deliveryReleasePositions are every position a Release attachment drawing a
// connection can be written at.
var deliveryReleasePositions = []string{
	"boundary-wide", "on a component", "in a nested boundary", "on a routed component", "on a top-level component",
}

// TestInjectDeliveryRules_ReachEveryReleaseBlock pins the Release's side of the
// axis: a block on a connection a Release attachment draws, wherever the Release
// writes the attachment — routed in from a sub-module included — is refused by
// both rules at the key path the Release author wrote, filed under the Release,
// in the Release's file; and the list form there passes.
func TestInjectDeliveryRules_ReachEveryReleaseBlock(t *testing.T) {
	t.Parallel()
	for _, where := range deliveryReleasePositions {
		t.Run(where, func(t *testing.T) {
			t.Parallel()

			rel, connection := releaseWithDeliveryOnAttachment(where, mapShapedDelivery())
			shape := deliveryFindings(t, emptyAggregate(), rel, ErrInjectDeliveryShape())
			if len(shape) != 1 {
				t.Fatalf("want one shape finding; got %v", shape)
			}
			if f := shape[0]; f.Entity != "Release rel" || f.Field != connection+".inject.env" || f.File != "release.yaml" {
				t.Errorf("shape finding filed under %q at %q in %q; want %q at %q in %q",
					f.Entity, f.Field, f.File, "Release rel", connection+".inject.env", "release.yaml")
			}

			rel, _ = releaseWithDeliveryOnAttachment(where, &types.InjectBlock{
				Env: []any{deliveryEntry("DB_URL", "literal", "host")},
			})
			ambiguous := deliveryFindings(t, emptyAggregate(), rel, ErrInjectEntryAmbiguous())
			if len(ambiguous) != 1 || ambiguous[0].Field != connection+".inject.env" {
				t.Errorf("want one ambiguity finding at %s; got %v", connection+".inject.env", ambiguous)
			}

			rel, _ = releaseWithDeliveryOnAttachment(where, &types.InjectBlock{
				Env: []any{deliveryEntry("DB_URL", "", "host")},
			})
			for _, check := range []constraint.Check[constraint.NormalizedView, constraint.InjectDeliverySubject]{
				injectDeliveryIsAList, injectDeliveryEntryIsUnambiguous,
			} {
				if got := deliveryObjections(t, emptyAggregate(), rel, check); len(got) != 0 {
					t.Errorf("the list form must pass; raised at %v", locatorsOf(got))
				}
			}
		})
	}
}

// literalBesideReference is a delivery list carrying a literal, a reference and an
// entry carrying both keys, in that order. Only the first is the attachment
// literal rule's: the reference is legal on every block, and an entry carrying
// both keys is the ambiguity rule's to report.
func literalBesideReference() *types.InjectBlock {
	return &types.InjectBlock{Env: []any{
		deliveryEntry("LITERAL_NAME", "a-literal", ""),
		deliveryEntry("TOKEN_NAME", "", "name"),
		deliveryEntry("BOTH", "x", "y"),
	}}
}

// TestAttachmentInjectValue_RefusedOnAnAttachmentsOwnBlockAndNowhereElse pins
// which blocks the attachment literal rule reads: every block the Aggregate
// writes carries the same list, and only the two written directly on an
// attachment — one on a component, one scope-wide — are refused, each at its own
// mechanism and naming the literal entry alone. A component's own block admits a
// literal, and a block on a connection, a component's or an attachment's, is the
// connection rule's.
func TestAttachmentInjectValue_RefusedOnAnAttachmentsOwnBlockAndNowhereElse(t *testing.T) {
	t.Parallel()

	verbose := types.OrderedMap[types.VerboseUsesEntry]{}
	verbose.Set("primary", types.VerboseUsesEntry{Target: "db", Inject: literalBesideReference()})
	ownUses := types.OrderedMap[types.UsesSocketValue]{}
	ownUses.Set("databases", types.UsesSocketValue{Verbose: verbose})

	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("creds", types.FeatureInstance{Inject: literalBesideReference()})
	features.Set("metrics", types.FeatureInstance{Uses: deliveryAttachmentUses(literalBesideReference())})

	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{
		Inject:   literalBesideReference(),
		Uses:     ownUses,
		Features: features,
	})
	scopeFeatures := types.OrderedMap[types.FeatureInstance]{}
	scopeFeatures.Set("audit", types.FeatureInstance{Inject: literalBesideReference()})

	subsystems := types.OrderedMap[types.Subsystem]{}
	subsystems.Set("web", types.Subsystem{Components: components})
	subsystems.Set("outer", types.Subsystem{Features: scopeFeatures})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "app"},
		Spec:     types.AggregateSpec{Subsystems: subsystems},
	}

	objections := deliveryObjections(t, agg, nil, attachmentInjectEntryNamesAKey)
	wantAt := []string{
		".spec.subsystems.web.components.api.features.creds.inject.env",
		".spec.subsystems.outer.features.audit.inject.env",
	}
	if got := locatorsOf(objections); !equalStrings(got, wantAt) {
		t.Fatalf("raised at\n got: %v\nwant: %v", got, wantAt)
	}
	if got, want := detailsOf(objections), []string{"env,LITERAL_NAME", "env,LITERAL_NAME"}; !equalStrings(got, want) {
		t.Errorf("detail %v; want %v", got, want)
	}
	for _, o := range objections {
		if !errors.Is(o.Sentinel(), ErrAttachmentInjectValue()) {
			t.Errorf("objection at %s carries %v; want the attachment literal condition", o.Locator(), o.Sentinel())
		}
	}
}

// TestAttachmentInjectValue_AHaltNamesTheEntryAndTheRemedy pins the refusal as a
// build halts with it: one finding per literal entry, filed under the Aggregate,
// at the attachment's mechanism, saying where a literal belongs; and a block
// carrying references alone passes.
func TestAttachmentInjectValue_AHaltNamesTheEntryAndTheRemedy(t *testing.T) {
	t.Parallel()

	attached := func(block *types.InjectBlock) *types.Aggregate {
		features := types.OrderedMap[types.FeatureInstance]{}
		features.Set("token", types.FeatureInstance{Type: "core/logging:1.0.0", Inject: block})
		components := types.OrderedMap[types.ComponentInstance]{}
		components.Set("api", types.ComponentInstance{Type: "core/server:1.0.0", Features: features})
		return &types.Aggregate{
			Metadata: types.AggregateMetadata{Name: "app"},
			Spec:     types.AggregateSpec{Components: components},
		}
	}

	findings := deliveryFindings(t, attached(&types.InjectBlock{Env: []any{
		deliveryEntry("TOKEN_NAME", "", "name"),
		deliveryEntry("LITERAL_NAME", "a-literal", ""),
	}}), nil, ErrAttachmentInjectValue())
	if len(findings) != 1 {
		t.Fatalf("want one finding for the one literal entry; got %d: %v", len(findings), findings)
	}
	f := findings[0]
	if f.Entity != "Aggregate app" || f.Field != ".spec.components.api.features.token.inject.env" {
		t.Errorf("finding filed under %q at %q; want %q at %q",
			f.Entity, f.Field, "Aggregate app", ".spec.components.api.features.token.inject.env")
	}
	for _, want := range []string{`"LITERAL_NAME"`, `"env"`, "the component's own inject block"} {
		if !strings.Contains(f.Error(), want) {
			t.Errorf("the finding does not say %q; got: %s", want, f.Error())
		}
	}
	if strings.Contains(f.Error(), "TOKEN_NAME") {
		t.Errorf("the reference beside the literal was named too; got: %s", f.Error())
	}

	if got := deliveryFindings(t, attached(&types.InjectBlock{Env: []any{
		deliveryEntry("TOKEN_NAME", "", "name"),
	}}), nil, ErrAttachmentInjectValue()); len(got) != 0 {
		t.Errorf("a block of references alone must pass; got %v", got)
	}
}

// TestAttachmentInjectValue_TheReleaseWritesNoAttachmentBlock pins the Release's
// side: a literal on a connection a Release attachment draws is a connection's
// block, so this rule reads it at no position the Release writes an attachment.
func TestAttachmentInjectValue_TheReleaseWritesNoAttachmentBlock(t *testing.T) {
	t.Parallel()
	for _, where := range deliveryReleasePositions {
		t.Run(where, func(t *testing.T) {
			t.Parallel()
			rel, _ := releaseWithDeliveryOnAttachment(where, literalBesideReference())
			if got := deliveryObjections(t, emptyAggregate(), rel, attachmentInjectEntryNamesAKey); len(got) != 0 {
				t.Errorf("a connection's block was read as an attachment's; raised at %v", locatorsOf(got))
			}
		})
	}
}

// TestInjectDeliveries_AggregateBeforeRelease pins the axis's order across the two
// documents: every Aggregate delivery precedes every Release delivery, and each
// objection carries the document its block was written in.
func TestInjectDeliveries_AggregateBeforeRelease(t *testing.T) {
	t.Parallel()

	rel, connection := releaseWithDeliveryOnAttachment("boundary-wide", mapShapedDelivery())
	objections := deliveryObjections(t, aggregateWithOwnBlock(mapShapedDelivery()), rel, injectDeliveryIsAList)
	want := []string{".spec.components.api.inject.env", connection + ".inject.env"}
	if got := locatorsOf(objections); !equalStrings(got, want) {
		t.Fatalf("raised at %v; want %v", got, want)
	}
	documents := []string{objections[0].AuthoringDocument(), objections[1].AuthoringDocument()}
	if !slices.Equal(documents, []string{constraint.AuthoringDocumentAggregate, constraint.AuthoringDocumentRelease}) {
		t.Errorf("the objections carry the documents %q; want the Aggregate's then the Release's", documents)
	}
}
