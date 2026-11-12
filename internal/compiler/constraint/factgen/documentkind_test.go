// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"strings"
	"testing"
)

// The document-kind derivation takes its fact and stage sets as arguments rather than
// reading the registry, and its own documentation says why: "so that the refusals
// below can be shown to fire: a refusal nothing exercises is indistinguishable
// from a refusal that never triggers". This file is where they are shown to fire.
//
// 🔴 The count in that documentation is short of what the function refuses, and
// the rows below are built from the function rather than from the sentence. Four
// shapes are enumerated there — a document kind declared away from the document stage,
// a document-stage fact naming no kind, a declaration disagreeing with a
// derivation, and a declaration repeating one. A fifth refusal sits ahead of all
// of them and is not in that enumeration: a registry declaring no stage of the
// document stage's name at all. And the shape guard the derivation defers to
// refuses two further shapes of its own. Seven are driven here.
//
// Each row carries a positive control in the same table where one is available,
// because a table of refusals alone passes for a function that refuses
// everything.

// documentStages is the minimal stage set a document-kind derivation needs: one stage
// of the name the axis applies at.
//
// It is built per call rather than shared, so a row cannot mutate the set a
// later row reads.
func documentStages() []stageDecl {
	return []stageDecl{{Name: documentStage, Holds: "one authored document", HostPowers: noHostPowers}}
}

// documentFact is a document-stage fact declaration with the coordinates every
// row below varies from.
func documentFact(name, sourceField, documentKind string) factDecl {
	return factDecl{
		Name:         name,
		Stage:        documentStage,
		SourceField:  sourceField,
		DocumentKind: documentKind,
		Subject:      []string{"document"},
		Returns:      "string",
		Doc:          "A fact declared for the document-kind derivation's own refusals.",
	}
}

// TestDeriveDocumentKindsRefusals drives every shape the document-kind derivation refuses,
// and asserts the refusal names the fact and says which shape it met.
//
// The assertion is on the message rather than only on the error being non-nil,
// because every row returns a non-nil error and a test asserting that alone
// would pass with all seven refusals collapsed into one. What each row pins is
// that the refusal it met is the refusal it aimed at.
func TestDeriveDocumentKindsRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// facts and stages are the two sets the derivation is handed.
		facts  []factDecl
		stages []stageDecl
		// wants are fragments the refusal must carry, each naming something
		// only this refusal says.
		wants []string
	}{
		{
			name:   "no stage of the document stage's name is declared",
			facts:  []factDecl{documentFact("probe", "types.ComponentSpec.Assertions", "")},
			stages: []stageDecl{{Name: "graph", Holds: "one graph", HostPowers: noHostPowers}},
			wants:  []string{"no stage named", documentStage, "has no stage to apply at"},
		},
		{
			name: "a document kind is declared at a stage other than the document stage",
			facts: []factDecl{{
				Name:         "probe",
				Stage:        "graph",
				SourceField:  "ir.Graph.Nodes",
				DocumentKind: "Component",
				Subject:      []string{"node"},
				Returns:      "string",
				Doc:          "A later-stage fact declaring a document kind it may not declare.",
			}},
			stages: append(documentStages(),
				stageDecl{Name: "graph", Extends: documentStage, Holds: "one graph", HostPowers: noHostPowers}),
			wants: []string{
				`fact "probe" declares document kind "Component" at stage "graph"`,
				"varies by authored document kind",
				"a single kind",
			},
		},
		{
			name:   "a declaration repeats what the source field already derives",
			facts:  []factDecl{documentFact("probe", "types.ComponentSpec.Assertions", "Component")},
			stages: documentStages(),
			wants:  []string{"already derives", "one home", "cannot drift apart"},
		},
		{
			name:   "a declaration disagrees with what the source field derives",
			facts:  []factDecl{documentFact("probe", "types.ComponentSpec.Assertions", "Feature")},
			stages: documentStages(),
			wants:  []string{`derives "Component"`, "one of the two", "is wrong"},
		},
		{
			name:   "a document-stage fact names no kind at all",
			facts:  []factDecl{documentFact("probe", "resolver.ResolveInputs.AggregateRawBytes", "")},
			stages: documentStages(),
			wants:  []string{"names no authored kind", "Declare DocumentKind", "an omission and a"},
		},
		{
			name:   "a declared kind is spelled as the source structure rather than the kind",
			facts:  []factDecl{documentFact("probe", "resolver.ResolveInputs.AggregateRawBytes", "types.ComponentSpec")},
			stages: documentStages(),
			wants:  []string{"is not a document kind", "no package qualifier", "no field path"},
		},
		{
			name:   "a declared kind is not capitalised",
			facts:  []factDecl{documentFact("probe", "resolver.ResolveInputs.AggregateRawBytes", "component")},
			stages: documentStages(),
			wants:  []string{"is not a document kind", "capitalised"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			groups, err := deriveDocumentKinds(tc.facts, tc.stages)
			if err == nil {
				t.Fatalf("the derivation accepted %d group(s); this shape must be refused", len(groups))
			}
			if groups != nil {
				t.Errorf("the refusal came back with %d group(s); a refused registry yields none", len(groups))
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not carry %q, so it is not the refusal this row aims at:\n\t%v",
						want, err)
				}
			}
		})
	}
}

// TestDeriveDocumentKindsAcceptsTheShapesItIsFor is the positive control for the table
// above: without it, every refusal there is satisfied by a derivation that
// refuses whatever it is handed.
//
// It also pins the two input paths a fact has to a kind — derived from the source
// field, and declared where that derivation yields nothing — and the order the
// groups come back in, which is the registry's own and not sorted: a generated
// artifact is byte-compared against its committed copy, so an order that is a
// property of the declarations reproduces and one that is a property of a map
// does not.
func TestDeriveDocumentKindsAcceptsTheShapesItIsFor(t *testing.T) {
	t.Parallel()
	facts := []factDecl{
		documentFact("derived-kind", "types.FeatureSpec.Schema", ""),
		documentFact("declared-kind", "resolver.ResolveInputs.AggregateRawBytes", "Aggregate"),
		documentFact("second-fact-of-the-first-kind", "types.FeatureSpec.Extends", ""),
		{
			Name:        "later-stage-fact",
			Stage:       "graph",
			SourceField: "ir.Graph.Nodes",
			Subject:     []string{"node"},
			Returns:     "string",
			Doc:         "A later-stage fact, which the document-kind axis does not apply to.",
		},
	}
	stages := append(documentStages(),
		stageDecl{Name: "graph", Extends: documentStage, Holds: "one graph", HostPowers: noHostPowers})

	groups, err := deriveDocumentKinds(facts, stages)
	if err != nil {
		t.Fatalf("the derivation refused a registry it is for: %v", err)
	}

	wantKinds := []string{"Feature", "Aggregate"}
	if len(groups) != len(wantKinds) {
		t.Fatalf("derived %d document-kind group(s); want %d, one per authored kind the document-stage "+
			"facts name", len(groups), len(wantKinds))
	}
	for i, want := range wantKinds {
		if groups[i].Kind != want {
			t.Errorf("group %d is %q; want %q — the groups come back in the order the registry "+
				"declares their first fact", i, groups[i].Kind, want)
		}
	}
	if got := len(groups[0].Facts); got != 2 {
		t.Errorf("the Feature group carries %d fact(s); want 2, so a second fact of one kind joins "+
			"the group rather than opening another", got)
	}
	if got := len(groups[1].Facts); got != 1 {
		t.Errorf("the Aggregate group carries %d fact(s); want 1", got)
	}
}

// TestDeriveDocumentKindsAdmitsASingleKindStageDeclaringNoDocumentKind pins the one thing
// the derivation cannot see, so that it is a recorded decision rather than a
// trap somebody meets later.
//
// A stage after the root may present one authored document rather than a
// compilation — the catalog stage does, holding one Manifest beside the catalog
// it resolves against. Such a stage is single-kind, so it needs no document kind and
// declares none, and this derivation admits it: the refusal keys on a NON-EMPTY
// document kind at a non-document stage, so a fact declaring none trips nothing
// wherever it sits.
//
// That admission is correct for a single-kind stage and silent for one that has
// stopped being single-kind. The first fact of a SECOND document kind at such a
// stage passes here exactly as this one does, while the stage's accessors then
// answer two kinds with no axis separating them. Nothing in this package can
// refuse that, which is why the obligation is written on the stage's own
// declaration in the registry — and why this test exists rather than an
// assertion nobody can write.
func TestDeriveDocumentKindsAdmitsASingleKindStageDeclaringNoDocumentKind(t *testing.T) {
	t.Parallel()
	facts := []factDecl{
		documentFact("derived-kind", "types.FeatureSpec.Schema", ""),
		{
			Name:        "single-kind-stage-fact",
			Stage:       "catalog",
			SourceField: "resolver.CatalogLookup.Feature(featureType)",
			Subject:     []string{"document", "annotation-binding"},
			Returns:     "bool",
			Doc: "A fact at a stage presenting one authored document of a fixed kind, " +
				"which declares no document kind because the stage itself names the kind.",
		},
	}
	stages := append(documentStages(), stageDecl{
		Name:       "catalog",
		Extends:    documentStage,
		Holds:      "one authored document together with the catalog it resolves against",
		HostPowers: noHostPowers,
	})

	groups, err := deriveDocumentKinds(facts, stages)
	if err != nil {
		t.Fatalf("the derivation refused a single-kind stage declaring no document kind: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("derived %d document-kind group(s); want 1 — the document-kind axis groups document-stage "+
			"facts alone, so a fact at the single-kind stage joins no group", len(groups))
	}
	for _, f := range groups[0].Facts {
		if f.Name == "single-kind-stage-fact" {
			t.Errorf("the fact at the single-kind stage was grouped under kind %q; the axis "+
				"applies at the document stage alone", groups[0].Kind)
		}
	}
}

// TestDeriveDocumentKindReadsTheKindOffTheSourceField pins the derivation itself,
// which is the half the table above reaches only through its caller.
//
// The negative rows are what make the positive ones mean something: a derivation
// answering every input would satisfy the positive rows alone, and the shapes it
// must decline are exactly the ones a fact declares its document kind for.
func TestDeriveDocumentKindReadsTheKindOffTheSourceField(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		sourceField string
		wantKind    string
		wantOK      bool
	}{
		{"types.ComponentSpec.Assertions", "Component", true},
		{"types.FeatureSpec.Schema", "Feature", true},
		{"types.ManifestSpec.Extends", "Manifest", true},
		{"types.AggregateSpec.Parameters", "Aggregate", true},
		// A resolver input carries no kind anywhere in its own text.
		{"resolver.ResolveInputs.AggregateRawBytes", "", false},
		// A structure nested inside an entity's spec names no kind either.
		{"types.AnnotationBinding.Feature", "", false},
		// The prefix without the suffix, and the suffix without the prefix.
		{"types.Component.Assertions", "", false},
		{"other.ComponentSpec.Assertions", "", false},
		// A source structure that is the prefix and suffix and nothing between.
		{"types.Spec.Field", "", false},
		// No dot at all, so there is no source structure to read.
		{"AggregateRawBytes", "", false},
	} {
		t.Run(tc.sourceField, func(t *testing.T) {
			t.Parallel()
			kind, ok := deriveDocumentKind(tc.sourceField)
			if ok != tc.wantOK {
				t.Fatalf("deriveDocumentKind(%q) reported ok=%v; want %v", tc.sourceField, ok, tc.wantOK)
			}
			if kind != tc.wantKind {
				t.Errorf("deriveDocumentKind(%q) = %q; want %q", tc.sourceField, kind, tc.wantKind)
			}
		})
	}
}
