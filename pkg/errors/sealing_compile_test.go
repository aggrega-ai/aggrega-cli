// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package errors_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIdentityCannotBeForgedOrRepointed is the enforcement for the property the
// identity shape exists to provide: the only way to hold a halting condition's
// identity is to call the accessor of the package that declares it.
//
// It is a compile gate rather than a runtime assertion on purpose. A runtime
// assertion can be deleted, skipped or worked around by whoever writes the next
// check; a compile error cannot be, because the program that trips it does not
// build. Each case below is a program under testdata that must FAIL to compile,
// one per route an author would actually take:
//
//   - name the identity type, which is the first move towards declaring one;
//   - write its field in a composite literal;
//   - overwrite the field on an identity the package legitimately owns;
//   - replace a published accessor with one of their own.
//
// The gate is calibrated by a fifth program that must COMPILE. Without it, an
// errors package that stopped compiling — or a broken toolchain invocation —
// would make every case "fail" and the gate would pass while proving nothing.
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
			dir:   "composite_literal",
			route: "writing the identity's field in a composite literal",
			wantAny: [][]string{
				{"cannot refer to unexported field", "code"},
				{"implicit assignment to unexported field"},
			},
		},
		{
			dir:   "set_the_field",
			route: "overwriting the code on an identity the package owns",
			wantAny: [][]string{
				{"cannot refer to unexported field", "code"},
				{"undefined", "code"},
			},
		},
		{
			dir:   "reassign_accessor",
			route: "replacing a published accessor",
			wantAny: [][]string{
				{"cannot assign to ErrProbe"},
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
