// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// ResolveFeatureSchemaChain gates every extends chain through the one extends
// walk, and so does every other entity path: the Component and Feature
// collectors call the same walk, which is what makes one repair reach all three.
// The tests below pin one guard each and fail if that guard stops reporting, so
// a guard cannot be dropped silently:
//
//   - TestResolveFeatureSchemaChain_CycleDetection — a chain that returns to an entity.
//   - TestResolveFeatureSchemaChain_MaxDepth — a chain longer than the depth bound.
//   - TestResolveFeatureSchemaChain_ParentNotFound — a parent the lookup cannot resolve.
//
// Each asserts the specific sentinel rather than merely "some error", so one
// guard cannot pass by another guard's failure.
//
// Two of them additionally assert the KIND the failure is attributed to. That
// kind is named once, at the entry point, instead of travelling on each chain
// entry — and nothing else reads it back, so a constant naming the wrong
// authoring surface would route every failure of this tier to that surface with
// no test able to notice.

func TestResolveFeatureSchemaChain_CycleDetection(t *testing.T) {
	entry := CatalogEntry{
		URI:     "a:1.0.0",
		Extends: "b:1.0.0",
	}

	// The lookup resolves BOTH links, which is what a catalog lookup does: the
	// entity a resolution starts from is in the catalog too. A lookup that
	// refused the chain's own root would make the closing link unresolvable, and
	// an unresolvable link is a missing parent rather than evidence of a cycle —
	// the walk compares the entity a link RESOLVES TO against the ones already in
	// the chain, so with nothing resolved there is nothing to compare.
	lookup := func(uri string) (CatalogEntry, bool) {
		switch uri {
		case "a:1.0.0":
			return entry, true
		case "b:1.0.0":
			return CatalogEntry{URI: "b:1.0.0", Extends: "a:1.0.0"}, true
		}
		return CatalogEntry{}, false
	}

	result := ResolveFeatureSchemaChain(entry, lookup)
	if len(result.Errors) == 0 {
		t.Fatal("expected cycle detection error")
	}
	if !errors.Is(result.Errors[0], ErrCyclicExtension()) {
		t.Errorf("expected ErrCyclicExtension, got %v", result.Errors[0])
	}
	// The message opens with the condition's registered sentence and carries
	// the occurrence detail after it, so both halves are asserted: a rendering
	// that dropped either would still satisfy the errors.Is check above.
	if !strings.HasPrefix(result.Errors[0].Error(), ErrCyclicExtension().Error()+" (") {
		t.Errorf("expected the diagnostic to open with the registered sentence; got: %s", result.Errors[0].Error())
	}
	// The edge named is the one that CLOSES the loop — b extends a — not the one
	// the resolution started from. Naming the wrong edge sends an author to the
	// wrong file, so the assertion is on the whole edge rather than on either end.
	if want := "b:1.0.0 extends a:1.0.0, which is already in the chain"; !strings.Contains(result.Errors[0].Error(), want) {
		t.Errorf("expected the diagnostic to name the closing edge %q; got: %s", want, result.Errors[0].Error())
	}
	// The entity reference the message renders opens with the kind the failure is
	// attributed to, and that kind is the constant this tier names for itself. It
	// is asserted because no chain entry carries a kind any more: a constant
	// naming another authoring surface would compile, resolve to a real domain and
	// misattribute every failure this tier raises.
	if want := string(envelope.KindFeature) + " a:1.0.0"; !strings.Contains(result.Errors[0].Error(), want) {
		t.Errorf("expected the failure to be attributed to %q; got: %s", want, result.Errors[0].Error())
	}
}

// TestResolveFeatureSchemaChain_MaxDepth pins the depth bound with a chain of
// distinct URIs longer than maxInheritanceDepth. The URIs are distinct on
// purpose: a repeated one would make the chain cyclic, and a cycle is reported
// as a cycle at any length, so the depth bound would go unexercised while the
// test passed.
func TestResolveFeatureSchemaChain_MaxDepth(t *testing.T) {
	const chainLen = maxInheritanceDepth + 5

	uri := func(i int) string { return fmt.Sprintf("e%02d:1.0.0", i) }

	lookup := func(want string) (CatalogEntry, bool) {
		for i := range chainLen {
			if uri(i) != want {
				continue
			}
			e := CatalogEntry{URI: uri(i)}
			if i < chainLen-1 {
				e.Extends = uri(i + 1)
			}
			return e, true
		}
		return CatalogEntry{}, false
	}

	entry, ok := lookup(uri(0))
	if !ok {
		t.Fatal("test setup: chain root must resolve")
	}

	result := ResolveFeatureSchemaChain(entry, lookup)
	if len(result.Errors) == 0 {
		t.Fatal("expected the depth bound to halt a chain longer than the maximum")
	}
	if !errors.Is(result.Errors[0], ErrExtendsChainTooDeep()) {
		t.Errorf("expected ErrExtendsChainTooDeep, got %v", result.Errors[0])
	}
	// Depth carries an identity of its own, so a reader can tell "too long" from
	// "parent not in the catalog". Asserting the second does not match is what
	// keeps the two distinguishable: one identity covering both would satisfy the
	// check above and still leave a consumer unable to say which it caught.
	if errors.Is(result.Errors[0], ErrInheritanceMerge()) {
		t.Error("the depth bound must not report under the parent-not-found identity")
	}
	if errors.Is(result.Errors[0], ErrCyclicExtension()) {
		t.Error("a chain of distinct URIs must not report a cycle; the depth bound is what should halt it")
	}
	if want := fmt.Sprintf("the bound is %d and this chain reaches %d", maxInheritanceDepth, chainLen); !strings.Contains(result.Errors[0].Error(), want) {
		t.Errorf("expected the diagnostic to name the bound and the length reached %q; got: %s", want, result.Errors[0].Error())
	}
}

// TestExtendsChainClassifiesCycleAndDepthAtEveryLength is the regression that
// pins the whole point of one walk: a chain that closes on itself reports a
// cycle at ANY length, and only a chain that ends cleanly can be too deep.
//
// The lengths are chosen around the depth bound on purpose. A walk that tested
// the bound while it walked classified a cycle shorter than the bound as a
// cycle and a cycle at or past it as a depth violation — telling the author to
// shorten a chain whose length was never the problem. Every row here is a cycle
// except the last, and the last is the only row that may report depth.
func TestExtendsChainClassifiesCycleAndDepthAtEveryLength(t *testing.T) {
	uri := func(i int) string { return fmt.Sprintf("e%02d", i) }

	// cyclic builds n entities where each extends the next and the last extends
	// the first, with every link carrying a version the entity's own name does
	// not — the spelling a catalog uses, and the one a walk keyed on the link
	// rather than on the resolved entity fails to match.
	cyclic := func(n int) (CatalogEntry, func(string) (CatalogEntry, bool)) {
		at := func(i int) CatalogEntry {
			return CatalogEntry{URI: uri(i), Extends: uri((i+1)%n) + ":1.0.0"}
		}
		lookup := func(want string) (CatalogEntry, bool) {
			for i := range n {
				if uri(i)+":1.0.0" == want || uri(i) == want {
					return at(i), true
				}
			}
			return CatalogEntry{}, false
		}
		return at(0), lookup
	}

	for _, n := range []int{2, 3, 7, 8, 9, 12, 25} {
		t.Run(fmt.Sprintf("cycle of %d", n), func(t *testing.T) {
			entry, lookup := cyclic(n)
			result := ResolveFeatureSchemaChain(entry, lookup)
			if len(result.Errors) == 0 {
				t.Fatalf("a %d-link cycle must halt", n)
			}
			if !errors.Is(result.Errors[0], ErrCyclicExtension()) {
				t.Errorf("a %d-link cycle must report a cycle; got: %v", n, result.Errors[0])
			}
			if errors.Is(result.Errors[0], ErrExtendsChainTooDeep()) {
				t.Errorf("a %d-link cycle must not report the depth bound; got: %v", n, result.Errors[0])
			}
			// The edge named is the one closing the loop: the LAST entity in the
			// chain extending the first. For a two-link cycle those coincide, which
			// is why the table starts at two and does not stop there.
			if want := fmt.Sprintf("%s extends %s:1.0.0, which is already in the chain", uri(n-1), uri(0)); !strings.Contains(result.Errors[0].Error(), want) {
				t.Errorf("expected the closing edge %q; got: %s", want, result.Errors[0].Error())
			}
		})
	}

	t.Run("acyclic chain past the bound", func(t *testing.T) {
		const n = maxInheritanceDepth + 2
		at := func(i int) CatalogEntry {
			e := CatalogEntry{URI: uri(i)}
			if i < n-1 {
				e.Extends = uri(i+1) + ":1.0.0"
			}
			return e
		}
		lookup := func(want string) (CatalogEntry, bool) {
			for i := range n {
				if uri(i)+":1.0.0" == want || uri(i) == want {
					return at(i), true
				}
			}
			return CatalogEntry{}, false
		}
		result := ResolveFeatureSchemaChain(at(0), lookup)
		if len(result.Errors) == 0 {
			t.Fatal("an acyclic chain past the bound must halt")
		}
		if !errors.Is(result.Errors[0], ErrExtendsChainTooDeep()) {
			t.Errorf("an acyclic chain past the bound must report depth; got: %v", result.Errors[0])
		}
		if errors.Is(result.Errors[0], ErrCyclicExtension()) {
			t.Errorf("an acyclic chain must not report a cycle; got: %v", result.Errors[0])
		}
	})

	t.Run("acyclic chain at the bound is accepted", func(t *testing.T) {
		const n = maxInheritanceDepth
		at := func(i int) CatalogEntry {
			e := CatalogEntry{URI: uri(i)}
			if i < n-1 {
				e.Extends = uri(i+1) + ":1.0.0"
			}
			return e
		}
		lookup := func(want string) (CatalogEntry, bool) {
			for i := range n {
				if uri(i)+":1.0.0" == want || uri(i) == want {
					return at(i), true
				}
			}
			return CatalogEntry{}, false
		}
		// The passing control. Without it every row above could be satisfied by a
		// walk that refused everything.
		if result := ResolveFeatureSchemaChain(at(0), lookup); len(result.Errors) != 0 {
			t.Errorf("a chain exactly at the bound must be accepted; got: %v", result.Errors[0])
		}
	})
}

func TestResolveFeatureSchemaChain_ParentNotFound(t *testing.T) {
	entry := CatalogEntry{
		URI:     "child:1.0.0",
		Extends: "nonexistent:1.0.0",
	}

	lookup := func(uri string) (CatalogEntry, bool) {
		return CatalogEntry{}, false
	}

	result := ResolveFeatureSchemaChain(entry, lookup)
	if len(result.Errors) == 0 {
		t.Fatal("expected error for missing parent")
	}
	if !errors.Is(result.Errors[0], ErrInheritanceMerge()) {
		t.Errorf("expected ErrInheritanceMerge, got %v", result.Errors[0])
	}
	// The second site the attributed kind is read back from, and a different
	// condition than the cycle test's: one constant serves all three faults, so a
	// wrong one would be caught here too rather than only on the cycle path.
	if want := string(envelope.KindFeature) + " child:1.0.0"; !strings.Contains(result.Errors[0].Error(), want) {
		t.Errorf("expected the failure to be attributed to %q; got: %s", want, result.Errors[0].Error())
	}
}

// TestResolveFeatureSchemaChain_NoExtends pins the walk's behaviour on an entity
// that extends nothing: no guard fires, and the lookup is never consulted —
// passing nil proves it, since calling it would panic.
func TestResolveFeatureSchemaChain_NoExtends(t *testing.T) {
	entry := CatalogEntry{
		URI: "standalone:1.0.0",
	}

	result := ResolveFeatureSchemaChain(entry, nil)
	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors[0].Error())
	}
	if result.MergedJSONSchema != nil {
		t.Errorf("an entry carrying no schema payload composes nothing, got %+v", result.MergedJSONSchema)
	}
}
