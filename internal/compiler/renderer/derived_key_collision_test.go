// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
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

// derivedNamesView presents one group's derived-name declarations and nothing
// else.
//
// The rule under test reads exactly one fact, so nothing behind the embedded
// interface is ever asked for. It carries no value there deliberately: a read
// panics rather than answering a zero value, so a rule that later starts reading
// a second fact fails loudly instead of being quietly handed nothing.
type derivedNamesView struct {
	constraint.DispatchedView
	declared []constraint.DerivedNameDeclaration
}

func (v derivedNamesView) AssemblyGroupDerivedNames(
	_ string, _ string,
) []constraint.DerivedNameDeclaration {
	return v.declared
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

	axes := derivedNamesAxes{path: "boundaries/default", provider: "collide"}
	var view constraint.DispatchedView = derivedNamesView{declared: set.declarations()}
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
		{space: "logical-ids", key: realDerivedLogicalID, producerKind: "component", producer: "one"},
		{space: "export-names", key: realDerivedExportName, producerKind: "component", producer: "two"},
		{space: "logical-ids", key: realDerivedLogicalID, producerKind: "component", producer: "three"},
		{space: "export-names", key: realDerivedExportName, producerKind: "component", producer: "four"},
		{space: "export-names", key: realDerivedExportName, producerKind: "component", producer: "five"},
	}

	var want []string
	for _, order := range permutations(len(declarations)) {
		var set derivedKeySet
		for _, i := range order {
			d := declarations[i]
			set.put(d.space, d.key, d.producerKind, d.producer)
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
		set.put("logical-ids", realDerivedLogicalID, "component", "alpha")
		set.put("logical-ids", realDerivedLogicalID, "component", "beta")
		if got := runDerivedNameRule(t, &set); len(got) != 2 {
			t.Fatalf("expected one objection per contributor, got %d: %v", len(got), got)
		}
	})

	t.Run("one character changed stays silent", func(t *testing.T) {
		t.Parallel()
		var set derivedKeySet
		set.put("logical-ids", realDerivedLogicalID, "component", "alpha")
		set.put("logical-ids", realDerivedLogicalIDNearMiss, "component", "beta")
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
	set.put("logical-ids", realDerivedLogicalID, "component", "alpha")
	set.put("export-names", realDerivedLogicalID, "component", "beta")
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
	set.put("logical-ids", realDerivedLogicalID, "component", "alpha")
	set.put("logical-ids", realDerivedLogicalID, "feature", "logging")
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
	set.put("logical-ids", realDerivedLogicalID, "component", "alpha")
	set.put("logical-ids", realDerivedLogicalID, "component", "alpha")
	if got := runDerivedNameRule(t, &set); len(got) != 0 {
		t.Fatalf("one contributor restating a name across crossings must not object: %v", got)
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
	set.putAll(acc, repeated, "component", "alpha")

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
	set.putAll(acc, repeated, "component", "alpha")
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
	set.put("logical-ids", realDerivedLogicalID, "component", "alpha")
	set.put("logical-ids", realDerivedLogicalID, "component", "alpha")
	set.put("logical-ids", realDerivedLogicalID, "component", "beta")
	set.repeats = append(set.repeats, derivedKeyDeclaration{
		space: "logical-ids", key: realDerivedLogicalID,
		producerKind: "component", producer: "alpha",
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
		producerKind: "component", producer: "alpha",
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
