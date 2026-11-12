// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The input-kind claim — which Recipe input kinds a provider renders — is one fact
// about one provider. Its host-side home is types.PluginInputKinds, and every
// host consumer that can share that type does, by alias.
//
// Three representations nonetheless remain distinct, and each is deliberate:
//
//   - types.PluginInputKinds — the entity-authoring shape, generated from the
//     entity schema, its wire names camelCase because an author types them in
//     catalog YAML.
//   - InputKinds — the host side of the describe socket, generated, snake_case.
//   - abi.InputKinds — the guest side of the same socket, generated into a separate
//     module because a Wasm guest must not import the host.
//
// Each shape is single-sourced at a schema, which is a stronger guarantee than
// sharing a Go type. But they are single-sourced at TWO DIFFERENT schemas — the
// entity schema and the describe-socket schema — so no compiler check ties them
// to each other, and the host-side mappers that cross between them copy field by
// field. So a kind added to one schema and missed on the other compiles, passes
// every other test, and produces the exact failure this guard exists for: the
// provider declares a capability, one schema carries it, the wire carries it, and
// the gate that should read it sees false.
//
// 🔴 THIS GUARD IS A FLOOR, NOT A REMEDY. It reports a divergence AFTER someone
// writes it. Nothing available makes declaring a fourth three-boolean struct fail to
// compile — not unexported fields, not aliases, not generation — so recurrence is
// not structurally prevented and this test is what stands in for that.
//
// It does NOT duplicate the load-time capability cross-check in this package. That
// one compares two operands of the SAME type (an entity's recorded contract against
// the guest's live describe()), so it cannot see a field lost between two DIFFERENT
// types; this one compares nothing but field sets, across types, and never looks at
// a value. Nor does it duplicate the schema/Go alignment walk in pkg/types, which
// holds the hand-written shape against the entity schema in both directions.
func TestInputKindShapesDeclareTheSameFields(t *testing.T) {
	shapes := []struct {
		name string
		typ  reflect.Type
	}{
		{"types.PluginInputKinds (the home; entity authoring)", reflect.TypeFor[types.PluginInputKinds]()},
		{"wasm.InputKinds (describe socket, host side; generated)", reflect.TypeFor[InputKinds]()},
		{"abi.InputKinds (describe socket, guest side; generated)", reflect.TypeFor[abi.InputKinds]()},
	}

	base := shapes[0]
	want := fieldNames(base.typ)
	if len(want) == 0 {
		t.Fatalf("%s declares no fields; the guard would compare nothing", base.name)
	}

	for _, s := range shapes[1:] {
		got := fieldNames(s.typ)
		// A set difference, named in both directions. Comparing counts would pass
		// with both sides short by one, and a count tells nobody which kind went
		// missing.
		if missing := difference(want, got); len(missing) > 0 {
			t.Errorf("%s is MISSING input kind(s) %s, which %s declares.\n"+
				"An input kind the home declares and this shape does not is a capability "+
				"the build cannot carry across that boundary.",
				s.name, strings.Join(missing, ", "), base.name)
		}
		if extra := difference(got, want); len(extra) > 0 {
			t.Errorf("%s declares input kind(s) %s that %s does not.\n"+
				"An input kind reaching a wire shape without a home is a capability no "+
				"host consumer can read.",
				s.name, strings.Join(extra, ", "), base.name)
		}
	}

	// Every kind is an independent boolean claim, and two consumers depend on that
	// rather than merely assuming it: the published capability grid reads each kind
	// reflectively as a bool, and the load-time cross-check compares them pairwise.
	// A non-boolean kind is a design change those consumers must be revisited for,
	// so it fails here rather than at whichever of them happens to run first.
	for _, s := range shapes {
		for _, f := range reflect.VisibleFields(s.typ) {
			if f.Type.Kind() != reflect.Bool {
				t.Errorf("%s field %q is %s, not a bool; every input kind is an independent boolean claim",
					s.name, f.Name, f.Type.Kind())
			}
		}
	}
}

// fieldNames returns a shape's exported field names, sorted so a comparison does
// not depend on declaration order — the shapes legitimately differ in wire-name
// spelling, and the host and guest codecs are emitted independently.
func fieldNames(t reflect.Type) []string {
	var out []string
	for _, f := range reflect.VisibleFields(t) {
		if f.IsExported() {
			out = append(out, f.Name)
		}
	}
	sort.Strings(out)
	return out
}

// difference returns the members of a that are absent from b.
func difference(a, b []string) []string {
	inB := make(map[string]bool, len(b))
	for _, s := range b {
		inB[s] = true
	}
	var out []string
	for _, s := range a {
		if !inB[s] {
			out = append(out, s)
		}
	}
	return out
}
