// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The declared names these tests use are the shapes a provider actually derives,
// not toy strings: a logical id sanitised from a node id joined to a resource
// name, and a four-segment export name. A toy string would not exercise the
// comparison over the character classes a real derivation produces, and the
// near-miss below turns on a single character inside one of them.
const (
	realDerivedLogicalID = "paymentsapiQueue"
	// realDerivedLogicalIDNearMiss is realDerivedLogicalID with one character
	// changed. It is what keeps the guard honest in the negative direction: a
	// check that matched on similarity rather than equality would reject it.
	realDerivedLogicalIDNearMiss = "paymentsapiQueve"
	realDerivedExportName        = "aggrega-prod-api-orders-db-host"
)

// The three shapes of contributor these cases build labels for, each composed
// through the one helper production uses. Written as helpers rather than as
// struct literals so a case cannot quietly assemble a label the production path
// could never produce — which is what would let a case pass over a coordinate
// production drops.
//
// Each pairs its label with the disposition production pairs it with when the
// contributor's output reaches its group's assembler, which is the shape every
// case below is about unless it says otherwise. The standalone direction is built
// by placedStandalone, so a case asserting the narrowing states it rather than
// inheriting it.
func declaredByComponent(name string) derivedKeyContributor {
	return derivedKeyContributorFor(derivedKeyProducerFor(producerKindComponent, name, ""), false)
}

func declaredByAttachment(node, attachment string) derivedKeyContributor {
	return derivedKeyContributorFor(
		derivedKeyProducerFor(producerKindComponent, node, attachment), false)
}

func declaredByBoundaryFeature(instance, boundaryPath string) derivedKeyContributor {
	return derivedKeyContributorFor(
		derivedKeyProducerFor(producerKindFeature, instance, boundaryPath), false)
}

// placedStandalone is the same contributor with the render having written its
// output as discrete files at its own position rather than handing it to its
// group's assembler.
func placedStandalone(c derivedKeyContributor) derivedKeyContributor {
	return derivedKeyContributorFor(c.producer, true)
}

// derivedNamesView presents one group's derived-name declarations and what its
// elected provider declared it does with the group's output, and nothing else.
//
// The rule under test reads exactly those two facts, so nothing behind the
// embedded interface is ever asked for. It carries no value there deliberately: a
// read panics rather than answering a zero value, so a rule that later starts
// reading a third fact fails loudly instead of being quietly handed nothing.
//
// assembly is empty by default, which is the answer for a provider declaring no
// assembly claim — the case in which nothing is narrowed and every contributor is
// compared against every other.
type derivedNamesView struct {
	constraint.DispatchedView
	declared []constraint.DerivedNameDeclaration
	assembly string
}

func (v derivedNamesView) AssemblyGroupDerivedNames(
	_ string, _ string,
) []constraint.DerivedNameDeclaration {
	return v.declared
}

func (v derivedNamesView) AssemblyGroupProviderAssembly(_ string, _ string) string {
	return v.assembly
}

// derivedNamesAxes supplies the one group coordinate these cases are about.
//
// It is the real axis pair the traversal takes rather than a hand-built subject,
// because a subject is this framework's to construct: driving the traversal is
// what makes these cases exercise the invocation a build performs rather than a
// call the production path never makes.
type derivedNamesAxes struct {
	path     string
	provider string
}

func (a derivedNamesAxes) AssemblyPaths() []constraint.AuthoredCoordinate {
	return []constraint.AuthoredCoordinate{{Value: a.path}}
}

func (a derivedNamesAxes) AssemblyGroupProviders(assemblyPath string) []string {
	if assemblyPath != a.path {
		return nil
	}
	return []string{a.provider}
}

// runDerivedNameRule invokes the declared rule over one group's declarations the
// way a build does, and returns the messages it raised in the order it raised
// them.
//
// It goes through the traversal rather than calling the rule's own function,
// which is what keeps these cases honest about invocation: a rule invoked zero
// times reports exactly what a rule that ran and approved reports, so a helper
// that bypassed the traversal could not tell the two apart.
func runDerivedNameRule(t *testing.T, set *derivedKeySet) []string {
	t.Helper()
	return runDerivedNameRuleUnder(t, set, "")
}

// runDerivedNameRuleUnder is the same invocation with the group's elected
// provider declaring assembly. The mode is a parameter rather than a second
// helper because it is one of the two facts the rule reads, and a case that fixes
// only the declarations fixes half its input.
func runDerivedNameRuleUnder(t *testing.T, set *derivedKeySet, assembly string) []string {
	t.Helper()

	axes := derivedNamesAxes{path: "boundaries/default", provider: "collide"}
	var view constraint.DispatchedView = derivedNamesView{
		declared: set.declarations(), assembly: assembly,
	}
	raised, err := constraint.RunOnAssemblyPathProviders(
		func() error { return nil }, axes, axes, view,
		AssemblyGroupDerivesEachNameOnce,
	)
	if err != nil {
		t.Fatalf("the traversal failed: %v", err)
	}
	out := make([]string, 0, len(raised))
	for _, o := range raised {
		if !errors.Is(o.Sentinel(), ErrDerivedKeyCollision()) {
			t.Fatalf("an objection carries %v rather than the derived-name condition", o.Sentinel())
		}
		out = append(out, constraint.ObjectionMessage(o))
	}
	return out
}

// TestDerivedNameRule_InvariantUnderDeclarationOrder proves the property at the
// unit that owns it: the same declarations, offered in any order, produce the
// same objections in the same sequence. The maps and groups feeding the
// accumulator upstream are walked in orders that are not fixed by the input, so
// any dependence on declaration order here would surface as a build that reports
// a different message on different runs of identical input.
func TestDerivedNameRule_InvariantUnderDeclarationOrder(t *testing.T) {
	t.Parallel()

	declarations := []derivedKeyDeclaration{
		{space: "logical-ids", key: realDerivedLogicalID, contributor: declaredByComponent("one")},
		{space: "export-names", key: realDerivedExportName, contributor: declaredByComponent("two")},
		{space: "logical-ids", key: realDerivedLogicalID, contributor: declaredByComponent("three")},
		{space: "export-names", key: realDerivedExportName, contributor: declaredByComponent("four")},
		{space: "export-names", key: realDerivedExportName, contributor: declaredByComponent("five")},
	}

	var want []string
	for _, order := range permutations(len(declarations)) {
		var set derivedKeySet
		for _, i := range order {
			d := declarations[i]
			set.put(d.space, d.key, d.contributor)
		}
		got := runDerivedNameRule(t, &set)
		if len(got) == 0 {
			t.Fatalf("order %v: expected objections", order)
		}
		if want == nil {
			want = got
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("the objections depend on declaration order\nwant: %v\ngot (order %v): %v", want, order, got)
		}
	}

	// Every colliding space and name is objected to, one objection per
	// contributor that derived it, and the spaces come out in space order rather
	// than in the order the accumulating walks happened to take.
	if len(want) != 5 {
		t.Fatalf("expected one objection per contributor per colliding name, got %d: %v", len(want), want)
	}
	for _, fragment := range []string{
		`key space "export-names"`,
		`key space "logical-ids"`,
		`derived by component "five"`,
		`derived by component "one"`,
		`dispatch path "boundaries/default"`,
		`provider "collide"`,
	} {
		var found bool
		for _, m := range want {
			if strings.Contains(m, fragment) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no objection carries %q\ngot: %v", fragment, want)
		}
	}
	joined := strings.Join(want, "\n")
	if strings.Index(joined, "export-names") > strings.Index(joined, "logical-ids") {
		t.Errorf("colliding spaces must be objected to in space order, got: %v", want)
	}
}

// TestDerivedNameRule_BothDirections is the near-miss pair. The same two
// contributors declaring into the same space object when the name is identical
// and stay silent when one character of it differs — so the rule is comparing the
// declared strings for equality and nothing else.
func TestDerivedNameRule_BothDirections(t *testing.T) {
	t.Parallel()

	t.Run("identical name objects", func(t *testing.T) {
		t.Parallel()
		var set derivedKeySet
		set.put("logical-ids", realDerivedLogicalID, declaredByComponent("alpha"))
		set.put("logical-ids", realDerivedLogicalID, declaredByComponent("beta"))
		if got := runDerivedNameRule(t, &set); len(got) != 2 {
			t.Fatalf("expected one objection per contributor, got %d: %v", len(got), got)
		}
	})

	t.Run("one character changed stays silent", func(t *testing.T) {
		t.Parallel()
		var set derivedKeySet
		set.put("logical-ids", realDerivedLogicalID, declaredByComponent("alpha"))
		set.put("logical-ids", realDerivedLogicalIDNearMiss, declaredByComponent("beta"))
		if got := runDerivedNameRule(t, &set); len(got) != 0 {
			t.Fatalf("a one-character difference must not object: %v", got)
		}
	})
}

// TestDerivedNameRule_SameNameInDifferentSpacesIsSilent pins the space as the
// scope of the comparison. Two contributors deriving one name into two different
// spaces have not collided — nothing in either output displaces the other — and a
// rule that compared names across spaces would reject legitimate authoring.
func TestDerivedNameRule_SameNameInDifferentSpacesIsSilent(t *testing.T) {
	t.Parallel()

	var set derivedKeySet
	set.put("logical-ids", realDerivedLogicalID, declaredByComponent("alpha"))
	set.put("export-names", realDerivedLogicalID, declaredByComponent("beta"))
	if got := runDerivedNameRule(t, &set); len(got) != 0 {
		t.Fatalf("one name in two different spaces must not object: %v", got)
	}
}

// TestDerivedNameRule_TwoKindsOfContributorAreNamedByKind covers the axis a
// component-only corpus cannot reach: a boundary-scope feature and a component
// meeting on one name are named by what each of them is, because that is what
// tells an author which block to open.
func TestDerivedNameRule_TwoKindsOfContributorAreNamedByKind(t *testing.T) {
	t.Parallel()

	var set derivedKeySet
	set.put("logical-ids", realDerivedLogicalID, declaredByComponent("alpha"))
	set.put("logical-ids", realDerivedLogicalID, declaredByBoundaryFeature("logging", "cluster"))
	got := runDerivedNameRule(t, &set)
	if len(got) != 2 {
		t.Fatalf("expected one objection per contributor, got %d: %v", len(got), got)
	}
	for _, want := range []string{
		`derived by component "alpha"`,
		`derived by feature "logging"`,
	} {
		var found bool
		for _, m := range got {
			if strings.Contains(m, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no objection carries %q\ngot: %v", want, got)
		}
	}
}

// TestDerivedNameRule_OneContributorRestatingItselfAcrossCrossingsIsSilent covers
// the shape a single node produces legitimately, and it is the bound the
// within-one-declaration case below must not break. A node renders across more
// than one crossing — a source render, a workload synthesis, an artifacts
// placement — and the same name declared at two of them is one contributor
// describing one resource twice, not two contributors meeting on one name.
// Objecting would name the same component on both sides of a collision with
// itself.
//
// Two separate put calls are exactly what that restatement looks like once the
// declarations have accumulated: each crossing declared the name once, so no
// declaration carried it twice and no repeat is recorded. That is the whole
// difference from the case below, and it is why the distinction cannot be drawn
// from the accumulated entries alone.
func TestDerivedNameRule_OneContributorRestatingItselfAcrossCrossingsIsSilent(t *testing.T) {
	t.Parallel()

	var set derivedKeySet
	set.put("logical-ids", realDerivedLogicalID, declaredByComponent("alpha"))
	set.put("logical-ids", realDerivedLogicalID, declaredByComponent("alpha"))
	if got := runDerivedNameRule(t, &set); len(got) != 0 {
		t.Fatalf("one contributor restating a name across crossings must not object: %v", got)
	}
}

// TestDerivedNameRule_WhereTheOutputMeets is the grid the narrowing lives on, and
// it is a grid rather than two lists: what the group's provider does with the
// group's output and where each contributor's own output landed are two axes, and
// a defect can sit in one cell while both margins read correctly.
//
// Every row declares ONE name in ONE space, by two contributors, and differs only
// in the two axes. The count is the whole assertion: two objections is the
// collision reported once per contributor, zero is the rule staying silent.
//
// 🔴 The rows that matter most are the ones that must still object. A predicate
// keyed on placement alone would silence every row here, because a provider that
// folds nothing places every contributor's output standalone — that is a removal
// rather than a narrowing, and these rows are what separates the two.
func TestDerivedNameRule_WhereTheOutputMeets(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		assembly string
		second   derivedKeyContributor
		want     int
	}{
		{
			// The subject of the narrowing: the folded contributor's name stands
			// inside the assembled document and the standalone one's stands in
			// files of its own, so neither can displace the other and no rename
			// would change that.
			name:     "folding group, one folded and one standalone contributor",
			assembly: wasm.BoundaryAssemblyMerge,
			second:   placedStandalone(declaredByAttachment("alpha", "aux")),
			want:     0,
		},
		{
			// The negative direction, from the same shape: two contributors whose
			// output really is folded into one document still collide.
			name:     "folding group, two folded contributors",
			assembly: wasm.BoundaryAssemblyMerge,
			second:   declaredByComponent("beta"),
			want:     2,
		},
		{
			// Two standalone contributors on a folding group meet nowhere
			// either: each stands in files of its own, so neither is inside the
			// assembled document and neither is inside the other's output.
			name:     "folding group, two standalone contributors",
			assembly: wasm.BoundaryAssemblyMerge,
			second:   placedStandalone(declaredByAttachment("beta", "aux")),
			want:     0,
		},
		{
			// A provider declaring no assembly folds nothing, so it has no
			// document for a placement to be inside or outside of and every
			// contributor is compared against every other. A resource identity
			// scoped wider than one file collides across two files.
			name:     "no assembly claim, one folded and one standalone contributor",
			assembly: "",
			second:   placedStandalone(declaredByAttachment("alpha", "aux")),
			want:     2,
		},
		{
			name:     "flat-output group, one folded and one standalone contributor",
			assembly: wasm.BoundaryAssemblyNone,
			second:   placedStandalone(declaredByAttachment("alpha", "aux")),
			want:     2,
		},
		{
			// Nesting combines a group without folding it: each node keeps a
			// document of its own under its own position. A narrowing keyed on
			// "does this group have an assembler" rather than on "does it fold"
			// would silence this row.
			name:     "nesting group, one folded and one standalone contributor",
			assembly: wasm.BoundaryAssemblyNest,
			second:   placedStandalone(declaredByAttachment("alpha", "aux")),
			want:     2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var set derivedKeySet
			set.put("logical-ids", realDerivedLogicalID, declaredByComponent("alpha"))
			set.put("logical-ids", realDerivedLogicalID, tc.second)
			got := runDerivedNameRuleUnder(t, &set, tc.assembly)
			if len(got) != tc.want {
				t.Fatalf("expected %d objection(s), got %d: %v", tc.want, len(got), got)
			}
		})
	}
}

// TestDerivedNameRule_OneAttachmentOnTwoHostsIsSilentOnAFoldingGroup is the shape
// an author actually writes and the one the narrowing exists for: one feature
// attached to two components of the same type, each host rendering the same
// attachment. Both attachments derive one name, and on a folding provider each
// one's output is written under its own host's position — so the name stands
// twice in two file sets that never meet, and no rename by the author would
// change that.
//
// It is stated apart from the grid above because the grid varies one axis at a
// time and this is the composed shape: two standalone contributors on a folding
// group, which is neither margin.
func TestDerivedNameRule_OneAttachmentOnTwoHostsIsSilentOnAFoldingGroup(t *testing.T) {
	t.Parallel()

	var set derivedKeySet
	set.put("logical-ids", realDerivedLogicalID, placedStandalone(declaredByAttachment("alpha", "aux")))
	set.put("logical-ids", realDerivedLogicalID, placedStandalone(declaredByAttachment("beta", "aux")))
	if got := runDerivedNameRuleUnder(t, &set, wasm.BoundaryAssemblyMerge); len(got) != 0 {
		t.Fatalf("one attachment on two hosts writes two file sets that never meet: %v", got)
	}

	// The same two contributors on a provider that folds nothing DO collide: the
	// name's uniqueness is then scoped to whatever reads the group's files as a
	// set, and two files are exactly where such a name meets itself. Written into
	// the same case because the contrast is the whole point — a narrowing that
	// silenced this would be a removal.
	if got := runDerivedNameRuleUnder(t, &set, wasm.BoundaryAssemblyNone); len(got) != 2 {
		t.Fatalf("expected one objection per contributor where nothing folds, got %d: %v", len(got), got)
	}
}

// TestDerivedNameRule_ARepeatInsideAStandaloneContributorStillObjects is the half
// of the condition the narrowing does not touch, at the axis it is most likely to
// be lost on. One declaration carrying a name twice is a fault inside one
// contributor's own derivation — the name is not unique in the contributor's own
// output — so it is answerable wherever that output landed, and a folding group
// that placed the contributor standalone answers it exactly as any other does.
func TestDerivedNameRule_ARepeatInsideAStandaloneContributorStillObjects(t *testing.T) {
	t.Parallel()

	declared := types.OrderedMap[[]string]{}
	declared.Set("logical-ids", []string{realDerivedLogicalID, realDerivedLogicalID})

	var acc types.OrderedMap[[]string]
	repeated := mergeDerivedKeys(&acc, declared)

	var set derivedKeySet
	set.putAll(acc, repeated, placedStandalone(declaredByAttachment("alpha", "aux")))

	got := runDerivedNameRuleUnder(t, &set, wasm.BoundaryAssemblyMerge)
	if len(got) != 1 {
		t.Fatalf("expected one objection naming the repeating contributor, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], `derived more than once by component "alpha", carrying attachment "aux"`) {
		t.Errorf("the objection does not name the repeating attachment\ngot: %s", got[0])
	}
}

// TestDerivedNameRule_AStandaloneContributorIsNotNamedForSomeoneElsesRepeat is
// the negative direction of the case above, and it is what keeps the repeat half
// from quietly restoring the broad comparison. A folded contributor that repeated
// a name inside one of its own declarations is named; a standalone contributor
// that merely declared the same name is not, because its name met nothing.
func TestDerivedNameRule_AStandaloneContributorIsNotNamedForSomeoneElsesRepeat(t *testing.T) {
	t.Parallel()

	var set derivedKeySet
	set.put("logical-ids", realDerivedLogicalID, declaredByComponent("alpha"))
	set.put("logical-ids", realDerivedLogicalID, placedStandalone(declaredByAttachment("beta", "aux")))
	set.repeats = append(set.repeats, derivedKeyDeclaration{
		space: "logical-ids", key: realDerivedLogicalID,
		contributor: declaredByComponent("alpha"),
	})

	got := runDerivedNameRuleUnder(t, &set, wasm.BoundaryAssemblyMerge)
	if len(got) != 1 {
		t.Fatalf("expected one objection naming only the repeating contributor, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], `derived more than once by component "alpha"`) {
		t.Errorf("the objection does not name the repeating contributor\ngot: %s", got[0])
	}
}

// TestDerivedKeyLedger_ContributorDispositionsCombineByConjunction pins the
// accumulation's answer for a producer that declared under both dispositions. One
// crossing whose output reached the assembler puts that producer's names inside
// the assembled document, so standing standalone has to hold for every crossing
// before it holds at all — and the producer stays ONE contributor, since the
// accumulation is keyed by the label alone.
func TestDerivedKeyLedger_ContributorDispositionsCombineByConjunction(t *testing.T) {
	t.Parallel()

	var ledger derivedKeyLedger
	ledger.declareDerived(placedStandalone(declaredByComponent("alpha")), "logical-ids", realDerivedLogicalID)
	ledger.declareDerived(declaredByComponent("alpha"), "logical-ids", realDerivedLogicalID)

	if n := len(ledger.entries); n != 1 {
		t.Fatalf("one producer opened %d ledger entries, want 1", n)
	}
	if ledger.entries[0].contributor.placedStandalone {
		t.Error("a producer that reached the assembler on one crossing still reads as standalone")
	}

	var set derivedKeySet
	ledger.into(&set)
	set.put("logical-ids", realDerivedLogicalID, declaredByComponent("beta"))
	if got := runDerivedNameRuleUnder(t, &set, wasm.BoundaryAssemblyMerge); len(got) != 2 {
		t.Fatalf("expected one objection per contributor, got %d: %v", len(got), got)
	}
}

// TestDerivedNameRule_EmptyIsSilent confirms the inert path: a group whose
// contributors declared nothing raises nothing, so the rule costs a contributor
// that declares nothing exactly nothing.
func TestDerivedNameRule_EmptyIsSilent(t *testing.T) {
	t.Parallel()

	var set derivedKeySet
	if got := runDerivedNameRule(t, &set); len(got) != 0 {
		t.Fatalf("a group with no declarations must raise nothing: %v", got)
	}
}

// TestMergeDerivedKeys_BothDirectionsWithinOneDeclaration is the near-miss pair
// at the site that owns the distinction. One declaration carrying a name twice
// inside one space is reported; the same declaration with one character of the
// second name changed is not — so the recognition is string equality within a
// space and nothing else.
func TestMergeDerivedKeys_BothDirectionsWithinOneDeclaration(t *testing.T) {
	t.Parallel()

	t.Run("the same name twice in one space is reported", func(t *testing.T) {
		t.Parallel()
		declared := types.OrderedMap[[]string]{}
		declared.Set("logical-ids", []string{realDerivedLogicalID, realDerivedLogicalID})

		var acc types.OrderedMap[[]string]
		repeated := mergeDerivedKeys(&acc, declared)

		want := []derivedKeyRepeat{{space: "logical-ids", key: realDerivedLogicalID}}
		if !reflect.DeepEqual(repeated, want) {
			t.Errorf("repeats = %v, want %v", repeated, want)
		}
	})

	t.Run("one character changed is not reported", func(t *testing.T) {
		t.Parallel()
		declared := types.OrderedMap[[]string]{}
		declared.Set("logical-ids", []string{realDerivedLogicalID, realDerivedLogicalIDNearMiss})

		var acc types.OrderedMap[[]string]
		if repeated := mergeDerivedKeys(&acc, declared); len(repeated) != 0 {
			t.Errorf("a one-character difference must not be reported as a repeat: %v", repeated)
		}
	})

	t.Run("the same name in two different spaces is not reported", func(t *testing.T) {
		t.Parallel()
		declared := types.OrderedMap[[]string]{}
		declared.Set("logical-ids", []string{realDerivedLogicalID})
		declared.Set("export-names", []string{realDerivedLogicalID})

		var acc types.OrderedMap[[]string]
		if repeated := mergeDerivedKeys(&acc, declared); len(repeated) != 0 {
			t.Errorf("one name in two spaces must not be reported as a repeat: %v", repeated)
		}
	})

	t.Run("a name declared three times is one repeat", func(t *testing.T) {
		t.Parallel()
		declared := types.OrderedMap[[]string]{}
		declared.Set("logical-ids", []string{realDerivedLogicalID, realDerivedLogicalID, realDerivedLogicalID})

		var acc types.OrderedMap[[]string]
		if repeated := mergeDerivedKeys(&acc, declared); len(repeated) != 1 {
			t.Errorf("a name declared three times must report one repeat, got %v", repeated)
		}
	})
}

// TestMergeDerivedKeys_RepeatReportIsAFunctionOfThePayload pins the ordering the
// diagnostic ultimately rests on. The declaration is walked in the guest's own
// order and each space's names in the order they were declared, so the same
// payload reports the same repeats in the same sequence on every run.
func TestMergeDerivedKeys_RepeatReportIsAFunctionOfThePayload(t *testing.T) {
	t.Parallel()

	declared := types.OrderedMap[[]string]{}
	declared.Set("logical-ids", []string{realDerivedLogicalID, "aTopic", realDerivedLogicalID})
	declared.Set("export-names", []string{realDerivedExportName, realDerivedExportName})

	want := []derivedKeyRepeat{
		{space: "logical-ids", key: realDerivedLogicalID},
		{space: "export-names", key: realDerivedExportName},
	}
	for i := 0; i < 40; i++ {
		var acc types.OrderedMap[[]string]
		if got := mergeDerivedKeys(&acc, declared); !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d: repeats = %v, want %v", i, got, want)
		}
	}
}

// TestDerivedNameRule_RepeatWithinOneDeclarationObjects carries the repeat
// through the accumulator to the objection. One contributor is named, and the
// caption says it derived the name more than once — which is what tells the
// author the defect is inside that one contributor's own derivation rather than a
// meeting between two of them.
func TestDerivedNameRule_RepeatWithinOneDeclarationObjects(t *testing.T) {
	t.Parallel()

	declared := types.OrderedMap[[]string]{}
	declared.Set("logical-ids", []string{realDerivedLogicalID, realDerivedLogicalID})

	var acc types.OrderedMap[[]string]
	repeated := mergeDerivedKeys(&acc, declared)

	var set derivedKeySet
	set.putAll(acc, repeated, declaredByComponent("alpha"))

	got := runDerivedNameRule(t, &set)
	if len(got) != 1 {
		t.Fatalf("expected one objection naming the one contributor, got %d: %v", len(got), got)
	}
	for _, fragment := range []string{
		`"paymentsapiQueue"`,
		`key space "logical-ids"`,
		`derived more than once by component "alpha"`,
	} {
		if !strings.Contains(got[0], fragment) {
			t.Errorf("objection missing %q\ngot: %s", fragment, got[0])
		}
	}
}

// TestDerivedNameRule_TheSameDeclarationWithoutTheRepeatIsSilent is the other
// direction, and it is the half that keeps the rule from being satisfied by one
// that refuses every declaration: the same contributor declaring two names that
// differ by one character in one space renders.
func TestDerivedNameRule_TheSameDeclarationWithoutTheRepeatIsSilent(t *testing.T) {
	t.Parallel()

	declared := types.OrderedMap[[]string]{}
	declared.Set("logical-ids", []string{realDerivedLogicalID, realDerivedLogicalIDNearMiss})

	var acc types.OrderedMap[[]string]
	repeated := mergeDerivedKeys(&acc, declared)

	var set derivedKeySet
	set.putAll(acc, repeated, declaredByComponent("alpha"))
	if got := runDerivedNameRule(t, &set); len(got) != 0 {
		t.Fatalf("one declaration carrying two distinct names must not object: %v", got)
	}
}

// TestDerivedNameRule_ARepeatAndACollisionOnOneNameNameBothContributors covers
// the overlap of the two halves. A name both repeated inside one contributor's
// declaration and derived by a second contributor is one defect in one space, so
// both contributors are named once each and only the repeating one is captioned
// as having derived it more than once.
func TestDerivedNameRule_ARepeatAndACollisionOnOneNameNameBothContributors(t *testing.T) {
	t.Parallel()

	var set derivedKeySet
	set.put("logical-ids", realDerivedLogicalID, declaredByComponent("alpha"))
	set.put("logical-ids", realDerivedLogicalID, declaredByComponent("alpha"))
	set.put("logical-ids", realDerivedLogicalID, declaredByComponent("beta"))
	set.repeats = append(set.repeats, derivedKeyDeclaration{
		space: "logical-ids", key: realDerivedLogicalID,
		contributor: declaredByComponent("alpha"),
	})

	got := runDerivedNameRule(t, &set)
	if len(got) != 2 {
		t.Fatalf("expected one objection per contributor, got %d: %v", len(got), got)
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		`derived more than once by component "alpha"`,
		`derived by component "beta"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the objections do not carry %q\ngot: %v", want, got)
		}
	}
	if strings.Count(joined, `derived by component "alpha"`) != 0 {
		t.Errorf("the repeating contributor must be captioned as repeating, got: %v", got)
	}
}

// TestDerivedNameRule_ARepeatWhoseDeclarationNeverAccumulatedIsStillAnswered
// pins the union the retention reads rather than a lookup into the declarations.
// Both are recorded through one entry point, so the case does not arise from the
// production path; reading the union rather than assuming it keeps the answer
// from depending on that.
func TestDerivedNameRule_ARepeatWhoseDeclarationNeverAccumulatedIsStillAnswered(t *testing.T) {
	t.Parallel()

	var set derivedKeySet
	set.repeats = append(set.repeats, derivedKeyDeclaration{
		space: "logical-ids", key: realDerivedLogicalID,
		contributor: declaredByComponent("alpha"),
	})

	got := runDerivedNameRule(t, &set)
	if len(got) != 1 {
		t.Fatalf("expected one objection, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], `derived more than once by component "alpha"`) {
		t.Errorf("objection does not name the repeating contributor\ngot: %s", got[0])
	}
}

// TestMergeDerivedKeys_AppendsAcrossCrossings pins the merge rather than a
// replace. A node's crossings each return their own declaration, and a replace
// would keep only the last one — dropping names silently, which is the failure
// this whole check exists to refuse.
func TestMergeDerivedKeys_AppendsAcrossCrossings(t *testing.T) {
	t.Parallel()

	first := types.OrderedMap[[]string]{}
	first.Set("logical-ids", []string{"aQueue"})
	second := types.OrderedMap[[]string]{}
	second.Set("logical-ids", []string{"aTopic"})
	second.Set("export-names", []string{realDerivedExportName})

	var acc types.OrderedMap[[]string]
	mergeDerivedKeys(&acc, first)
	mergeDerivedKeys(&acc, second)

	got, ok := acc.Get("logical-ids")
	if !ok {
		t.Fatal("logical-ids space lost across the merge")
	}
	if want := []string{"aQueue", "aTopic"}; !reflect.DeepEqual(got, want) {
		t.Errorf("logical-ids = %v, want %v", got, want)
	}
	if _, ok := acc.Get("export-names"); !ok {
		t.Error("a space introduced by a later crossing was dropped")
	}
	// The accumulator preserves the order the spaces were declared in, so the
	// recorded contents are a function of the input.
	if want := []string{"logical-ids", "export-names"}; !reflect.DeepEqual(acc.Keys(), want) {
		t.Errorf("space order = %v, want %v", acc.Keys(), want)
	}
}

// TestMergeDerivedKeysDoesNotAliasItsInput keeps a merged space from sharing a
// backing array with a caller's slice, which would let a later append into one
// silently rewrite the other.
func TestMergeDerivedKeysDoesNotAliasItsInput(t *testing.T) {
	t.Parallel()

	source := []string{"aQueue", "aTopic"}
	declared := types.OrderedMap[[]string]{}
	declared.Set("logical-ids", source)

	var acc types.OrderedMap[[]string]
	mergeDerivedKeys(&acc, declared)
	source[0] = "mutated"

	got, _ := acc.Get("logical-ids")
	if got[0] != "aQueue" {
		t.Errorf("the merged space aliases the caller's slice: got %v", got)
	}
}

// producerCoordinates is the corpus the label's own property is asserted over:
// coordinate triples chosen for the edges a joined label would fold at rather
// than for how a real one reads.
//
// The first two rows are the fold itself, written out because they are the whole
// argument: a feature named `a@b` attached at `c` and one named `a` attached at
// `b@c` are two attachments an author can tell apart, and any label that runs the
// two coordinates together with a separator either of them may contain reaches
// one string for both. The rest carry the separators a boundary path actually
// uses, a coordinate that is empty, one carrying the quote a message renders
// with, and one that is not valid UTF-8.
func producerCoordinates() []derivedKeyProducer {
	return []derivedKeyProducer{
		{kind: producerKindFeature, name: "a@b", scope: "c"},
		{kind: producerKindFeature, name: "a", scope: "b@c"},
		{kind: producerKindFeature, name: "a", scope: "b"},
		{kind: producerKindFeature, name: "a-b", scope: "c"},
		{kind: producerKindFeature, name: "a", scope: "b-c"},
		{kind: producerKindFeature, name: "a/b", scope: "c"},
		{kind: producerKindFeature, name: "a", scope: "b/c"},
		{kind: producerKindFeature, name: "bridge", scope: "eu/prod"},
		{kind: producerKindFeature, name: "bridge", scope: "eu/prod/west"},
		{kind: producerKindFeature, name: "bridge", scope: ""},
		{kind: producerKindComponent, name: "a", scope: "b"},
		{kind: producerKindComponent, name: "alpha", scope: ""},
		{kind: producerKindComponent, name: "alpha", scope: "cache a"},
		{kind: producerKindComponent, name: "alpha", scope: "cache-a"},
		{kind: producerKindComponent, name: `say "hi"`, scope: ""},
		{kind: producerKindComponent, name: "", scope: `say "hi"`},
		{kind: producerKindComponent, name: "\xff\xfe", scope: "b"},
		{kind: producerKindComponent, name: "\xff", scope: "\xfeb"},
	}
}

// TestDerivedKeyProducer_ReadsBackIntoTheCoordinatesItWasBuiltFrom is the round
// trip that makes the label's structure a property rather than an intention.
//
// The reader is direct field access and shares no code with the composer: a
// reader that called the composer, or any decoder written beside it, would agree
// with it whatever it did. What is being established is that every coordinate
// survives composition intact and separable — which a label run together into one
// string cannot provide, since no reader can put back a boundary the join
// destroyed.
func TestDerivedKeyProducer_ReadsBackIntoTheCoordinatesItWasBuiltFrom(t *testing.T) {
	t.Parallel()

	for _, want := range producerCoordinates() {
		got := derivedKeyProducerFor(want.kind, want.name, want.scope)
		if got.kind != want.kind || got.name != want.name || got.scope != want.scope {
			t.Errorf("(%q, %q, %q) composed to (%q, %q, %q)",
				want.kind, want.name, want.scope, got.kind, got.name, got.scope)
		}
	}
}

// TestDerivedKeyProducer_TwoCoordinateTriplesReachTwoLabels is the half that can
// fail. Every distinct triple in the corpus reaches a distinct label, including
// the two that a separator join would fold — so the label is injective over its
// coordinates by construction, with no encoding and no comparison of strings.
func TestDerivedKeyProducer_TwoCoordinateTriplesReachTwoLabels(t *testing.T) {
	t.Parallel()

	corpus := producerCoordinates()
	for i := range corpus {
		for j := i + 1; j < len(corpus); j++ {
			a := derivedKeyProducerFor(corpus[i].kind, corpus[i].name, corpus[i].scope)
			b := derivedKeyProducerFor(corpus[j].kind, corpus[j].name, corpus[j].scope)
			if a == b {
				t.Errorf("(%q, %q, %q) and (%q, %q, %q) reach one label",
					corpus[i].kind, corpus[i].name, corpus[i].scope,
					corpus[j].kind, corpus[j].name, corpus[j].scope)
			}
		}
	}
}

// TestDerivedNameRule_TheFoldingPairAreTwoContributors carries the property above
// through to what it buys. The pair a joined label folds declares one name, and
// the rule reports two contributors — which is what it would stop doing the day
// the label became one string.
func TestDerivedNameRule_TheFoldingPairAreTwoContributors(t *testing.T) {
	t.Parallel()

	var set derivedKeySet
	set.put("logical-ids", realDerivedLogicalID, declaredByBoundaryFeature("a@b", "c"))
	set.put("logical-ids", realDerivedLogicalID, declaredByBoundaryFeature("a", "b@c"))

	got := runDerivedNameRule(t, &set)
	if len(got) != 2 {
		t.Fatalf("expected one objection per contributor, got %d: %v", len(got), got)
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		`derived by feature "a@b", attached at boundary "c"`,
		`derived by feature "a", attached at boundary "b@c"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the objections do not carry %q\ngot: %v", want, got)
		}
	}
}

// TestDerivedNameRule_OneNameAtTwoBoundariesIsTwoContributors is the coordinate
// the boundary-scope label had been dropping, at the unit that owns the
// comparison. One instance name written at two boundaries is two attachments the
// author wrote, so one name declared by both is two declarers.
func TestDerivedNameRule_OneNameAtTwoBoundariesIsTwoContributors(t *testing.T) {
	t.Parallel()

	var set derivedKeySet
	set.put("logical-ids", realDerivedLogicalID, declaredByBoundaryFeature("bridge", "eu"))
	set.put("logical-ids", realDerivedLogicalID, declaredByBoundaryFeature("bridge", "us"))

	if got := runDerivedNameRule(t, &set); len(got) != 2 {
		t.Fatalf("expected one objection per boundary, got %d: %v", len(got), got)
	}
}

// TestDerivedNameRule_TwoAttachmentsOnOneComponentAreTwoContributors is the
// mirror coordinate, at the same unit: two attachment keys on one component are
// two contributors, where the component's own name alone made them one.
func TestDerivedNameRule_TwoAttachmentsOnOneComponentAreTwoContributors(t *testing.T) {
	t.Parallel()

	var set derivedKeySet
	set.put("logical-ids", realDerivedLogicalID, declaredByAttachment("alpha", "cache a"))
	set.put("logical-ids", realDerivedLogicalID, declaredByAttachment("alpha", "cache-a"))

	got := runDerivedNameRule(t, &set)
	if len(got) != 2 {
		t.Fatalf("expected one objection per attachment, got %d: %v", len(got), got)
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		`derived by component "alpha", carrying attachment "cache a"`,
		`derived by component "alpha", carrying attachment "cache-a"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the objections do not carry %q\ngot: %v", want, got)
		}
	}
}

// TestDerivedNameRule_AComponentsOwnRenderCarriesNoFurtherCoordinate is the
// negative direction of the coordinate's presentation. A component's own render
// is identified by its name alone, so nothing further is presented — a caption
// standing in front of an empty value would read as a coordinate the author
// failed to supply rather than one that does not apply.
func TestDerivedNameRule_AComponentsOwnRenderCarriesNoFurtherCoordinate(t *testing.T) {
	t.Parallel()

	var set derivedKeySet
	set.put("logical-ids", realDerivedLogicalID, declaredByComponent("alpha"))
	set.put("logical-ids", realDerivedLogicalID, declaredByComponent("beta"))

	got := runDerivedNameRule(t, &set)
	if len(got) != 2 {
		t.Fatalf("expected one objection per contributor, got %d: %v", len(got), got)
	}
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, `carrying attachment ""`) {
		t.Errorf("a contributor with no further coordinate presented an empty one\ngot: %v", got)
	}
	if !strings.Contains(joined, `derived by component "alpha", provider`) {
		t.Errorf("the objection does not read as a component's own render\ngot: %v", got)
	}
}

// TestDerivedKeyLedger_KeepsEachProducersDeclarationsApart is the accumulator's
// own case. One node's several producers declare into one ledger, and what
// reaches the group is one contributor per producer — not one contributor for the
// node. Two attachments declaring one name are therefore two entries there, which
// is what the rule above needs in order to see them at all.
func TestDerivedKeyLedger_KeepsEachProducersDeclarationsApart(t *testing.T) {
	t.Parallel()

	var ledger derivedKeyLedger
	ledger.declareDerived(declaredByAttachment("alpha", "cache a"), componentIDKeySpace, "cache-a")
	ledger.declareDerived(declaredByAttachment("alpha", "cache-a"), componentIDKeySpace, "cache-a")

	var set derivedKeySet
	ledger.into(&set)
	if got := runDerivedNameRule(t, &set); len(got) != 2 {
		t.Fatalf("expected one objection per attachment, got %d: %v", len(got), got)
	}
}

// TestDerivedKeyLedger_ANodeAndItsOwnAttachmentAreTwoContributors states the
// consequence of identifying a contributor by every coordinate its derivation
// reads, and it is worth asserting because it widens what halts.
//
// A component's own render and the render of an attachment it carries are two
// derivations over two different authored inputs, and their output merges into
// one assembled output — so one name reached by both is one of them displacing
// the other, exactly as it is between two components. The finer identification is
// not optional either: attachments on one component can only be told apart from
// each other if the attachment is the unit, and once it is, the component's own
// render is a third thing rather than a member of one of them.
//
// The bound the ledger still keeps is the one below: several crossings of the
// SAME producer remain one contributor.
func TestDerivedKeyLedger_ANodeAndItsOwnAttachmentAreTwoContributors(t *testing.T) {
	t.Parallel()

	var ledger derivedKeyLedger
	ledger.declareDerived(declaredByComponent("alpha"), "logical-ids", realDerivedLogicalID)
	ledger.declareDerived(declaredByAttachment("alpha", "cache"), "logical-ids", realDerivedLogicalID)

	var set derivedKeySet
	ledger.into(&set)
	got := runDerivedNameRule(t, &set)
	if len(got) != 2 {
		t.Fatalf("expected one objection per contributor, got %d: %v", len(got), got)
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		`derived by component "alpha", provider`,
		`derived by component "alpha", carrying attachment "cache"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the objections do not carry %q\ngot: %v", want, got)
		}
	}
}

// TestDerivedKeyLedger_OneProducerRestatingItselfAcrossCrossingsIsSilent is the
// bound the case above must not break. One producer renders across several
// crossings and may restate a name at each; the ledger merges those into that
// producer's own accumulation, so the group still sees one contributor and the
// legitimate restatement stays legitimate.
func TestDerivedKeyLedger_OneProducerRestatingItselfAcrossCrossingsIsSilent(t *testing.T) {
	t.Parallel()

	declared := types.OrderedMap[[]string]{}
	declared.Set("logical-ids", []string{realDerivedLogicalID})

	var ledger derivedKeyLedger
	producer := declaredByAttachment("alpha", "cache a")
	ledger.declare(producer, declared)
	ledger.declare(producer, declared)

	var set derivedKeySet
	ledger.into(&set)
	if got := runDerivedNameRule(t, &set); len(got) != 0 {
		t.Fatalf("one producer restating a name across crossings must not object: %v", got)
	}
}

// TestDerivedKeyLedger_ReportsARepeatInsideOneOfAProducersDeclarations keeps the
// within-one-declaration half alive through the ledger. The distinction is drawn
// while a single declaration is whole, which is now inside one producer's
// accumulation rather than inside the node's — so a producer whose own single
// declaration carried a name twice is still reported as having derived it more
// than once.
func TestDerivedKeyLedger_ReportsARepeatInsideOneOfAProducersDeclarations(t *testing.T) {
	t.Parallel()

	declared := types.OrderedMap[[]string]{}
	declared.Set("logical-ids", []string{realDerivedLogicalID, realDerivedLogicalID})

	var ledger derivedKeyLedger
	ledger.declare(declaredByAttachment("alpha", "cache a"), declared)

	var set derivedKeySet
	ledger.into(&set)
	got := runDerivedNameRule(t, &set)
	if len(got) != 1 {
		t.Fatalf("expected one objection naming the one producer, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], `derived more than once by component "alpha", carrying attachment "cache a"`) {
		t.Errorf("the objection does not name the repeating attachment\ngot: %s", got[0])
	}
}

// TestDerivedKeyLedger_DrainsInDeclarationOrder pins the accumulator's own
// contribution to a report that is a function of the input. The rule sorts what
// it presents, so this ordering reaches no message directly; what it protects is
// that nothing upstream of the sort depends on Go's map iteration.
func TestDerivedKeyLedger_DrainsInDeclarationOrder(t *testing.T) {
	t.Parallel()

	want := []derivedKeyContributor{
		declaredByComponent("alpha"),
		declaredByAttachment("alpha", "cache a"),
		declaredByAttachment("alpha", "cache-a"),
	}
	for run := 0; run < 40; run++ {
		var ledger derivedKeyLedger
		for _, p := range want {
			ledger.declareDerived(p, componentIDKeySpace, "value")
		}
		// A second declaration by a producer already in the ledger joins that
		// producer's own accumulation rather than opening a new entry.
		ledger.declareDerived(want[0], componentIDKeySpace, "value")

		var set derivedKeySet
		ledger.into(&set)
		// One entry per name per producer reaches the set, so the order under test
		// is the order the producers first appear in.
		seen := map[derivedKeyContributor]bool{}
		got := make([]derivedKeyContributor, 0, len(want))
		for _, e := range set.entries {
			if seen[e.contributor] {
				continue
			}
			seen[e.contributor] = true
			got = append(got, e.contributor)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d: drained %v, want %v", run, got, want)
		}
	}
}

// TestDerivedKeySetIsTheOnlyDeclarationPath keeps the guard reachable from every
// contributor. The set carries no map, so recording a declaration cannot be
// spelled as an index expression that skips the producer label, and every group
// the orchestrator builds is checked — so a contributor added later cannot hand
// the host declarations the check never sees.
func TestDerivedKeySetIsTheOnlyDeclarationPath(t *testing.T) {
	t.Parallel()

	setType := reflect.TypeOf(derivedKeySet{})
	for i := 0; i < setType.NumField(); i++ {
		if setType.Field(i).Type.Kind() == reflect.Map {
			t.Errorf("derivedKeySet field %q is a map; a declaration must be recorded through put, not by indexing",
				setType.Field(i).Name)
		}
	}
}
