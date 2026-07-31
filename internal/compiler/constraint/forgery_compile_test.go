// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestObjectionCannotBeForgedOutsideTheFramework is the enforcement for the
// framework's load-bearing property: an objection carrying a sentinel can only
// come from a subject the framework supplied.
//
// It is a compile gate rather than a runtime assertion on purpose. A runtime
// assertion can be deleted, skipped or worked around by whoever writes the next
// check; a compile error cannot be, because the program that trips it does not
// build. Each case below is a program under testdata that must FAIL to compile,
// one per route an author would actually take:
//
//   - name the objection's fields;
//   - assign them positionally instead;
//   - zero-value the subject the objection needs;
//   - implement the subject contract, seal included;
//   - call the constructor a value type usually has.
//
// The gate is calibrated by a sixth program that must COMPILE. Without it, a
// framework package that stopped compiling — or a broken toolchain invocation —
// would make every case "fail" and the gate would pass while proving nothing.
func TestObjectionCannotBeForgedOutsideTheFramework(t *testing.T) {
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
			dir:   "forge_named_fields",
			route: "naming the objection's unexported fields",
			wantAny: [][]string{
				{"unknown field sentinel"},
				{"cannot refer to unexported field", "sentinel"},
			},
		},
		{
			dir:   "forge_positional",
			route: "assigning the objection's fields positionally",
			wantAny: [][]string{
				{"implicit assignment to unexported field"},
				{"cannot refer to unexported field"},
			},
		},
		{
			dir:   "forge_subject_literal",
			route: "zero-valuing a subject kind",
			wantAny: [][]string{
				{"invalid composite literal type"},
			},
		},
		{
			dir:   "forge_impl_subject",
			route: "implementing the subject contract from another package",
			wantAny: [][]string{
				{"does not implement", "sealed"},
				{"missing method sealed"},
			},
		},
		{
			dir:   "forge_constructor",
			route: "calling an exported objection constructor",
			wantAny: [][]string{
				{"undefined: constraint.NewObjection"},
				{"undefined", "NewObjection"},
			},
		},
	}

	// Calibration first: if the control does not compile, every verdict below
	// is meaningless, so this is a hard stop rather than one failure among many.
	if out, err := compileTestdata(t, "legitimate"); err != nil {
		t.Fatalf("the calibration program under testdata/legitimate must compile, "+
			"otherwise the forgery cases below would all fail for an unrelated reason.\n"+
			"go build error: %v\noutput:\n%s", err, out)
	}

	for _, tc := range cases {
		t.Run(tc.dir, func(t *testing.T) {
			t.Parallel()

			out, err := compileTestdata(t, tc.dir)
			if err == nil {
				t.Fatalf("testdata/%s compiled, so %s is possible from outside the framework. "+
					"An objection carrying a sentinel must be obtainable only from a "+
					"framework-supplied subject; if that is no longer true, the framework's "+
					"halting currency is forgeable and the placement guarantees above it do "+
					"not hold.", tc.dir, tc.route)
			}
			if !containsAnyGroup(out, tc.wantAny) {
				t.Errorf("testdata/%s failed to compile, but for an unrecognised reason — "+
					"the failure must be the forgery being rejected, not something else.\n"+
					"want output matching one of %v\ngot:\n%s", tc.dir, tc.wantAny, out)
			}
		})
	}
}

// compileTestdata compiles one program under testdata and returns the
// toolchain's combined output together with its error. A library package
// produces no artifact, so nothing is written into the tree.
func compileTestdata(t *testing.T, dir string) (string, error) {
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

func containsAnyGroup(out string, groups [][]string) bool {
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
