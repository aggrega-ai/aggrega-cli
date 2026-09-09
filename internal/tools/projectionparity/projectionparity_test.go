// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package projectionparity_test

import (
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
