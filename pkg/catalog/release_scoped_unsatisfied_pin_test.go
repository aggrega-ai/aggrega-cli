// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// The proofs in this file cover the surfaces that resolve a Release's references
// against a catalog: the dependency-closure lookup, the extends resolution over the
// closure, and the extends ancestry the mapping-consistency gate reads — each over
// both catalog backings, the eagerly loaded one and the lazily enumerated view. A
// reference whose name the catalog holds at other versions only is reported as the
// version no catalog entry satisfies, never as an entity the catalog does not hold;
// a name the catalog does not hold keeps the not-found report; and a pin the
// catalog satisfies resolves. Conditions are matched through their accessors.

// unsatisfiedPinComponent renders a Component at version 1.0.0 that extends parent,
// or extends nothing when parent is empty.
func unsatisfiedPinComponent(name, parent string) string {
	doc := `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: ` + name + `
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
`
	if parent == "" {
		return doc + "  schema:\n    values:\n      port: { type: integer }\n"
	}
	return doc + "  extends: " + parent + "\n  schema: inherited\n"
}

// unsatisfiedPinWorkspace writes a catalog holding core/base at 1.0.0 and one
// child per extends link the tests read, and returns the workspace root and the
// catalog directory.
func unsatisfiedPinWorkspace(t *testing.T) (string, string) {
	t.Helper()
	ws := t.TempDir()
	cd := filepath.Join(ws, "catalog")
	writeCatalogFile(t, cd, "base.yaml", unsatisfiedPinComponent("core/base", ""))
	writeCatalogFile(t, cd, "child-held.yaml", unsatisfiedPinComponent("core/child-held", "core/base:1.0.0"))
	writeCatalogFile(t, cd, "child-unsat.yaml", unsatisfiedPinComponent("core/child-unsat", "core/base:9.9.9"))
	writeCatalogFile(t, cd, "child-absent.yaml", unsatisfiedPinComponent("core/child-absent", "core/absent:1.0.0"))
	return ws, cd
}

// TestClosureLookup_ReportsAPinTheCatalogCannotSatisfy drives the closure lookup
// of both backings with one reference per row and a satisfied pin as the control.
//
// INVARIANT: a reference every tier missed carries the unsatisfiable-version
// condition exactly when the catalog holds the entity under its name at other
// versions only.
// CONSEQUENCE: were the condition missing, the walk would report an entity the
// catalog does not hold and send the author looking for a misspelt name; were it
// present for an absent name, the walk would report a version fault where the
// name is at fault.
func TestClosureLookup_ReportsAPinTheCatalogCannotSatisfy(t *testing.T) {
	t.Parallel()
	ws, cd := unsatisfiedPinWorkspace(t)
	const cache = ".aggrega/cache"
	eager, err := LoadAll(ws, cd)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	backings := map[string]closure.Lookup{
		"eager": eager.ClosureLookupWithReuse(nil, ws, cache, nil, nil, nil),
		"lazy":  NewLazyLookup(ws, cache, []string{cd}, nil).ClosureLookup(nil, ws, cache, nil, nil, nil),
	}
	for _, backing := range []string{"eager", "lazy"} {
		for _, tc := range []struct {
			name        string
			ref         string
			wantFound   bool
			wantRefusal bool
		}{
			{"a pin the catalog holds (control)", "core/base:1.0.0", true, false},
			{"an exact pin the catalog does not hold", "core/base:9.9.9", false, true},
			{"a caret pin nothing satisfies", "core/base:^9.0.0", false, true},
			{"a name the catalog does not hold", "core/absent:1.0.0", false, false},
		} {
			t.Run(backing+"/"+tc.name, func(t *testing.T) {
				res, err := backings[backing].ResolveCatalogEntity(t.Context(), tc.ref, "component")
				if err != nil {
					t.Fatalf("ResolveCatalogEntity(%q): %v", tc.ref, err)
				}
				if res.NotFound == tc.wantFound {
					t.Fatalf("ResolveCatalogEntity(%q): NotFound = %v, want %v", tc.ref, res.NotFound, !tc.wantFound)
				}
				got := errors.Is(res.UnsatisfiedPin, ErrUnsatisfiableEntityVersion())
				if got != tc.wantRefusal {
					t.Errorf("ResolveCatalogEntity(%q): unsatisfied-pin condition carried = %v, want %v (%v)",
						tc.ref, got, tc.wantRefusal, res.UnsatisfiedPin)
				}
			})
		}
	}
}

// TestReleaseScopedExtends_ReportsAParentPinTheCatalogCannotSatisfy drives the two
// extends readings a Release reaches — the closure-scoped extends resolution and the
// ancestry the mapping-consistency gate reads — over both backings, one child per
// row, with a child whose parent pin the catalog satisfies as the control.
//
// INVARIANT: an extends link naming a parent the catalog holds at other versions
// only is refused with the unsatisfiable-version condition, naming the link; a link
// naming a parent the catalog does not hold keeps the inheritance-merge condition.
// CONSEQUENCE: were the condition missing, an author whose parent's version is
// wrong would be told no catalog entity provides the parent.
func TestReleaseScopedExtends_ReportsAParentPinTheCatalogCannotSatisfy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		child string
		// want is the condition the refusal carries; nil for the control.
		want error
		// not is a condition the refusal must not carry.
		not error
	}{
		{"a parent pin the catalog holds (control)", "core/child-held", nil, nil},
		{"a parent pin the catalog does not hold", "core/child-unsat",
			ErrUnsatisfiableEntityVersion(), validator.ErrInheritanceMerge()},
		{"a parent the catalog does not hold", "core/child-absent",
			validator.ErrInheritanceMerge(), ErrUnsatisfiableEntityVersion()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ws, cd := unsatisfiedPinWorkspace(t)
			eager, err := LoadAll(ws, cd)
			if err != nil {
				t.Fatalf("catalog load: %v", err)
			}
			lazy := NewLazyLookup(ws, ".aggrega/cache", []string{cd}, nil)
			cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: tc.child + ":1.0.0"}}}

			// The ancestry is read before the closure pass, which merges a resolved
			// chain into the catalog it holds.
			_, eagerAncestry := eager.ComponentAncestry(tc.child)
			_, lazyAncestry := lazy.componentAncestry(tc.child)
			var eagerExtends []error
			for _, ve := range eager.ResolveClosureExtends(cl) {
				eagerExtends = append(eagerExtends, ve)
			}
			readings := []struct {
				name string
				errs []error
			}{
				{"eager closure extends", eagerExtends},
				{"lazy closure extends", lazy.ResolveClosureExtends(t.Context(), cl)},
				{"eager ancestry", nonNil(eagerAncestry)},
				{"lazy ancestry", nonNil(lazyAncestry)},
			}
			for _, r := range readings {
				if tc.want == nil {
					if len(r.errs) != 0 {
						t.Errorf("%s: control refused: %v", r.name, r.errs)
					}
					continue
				}
				if len(r.errs) != 1 {
					t.Errorf("%s: want one refusal, got %d: %v", r.name, len(r.errs), r.errs)
					continue
				}
				if !errors.Is(r.errs[0], tc.want) || errors.Is(r.errs[0], tc.not) {
					t.Errorf("%s: refusal %v; want it to carry %v and not %v", r.name, r.errs[0], tc.want, tc.not)
				}
				if tc.want == ErrUnsatisfiableEntityVersion() &&
					!strings.Contains(r.errs[0].Error(), `extends parent "core/base:9.9.9"`) {
					t.Errorf("%s: refusal %q does not name the link", r.name, r.errs[0].Error())
				}
			}
		})
	}
}

// nonNil is err as a slice of the findings it stands for.
func nonNil(err error) []error {
	if err == nil {
		return nil
	}
	return []error{err}
}
