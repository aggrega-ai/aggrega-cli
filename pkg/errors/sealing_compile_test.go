// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package errors_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIdentityCannotBeForgedOrRepointed is the enforcement for two properties,
// and they are one property seen from each end: a halting condition's identity
// cannot be WRITTEN OUT by a package that did not register it, and the only way
// to bring a categorised diagnostic into existence is through a constructor
// that decides whether it names such a condition.
//
// It is a compile gate rather than a runtime assertion where a compile gate
// reaches. A runtime assertion can be deleted, skipped or worked around by
// whoever writes the next check; a compile error cannot be, because the program
// that trips it does not build. Each case below is a program under testdata that
// must FAIL to compile, one per route an author would actually take.
//
// 🔴 One route is not here and cannot be, so the boundary is recorded rather
// than left to be inferred from an absence. A struct that EMBEDS the identity
// interface has the unexported marker promoted onto it, so it satisfies the
// interface from any package and the program COMPILES. Nothing a compile gate
// can be handed refuses it, because there is nothing wrong with it at compile
// time. That route is refused by the constructor at run time and is asserted in
// errors_test.go, which is the instrument that can see it — a table row here
// would have to assert a build failure that does not occur.
//
// Against the identity:
//
//   - name the identity type, which is the first move towards declaring one;
//   - overwrite the field on an identity the package legitimately owns;
//   - replace a published accessor with one of their own.
//
// Writing the identity's own field in a composite literal carries no case, and
// the reason is recorded here so that one is not written back in. The identity's
// concrete type is unexported, so a program in another package cannot name it,
// and one reaching for its field is refused at the NAME rather than at the
// field — which is exactly the refusal testdata/name_the_type pins. A case
// asserting the field's own refusal would pin one fault twice and cover nothing
// of its own. Were the concrete type ever published, a program could name it and
// the field route would owe a case at that moment. The composite-literal route
// itself is pinned below, on the envelope, whose type IS exported and whose
// fields are not.
//
// # What a compile gate cannot be asked to prove, and what can
//
// No case here asserts that the identity's concrete type is ABSENT under an
// exported spelling, and the reason is recorded so that one is not written in.
// Such a program would name a type nothing declares, so the compiler refuses it
// for the name not resolving — the identical refusal any nonsense spelling
// earns, which makes the case green from the day it is written for a reason
// unconnected to the seal. It would also fix on one spelling out of every name a
// publication could choose, leaving the rest uncovered while reading as cover
// for all of them.
//
// That question wants an enumerating check rather than a program: a walk of this
// package's exported types, asserting that none of them carries Identity in its
// method set by value or by pointer. A compile gate proves things about the
// programs it is handed and can enumerate nothing, so the limit is the
// instrument's rather than these cases'.
//
// Against the envelope, which is the same forgery one layer out — what it
// produces is not a fake identity but a diagnostic that RENDERS like one,
// bracketed category and all, while naming no condition a reader can look up:
//
//   - write the envelope's fields in a composite literal, which is construction
//     with no constructor and therefore no identity decision;
//   - overwrite a field on an envelope a constructor legitimately built, which
//     is the same thing performed after the decision was made;
//   - raise against a value that merely REPORTS a code. This is the case worth
//     reading twice: the package's matching interface is open by design, so a
//     type declaring those reads looks like the key to the constructor and is
//     not one;
//   - raise against a value that declares the unexported marker as well, which
//     is the deliberate version of the same attempt.
//
// The gate is calibrated by one further program that must COMPILE, and that
// program exercises the identity AND both constructors. Without it, an errors
// package that stopped compiling — or a broken toolchain invocation — would make
// every case "fail" and the gate would pass while proving nothing.
func TestIdentityCannotBeForgedOrRepointed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		dir string
		// route names what the program attempts, for the failure message.
		route string
		// wantAny is satisfied when the compiler output contains every
		// substring in one of its groups. Groups exist so a message whose
		// exact wording differs across toolchain versions can still be
		// matched on the part that carries the meaning.
		wantAny [][]string
	}{
		{
			dir:   "name_the_type",
			route: "naming the identity type",
			wantAny: [][]string{
				{"not exported by package errors"},
				{"undefined", "sealed"},
			},
		},
		{
			dir:   "set_the_field",
			route: "overwriting the code on an identity the package owns",
			// What a package holds after registering is the published
			// interface, which carries reads and no fields at all, so the
			// write is refused for having no field to reach rather than for
			// reaching an unexported one. The second group is the shape the
			// same route takes wherever the concrete type is in hand.
			wantAny: [][]string{
				{"own.code undefined", "has no field or method code"},
				{"cannot refer to unexported field", "code"},
			},
		},
		{
			dir:   "reassign_accessor",
			route: "replacing a published accessor",
			wantAny: [][]string{
				{"cannot assign to ErrProbe"},
			},
		},
		{
			dir:   "envelope_literal",
			route: "writing the diagnostic envelope's fields in a composite literal",
			wantAny: [][]string{
				{"unknown field Category", "unexported category"},
				{"unknown field", "CompilerError"},
			},
		},
		{
			dir:   "envelope_field_write",
			route: "overwriting a field on an envelope a constructor built",
			wantAny: [][]string{
				{"cannot assign to ce.Category"},
				{"cannot assign to", "Category"},
			},
		},
		{
			dir:   "unregistered_condition",
			route: "raising against a value that reports a code without registering one",
			// "missing" is the fragment that separates this case from the one
			// below: the value here declares no method of that name at all. The
			// two cases are refused for different reasons and each asserts its
			// own, so neither group may be relaxed to the text they share — a
			// group both satisfy stops telling them apart, and each case then
			// keeps passing on the other's fault.
			wantAny: [][]string{
				{"does not implement", "Identity", "missing method sealedIdentity"},
			},
		},
		{
			dir:   "forged_identity",
			route: "raising against a value that declares the identity marker itself",
			// "unexported" is this case's fragment: the method IS declared here,
			// and it belongs to the package that declared it rather than to the
			// one naming the interface. Asserting that word is what makes the
			// deliberate declaration in the fixture load-bearing — without it,
			// deleting that declaration leaves a copy of the case above and
			// nothing fails.
			wantAny: [][]string{
				{"does not implement", "Identity", "unexported method sealedIdentity"},
			},
		},
	}

	// Calibration first: if the control does not compile, every verdict below
	// is meaningless, so this is a hard stop rather than one failure among many.
	if out, err := compileSealingTestdata(t, "legitimate"); err != nil {
		t.Fatalf("the calibration program under testdata/legitimate must compile, "+
			"otherwise the cases below would all fail for an unrelated reason.\n"+
			"go build error: %v\noutput:\n%s", err, out)
	}

	for _, tc := range cases {
		t.Run(tc.dir, func(t *testing.T) {
			t.Parallel()

			out, err := compileSealingTestdata(t, tc.dir)
			if err == nil {
				t.Fatalf("testdata/%s compiled, so %s is possible from outside the "+
					"declaring package. An identity obtainable anywhere but from its "+
					"accessor lets any package claim any condition, and the declaring "+
					"package can then never move the condition without silently breaking "+
					"a matcher it cannot see.", tc.dir, tc.route)
			}
			if !containsAnySealingGroup(out, tc.wantAny) {
				t.Errorf("testdata/%s failed to compile, but for an unrecognised reason — "+
					"the failure must be the sealing being enforced, not something else.\n"+
					"want output matching one of %v\ngot:\n%s", tc.dir, tc.wantAny, out)
			}
		})
	}
}

// compileSealingTestdata compiles one program under testdata and returns the
// toolchain's combined output together with its error. A library package
// produces no artifact, so nothing is written into the tree.
func compileSealingTestdata(t *testing.T, dir string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "build", "./"+filepath.ToSlash(filepath.Join("testdata", dir)))
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("compiling testdata/%s timed out: %v", dir, ctx.Err())
	}
	return string(out), err
}

func containsAnySealingGroup(out string, groups [][]string) bool {
	for _, group := range groups {
		all := true
		for _, want := range group {
			if !strings.Contains(out, want) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}
