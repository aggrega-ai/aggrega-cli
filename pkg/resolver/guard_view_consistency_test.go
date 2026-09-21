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

// raiseOverOneBareComponent runs the catalog-entry rule over a single component
// naming typeRef against a catalog that holds nothing, and returns what it
// raised. The catalog miss is the whole precondition of the rule, so a helper
// that assembled anything else would be testing a different fixture.
func raiseOverOneBareComponent(t *testing.T, typeRef string) []constraint.Objection {
	t.Helper()
	view := newNormalizedView(normalized(t, oneBareComponent(typeRef, nil)), nil, notFoundCatalog(), nil)
	objections, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, newCoordinateSet(view), view, componentTypeResolvesToACatalogEntry)
	if err != nil {
		t.Fatalf("the node walk returned an error: %v", err)
	}
	return objections
}

// notFoundCatalog is a CatalogLookup whose Component lookup always misses — the
// resolved-but-absent condition a bare catalog reference turns into a loud halt.
func notFoundCatalog() CatalogLookup {
	return CatalogLookup{Component: func(string) (types.Component, bool) {
		return types.Component{}, false
	}}
}

func oneBareComponent(typeRef string, inject *types.InjectBlock) *types.Aggregate {
	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set("svc", types.ComponentInstance{Type: typeRef, Inject: inject})
	return &types.Aggregate{Metadata: types.AggregateMetadata{Name: "demo"}, Spec: types.AggregateSpec{Components: comps}}
}

// TestGuardEntityViewInconsistency_Classifies pins the shared classifier: a
// relative-path type is a legitimate skip (nil); a bare catalog reference
// absent from the view is the loud halt.
func TestGuardEntityViewInconsistency_Classifies(t *testing.T) {
	t.Parallel()
	if verr := guardEntityViewInconsistency("./local-variant"); verr != nil {
		t.Errorf("relative-path type must classify as a legitimate skip (nil); got %v", verr)
	}
	if verr := guardEntityViewInconsistency("../sibling"); verr != nil {
		t.Errorf("parent-relative-path type must classify as a legitimate skip (nil); got %v", verr)
	}
	verr := guardEntityViewInconsistency("core/absent:1.0.0")
	if verr == nil || !errors.Is(verr, ErrGuardEntityViewInconsistency()) {
		t.Fatalf("bare catalog ref must halt with ErrGuardEntityViewInconsistency; got %v", verr)
	}
}

// TestTheAbsentCatalogEntryIsClassifiedByTheRuleThatOwnsIt pins the one-home
// property for a fault that more than one rule can see.
//
// A component whose type the catalog holds no entry for declares no socket set,
// so every rule reading what a type declares ends on that component rather than
// classifying it. The classification is a separate rule with its own sentinel,
// asked once per component naming such a type; a second rule that also
// classified would report one fault twice.
//
// 🔴 The other half of the pair — that the required-socket rule stays silent on
// such a component — is asserted where that rule is proved, over the
// composition whose facts it reads. The silence alone would also be produced by a
// rule that never reached the component, which is why the assertion sits beside a
// fixture that can show the rule being invoked.
func TestTheAbsentCatalogEntryIsClassifiedByTheRuleThatOwnsIt(t *testing.T) {
	t.Parallel()

	agg := oneBareComponent("core/absent:1.0.0", nil)
	na := normalized(t, agg)
	catalog := notFoundCatalog()

	view := newNormalizedView(na, nil, catalog, nil)
	objections, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, newCoordinateSet(view), view, componentTypeResolvesToACatalogEntry)
	if err != nil {
		t.Fatalf("the node walk returned an error: %v", err)
	}
	if len(objections) != 1 {
		t.Fatalf("the rule that owns the classification raised %d objection(s); want 1.\n"+
			"INVARIANT: a component naming a catalog identity nobody found is reported once.\n"+
			"CONSEQUENCE: with the guard's own raise removed, nothing reports the fault at all.",
			len(objections))
	}
	if !errors.Is(objections[0].Sentinel(), ErrGuardEntityViewInconsistency()) {
		t.Errorf("the objection carries %v; want the catalog-entry rule's own sentinel.\n"+
			"INVARIANT: the fault keeps its identity when its home moves.\n"+
			"CONSEQUENCE: a caller matching on the sentinel stops seeing this refusal.",
			objections[0].Sentinel())
	}
	if want := ".spec.components.svc"; objections[0].Locator() != want {
		t.Errorf("the objection is positioned at %q; want %q", objections[0].Locator(), want)
	}
}

// TestTheAbsentCatalogEntryReadsTheSameWhicheverSurfaceReportsIt holds the
// one-wording property for a condition that two surfaces report.
//
// The rule asked once per component raises an objection; the sub-aggregate
// conformance guard composes a message. They are two shapes, and they are
// supposed to be one wording — so the assertion is byte equality of the text an
// author reads, not similarity of it.
//
// 🔴 Equality alone would also be satisfied by both surfaces saying nothing, so
// the naming assertion below it is what stops the pair passing vacuously: the
// text has to carry the type the author wrote AND the words that say what went
// wrong. Without the second, a rule reporting the bare type and a guard reporting
// the bare type would agree perfectly and tell an author nothing.
func TestTheAbsentCatalogEntryReadsTheSameWhicheverSurfaceReportsIt(t *testing.T) {
	t.Parallel()

	const typeRef = "core/absent:1.0.0"

	objections := raiseOverOneBareComponent(t, typeRef)
	if len(objections) != 1 {
		t.Fatalf("the rule raised %d objection(s); want 1", len(objections))
	}
	// The conversion the declared-rule pass actually binds this rule to. Reading
	// it through a different one would compare a rendering no build produces.
	raised := findingsWithNoSubject(objections)

	composed := guardEntityViewInconsistency(typeRef)
	if composed == nil {
		t.Fatalf("the composing guard read %q as a legitimate skip; a bare catalog reference is the loud case", typeRef)
	}

	if raised[0].Message != composed.Message {
		t.Errorf("the two surfaces report one condition in two wordings.\n"+
			"  rule:  %s\n"+
			"  guard: %s\n"+
			"INVARIANT: one condition has one wording, however many surfaces report it.\n"+
			"CONSEQUENCE: an author meeting the condition at one surface is told something "+
			"the other surface does not say, and neither text says it is the same condition.",
			raised[0].Message, composed.Message)
	}
	if !strings.Contains(raised[0].Message, typeRef) {
		t.Errorf("the reported text %q does not name the type the component declared (%q).\n"+
			"INVARIANT: the text names the value the author wrote.\n"+
			"CONSEQUENCE: the author is told a condition was met and not which type met it.",
			raised[0].Message, typeRef)
	}
	if !strings.Contains(raised[0].Message, catalogEntryAbsentCaption) {
		t.Errorf("the reported text %q carries no wording beyond the type.\n"+
			"INVARIANT: the text says what went wrong, not only what it is about.\n"+
			"CONSEQUENCE: the rule reports a bare type and the author is left to infer the fault.",
			raised[0].Message)
	}
}

// TestNeitherSurfaceSendsAReaderToAnAuthoredCoordinate holds the property that
// distinguishes this condition from every other finding the resolve reports: it
// names no authoring document and sits at no key inside one.
//
// A reader cannot correct this condition anywhere, so a document reference or a
// field path would be read as the accusation and the wording as boilerplate
// around it — which is exactly what the text spends its length denying. The
// assertion is over the FINDING each surface produces rather than over the
// objection the rule raises: the objection still carries the coordinate it was
// raised at, because that is where the walk was, and the conversion is what
// decides that the coordinate is not reported.
//
// 🔴 The category is asserted alongside, and it is asserted to be ABSENT. A
// category answers "which authoring surface do I fix", so a fault that is
// nobody's has none to name, and the head prints the condition without one. The
// assertion is written as "no category" rather than as "not the developer's"
// deliberately: what it has to exclude is any surface at all, since sending a
// reader to the catalog would be the same misdirection as sending them to the
// Aggregate.
//
// 🔴 It does NOT invent a domain for the condition, and the difference matters
// because the two repairs look alike from the outside. Filing this under a
// category of its own would be a new answer to "which surface do I fix" for a
// failure that has no surface; filing it under none is the answer the question
// actually has. So a later change that gave this condition a category — any
// category — is a re-attribution rather than a continuation of this one, and
// this assertion is what makes it fail rather than pass quietly.
func TestNeitherSurfaceSendsAReaderToAnAuthoredCoordinate(t *testing.T) {
	t.Parallel()

	const typeRef = "core/absent:1.0.0"

	objections := raiseOverOneBareComponent(t, typeRef)
	if len(objections) != 1 {
		t.Fatalf("the rule raised %d objection(s); want 1", len(objections))
	}
	if objections[0].Locator() == "" {
		t.Fatal("the objection carries no locator, so this test can no longer show that " +
			"the CONVERSION is what drops it rather than the walk never having one")
	}

	for surface, finding := range map[string]*ResolverError{
		"the rule asked once per component": findingsWithNoSubject(objections)[0],
		"the composing guard":               guardEntityViewInconsistency(typeRef),
	} {
		if finding == nil {
			t.Fatalf("%s produced no finding for a bare catalog reference", surface)
		}
		if finding.Entity != "" {
			t.Errorf("%s names the document %q.\n"+
				"INVARIANT: a condition no authored document causes names no authored document.\n"+
				"CONSEQUENCE: a reader is sent to a file that is not at fault and cannot be corrected.",
				surface, finding.Entity)
		}
		if finding.Field != "" {
			t.Errorf("%s positions the finding at %q.\n"+
				"INVARIANT: a condition no authored document causes sits at no key inside one.\n"+
				"CONSEQUENCE: an editor draws the diagnostic on an authoring key the author must not change.",
				surface, finding.Field)
		}
		if got := finding.ToCompilerError().Category(); got != "" {
			t.Errorf("%s files the condition under %v; want no category at all.\n"+
				"INVARIANT: a category answers which authoring surface to fix, so a fault in the "+
				"compiler carries none — not the developer's, and not a domain of its own either.\n"+
				"CONSEQUENCE: the head sends the reader to a surface that is not at fault, and the "+
				"reader believes it, because the head is the part of a diagnostic they act on first.",
				surface, got)
		}
		if !strings.Contains(finding.Message, typeRef) {
			t.Errorf("%s does not name the type the two views disagree on.\n"+
				"INVARIANT: with no coordinate to give, the type is the whole of what makes the "+
				"divergence reproducible.\n"+
				"CONSEQUENCE: the report the wording asks for cannot be acted on.",
				surface)
		}
	}
}

// TestTheRuleStaysSilentOnALocalPathType is the negative control for the pair
// above: the same rule, the same empty catalog, a type addressed as a file
// beside the authoring document. Nothing is raised, because such a type has no
// catalog entity to be missing.
//
// It sits here rather than beside the classifier's own relative-path case
// because the two are separate claims: that the classifier reads a relative path
// as a skip, and that the rule reaches the same verdict through the fact the view
// publishes for it. A green on the first would not establish the second.
func TestTheRuleStaysSilentOnALocalPathType(t *testing.T) {
	t.Parallel()

	for _, typeRef := range []string{"./local-variant.yaml", "../sibling/component.yaml"} {
		if objections := raiseOverOneBareComponent(t, typeRef); len(objections) != 0 {
			t.Errorf("the rule raised %d objection(s) for %q; want none.\n"+
				"INVARIANT: a type addressed as a local path has no catalog entity to be absent.\n"+
				"CONSEQUENCE: a legitimate local-file type halts a build that should proceed.",
				len(objections), typeRef)
		}
	}
}
