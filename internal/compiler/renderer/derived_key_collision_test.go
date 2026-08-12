// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"reflect"
	"strings"
	"testing"

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

// TestDerivedKeySet_DiagnosticInvariantUnderDeclarationOrder proves the property
// at the unit that owns it: the same declarations, offered in any order, produce
// the same diagnostic. The maps and groups feeding the accumulator upstream are
// walked in orders that are not fixed by the input, so any dependence on
// declaration order here would surface as a build that fails with a different
// message on different runs of identical input.
func TestDerivedKeySet_DiagnosticInvariantUnderDeclarationOrder(t *testing.T) {
	t.Parallel()

	declarations := []derivedKeyDeclaration{
		{space: "logical-ids", key: realDerivedLogicalID, producer: `component "one"`},
		{space: "export-names", key: realDerivedExportName, producer: `component "two"`},
		{space: "logical-ids", key: realDerivedLogicalID, producer: `component "three"`},
		{space: "export-names", key: realDerivedExportName, producer: `component "four"`},
		{space: "export-names", key: realDerivedExportName, producer: `component "five"`},
	}

	var want string
	for _, order := range permutations(len(declarations)) {
		var set derivedKeySet
		for _, i := range order {
			d := declarations[i]
			set.put(d.space, d.key, d.producer)
		}
		err := set.resolve("boundaries/default", "collide")
		if err == nil {
			t.Fatalf("order %v: expected a collision", order)
		}
		if !errors.Is(err, ErrDerivedKeyCollision()) {
			t.Fatalf("order %v: expected ErrDerivedKeyCollision, got %v", order, err)
		}
		if want == "" {
			want = err.Error()
			continue
		}
		if err.Error() != want {
			t.Fatalf("diagnostic depends on declaration order\nwant: %s\ngot (order %v): %s", want, order, err.Error())
		}
	}

	// Every colliding space and name is reported, with every contributor named,
	// and the spaces come out in space order rather than in the order the
	// accumulating walks happened to take.
	for _, fragment := range []string{
		`in key space "export-names" the name "aggrega-prod-api-orders-db-host" is derived by component "five", component "four" and component "two"`,
		`in key space "logical-ids" the name "paymentsapiQueue" is derived by component "one" and component "three"`,
		`assembly path "boundaries/default" provider "collide"`,
		"change what one of them derives so each name is derived once within its space",
	} {
		if !strings.Contains(want, fragment) {
			t.Errorf("diagnostic missing %q\ngot: %s", fragment, want)
		}
	}
	if strings.Index(want, "export-names") > strings.Index(want, "logical-ids") {
		t.Errorf("colliding spaces must be reported in space order, got: %s", want)
	}
}

// TestDerivedKeySet_BothDirections is the near-miss pair. The same two
// contributors declaring into the same space halt when the name is identical and
// do not halt when one character of it differs — so the guard is comparing the
// declared strings for equality and nothing else.
func TestDerivedKeySet_BothDirections(t *testing.T) {
	t.Parallel()

	t.Run("identical name halts", func(t *testing.T) {
		t.Parallel()
		var set derivedKeySet
		set.put("logical-ids", realDerivedLogicalID, `component "alpha"`)
		set.put("logical-ids", realDerivedLogicalID, `component "beta"`)
		err := set.resolve("boundaries/default", "collide")
		if err == nil {
			t.Fatal("expected a halt when two contributors derive one name")
		}
		if !errors.Is(err, ErrDerivedKeyCollision()) {
			t.Fatalf("expected ErrDerivedKeyCollision, got: %v", err)
		}
	})

	t.Run("one character changed does not halt", func(t *testing.T) {
		t.Parallel()
		var set derivedKeySet
		set.put("logical-ids", realDerivedLogicalID, `component "alpha"`)
		set.put("logical-ids", realDerivedLogicalIDNearMiss, `component "beta"`)
		if err := set.resolve("boundaries/default", "collide"); err != nil {
			t.Fatalf("a one-character difference must not halt: %v", err)
		}
	})
}

// TestDerivedKeySet_SameNameInDifferentSpacesDoesNotHalt pins the space as the
// scope of the comparison. Two contributors deriving one name into two different
// spaces have not collided — nothing in either output displaces the other — and a
// check that compared names across spaces would reject legitimate authoring.
func TestDerivedKeySet_SameNameInDifferentSpacesDoesNotHalt(t *testing.T) {
	t.Parallel()

	var set derivedKeySet
	set.put("logical-ids", realDerivedLogicalID, `component "alpha"`)
	set.put("export-names", realDerivedLogicalID, `component "beta"`)
	if err := set.resolve("boundaries/default", "collide"); err != nil {
		t.Fatalf("one name in two different spaces must not halt: %v", err)
	}
}

// TestDerivedKeySet_OneContributorRepeatingItselfDoesNotHalt covers the shape a
// single node produces legitimately. A node renders across more than one crossing
// — a source render, a workload synthesis, an artifacts placement — and the same
// name declared at two of them is one contributor describing one resource twice,
// not two contributors meeting on one name. Reporting it would name the same
// component on both sides of a collision with itself.
func TestDerivedKeySet_OneContributorRepeatingItselfDoesNotHalt(t *testing.T) {
	t.Parallel()

	var set derivedKeySet
	set.put("logical-ids", realDerivedLogicalID, `component "alpha"`)
	set.put("logical-ids", realDerivedLogicalID, `component "alpha"`)
	if err := set.resolve("boundaries/default", "collide"); err != nil {
		t.Fatalf("one contributor declaring a name twice must not halt: %v", err)
	}
}

// TestDerivedKeySet_EmptyResolves confirms the inert path: a group whose
// contributors declared nothing passes, so the check costs a guest that declares
// nothing exactly nothing.
func TestDerivedKeySet_EmptyResolves(t *testing.T) {
	t.Parallel()

	var set derivedKeySet
	if err := set.resolve("boundaries/default", "collide"); err != nil {
		t.Fatalf("a group with no declarations must resolve: %v", err)
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
