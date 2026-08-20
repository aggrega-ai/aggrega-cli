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

// TestAConvertedFindingStaysMatchableByItsSentinel is the property every caller
// that reads a rule's identity depends on, and it is asserted over an objection
// a real check raised rather than over a hand-built one.
//
// Two callers read a finding by identity rather than by text. One drops a single
// rule's findings out of a halt set so the build proceeds with that condition
// reported instead of fatal; the other matches a rule through the aggregate
// error a failed resolve returns. Both ask the same question — does this finding
// wrap that sentinel — so both hold exactly when the conversion carries the
// sentinel through.
//
// The objection is produced by invoking a declared rule through the framework's
// own traversal over the coordinate set, because an objection cannot be
// constructed any other way: the fields are unexported and the only producer is
// a subject the framework supplies while it invokes a check. That makes this a
// statement about the real path rather than about a fixture shaped to pass.
//
// INVARIANT: a converted finding is matchable by the sentinel of the rule that
// raised it, both on its own and through the aggregate error carrying it.
// CONSEQUENCE: were the sentinel not carried, the tolerant path would stop
// recognising the condition it exists to tolerate and would halt a build it is
// supposed to let through — and no text comparison would notice, because the
// message would still read correctly.
func TestAConvertedFindingStaysMatchableByItsSentinel(t *testing.T) {
	t.Parallel()

	// The type is a catalog identity the catalog holds no entry for, which is
	// exactly the condition the rule below reports.
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web", types.ComponentInstance{Type: componentTypeURI})
	na := normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "conversion"},
		Spec:     types.AggregateSpec{Components: components},
	})

	view := newNormalizedView(na, nil, CatalogLookup{}, nil)
	objections, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, newCoordinateSet(view), view, componentTypeResolvesToACatalogEntry)
	if err != nil {
		t.Fatalf("the node traversal returned an error: %v", err)
	}
	if len(objections) == 0 {
		t.Fatal("the rule raised nothing for a type absent from the catalog.\n" +
			"INVARIANT: the fixture violates the rule, so there is a real objection to convert.\n" +
			"CONSEQUENCE: the assertions below run over an empty set and report the " +
			"conversion as correct without converting anything.")
	}

	findings := FindingsFromObjections(objections, "Aggregate conversion")
	if len(findings) != len(objections) {
		t.Fatalf("the conversion produced %d findings from %d objections.\n"+
			"INVARIANT: one finding per objection, none dropped.\n"+
			"CONSEQUENCE: a dropped objection is a violation the build never reports.",
			len(findings), len(objections))
	}

	sentinel := ErrGuardEntityViewInconsistency()

	// The per-finding match: what the tolerant path's filter asks of each halt.
	for _, f := range findings {
		if !errors.Is(f, sentinel) {
			t.Fatalf("a converted finding does not match the sentinel of the rule that raised it.\n" +
				"INVARIANT: the conversion carries the sentinel onto the finding it builds.\n" +
				"CONSEQUENCE: the path that drops this rule's findings out of a halt set " +
				"stops recognising them, and halts a build it exists to let through.")
		}
	}

	// The aggregate match: what a caller asks of the error a failed resolve
	// returns, which carries the findings rather than re-wrapping them.
	aggregated := &MultiResolverError{
		Entity:     "resolution",
		Summary:    "guard violations",
		Violations: findings,
	}
	if !errors.Is(aggregated, sentinel) {
		t.Fatal("the aggregate error does not match a sentinel one of its findings wraps.\n" +
			"INVARIANT: the aggregate unwraps to its findings, so a caller matches a " +
			"specific rule through it.\n" +
			"CONSEQUENCE: a caller that branches on a particular rule stops seeing it and " +
			"falls through to generic handling.")
	}

	// A rule the findings did NOT raise must not match, or the two assertions
	// above would hold for any sentinel and would be measuring nothing.
	if errors.Is(aggregated, ErrSchemaConformance()) {
		t.Fatal("the aggregate error matched a sentinel none of its findings raised.\n" +
			"INVARIANT: identity matching distinguishes rules.\n" +
			"CONSEQUENCE: the positive assertions above pass for every sentinel, so they " +
			"prove nothing about the one that was actually raised.")
	}
}

// TestTheConversionAttributesFindingsToTheDocumentItIsGiven pins the entity the
// conversion names, which is the half a caller supplies.
//
// The rules reporting through this conversion span both authoring documents, so
// the attribution cannot be derived from the objection: a rule over what the
// deployment wrote is not the application's fault, and naming one document for
// everything would send an operator to a file they did not write in.
//
// INVARIANT: findings carry the entity the caller named.
// CONSEQUENCE: were the entity fixed to one document, every finding about the
// other would be attributed to a file its author never edited.
func TestTheConversionAttributesFindingsToTheDocumentItIsGiven(t *testing.T) {
	t.Parallel()

	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web", types.ComponentInstance{Type: componentTypeURI})
	na := normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "attribution"},
		Spec:     types.AggregateSpec{Components: components},
	})

	view := newNormalizedView(na, nil, CatalogLookup{}, nil)
	objections, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, newCoordinateSet(view), view, componentTypeResolvesToACatalogEntry)
	if err != nil {
		t.Fatalf("the node traversal returned an error: %v", err)
	}
	if len(objections) == 0 {
		t.Fatal("the rule raised nothing, so there is no finding to attribute.")
	}

	for _, entity := range []string{"Aggregate attribution", "Release prod"} {
		for _, f := range FindingsFromObjections(objections, entity) {
			if f.Entity != entity {
				t.Errorf("a finding was attributed to %q; want %q.\n"+
					"INVARIANT: the conversion names the document the caller gave it.\n"+
					"CONSEQUENCE: a finding about one document is reported against the other.",
					f.Entity, entity)
			}
		}
	}
}

// TestTwoRulesOverOneComponentPositionAtTheDepthEachRaisedAt measures the two
// positions a finding can take, on ONE component, so the only thing that differs
// between the two readings is which raise the rule used.
//
// One rule objects about a key inside the component's configuration and names it;
// the other objects about a socket name the component wrote and keeps its finding
// at the component whose block holds it. Both are invoked over the same
// subject through the same traversal, and both convert through the same
// conversion — so a difference in where they land is attributable to the raise and
// to nothing else.
//
// The second rule is invoked once per component and reads the socket set that
// component wrote as a fact, so the offending socket is never presented to it as a
// subject: it names that socket in its detail and keeps the finding at the
// component. That is what makes it the honest exemplar of a raise that stays at
// the subject. Its position is asserted below rather than assumed, so a rule whose
// own position was later corrected fails here loudly instead of quietly becoming
// an exemplar of the other case.
//
// INVARIANT: a rule that raised at a leaf is positioned at that leaf; a rule that
// raised at the subject stays at the subject.
// CONSEQUENCE: were the two collapsed, an author whose configuration violates a
// contract would be sent to the component and left to find which key, which is
// the position this rule's hand-written predecessor did not settle for.
func TestTwoRulesOverOneComponentPositionAtTheDepthEachRaisedAt(t *testing.T) {
	t.Parallel()

	catalog := conformanceRuleCatalog(t)

	// One component: its configuration violates the contract its type declares,
	// and it wires a dependency socket one edit from the one that type declares.
	// The first is reported by a rule that names the offending key; the second by
	// a rule that names the offending socket and stays at the component.
	components := types.OrderedMap[types.ComponentInstance]{}
	values := types.OrderedMap[any]{}
	values.Set("replicas", "many")
	nearMiss := types.UsesSocketValue{}
	nearMiss.Verbose.Set("primary", types.VerboseUsesEntry{Target: "web"})
	instance := types.ComponentInstance{
		Type:   conformanceContractedType,
		Values: values,
	}
	// conformanceSocket is "store"; "stores" is one insertion from it.
	instance.Uses.Set(conformanceSocket+"s", nearMiss)
	components.Set("web", instance)
	na := normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "positions"},
		Spec:     types.AggregateSpec{Components: components},
	})
	view := newNormalizedView(na, nil, catalog, nil)

	atLeaf := fieldsFromRule(t, view, componentValuesConformToTheType)
	atSubject := fieldsFromRule(t, view, authoredSocketNameNearADeclaredOne)

	if want := []string{".spec.components.web.values.replicas"}; !equalStrings(atLeaf, want) {
		t.Errorf("the rule that raised at a leaf positioned at %v; want %v", atLeaf, want)
	}
	if want := []string{".spec.components.web"}; !equalStrings(atSubject, want) {
		t.Errorf("the rule that raised at the subject positioned at %v; want %v", atSubject, want)
	}

	// The control. Both rules are invoked over the same component, so if the
	// composition silently stopped happening both would report the component and
	// the two assertions above would still be describing a coherent-looking
	// system — one of them wrongly.
	if equalStrings(atLeaf, atSubject) {
		t.Errorf("both rules positioned at %v.\n"+
			"INVARIANT: the two raises produce different depths over one subject.\n"+
			"CONSEQUENCE: the leaf positioning has stopped happening, and nothing else "+
			"in this package would notice — the finding is still produced, still carries "+
			"its identity, and still names a real component.", atLeaf)
	}
}

// fieldsFromRule invokes one rule over the presented documents and returns the
// field each converted finding positions at, in the order raised.
//
// It goes through the conversion rather than reading the objections directly,
// because the field is what a consumer recovering a position reads and the
// conversion is what puts it there.
func fieldsFromRule(
	t *testing.T,
	view constraint.NormalizedView,
	check constraint.Check[constraint.NormalizedView, constraint.NodeSubject],
) []string {
	t.Helper()

	objections, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, newCoordinateSet(view.(*normalizedView)), view, check)
	if err != nil {
		t.Fatalf("the node traversal returned an error: %v", err)
	}
	if len(objections) == 0 {
		t.Fatal("the rule raised nothing, so the row measures no position at all")
	}
	out := make([]string, 0, len(objections))
	for _, f := range FindingsFromObjections(objections, "Aggregate positions") {
		out = append(out, f.Field)
	}
	return out
}

// TestAFindingCarriesWhatTheCheckRaisedItAbout pins the half of a message a
// registered sentinel cannot carry.
//
// A sentinel's message is one static string per rule, so it names the rule and
// can never name the value the rule objected about. The check supplies that value
// and the framework carries it, and this is where the two are put together — so a
// conversion reading only the sentinel would report every violation of one rule
// with identical text, whatever the author actually wrote.
//
// INVARIANT: a finding opens with the rule's registered wording and goes on to
// name what the check raised about.
// CONSEQUENCE: without the second half, two components with different faults
// under one rule produce two findings a reader cannot tell apart, and the
// offending value reaches the author from nowhere at all.
func TestAFindingCarriesWhatTheCheckRaisedItAbout(t *testing.T) {
	t.Parallel()

	// Two components naming two different absent types, so a message that
	// dropped the detail would render both identically.
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/absent-one:1.0.0"})
	components.Set("web", types.ComponentInstance{Type: "core/absent-two:1.0.0"})
	na := normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "detail"},
		Spec:     types.AggregateSpec{Components: components},
	})

	view := newNormalizedView(na, nil, presentCatalog(), nil)
	objections, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, newCoordinateSet(view), view, componentTypeResolvesToACatalogEntry)
	if err != nil {
		t.Fatalf("the node traversal returned an error: %v", err)
	}

	findings := FindingsFromObjections(objections, "Aggregate detail")
	if len(findings) != 2 {
		t.Fatalf("the rule produced %d findings; want 2, one per component naming an absent "+
			"type — with one finding the rows below cannot tell two messages apart",
			len(findings))
	}

	registered := ErrGuardEntityViewInconsistency().Error()
	for _, f := range findings {
		if !strings.HasPrefix(f.Message, registered) {
			t.Errorf("message = %q; want it to open with %q, the rule's own registered "+
				"wording, so the wording has one home", f.Message, registered)
		}
	}
	if a, b := findings[0].Message, findings[1].Message; a == b {
		t.Errorf("both findings read %q.\n"+
			"INVARIANT: a message names what its own check raised about.\n"+
			"CONSEQUENCE: the detail is being dropped, and every violation of one rule "+
			"reads identically however different the authoring faults are.", a)
	}
	for i, want := range []string{`"core/absent-one:1.0.0"`, `"core/absent-two:1.0.0"`} {
		if !strings.Contains(findings[i].Message, want) {
			t.Errorf("message = %q; want it to name %s, the type the check raised about",
				findings[i].Message, want)
		}
	}
}

// TestAFindingWithNoDetailReadsAsTheRulesOwnWording pins the arm on the other
// side of the enrichment: a rule supplying no value has nothing to append, and
// its message is the registered wording alone rather than that wording with an
// empty parenthesis after it.
//
// It is asserted over an objection built through the framework's own raise, so
// the empty-detail arm is exercised on the real path rather than described.
func TestAFindingWithNoDetailReadsAsTheRulesOwnWording(t *testing.T) {
	t.Parallel()

	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web", types.ComponentInstance{Type: componentTypeURI})
	na := normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "bare"},
		Spec:     types.AggregateSpec{Components: components},
	})
	view := newNormalizedView(na, nil, CatalogLookup{}, nil)

	// A rule declared here rather than reused, because every shipped rule
	// supplies detail and this arm needs one that does not.
	silent := constraint.Check[constraint.NormalizedView, constraint.NodeSubject]{
		Sentinel: ErrGuardEntityViewInconsistency(),
		Severity: constraint.SeverityFatal,
		Run: func(
			_ constraint.NormalizedView,
			s constraint.NodeSubject,
		) []constraint.Objection {
			return []constraint.Objection{s.Raise(ErrGuardEntityViewInconsistency())}
		},
	}

	objections, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, newCoordinateSet(view), view, silent)
	if err != nil {
		t.Fatalf("the node traversal returned an error: %v", err)
	}
	findings := FindingsFromObjections(objections, "Aggregate bare")
	if len(findings) == 0 {
		t.Fatal("the rule raised nothing, so there is no message to read")
	}
	for _, f := range findings {
		if want := ErrGuardEntityViewInconsistency().Error(); f.Message != want {
			t.Errorf("message = %q; want %q exactly — a rule that supplied no value has "+
				"nothing to name, and rendering an empty list would put punctuation with "+
				"nothing in it into what an author reads", f.Message, want)
		}
	}
}
