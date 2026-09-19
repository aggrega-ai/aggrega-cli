// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package projectionparity_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/tools/projectionparity"
)

// The fixtures are written into a temporary tree from source in this file
// rather than kept under testdata, and the reason is mechanical: a fixture tree
// this read can analyse needs a module file at its root, and a module file
// committed under testdata is picked up by the module censuses that walk the
// repository — which would try to build a tree whose import paths resolve to
// nothing. Writing the tree per case keeps the fixtures readable here and out of
// every other instrument's way.

// surfaces is the two-surface corpus every case below is read with, so a case's
// source is the only thing that varies.
func surfaces() []projectionparity.Surface {
	return []projectionparity.Surface{
		{Name: "the first surface", Dirs: []string{"internal/one"}},
		{Name: "the second surface", Dirs: []string{"internal/two"}},
	}
}

// write lays a fixture tree down in a temporary directory and returns its root.
func write(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	all := map[string]string{"go.mod": "module planted\n\ngo 1.26.0\n"}
	for path, body := range files {
		all[path] = body
	}
	for path, body := range all {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", path, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}

func analyze(t *testing.T, files map[string]string) projectionparity.Result {
	t.Helper()

	result, err := projectionparity.Analyze(write(t, files), "planted", "internal/", surfaces())
	if err != nil {
		t.Fatalf("run the projection read: %v", err)
	}
	return result
}

// kinds are a result's findings as "kind Result.Field" strings, sorted, which is
// what a case asserts against.
func kinds(result projectionparity.Result) []string {
	out := make([]string, 0, len(result.Findings))
	for _, f := range result.Findings {
		entry := string(f.Kind) + " " + f.Result
		if f.Field != "" {
			entry += "." + f.Field
		}
		if f.Surface != "" {
			entry += " on " + f.Surface
		}
		out = append(out, entry)
	}
	slices.Sort(out)
	return out
}

// twoFieldCore is the shared computation every case that does not need its own
// reads: one struct, two exported fields.
const twoFieldCore = `package core

type Answer struct {
	Verdict bool
	Hint    string
}

func Compute() (Answer, error) { return Answer{}, nil }
`

// TestTheReadNamesWhatOneSurfaceCarriesAndAnotherDrops walks the cells of the
// two conditions this read turns on — whether a surface reads a field, and
// whether it states an exemption for not reading it — rather than their margins.
//
// The two are independent, so the interesting cases are the combinations: above
// all an exemption sitting on a surface that DOES read the field, which is the
// shape that would let the declaration launder the very defect the read exists
// to find, and an exemption sitting away from every projection of the result,
// which is the central list this read refuses to have. Testing the conditions
// separately would leave both cells unstated while every row passed.
func TestTheReadNamesWhatOneSurfaceCarriesAndAnotherDrops(t *testing.T) {
	t.Parallel()

	// detail is asserted only where a row shares its finding shape with another
	// row: two refusals of one exemption report the same kind, result and
	// surface, so without it a check that fired the wrong refusal would pass.
	for _, row := range []struct {
		name   string
		files  map[string]string
		want   []string
		detail string
	}{
		{
			// The planted defect: the same field, carried on one surface and
			// dropped on the other, with everything else about the two alike.
			name: "a field one surface carries and the other drops is named",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
}
`,
			},
			want: []string{"dropped-field Answer.Hint on the second surface"},
		},
		{
			name: "a field both surfaces carry is cleared",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Hint
	_ = answer.Verdict
}
`,
			},
			want: nil,
		},
		{
			// A field the computation produces and nobody projects is a
			// different defect with a different repair, and reporting it here
			// would put an unused-output finding inside a parity verdict.
			name: "a field neither surface carries is not a divergence",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
}
`,
			},
			want: nil,
		},
		{
			name: "an exemption stated at the site clears the drop",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

// projection-omits Answer.Hint: this surface answers a machine, which acts on
// the verdict and has nothing to do with a phrasing meant for a person.
func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
}
`,
			},
			want: nil,
		},
		{
			// The cell that matters most: a declaration on a surface that does
			// read the field. A declaration nothing verifies is one a later
			// author applies by habit.
			name: "an exemption for a field the surface does read is refused",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

// projection-omits Answer.Verdict: a reason that describes some other surface.
func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
}
`,
			},
			want: []string{
				"dropped-field Answer.Hint on the second surface",
				"exemption-unsupported Answer.Verdict on the second surface",
			},
		},
		{
			name: "an exemption naming a field the result has not got is refused",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

// projection-omits Answer.Reason: a field this result does not carry at all.
func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
}
`,
			},
			want: []string{
				"dropped-field Answer.Hint on the second surface",
				"exemption-unsupported Answer.Reason on the second surface",
			},
		},
		{
			name: "an exemption stating no reason is refused",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

// projection-omits Answer.Hint:
func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
}
`,
			},
			want: []string{
				"dropped-field Answer.Hint on the second surface",
				"exemption-unsupported Answer.Hint on the second surface",
			},
		},
		{
			// The central-list cell. An exemption in a file that projects
			// nothing is a registry entry wearing the clothes of a statement at
			// the site, and it is exactly the second copy this read refuses.
			name: "an exemption away from every projection of the result is refused",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
}
`,
				"internal/two/exemptions.go": `package two

// projection-omits Answer.Hint: stated nowhere near the projection it excuses.
func unrelated() {}
`,
			},
			want: []string{
				"dropped-field Answer.Hint on the second surface",
				"exemption-unsupported Answer.Hint on the second surface",
			},
		},
		{
			// A value the reading cannot follow withholds the verdict rather
			// than clearing it: the dropped field is NOT reported, because the
			// field set is incomplete and a verdict over it would certify
			// nothing.
			name: "a value that escapes the body withholds the verdict rather than clearing it",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

var held core.Answer

func project() {
	answer, _ := core.Compute()
	held = answer
}
`,
			},
			want: []string{"unreadable-site Answer on the second surface"},
		},
		{
			name: "a value handed to a same-package helper is followed into it",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	render(answer)
}

func render(a core.Answer) {
	_ = a.Verdict
	_ = a.Hint
}
`,
			},
			want: nil,
		},
		{
			name: "a result one surface discards while another carries fields is named",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

func project() error {
	_, err := core.Compute()
	return err
}
`,
			},
			want: []string{"result-discarded Answer on the second surface"},
		},
		{
			// The whole-result form of the exemption, and the case it exists for:
			// a caller consuming whether the computation answered rather than what
			// it answered, with no reader for the value to be reported to.
			name: "an exemption naming the whole result clears a result the surface discards",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

// projection-omits Answer: this surface calls the computation to learn whether
// the identifier resolves at all, and reports to nobody, so there is no reader
// the answer could be owed to.
func project() error {
	_, err := core.Compute()
	return err
}
`,
			},
			want: nil,
		},
		{
			// The broad form must not clear more than the narrow one could. A
			// surface that never discarded the result is claiming to omit
			// something it was never handed whole.
			name: "an exemption naming a whole result the surface does not discard is refused",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

// projection-omits Answer: a claim to omit the whole result from a body that
// reads part of it.
func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
}
`,
			},
			want: []string{
				"dropped-field Answer.Hint on the second surface",
				"exemption-unsupported Answer on the second surface",
			},
			detail: "does not discard",
		},
		{
			// The interior cell of the broad form: discarded at one call and
			// projected at another. The claim covers the whole result and the
			// surface carries part of it, so the narrow form is what this site
			// owes — and the discarded finding stands.
			name: "an exemption naming a whole result the surface projects elsewhere is refused",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

// projection-omits Answer: claimed for the whole result while a second call in
// this package reads part of it.
func project() error {
	_, err := core.Compute()
	return err
}

func also() {
	answer, _ := core.Compute()
	_ = answer.Verdict
}
`,
			},
			want: []string{
				"exemption-unsupported Answer on the second surface",
				"result-discarded Answer on the second surface",
			},
			detail: "does project",
		},
		{
			// The reason check reaches the broad form too, and its vacuous case is
			// restating the result rather than the field.
			name: "an exemption naming a whole result and restating its name is refused",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

// projection-omits Answer: Answer
func project() error {
	_, err := core.Compute()
	return err
}
`,
			},
			want: []string{
				"exemption-unsupported Answer on the second surface",
				"result-discarded Answer on the second surface",
			},
			detail: "states no reason",
		},
		{
			name: "a result every surface discards is not a divergence",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() error {
	_, err := core.Compute()
	return err
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

func project() error {
	_, err := core.Compute()
	return err
}
`,
			},
			want: nil,
		},
		{
			name: "a computation only one surface calls raises no parity question",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

func project() {}
`,
			},
			want: nil,
		},
		{
			// A conversion is a call expression syntactically and the caller's
			// own value semantically, so there is no computed answer to compare.
			name: "a type conversion that looks like a call is not a computation",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore + `
type Label string
`,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	_ = core.Label("first")
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

func project() {
	_ = core.Label("second")
}
`,
			},
			want: nil,
		},
		{
			// A re-export is followed to the function it re-exports, so the
			// result struct is read from the package that declares it.
			name: "a re-exported computation reads as the thing it re-exports",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/facade/facade.go": `package facade

import "planted/internal/core"

var Compute = core.Compute
`,
				"internal/one/one.go": `package one

import "planted/internal/facade"

func project() {
	answer, _ := facade.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import "planted/internal/facade"

func project() {
	answer, _ := facade.Compute()
	_ = answer.Verdict
}
`,
			},
			want: []string{"dropped-field Answer.Hint on the second surface"},
		},
		{
			name: "a name bound twice in one body withholds the verdict",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	answer = core.Answer{}
	_ = answer.Hint
}
`,
			},
			want: []string{"unreadable-site Answer on the second surface"},
		},
		{
			// A test against nil reads no field and lets nothing out of the
			// body, so it is neither a read nor an escape. Every surface makes
			// it, and treating it as an escape would report the shape they share.
			name: "a test against nil is neither a read nor an escape",
			files: map[string]string{
				"internal/core/core.go": `package core

type Answer struct {
	Verdict bool
	Hint    string
}

func Compute() (*Answer, error) { return nil, nil }
`,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	if answer == nil {
		return
	}
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	if answer != nil {
		_ = answer.Verdict
	}
}
`,
			},
			want: []string{"dropped-field Answer.Hint on the second surface"},
		},
		{
			// The follow does not stop at the package boundary, because the
			// boundary where this read's source stops is the module. A helper
			// living one import away is the same body it would be next door, so
			// abandoning the value at the call would report this read's reach as
			// though it were a fact about the surface.
			name: "a value handed to a helper in another package of this module is followed into it",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import (
	"planted/internal/core"
	"planted/internal/render"
)

func project() {
	answer, _ := core.Compute()
	render.Show(answer)
}
`,
				"internal/render/render.go": `package render

import "planted/internal/core"

func Show(a core.Answer) {
	_ = a.Verdict
	_ = a.Hint
}
`,
			},
			want: nil,
		},
		{
			// 🔴 The calibration the row above cannot give. Following a call
			// makes findings disappear, and a follow that only ever made them
			// disappear would be indistinguishable from switching the read off.
			// Here BOTH surfaces are readable only through a cross-package hop,
			// and the divergence between the two bodies is still named — so the
			// reach was gained by reading those bodies rather than by excusing
			// the sites that reach them.
			name: "a drop inside a helper in another package is still named",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import (
	"planted/internal/core"
	"planted/internal/render"
)

func project() {
	answer, _ := core.Compute()
	render.Full(answer)
}
`,
				"internal/two/two.go": `package two

import (
	"planted/internal/core"
	"planted/internal/render"
)

func project() {
	answer, _ := core.Compute()
	render.Brief(answer)
}
`,
				"internal/render/render.go": `package render

import "planted/internal/core"

func Full(a core.Answer) {
	_ = a.Verdict
	_ = a.Hint
}

func Brief(a core.Answer) {
	_ = a.Verdict
}
`,
			},
			want: []string{"dropped-field Answer.Hint on the second surface"},
		},
		{
			// The bound, held from the other side. A call into another module
			// names a body this read has no source for, so the field set it would
			// assemble would be short — and a set that may be short cannot
			// support a claim that some other surface dropped something.
			name: "a value handed to a call outside this module stays unreadable",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import (
	"fmt"

	"planted/internal/core"
)

func project() {
	answer, _ := core.Compute()
	fmt.Println(answer)
}
`,
			},
			want: []string{"unreadable-site Answer on the second surface"},
		},
		{
			// A qualified name that resolves to a package of this module and to
			// no top-level function in it. A type conversion is a call
			// syntactically and no body at all, and treating the resolution as
			// enough would follow a name into nothing and report the silence as
			// a complete read.
			name: "a qualified name naming no function in the package it resolves to stays unreadable",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import (
	"planted/internal/core"
	"planted/internal/render"
)

func project() {
	answer, _ := core.Compute()
	_ = render.Wrapped(answer)
}
`,
				"internal/render/render.go": `package render

import "planted/internal/core"

type Wrapped core.Answer
`,
			},
			want: []string{"unreadable-site Answer on the second surface"},
		},
		{
			// The hop budget is shared between a same-package hop and a
			// cross-package one, because what it bounds is how far the value has
			// travelled from the binding a finding names rather than how many
			// package boundaries it crossed. Beyond it the site is declined, not
			// cleared.
			name: "a value handed on beyond the hop budget stays unreadable",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

import (
	"planted/internal/core"
	"planted/internal/render"
)

func project() {
	answer, _ := core.Compute()
	render.First(answer)
}
`,
				"internal/render/render.go": `package render

import (
	"planted/internal/core"
	"planted/internal/deeper"
)

func First(a core.Answer) {
	deeper.Second(a)
}
`,
				"internal/deeper/deeper.go": `package deeper

import (
	"planted/internal/core"
	"planted/internal/deepest"
)

func Second(a core.Answer) {
	deepest.Third(a)
}
`,
				"internal/deepest/deepest.go": `package deepest

import "planted/internal/core"

func Third(a core.Answer) {
	_ = a.Verdict
	_ = a.Hint
}
`,
			},
			want: []string{"unreadable-site Answer on the second surface"},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			result := analyze(t, row.files)
			got := kinds(result)
			if !slices.Equal(got, row.want) {
				t.Fatalf("the read reports\n  %s\nwant\n  %s\n--- full report ---\n%s",
					strings.Join(got, "\n  "), strings.Join(row.want, "\n  "),
					projectionparity.Report(result.Findings))
			}
			if row.detail != "" && !strings.Contains(projectionparity.Report(result.Findings), row.detail) {
				t.Fatalf("the read reports the right finding shape for the wrong reason: no refusal says %q\n%s",
					row.detail, projectionparity.Report(result.Findings))
			}
		})
	}
}

// TestTheReadTerminatesOnAValueHandedRoundACycle holds the two bounds that keep
// the follow from running forever, and holds them apart, because they answer
// different shapes and only one of them is a depth.
//
// The hop budget alone would terminate a ring by exhausting itself, and would
// then report the exhaustion as a site it could not read — a loss of coverage
// dressed as a limit. The set of function-and-value pairs already opened is what
// answers a ring properly: meeting the same value in the same function a second
// time adds nothing, so the second visit returns the complete answer it already
// has rather than an escape. The rows below pin one case to each, so a change
// that removed either would fail here rather than passing on the other's cover.
//
// Both rows would hang rather than fail if neither bound held, which is the
// assertion that cannot be written as a comparison: the test completing is it.
func TestTheReadTerminatesOnAValueHandedRoundACycle(t *testing.T) {
	t.Parallel()

	carrier := `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`

	for _, row := range []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			// Answered by the visited set. The recursion adds nothing the first
			// visit did not already read, so the field set is complete and the
			// surface's reads stand: the second surface reads Verdict through
			// the helper and drops Hint, and that is what is reported.
			name: "a helper that hands the value to itself is answered rather than followed again",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go":   carrier,
				"internal/two/two.go": `package two

import (
	"planted/internal/core"
	"planted/internal/render"
)

func project() {
	answer, _ := core.Compute()
	render.Show(answer)
}
`,
				"internal/render/render.go": `package render

import "planted/internal/core"

func Show(a core.Answer) {
	_ = a.Verdict
	Show(a)
}
`,
			},
			want: []string{"dropped-field Answer.Hint on the second surface"},
		},
		{
			// Answered by the hop budget. Two helpers handing the value back and
			// forth reach the budget before the visited set meets a repeat, so
			// the site is declined — which is the honest answer, since the read
			// stopped partway and a field set it stopped assembling cannot clear
			// anything.
			//
			// The ring is inside one package and the hop that reaches it crosses
			// a package boundary, which is the only arrangement valid Go can
			// have: a ring of direct calls spanning two packages needs those two
			// packages to import each other, so the shape this read has to
			// terminate on is a cross-package hop INTO a ring rather than a ring
			// made of cross-package hops.
			name: "a value handed back and forth inside a helper package is declined at the budget",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go":   carrier,
				"internal/two/two.go": `package two

import (
	"planted/internal/core"
	"planted/internal/render"
)

func project() {
	answer, _ := core.Compute()
	render.Show(answer)
}
`,
				"internal/render/render.go": `package render

import "planted/internal/core"

func Show(a core.Answer) {
	back(a)
}

func back(a core.Answer) {
	Show(a)
}
`,
			},
			want: []string{"unreadable-site Answer on the second surface"},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			got := kinds(analyze(t, row.files))
			if !slices.Equal(got, row.want) {
				t.Fatalf("the read reports\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(row.want, "\n  "))
			}
		})
	}
}

// twoPackagesOneResultName is the shape a bare result name does not identify:
// two shared computations in two packages, each handing back a struct of the
// same name. Both are called from both surfaces, so both are collected and the
// bare name reaches two results.
var twoPackagesOneResultName = map[string]string{
	"internal/alpha/alpha.go": `package alpha

type Answer struct {
	Verdict bool
	Hint    string
}

func Compute() (Answer, error) { return Answer{}, nil }
`,
	"internal/beta/beta.go": `package beta

type Answer struct {
	Verdict bool
	Hint    string
}

func Compute() (Answer, error) { return Answer{}, nil }
`,
	"internal/one/one.go": `package one

import (
	"planted/internal/alpha"
	"planted/internal/beta"
)

func project() {
	first, _ := alpha.Compute()
	_ = first.Verdict
	_ = first.Hint
	second, _ := beta.Compute()
	_ = second.Verdict
	_ = second.Hint
}
`,
}

// prescribedMarker is the marker text a finding's own message tells an author to
// write, taken from that message rather than rebuilt from the finding's fields,
// with a real reason in place of the placeholder.
//
// Reading it out of the rendered message is the point: it is the message that an
// author follows, so a marker rebuilt from the fields would assert agreement
// between the fields and the grammar while leaving the message — the only part
// anybody reads — free to prescribe something else.
func prescribedMarker(t *testing.T, f projectionparity.Finding, reason string) string {
	t.Helper()

	const opener, placeholder = "// projection-omits ", "<reason>"
	line := f.String()
	at := strings.Index(line, opener)
	if at < 0 {
		t.Fatalf("the finding prescribes no marker, so an author has nothing to follow:\n%s", line)
	}
	marker := line[at:]
	if !strings.HasSuffix(marker, placeholder) {
		t.Fatalf("the prescribed marker does not end in the reason placeholder, so this test cannot fill one in:\n%s", marker)
	}
	return strings.TrimSuffix(marker, placeholder) + reason
}

// TestTheMarkerTheReadPrescribesIsAMarkerTheReadAccepts is the round trip, and
// it is the property the exemption mechanism is worth nothing without.
//
// A finding that tells an author to write a marker the same read then refuses is
// worse than a finding with no remedy at all: the author writes the reason, the
// refusal names a different defect, and the fact still goes unreported as
// dropped. That is the state this read shipped in for any result whose bare name
// two packages declare, so the shared-name rows below are the direct negation of
// it and the unshared row holds the case that already worked.
//
// The marker is taken from the finding's own rendered message and written into
// the source verbatim. Nothing here constructs a marker of its own — a test that
// spelled the marker itself would pass while the message prescribed something
// else, which is exactly the defect.
func TestTheMarkerTheReadPrescribesIsAMarkerTheReadAccepts(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name string
		// files are the fixture with one %s where the marker is written, in the
		// doc position of the projecting function.
		files map[string]string
		with  string
		// wantQualified is the package the prescription must name, asserted so a
		// silent return to the bare form cannot pass this test.
		wantQualified string
		wantKind      projectionparity.Kind
		wantField     string
		reason        string
	}{
		{
			// The case that was broken: two packages declare Answer, so the bare
			// name identifies neither and only the qualified form can.
			name:  "a dropped field of a result whose bare name two packages declare",
			files: twoPackagesOneResultName,
			with: `package two

import (
	"planted/internal/alpha"
	"planted/internal/beta"
)

%sfunc project() {
	first, _ := alpha.Compute()
	_ = first.Verdict
	second, _ := beta.Compute()
	_ = second.Verdict
	_ = second.Hint
}
`,
			wantQualified: "internal/alpha.Answer",
			wantKind:      projectionparity.DroppedField,
			wantField:     "Hint",
			reason:        "this surface answers a machine that acts on the verdict alone.",
		},
		{
			// The whole-result form of the same trap, which needs its own row:
			// the two forms are rendered by different branches of the message and
			// read back by different checks, so one working says nothing about the
			// other.
			name:  "a whole result discarded, whose bare name two packages declare",
			files: twoPackagesOneResultName,
			with: `package two

import (
	"planted/internal/alpha"
	"planted/internal/beta"
)

%sfunc project() error {
	_, err := alpha.Compute()
	second, _ := beta.Compute()
	_ = second.Verdict
	_ = second.Hint
	return err
}
`,
			wantQualified: "internal/alpha.Answer",
			wantKind:      projectionparity.ResultDiscarded,
			reason:        "this surface calls it to learn whether the identifier resolves and reports to nobody.",
		},
		{
			// The unshared name, so the row that already worked is held rather
			// than assumed: the prescription is qualified here too, and it is
			// still accepted.
			name: "a dropped field of a result only one package declares",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
			},
			with: `package two

import "planted/internal/core"

%sfunc project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
}
`,
			wantQualified: "internal/core.Answer",
			wantKind:      projectionparity.DroppedField,
			wantField:     "Hint",
			reason:        "this surface answers a machine that acts on the verdict alone.",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			before := map[string]string{}
			for path, body := range row.files {
				before[path] = body
			}
			before["internal/two/two.go"] = fmt.Sprintf(row.with, "")

			found := analyze(t, before)
			if len(found.Findings) != 1 {
				t.Fatalf("the fixture reports %d finding(s), want exactly 1 so the marker below answers a known one:\n%s",
					len(found.Findings), projectionparity.Report(found.Findings))
			}
			finding := found.Findings[0]
			if finding.Kind != row.wantKind || finding.Field != row.wantField {
				t.Fatalf("the fixture reports %q on field %q, want %q on %q",
					finding.Kind, finding.Field, row.wantKind, row.wantField)
			}

			marker := prescribedMarker(t, finding, row.reason)
			if !strings.Contains(marker, row.wantQualified) {
				t.Fatalf("the prescribed marker does not name the package that declares the result:\n  %s\nwant it to name %s",
					marker, row.wantQualified)
			}

			after := map[string]string{}
			for path, body := range row.files {
				after[path] = body
			}
			after["internal/two/two.go"] = fmt.Sprintf(row.with, marker+"\n")

			cleared := analyze(t, after)
			if len(cleared.Findings) != 0 {
				t.Fatalf("the read prescribed a marker it then refuses. Written verbatim from its own message:\n  %s\nand it reports:\n%s",
					marker, projectionparity.Report(cleared.Findings))
			}
		})
	}
}

// TestTheBareMarkerStaysAcceptedWhereItIdentifiesOneResult holds the form the
// tree already uses.
//
// Markers written before the qualified form existed name their result bare, and
// every one of them still has to verify: a grammar extension that quietly
// invalidated the exemptions already in the tree would turn each of them into a
// finding, which is a regression wearing the clothes of stricter checking.
func TestTheBareMarkerStaysAcceptedWhereItIdentifiesOneResult(t *testing.T) {
	t.Parallel()

	result := analyze(t, map[string]string{
		"internal/core/core.go": twoFieldCore,
		"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
		"internal/two/two.go": `package two

import "planted/internal/core"

// projection-omits Answer.Hint: this surface answers a machine, which acts on
// the verdict and has nothing to do with a phrasing meant for a person.
func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
}
`,
	})
	if len(result.Findings) != 0 {
		t.Fatalf("a bare marker naming the only result of that name was refused:\n%s",
			projectionparity.Report(result.Findings))
	}
}

// TestTheQualifiedMarkerCannotExcuseWhatTheBareOneCouldNot walks every way the
// qualified form is refused, and asserts WHICH refusal fired for each.
//
// The qualified form only decides which result a marker names. It must widen
// nothing else, because the read-back checks are the whole reason an exemption is
// a statement rather than a licence — a qualified marker that could excuse a
// field the surface demonstrably reads would make the marker a way to silence
// any finding at all.
//
// Every row asserts a distinct phrase from its own refusal rather than only the
// finding's kind, and that is not belt-and-braces: several of these refusals
// report the same kind, result and surface, so a row checking the kind alone
// would pass on the wrong refusal firing and the check would silently stop
// covering its subject.
func TestTheQualifiedMarkerCannotExcuseWhatTheBareOneCouldNot(t *testing.T) {
	t.Parallel()

	// threeSurfaces is needed by exactly one row: a result that is collected
	// (two surfaces call it) while a third surface calls nothing that hands it
	// back. With two surfaces every collected result is consumed by both, so the
	// refusal for a marker on a result the surface never receives is unreachable.
	threeSurfaces := []projectionparity.Surface{
		{Name: "the first surface", Dirs: []string{"internal/one"}},
		{Name: "the second surface", Dirs: []string{"internal/two"}},
		{Name: "the third surface", Dirs: []string{"internal/three"}},
	}

	for _, row := range []struct {
		name     string
		files    map[string]string
		surfaces []projectionparity.Surface
		// wantRefusal is the phrase that identifies which refusal fired.
		wantRefusal string
		// wantAlso is a finding the refusal must NOT have cleared, empty where
		// the fixture has none.
		wantAlso projectionparity.Kind
	}{
		{
			name: "a qualified marker naming a package that declares no such shared result",
			files: mergeFiles(twoPackagesOneResultName, `package two

import (
	"planted/internal/alpha"
	"planted/internal/beta"
)

// projection-omits internal/elsewhere.Answer.Hint: a package that declares no
// shared result of that name at all.
func project() {
	first, _ := alpha.Compute()
	_ = first.Verdict
	second, _ := beta.Compute()
	_ = second.Verdict
	_ = second.Hint
}
`),
			wantRefusal: "internal/elsewhere declares no shared result named Answer",
			wantAlso:    projectionparity.DroppedField,
		},
		{
			name: "a qualified marker on a result this surface calls nothing that hands back",
			files: map[string]string{
				"internal/core/core.go": twoFieldCore,
				"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
				"internal/two/two.go": `package two

// projection-omits internal/core.Answer.Hint: a result this surface never
// receives, so it drops no field of one.
func project() {}
`,
				"internal/three/three.go": `package three

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`,
			},
			surfaces:    threeSurfaces,
			wantRefusal: "calls nothing that hands back internal/core.Answer",
		},
		{
			// The cell that would turn the marker into a licence: a qualified
			// claim to omit a field the surface plainly reads.
			name: "a qualified marker on a field the surface does read is stale",
			files: mergeFiles(twoPackagesOneResultName, `package two

import (
	"planted/internal/alpha"
	"planted/internal/beta"
)

// projection-omits internal/alpha.Answer.Verdict: a claim about a field this
// body reads on the very next line.
func project() {
	first, _ := alpha.Compute()
	_ = first.Verdict
	second, _ := beta.Compute()
	_ = second.Verdict
	_ = second.Hint
}
`),
			wantRefusal: "does read internal/alpha.Answer.Verdict",
			wantAlso:    projectionparity.DroppedField,
		},
		{
			// The whole-result form of the same cell, and the one the ruling
			// authorising this grammar named explicitly: the broad form must not
			// clear more than the narrow one could.
			name: "a qualified whole-result marker on a surface that projects a field of it",
			files: mergeFiles(twoPackagesOneResultName, `package two

import (
	"planted/internal/alpha"
	"planted/internal/beta"
)

// projection-omits internal/alpha.Answer: claimed for the whole result by a
// body that reads part of it.
func project() {
	first, _ := alpha.Compute()
	_ = first.Verdict
	second, _ := beta.Compute()
	_ = second.Verdict
	_ = second.Hint
}
`),
			wantRefusal: "does not discard the internal/alpha.Answer it is handed",
			wantAlso:    projectionparity.DroppedField,
		},
		{
			name: "a qualified whole-result marker on a surface that projects the result at another call",
			files: mergeFiles(twoPackagesOneResultName, `package two

import (
	"planted/internal/alpha"
	"planted/internal/beta"
)

// projection-omits internal/alpha.Answer: claimed for the whole result while a
// second call in this package reads part of it.
func project() error {
	_, err := alpha.Compute()
	second, _ := beta.Compute()
	_ = second.Verdict
	_ = second.Hint
	return err
}

func also() {
	first, _ := alpha.Compute()
	_ = first.Verdict
}
`),
			wantRefusal: "does project internal/alpha.Answer (it reads Verdict)",
			wantAlso:    projectionparity.ResultDiscarded,
		},
		{
			name: "a qualified marker away from every projection of the result it names",
			files: mergeFiles(twoPackagesOneResultName, `package two

import (
	"planted/internal/alpha"
	"planted/internal/beta"
)

func project() {
	first, _ := alpha.Compute()
	_ = first.Verdict
	second, _ := beta.Compute()
	_ = second.Verdict
	_ = second.Hint
}
`, "internal/two/exemptions.go", `package two

// projection-omits internal/alpha.Answer.Hint: stated nowhere near the
// projection it excuses.
func unrelated() {}
`),
			wantRefusal: "sits away from every projection",
			wantAlso:    projectionparity.DroppedField,
		},
		{
			name: "a qualified marker naming a field the result has not got",
			files: mergeFiles(twoPackagesOneResultName, `package two

import (
	"planted/internal/alpha"
	"planted/internal/beta"
)

// projection-omits internal/alpha.Answer.Reason: a field this result does not
// carry at all.
func project() {
	first, _ := alpha.Compute()
	_ = first.Verdict
	second, _ := beta.Compute()
	_ = second.Verdict
	_ = second.Hint
}
`),
			wantRefusal: "carries no field of that name",
			wantAlso:    projectionparity.DroppedField,
		},
		{
			name: "a qualified marker stating no reason",
			files: mergeFiles(twoPackagesOneResultName, `package two

import (
	"planted/internal/alpha"
	"planted/internal/beta"
)

// projection-omits internal/alpha.Answer.Hint:
func project() {
	first, _ := alpha.Compute()
	_ = first.Verdict
	second, _ := beta.Compute()
	_ = second.Verdict
	_ = second.Hint
}
`),
			wantRefusal: "states no reason",
			wantAlso:    projectionparity.DroppedField,
		},
		{
			// The refusal that made the qualified form necessary, held so it
			// cannot quietly stop firing — and its message must name every result
			// it matched, because the list is what tells an author which packages
			// to choose between.
			name: "a bare marker on a name two packages declare still names both and refuses",
			files: mergeFiles(twoPackagesOneResultName, `package two

import (
	"planted/internal/alpha"
	"planted/internal/beta"
)

// projection-omits Answer.Hint: a bare name that reaches two results.
func project() {
	first, _ := alpha.Compute()
	_ = first.Verdict
	second, _ := beta.Compute()
	_ = second.Verdict
	_ = second.Hint
}
`),
			wantRefusal: "the shared results named Answer are internal/alpha.Answer, internal/beta.Answer",
			wantAlso:    projectionparity.DroppedField,
		},
		{
			// A shape that named neither form used to match no marker at all, so
			// the author's reason was ignored in silence. It is refused out loud
			// now, which is the same defect class as prescribing an unsatisfiable
			// marker: a reason nobody reads reads as a reason given.
			name: "a marker naming three parts with no package among them is refused out loud",
			files: mergeFiles(twoPackagesOneResultName, `package two

import (
	"planted/internal/alpha"
	"planted/internal/beta"
)

// projection-omits alpha.Answer.Hint: a package written without its directory,
// which names three parts and qualifies nothing.
func project() {
	first, _ := alpha.Compute()
	_ = first.Verdict
	second, _ := beta.Compute()
	_ = second.Verdict
	_ = second.Hint
}
`),
			wantRefusal: "it names three parts, alpha.Answer.Hint, with no package among them",
			wantAlso:    projectionparity.DroppedField,
		},
		{
			name: "a marker naming a package and no result in it is refused out loud",
			files: mergeFiles(twoPackagesOneResultName, `package two

import (
	"planted/internal/alpha"
	"planted/internal/beta"
)

// projection-omits internal/alpha: a package with no result named after it.
func project() {
	first, _ := alpha.Compute()
	_ = first.Verdict
	second, _ := beta.Compute()
	_ = second.Verdict
	_ = second.Hint
}
`),
			wantRefusal: "it names the package internal/alpha and no result in it",
			wantAlso:    projectionparity.DroppedField,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			corpus := row.surfaces
			if corpus == nil {
				corpus = surfaces()
			}
			result, err := projectionparity.Analyze(write(t, row.files), "planted", "internal/", corpus)
			if err != nil {
				t.Fatalf("run the projection read: %v", err)
			}

			report := projectionparity.Report(result.Findings)
			var refusals int
			for _, f := range result.Findings {
				if f.Kind == projectionparity.ExemptionUnsupported {
					refusals++
				}
			}
			if refusals != 1 {
				t.Fatalf("the read reports %d refusal(s) of the stated exemption, want exactly 1:\n%s", refusals, report)
			}
			if !strings.Contains(report, row.wantRefusal) {
				t.Fatalf("the right refusal shape fired for the wrong reason: no refusal says %q\n%s",
					row.wantRefusal, report)
			}
			if row.wantAlso == "" {
				return
			}
			var kept bool
			for _, f := range result.Findings {
				if f.Kind == row.wantAlso {
					kept = true
				}
			}
			if !kept {
				t.Fatalf("the refused exemption cleared the %s it was supposed to leave standing, which would make a "+
					"malformed marker a way to silence a finding:\n%s", row.wantAlso, report)
			}
		})
	}
}

// mergeFiles is twoPackagesOneResultName plus the second surface's own source,
// and any further path-and-body pairs a row needs.
func mergeFiles(base map[string]string, two string, extra ...string) map[string]string {
	out := map[string]string{"internal/two/two.go": two}
	for path, body := range base {
		out[path] = body
	}
	for i := 0; i+1 < len(extra); i += 2 {
		out[extra[i]] = extra[i+1]
	}
	return out
}

// TestTheReadNamesTheResultsNobodyProjectsRatherThanReportingThem pins the one
// case that is deliberately data rather than a finding.
//
// Where NO surface projects a result readably there was never a comparison to
// draw, so a finding would describe how far this read reaches rather than
// anything about the tree. Naming it in the census is what keeps a green from
// being read as covering it, and the difference between "reported as data" and
// "passed over in silence" is the whole point of asserting it.
func TestTheReadNamesTheResultsNobodyProjectsRatherThanReportingThem(t *testing.T) {
	t.Parallel()

	result := analyze(t, map[string]string{
		"internal/core/core.go": twoFieldCore,
		"internal/one/one.go": `package one

import "planted/internal/core"

var held core.Answer

func project() {
	answer, _ := core.Compute()
	held = answer
}
`,
		"internal/two/two.go": `package two

import "planted/internal/core"

var held core.Answer

func project() {
	answer, _ := core.Compute()
	held = answer
}
`,
	})

	if len(result.Findings) != 0 {
		t.Fatalf("the read reports %d finding(s) about a result nobody projects:\n%s",
			len(result.Findings), projectionparity.Report(result.Findings))
	}
	if want := []string{"internal/core.Answer"}; !slices.Equal(result.UnreadResultTypes, want) {
		t.Fatalf("the census names %v as unprojected, want %v", result.UnreadResultTypes, want)
	}
}

// TestTheReadRefusesACorpusItCannotCompare pins the refusals, because each of
// them is a state in which a clean result would describe the arguments rather
// than the tree.
func TestTheReadRefusesACorpusItCannotCompare(t *testing.T) {
	t.Parallel()

	root := write(t, map[string]string{"internal/core/core.go": twoFieldCore})

	for _, row := range []struct {
		name     string
		surfaces []projectionparity.Surface
		want     string
	}{
		{
			name:     "one surface",
			surfaces: []projectionparity.Surface{{Name: "the only surface", Dirs: []string{"internal/core"}}},
			want:     "parity needs two surfaces",
		},
		{
			name: "a surface with no name",
			surfaces: []projectionparity.Surface{
				{Dirs: []string{"internal/core"}},
				{Name: "named", Dirs: []string{"internal/core"}},
			},
			want: "no name",
		},
		{
			name: "a surface with no directory",
			surfaces: []projectionparity.Surface{
				{Name: "empty"},
				{Name: "named", Dirs: []string{"internal/core"}},
			},
			want: "no directory",
		},
		{
			name: "a directory holding no source",
			surfaces: []projectionparity.Surface{
				{Name: "absent", Dirs: []string{"internal/absent"}},
				{Name: "named", Dirs: []string{"internal/core"}},
			},
			want: "internal/absent",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			_, err := projectionparity.Analyze(root, "planted", "internal/", row.surfaces)
			if err == nil {
				t.Fatalf("the read returned no error for %s, so a clean result would certify nothing", row.name)
			}
			if !strings.Contains(err.Error(), row.want) {
				t.Fatalf("the refusal does not name %q: %v", row.want, err)
			}
		})
	}
}

// TestTheReadReportsTheSameBytesTwice pins the determinism the report depends
// on: findings are ordered by source position rather than by the iteration of
// any map, so the same tree reports the same text.
func TestTheReadReportsTheSameBytesTwice(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"internal/core/core.go": `package core

type Answer struct {
	Verdict bool
	Hint    string
	Detail  string
}

func Compute() (Answer, error) { return Answer{}, nil }

type Second struct {
	Count int
	Note  string
}

func Count() (Second, error) { return Second{}, nil }
`,
		"internal/one/one.go": `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
	_ = answer.Detail
	second, _ := core.Count()
	_ = second.Count
	_ = second.Note
}
`,
		"internal/two/two.go": `package two

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	second, _ := core.Count()
	_ = second.Count
}
`,
	}

	root := write(t, files)
	first, err := projectionparity.Analyze(root, "planted", "internal/", surfaces())
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	second, err := projectionparity.Analyze(root, "planted", "internal/", surfaces())
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if projectionparity.Report(first.Findings) != projectionparity.Report(second.Findings) {
		t.Fatalf("two reads of one tree report different text:\n--- first ---\n%s\n--- second ---\n%s",
			projectionparity.Report(first.Findings), projectionparity.Report(second.Findings))
	}
	if len(first.Findings) != 3 {
		t.Fatalf("the read reports %d finding(s) for three planted drops:\n%s",
			len(first.Findings), projectionparity.Report(first.Findings))
	}
}

// TestTheConstantReadDerivesADriversCorpus pins the derivation a driver holds
// its own mapping against, including the refusal over a directory declaring no
// such constant — the state in which a driver would derive an empty corpus and
// call it accounted for.
func TestTheConstantReadDerivesADriversCorpus(t *testing.T) {
	t.Parallel()

	root := write(t, map[string]string{
		"internal/vocab/vocab.go": `package vocab

type Surface string

const (
	SurfaceOne Surface = "the first surface"
	SurfaceTwo Surface = "the second surface"
)

type Other string

const OtherOne Other = "not a surface"
`,
	})

	got, err := projectionparity.ConstantValues(root, "internal/vocab", "Surface")
	if err != nil {
		t.Fatalf("read the declared surfaces: %v", err)
	}
	if want := []string{"the first surface", "the second surface"}; !slices.Equal(got, want) {
		t.Fatalf("the constant read returns %v, want %v", got, want)
	}

	if _, err := projectionparity.ConstantValues(root, "internal/vocab", "Absent"); err == nil {
		t.Fatal("the constant read cleared a type it declares no constant of, so a driver would derive an empty corpus and call it accounted for")
	}
}
