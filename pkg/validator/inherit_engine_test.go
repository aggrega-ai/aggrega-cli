// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// ResolveInheritance gates every extends chain with three guards, and callers
// that discard its result value still depend on all three: ResolveComponentExtends
// and ResolveFeatureExtends read only Errors, using the walk purely to refuse a
// chain the subsequent typed merge could not survive. The three tests below pin
// one guard each and fail if that guard stops reporting, so a guard cannot be
// dropped silently:
//
//   - TestResolveInheritance_CycleDetection — a chain that revisits a URI.
//   - TestResolveInheritance_MaxDepth — a chain longer than the depth bound.
//   - TestResolveInheritance_ParentNotFound — a parent the lookup cannot resolve.
//
// Each asserts the specific sentinel rather than merely "some error", so one
// guard cannot pass by another guard's failure.

func TestResolveInheritance_CycleDetection(t *testing.T) {
	entry := CatalogEntry{
		Kind:    envelope.KindComponent,
		URI:     "a:1.0.0",
		Extends: "b:1.0.0",
	}

	lookup := func(uri string) (CatalogEntry, bool) {
		switch uri {
		case "b:1.0.0":
			return CatalogEntry{Kind: envelope.KindComponent, URI: "b:1.0.0", Extends: "a:1.0.0"}, true
		}
		return CatalogEntry{}, false
	}

	result := ResolveInheritance(entry, lookup)
	if len(result.Errors) == 0 {
		t.Fatal("expected cycle detection error")
	}
	if !errors.Is(result.Errors[0], ErrCyclicExtension()) {
		t.Errorf("expected ErrCyclicExtension, got %v", result.Errors[0])
	}
	if !strings.Contains(result.Errors[0].Error(), " — ") {
		t.Errorf("expected the diagnostic to carry a constraint clause; got: %s", result.Errors[0].Error())
	}
}

// TestResolveInheritance_MaxDepth pins the depth bound with a chain of distinct
// URIs longer than maxInheritanceDepth. The URIs are distinct on purpose: a
// repeated one would trip the cycle guard first and the depth bound would go
// unexercised while the test still passed.
func TestResolveInheritance_MaxDepth(t *testing.T) {
	const chainLen = maxInheritanceDepth + 5

	uri := func(i int) string { return fmt.Sprintf("e%02d:1.0.0", i) }

	lookup := func(want string) (CatalogEntry, bool) {
		for i := range chainLen {
			if uri(i) != want {
				continue
			}
			e := CatalogEntry{Kind: envelope.KindComponent, URI: uri(i)}
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

	result := ResolveInheritance(entry, lookup)
	if len(result.Errors) == 0 {
		t.Fatal("expected the depth bound to halt a chain longer than the maximum")
	}
	if !errors.Is(result.Errors[0], ErrInheritanceMerge()) {
		t.Errorf("expected ErrInheritanceMerge, got %v", result.Errors[0])
	}
	if errors.Is(result.Errors[0], ErrCyclicExtension()) {
		t.Error("a chain of distinct URIs must not report a cycle; the depth bound is what should halt it")
	}
	if want := fmt.Sprintf("maximum depth of %d", maxInheritanceDepth); !strings.Contains(result.Errors[0].Error(), want) {
		t.Errorf("expected the diagnostic to name the bound %q; got: %s", want, result.Errors[0].Error())
	}
}

func TestResolveInheritance_ParentNotFound(t *testing.T) {
	entry := CatalogEntry{
		Kind:    envelope.KindComponent,
		URI:     "child:1.0.0",
		Extends: "nonexistent:1.0.0",
	}

	lookup := func(uri string) (CatalogEntry, bool) {
		return CatalogEntry{}, false
	}

	result := ResolveInheritance(entry, lookup)
	if len(result.Errors) == 0 {
		t.Fatal("expected error for missing parent")
	}
	if !errors.Is(result.Errors[0], ErrInheritanceMerge()) {
		t.Errorf("expected ErrInheritanceMerge, got %v", result.Errors[0])
	}
}

// TestResolveInheritance_NoExtends pins the walk's behaviour on an entity that
// extends nothing: no guard fires, and the lookup is never consulted — passing
// nil proves it, since calling it would panic.
func TestResolveInheritance_NoExtends(t *testing.T) {
	entry := CatalogEntry{
		Kind: envelope.KindComponent,
		URI:  "standalone:1.0.0",
	}

	result := ResolveInheritance(entry, nil)
	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors[0].Error())
	}
	if result.MergedJSONSchema != nil {
		t.Errorf("an entry carrying no schema payload composes nothing, got %+v", result.MergedJSONSchema)
	}
}
