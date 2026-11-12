// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

// The daemon's three answers to "which kinds may I author": the set it puts on
// the wire, the set it offers as a `kind:` completion, and the set whose
// documents it validates.
//
// Two oracles answer here, and they answer different halves of the question.
//
// WHICH DOCUMENTS CARRY AN ENTITY HEADER is answered by the SCHEMA DOCUMENTS
// this binary ships, parsed from their own bytes. That is deliberate and it is
// most of the value of this file: the three surfaces each read the entity-kind
// vocabulary declared in Go, so an expectation derived from that same vocabulary
// would move with them and could never fail. Reading the schemas instead means a
// kind wrongly added to or dropped from any surface moves one side of the
// comparison only.
//
// WHICH OF THOSE AN AUTHOR WRITES A FILE OF is answered by a roll-call held
// here, because the schemas cannot answer it. A kind whose content is written
// inside another kind's document fixes its own apiVersion and kind exactly as a
// standalone kind does, so the content can be validated where it sits — the two
// cases are byte-identical in the respect a schema could be asked about. So the
// second half is a decision somebody records, and the roll-call is what forces
// it to be recorded before a new kind reaches an author.

import (
	"encoding/json"
	"sort"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/internal/serve/completion"
	"github.com/aggrega-ai/aggrega-cli/internal/serve/standalonekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
)

// emittedDocumentKinds names the document kinds that carry an entity header and
// that the entity-kind vocabulary does not declare at all, with the reason each
// is excluded.
//
// The intermediate representation is the compiler's own output: it is read back,
// never typed, so offering it to an author would offer a document they have no
// occasion to write. The exclusion is held from both ends below — a name here
// that no longer ships a schema is reported, so it cannot outlive its subject.
var emittedDocumentKinds = map[string]string{
	"IR": "the compiler's emitted intermediate representation, read back rather than authored",
}

// documentDisposition is what somebody decided about a declared kind: whether an
// author writes a document of it, or whether its content is written inside
// another kind's document.
type documentDisposition int

const (
	standaloneDocument documentDisposition = iota
	embeddedInAnotherDocument
)

// declaredKindDisposition is a roll-call over the whole declared vocabulary, and
// being a roll-call rather than a filter is its entire job.
//
// The three surfaces below take their membership from the vocabulary, so an
// expectation taken from the vocabulary the same way would move with them and
// could never fail. This is written by hand instead, and every declared kind has
// to appear in it: a kind added to the vocabulary is absent, and its absence
// fails. That is what makes "does an author write a document of this kind" a
// question somebody answers rather than one the offer answers on their behalf —
// an exclusion list alone would leave the new kind offered as authorable by
// default, decided by nobody.
//
// Nothing separates the two dispositions mechanically, which is why the decision
// is recorded rather than computed. A kind whose content is embedded fixes its
// own apiVersion and kind exactly as a standalone kind does, so that the content
// can be validated where it sits, so the schema bytes answer a different
// question than the one asked here.
//
// 🔴 It is a DELIBERATE second statement of the classification
// internal/serve/standalonekind holds, and the duplication is the mechanism: one
// side cannot disagree with itself, and the disagreement is the whole signal.
// Unifying them removes the only thing either side is here to catch.
var declaredKindDisposition = map[entitykind.Kind]documentDisposition{
	entitykind.Aggregate:       standaloneDocument,
	entitykind.Component:       standaloneDocument,
	entitykind.Feature:         standaloneDocument,
	entitykind.Recipe:          standaloneDocument,
	entitykind.FeatureRecipe:   standaloneDocument,
	entitykind.SchemaLibrary:   standaloneDocument,
	entitykind.MappingLibrary:  standaloneDocument,
	entitykind.WorkloadLibrary: standaloneDocument,
	entitykind.ArtifactLibrary: standaloneDocument,
	entitykind.Plugin:          standaloneDocument,
	entitykind.Manifest:        standaloneDocument,
	entitykind.Release:         standaloneDocument,

	// Workload content is written inside a Recipe or a FeatureRecipe, in its
	// `workload:` field — inline or from a file that field imports. There is no
	// workload document to address on its own; the header the content carries
	// names it for validation where it sits rather than making it addressable.
	entitykind.Workload: embeddedInAnotherDocument,
}

// unauthoredDocumentKinds is every document kind carrying an entity header that
// an author does not write a file of, with the reason for each — the two
// exclusions above, joined so the derivation below reads one set.
func unauthoredDocumentKinds() map[string]string {
	out := make(map[string]string, len(emittedDocumentKinds)+len(declaredKindDisposition))
	for documentKind, reason := range emittedDocumentKinds {
		out[documentKind] = reason
	}
	for _, k := range entitykind.Kinds() {
		if declaredKindDisposition[k] == embeddedInAnotherDocument {
			out[string(k)] = "content written inside another kind's document rather than as a document of its own"
		}
	}
	return out
}

// authorableKindsFromShippedSchemas derives the kinds an author writes a
// document of from the schema documents this binary ships, keyed by the value a
// document carries in its `kind:` field and valued by the schema token that
// names it.
//
// The derivation is a criterion plus a named exclusion, not a list: a schema
// that fixes both its apiVersion and its kind describes a document carrying an
// entity header, and every such document is one somebody writes bar the kinds
// named above. A list written out here would be one more copy of the vocabulary,
// passing while the surfaces and the binary disagreed.
//
// The criterion cannot carry the exclusion by itself, and that is the reason the
// exclusion exists rather than a shortcoming of it: an embedded kind's schema
// fixes both consts for the same reason a standalone kind's does, so the bytes
// are identical in the respect being tested.
func authorableKindsFromShippedSchemas(t *testing.T) map[string]string {
	t.Helper()

	unauthored := unauthoredDocumentKinds()

	tokens := schemas.Kinds()
	if len(tokens) == 0 {
		t.Fatal("the binary embeds no schemas, so every comparison below would be vacuous")
	}

	type headerProperties struct {
		Properties map[string]struct {
			Const json.RawMessage `json:"const"`
		} `json:"properties"`
	}

	out := map[string]string{}
	shipped := map[string]bool{}
	for _, token := range tokens {
		raw, ok := schemas.SchemaByKind(token)
		if !ok {
			t.Fatalf("the enumeration names %q but no schema answers to it", token)
		}
		var doc headerProperties
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatalf("the %s schema is not readable as JSON: %v", token, err)
		}
		if len(doc.Properties["apiVersion"].Const) == 0 || len(doc.Properties["kind"].Const) == 0 {
			continue
		}
		var documentKind string
		if err := json.Unmarshal(doc.Properties["kind"].Const, &documentKind); err != nil {
			t.Fatalf("the %s schema fixes a kind that is not a string: %v", token, err)
		}
		shipped[documentKind] = true
		if _, notAuthored := unauthored[documentKind]; notAuthored {
			continue
		}
		out[documentKind] = token
	}

	if len(out) == 0 {
		t.Fatal("no shipped schema fixes both an apiVersion and a kind, so the criterion selected nothing " +
			"and every comparison below would be vacuous")
	}
	excludedNames := make([]string, 0, len(unauthored))
	for documentKind := range unauthored {
		excludedNames = append(excludedNames, documentKind)
	}
	sort.Strings(excludedNames)
	for _, documentKind := range excludedNames {
		if !shipped[documentKind] {
			t.Errorf("%q is excluded as %s but no shipped schema declares it; the exclusion has outlived its subject",
				documentKind, unauthored[documentKind])
		}
	}
	return out
}

// TestEveryDeclaredKindHasADecidedDisposition holds the roll-call to the
// vocabulary in both directions, so a kind arriving in the vocabulary cannot
// reach an author before somebody has decided whether an author writes a
// document of it.
//
// It is the assertion that keeps the exclusion a decided ledger rather than a
// filter. The narrowing the three surfaces read is derived, so a newly declared
// kind lands in it by default and every other assertion here passes; this one is
// the only place that default is visible.
func TestEveryDeclaredKindHasADecidedDisposition(t *testing.T) {
	t.Parallel()

	declared := entitykind.Kinds()
	if len(declared) == 0 {
		t.Fatal("the vocabulary declares no kind, so the walks below assert nothing")
	}
	for _, k := range declared {
		if _, decided := declaredKindDisposition[k]; !decided {
			t.Errorf("kind %s is declared and nothing records whether an author writes a document of it.\n"+
				"Until that is recorded the kind reaches the wire, the `kind:` completion and the "+
				"per-file envelope leg as authorable by default, decided by nobody. Record it as a "+
				"document an author writes, or as content written inside another kind's document with "+
				"the reason beside it.", k)
		}
	}
	for k := range declaredKindDisposition {
		if !entitykind.IsKind(k) {
			t.Errorf("%q carries a disposition and the vocabulary no longer declares it: the decision has "+
				"outlived its subject", k)
		}
	}
}

// TestStandaloneKindsAreTheDecidedDisposition holds the narrowing the three
// surfaces read to the decided roll-call, by set difference in both directions.
//
// The two failures it separates are the two that matter. A kind decided to be a
// document an author writes and missing from the narrowing is a kind no surface
// offers, so an author typing it is told nothing — which reads as the kind not
// existing. A kind decided to be embedded content and present in the narrowing
// is a value an author is offered and then has nowhere to put.
func TestStandaloneKindsAreTheDecidedDisposition(t *testing.T) {
	t.Parallel()

	offered := standalonekind.Kinds()
	if len(offered) == 0 {
		t.Fatal("the narrowing holds no kind, so the comparisons below assert nothing")
	}
	got := make(map[entitykind.Kind]bool, len(offered))
	for _, k := range offered {
		got[k] = true
	}
	for _, k := range entitykind.Kinds() {
		disposition, decided := declaredKindDisposition[k]
		if !decided {
			// Reported by the roll-call assertion above; reporting it twice would
			// name one defect as two.
			continue
		}
		switch {
		case disposition == standaloneDocument && !got[k]:
			t.Errorf("kind %s is recorded as a document an author writes and the narrowing omits it: no "+
				"surface offers it, and an author typing it is told nothing", k)
		case disposition == embeddedInAnotherDocument && got[k]:
			t.Errorf("kind %s is recorded as content written inside another kind's document and the "+
				"narrowing offers it anyway: an author accepting the offer writes a document there is "+
				"nowhere to put", k)
		}
	}
	for _, k := range offered {
		if _, decided := declaredKindDisposition[k]; !decided {
			t.Errorf("the narrowing offers %s and nothing records that an author writes a document of it", k)
		}
	}
}

// namesMissingAndSurplus reports which of want are absent from got and which of
// got are not in want, so a failure says what moved rather than that two counts
// differ. Two sets short by one entry each agree on their counts.
func namesMissingAndSurplus(want map[string]string, got map[string]bool) (missing, surplus []string) {
	for name := range want {
		if !got[name] {
			missing = append(missing, name)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			surplus = append(surplus, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(surplus)
	return missing, surplus
}

// TestWireAuthorableKindsAreTheShippedAuthoringSchemas holds the set the daemon
// puts on the wire to the schemas that ship, in both registers and in both
// directions.
//
// This is the answer a client populates a drafting control from, so a kind
// missing here is a kind an author cannot be offered, and a surplus one is a
// value they would be offered and then refused.
func TestWireAuthorableKindsAreTheShippedAuthoringSchemas(t *testing.T) {
	t.Parallel()

	want := authorableKindsFromShippedSchemas(t)
	offered := authorableEntityKinds()
	if len(offered) == 0 {
		t.Fatal("the wire answer enumerates nothing, so the comparisons below assert nothing")
	}

	got := map[string]bool{}
	for _, d := range offered {
		got[d.DocumentKind] = true
		if token, ok := want[d.DocumentKind]; ok && d.Kind != token {
			t.Errorf("the wire names %s under the token %q, but its schema ships as %q: a client sending "+
				"the reported token would be refused", d.DocumentKind, d.Kind, token)
		}
	}

	missing, surplus := namesMissingAndSurplus(want, got)
	if len(missing) > 0 {
		t.Errorf("these kinds ship an authoring schema but are absent from the wire answer: %v\n"+
			"An author cannot be offered a kind the compiler does not name.", missing)
	}
	if len(surplus) > 0 {
		t.Errorf("the wire answer names these, and no shipped authoring schema does: %v\n"+
			"A client offering one would produce a document nothing accepts.", surplus)
	}
}

// TestWireAuthorableKindsResolveToASchema holds every reported token to the
// method that consumes it: a client walks this answer and asks about each entry,
// so a token that resolves to nothing turns the answer into a refusal.
func TestWireAuthorableKindsResolveToASchema(t *testing.T) {
	t.Parallel()

	offered := authorableEntityKinds()
	if len(offered) == 0 {
		t.Fatal("the wire answer enumerates nothing, so the walk below asserts nothing")
	}
	for _, d := range offered {
		if _, ok := schemas.SchemaByKind(d.Kind); !ok {
			t.Errorf("the wire reports the token %q for %s, and no schema answers to it",
				d.Kind, d.DocumentKind)
		}
	}
}

// TestCompletionOfferedKindsAreTheShippedAuthoringSchemas holds the `kind:`
// completion set to the same oracle.
//
// It is a separate assertion from the wire one rather than a comparison between
// the two, and that is the point: both read one narrowing of one vocabulary, so
// holding them to each other passes on any defect in what they share — a kind
// wrongly declared, and a kind wrongly excluded from the narrowing alike, since
// either moves both sides together. Each is held to the schemas instead, and to
// the roll-call, which is the one expectation neither of them reads.
func TestCompletionOfferedKindsAreTheShippedAuthoringSchemas(t *testing.T) {
	t.Parallel()

	want := authorableKindsFromShippedSchemas(t)
	if len(completion.EntityKinds) == 0 {
		t.Fatal("the completion surface offers no kinds, so the comparisons below assert nothing")
	}

	got := map[string]bool{}
	for _, name := range completion.EntityKinds {
		got[name] = true
	}
	missing, surplus := namesMissingAndSurplus(want, got)
	if len(missing) > 0 {
		t.Errorf("these kinds ship an authoring schema and are not offered as a `kind:` completion: %v\n"+
			"An author typing one is told nothing, which reads as the kind not existing.", missing)
	}
	if len(surplus) > 0 {
		t.Errorf("these are offered as a `kind:` completion and ship no authoring schema: %v\n"+
			"Accepting the completion writes a document nothing validates.", surplus)
	}
}

// TestEnvelopeClassifiableKindsAreTheShippedAuthoringSchemas holds the
// per-file envelope leg's classification to the same oracle.
//
// A kind absent here is authored, offered, and then never structurally checked:
// the buffer classifies as foreign YAML and the whole envelope leg passes over
// it in silence, which is the least visible of the three failures.
func TestEnvelopeClassifiableKindsAreTheShippedAuthoringSchemas(t *testing.T) {
	t.Parallel()

	want := authorableKindsFromShippedSchemas(t)
	if len(entityKindByName) == 0 {
		t.Fatal("no kind is envelope-classifiable, so the comparisons below assert nothing")
	}

	got := map[string]bool{}
	for name := range entityKindByName {
		got[name] = true
	}
	missing, surplus := namesMissingAndSurplus(want, got)
	if len(missing) > 0 {
		t.Errorf("these kinds ship an authoring schema and are not envelope-classifiable: %v\n"+
			"A document of one classifies as foreign YAML, so nothing checks its shape.", missing)
	}
	if len(surplus) > 0 {
		t.Errorf("these are envelope-classifiable and ship no authoring schema: %v", surplus)
	}
}
