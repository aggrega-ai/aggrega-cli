// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package entitykind_test

import (
	"sort"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	aggerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// nonEntitySchemaTokens names every shipped schema that documents something other
// than an entity kind, with the reason it is not one. It is the counterpart to the
// declared vocabulary: between them the two must account for every schema that
// ships, so a schema added to neither fails the assertion below rather than
// quietly landing outside both.
//
// It is deliberately an exclusion list rather than the vocabulary being a hand
// list. A new entity kind arrives with a schema, and the person adding it is the
// same person who would have to remember to extend a hand list — so a hand list
// passes exactly when it is least useful. An exclusion list inverts that: the new
// schema is unaccounted-for by default and has to be claimed as one thing or the
// other.
var nonEntitySchemaTokens = map[string]string{
	"ir":                        "the emitted intermediate representation, an output rather than an authored document",
	"lockfile":                  "a generated build artifact, not authored by hand",
	"aggrega-config":            "workspace configuration, which is not an entity",
	"cache-metadata":            "bookkeeping for the artifact cache",
	"template-context":          "the shape handed to a template, not a document a user writes",
	"path-template-context":     "the shape handed to a path template, not a document a user writes",
	"plugins-trust":             "a generated record of the plugin identity trusted for each verdict-returning attachment, not authored by hand",
	"wasm-annotation-transform": "a plugin wire contract",
	"wasm-cli":                  "a plugin wire contract",
	"wasm-describe":             "a plugin wire contract",
	"wasm-diagnostic":           "a plugin wire contract",
	"wasm-frp-step":             "a plugin wire contract",
	"wasm-hostfunc-error":       "a plugin wire contract",
	"wasm-import":               "a plugin wire contract",
	"wasm-linter":               "a plugin wire contract",
	"wasm-mutator":              "a plugin wire contract",
	"wasm-parse":                "a plugin wire contract",
	"wasm-policy":               "a plugin wire contract",
	"wasm-provider":             "a plugin wire contract",
}

// TestDeclaredVocabularyAccountsForEveryShippedSchema derives the entity-kind set
// from the schemas that actually ship in the binary and holds the declared
// vocabulary against it, in both directions.
//
// The schema set is the authoritative structural contract, and it is read from the
// shipped artifact rather than transcribed, so it cannot be out of date. Every
// shipped schema is therefore either an entity kind or named as something else,
// and a kind declared with no schema behind it is just as much an error as a schema
// nothing claims.
func TestDeclaredVocabularyAccountsForEveryShippedSchema(t *testing.T) {
	shipped := schemas.Kinds()
	if len(shipped) == 0 {
		t.Fatal("no schemas were read from the binary; every assertion below would be vacuous")
	}

	declared := map[string]bool{}
	for _, segment := range entitykind.Segments() {
		declared[segment] = true
	}

	var unaccounted []string
	for _, token := range shipped {
		if declared[token] {
			continue
		}
		if _, named := nonEntitySchemaTokens[token]; named {
			continue
		}
		unaccounted = append(unaccounted, token)
	}
	if len(unaccounted) > 0 {
		sort.Strings(unaccounted)
		t.Errorf("these shipped schemas are claimed neither as entity kinds nor as non-entity documents: %v\n"+
			"Declare each one in the entity-kind vocabulary, or name it as a non-entity with its reason. "+
			"Leaving it in neither place is how a kind ends up absent from every membership set that reads the vocabulary.",
			unaccounted)
	}

	shippedSet := map[string]bool{}
	for _, token := range shipped {
		shippedSet[token] = true
	}
	for _, segment := range entitykind.Segments() {
		if !shippedSet[segment] {
			t.Errorf("entity kind %q is declared but ships no schema, so nothing can validate a document of that kind", segment)
		}
	}

	for token, reason := range nonEntitySchemaTokens {
		if !shippedSet[token] {
			t.Errorf("%q is named as a non-entity document (%s) but ships no schema; the exclusion is stale and should be removed", token, reason)
		}
		if declared[token] {
			t.Errorf("%q is both declared as an entity kind and named as a non-entity document; exactly one is true", token)
		}
	}
}

// TestEveryEntityKindHasAnEnvelopeSchema holds the entity-kind vocabulary against
// the document families that carry an embedded envelope schema, in both directions.
//
// The envelope family set is the wider of the two — it covers generated and
// configuration documents as well as authored entities — so the containment runs
// one way and the surplus is named rather than tolerated. What the assertion buys
// is that the two cannot drift silently: a kind declared here with no envelope
// schema cannot be validated at all, and a new envelope family that is in fact an
// entity kind fails until it is declared as one.
func TestEveryEntityKindHasAnEnvelopeSchema(t *testing.T) {
	// Envelope families that are not entity kinds, with the reason. Kept in the
	// same shape as the schema exclusions so a new family lands in neither by
	// default.
	nonEntityEnvelopeKinds := map[envelope.Kind]string{
		envelope.KindAggregaConfig: "workspace configuration",
		envelope.KindLockfile:      "a generated build artifact",
		envelope.KindPluginsTrust:  "a generated record of trusted plugin identities",
		envelope.KindIR:            "the emitted intermediate representation",
	}

	families := map[envelope.Kind]bool{}
	for _, k := range envelope.Kinds() {
		families[k] = true
	}
	if len(families) == 0 {
		t.Fatal("no envelope families were enumerated; every assertion below would be vacuous")
	}

	for _, k := range entitykind.Kinds() {
		if !families[envelope.Kind(string(k))] {
			t.Errorf("entity kind %s has no envelope schema family, so a document of that kind is never structurally validated", k)
		}
	}

	declared := map[envelope.Kind]bool{}
	for _, k := range entitykind.Kinds() {
		declared[envelope.Kind(string(k))] = true
	}
	for family := range families {
		if declared[family] {
			continue
		}
		if _, named := nonEntityEnvelopeKinds[family]; named {
			continue
		}
		t.Errorf("envelope family %s is claimed neither as an entity kind nor as a non-entity document; declare it as one or name it as the other", family)
	}
}

// TestPublishableKindsAreEntityKinds holds the publishable-kind set against the
// vocabulary.
//
// Which kinds may be published, and under which mediatype, stays where it is: the
// mediatype is genuinely new per-kind information rather than a copy of anything
// here, and a kind can be catalog-resident without being publishable. What this
// pins is only that a publishable kind is a kind — so the two surfaces cannot
// disagree about the spelling of one, and a kind cannot be publishable without
// existing.
func TestPublishableKindsAreEntityKinds(t *testing.T) {
	publishable := oci.MediaTypeKinds()
	if len(publishable) == 0 {
		t.Fatal("no publishable kinds were enumerated; this assertion would be vacuous")
	}
	for _, kind := range publishable {
		if !entitykind.IsKind(entitykind.Kind(kind)) {
			t.Errorf("%q is publishable but is not a declared entity kind", kind)
		}
	}
}

// TestPublishableKindsAreConventionsLinted holds the two sets that answer
// overlapping questions about the same kind — may it be published, and do the
// publishing-conventions rules examine it — against each other, in both directions.
//
// They are separate facts and neither implies the other, which is exactly why they
// need pinning rather than merging: a kind can be catalog-resident without being
// publishable, and an authoring-side document can be publishable without the
// catalog-publishing rules having anything to say about it. What must not happen is
// for the two to disagree by accident. A kind that is published while no convention
// rule examines it ships unlinted, and nothing reports the gap — the walk drops the
// file before it reaches any rule, so the result is a clean report rather than a
// skipped one.
//
// Every difference between the sets is therefore declared below with its reason. A
// kind entering one set without entering the other, or without being claimed as a
// deliberate difference, fails here.
func TestPublishableKindsAreConventionsLinted(t *testing.T) {
	// Publishable kinds the conventions rules deliberately do not examine, with the
	// reason each is exempt.
	publishableButNotLinted := map[entitykind.Kind]string{
		entitykind.Aggregate: "an Aggregate is a developer's application-intent document; it is publishable as a reusable module, but the conventions rules describe catalog-publishing discipline, which is not what an Aggregate is authored against",
	}

	// Conventions-linted kinds that are not publishable, with the reason each is
	// linted anyway. Empty today: every kind the rules examine is one that ships.
	lintedButNotPublishable := map[entitykind.Kind]string{}

	publishable := map[entitykind.Kind]bool{}
	for _, kind := range oci.MediaTypeKinds() {
		publishable[entitykind.Kind(kind)] = true
	}
	if len(publishable) == 0 {
		t.Fatal("no publishable kinds were enumerated; every assertion below would be vacuous")
	}

	linted := map[entitykind.Kind]bool{}
	for _, kind := range entitykind.ConventionsLinted() {
		linted[kind] = true
	}
	if len(linted) == 0 {
		t.Fatal("no conventions-linted kinds were enumerated; every assertion below would be vacuous")
	}

	for kind := range publishable {
		if linted[kind] {
			continue
		}
		if _, named := publishableButNotLinted[kind]; named {
			continue
		}
		t.Errorf("kind %s is publishable but no convention rule examines it, and it is not named as a deliberate exemption: "+
			"a document of that kind ships without ever being checked, and because the walk drops it before any rule runs, "+
			"the pass reports success rather than reporting that it skipped anything. "+
			"Either mark the kind conventions-linted in the vocabulary, or name it here with the reason it is exempt.", kind)
	}

	for kind := range linted {
		if publishable[kind] {
			continue
		}
		if _, named := lintedButNotPublishable[kind]; named {
			continue
		}
		t.Errorf("kind %s is conventions-linted but is not publishable, and is not named as a deliberate difference: "+
			"the publishing-conventions rules are being run over a document that is never published. "+
			"Either give the kind a mediatype, or name it here with the reason it is linted anyway.", kind)
	}

	// Both exclusion sets are held against reality so neither can go stale. An
	// entry describing a difference that no longer exists is worse than no entry:
	// it silently licenses the very drift it was written to make visible.
	for kind, reason := range publishableButNotLinted {
		if !publishable[kind] {
			t.Errorf("%s is named as publishable-but-not-linted (%s) but is no longer publishable; the exemption is stale and should be removed", kind, reason)
		}
		if linted[kind] {
			t.Errorf("%s is named as publishable-but-not-linted (%s) but the rules now examine it; remove the exemption so the sets read as agreeing", kind, reason)
		}
	}
	for kind, reason := range lintedButNotPublishable {
		if !linted[kind] {
			t.Errorf("%s is named as linted-but-not-publishable (%s) but is no longer conventions-linted; the entry is stale and should be removed", kind, reason)
		}
		if publishable[kind] {
			t.Errorf("%s is named as linted-but-not-publishable (%s) but is now publishable; remove the entry so the sets read as agreeing", kind, reason)
		}
	}
}

// TestKindPathSegmentNeedsNoPerKindException holds the OCI repository path rule to
// the vocabulary's own segment rule.
//
// The path rule is a single lowercase transform with one alias for the raw plugin
// artifact, which is not an entity kind. This assertion is what lets it stay a
// single rule: if a future kind ever needed a path segment that is not its
// lowercase name, this fails and the exception has to be written down deliberately
// rather than discovered by a reader wondering why an address does not resolve.
func TestKindPathSegmentNeedsNoPerKindException(t *testing.T) {
	kinds := entitykind.Kinds()
	if len(kinds) == 0 {
		t.Fatal("no entity kinds were enumerated; this assertion would be vacuous")
	}
	for _, k := range kinds {
		want := entitykind.Segment(k)
		if got := catalog.KindPathSegment(string(k)); got != want {
			t.Errorf("kind %s addresses as %q in an OCI repository path but as %q everywhere else; one of the two is wrong", k, got, want)
		}
	}
	// The one alias, asserted so it cannot be dropped by someone reading the loop
	// above and concluding the transform is unconditional.
	if got := catalog.KindPathSegment("Wasm"); got != "plugin" {
		t.Errorf("the raw plugin artifact addresses as %q, want %q", got, "plugin")
	}
}

// TestLibraryAxisKindsAreEntityKinds holds the library-axis table's kind names to
// the vocabulary.
//
// The axis table carries closure and pin machinery this vocabulary knows nothing
// about, so it stays where it is. What must not drift is the kind name it publishes
// under: an axis naming a kind the vocabulary does not have would resolve against
// nothing on every surface that reads the vocabulary.
func TestLibraryAxisKindsAreEntityKinds(t *testing.T) {
	axes := closure.LibraryAxes()
	if len(axes) == 0 {
		t.Fatal("no library axes were enumerated; this assertion would be vacuous")
	}
	for _, axis := range axes {
		if !entitykind.IsKind(entitykind.Kind(axis.Kind)) {
			t.Errorf("library axis %s publishes under kind %q, which is not a declared entity kind", axis.Axis, axis.Kind)
		}
	}
}

// TestCategorisedKindsAreEntityKinds holds the kind-to-domain mapping to the
// vocabulary and records, explicitly, which kinds it does not cover.
//
// The mapping is deliberately partial: it reports an uncovered kind as uncovered
// rather than folding it into a default, so a caller has to decide what an
// unmappable kind means. That partiality is a design choice and is not corrected
// here. What is pinned is that every kind it does cover exists, and that the
// uncovered set is written down — so it is a visible decision rather than an
// unremarked gap, and a kind leaving the vocabulary cannot leave a mapping behind.
func TestCategorisedKindsAreEntityKinds(t *testing.T) {
	// The kinds the mapping deliberately leaves uncovered today. Listed so the
	// partiality is legible; a change either way fails here and is then a decision
	// someone made rather than a drift nobody saw.
	expectedUncovered := map[entitykind.Kind]bool{
		entitykind.SchemaLibrary:    true,
		entitykind.MappingLibrary:   true,
		entitykind.WorkloadLibrary:  true,
		entitykind.ArtifactsLibrary: true,
		entitykind.Plugin:           true,
		entitykind.Workload:         true,
	}

	var covered, uncovered []string
	for _, k := range entitykind.Kinds() {
		if _, ok := aggerrors.ClassifyEntityKind(string(k)); ok {
			covered = append(covered, string(k))
			if expectedUncovered[k] {
				t.Errorf("kind %s is recorded as having no domain mapping, but the mapping now covers it; update the record so the partiality stays legible", k)
			}
			continue
		}
		uncovered = append(uncovered, string(k))
		if !expectedUncovered[k] {
			t.Errorf("kind %s has no domain mapping and is not recorded as one of the deliberately uncovered kinds: a fault on it is reported at the developer's surface by fallback rather than by decision", k)
		}
	}
	if len(covered) == 0 {
		t.Fatal("the mapping covered no kind at all; this assertion would be vacuous")
	}
	t.Logf("domain mapping covers %d kinds (%s); deliberately uncovered: %s",
		len(covered), strings.Join(covered, ", "), strings.Join(uncovered, ", "))
}
