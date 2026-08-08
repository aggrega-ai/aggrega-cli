// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"testing"
	"text/template"
)

// TestExtractPathRoot verifies extractPath returns empty for a root
// validation failure.
func TestExtractPathRoot(t *testing.T) {
	t.Parallel()

	err := &stubErr{msg: "validating root: required: missing properties: [\"name\"]"}
	if got := extractPath(err); got != "" {
		t.Errorf("root validation should produce empty path; got %q", got)
	}
}

// TestExtractPathSingleProperty verifies extractPath returns the
// single property name for a one-level-deep validation failure.
func TestExtractPathSingleProperty(t *testing.T) {
	t.Parallel()

	err := &stubErr{msg: "validating root: validating /properties/port: minimum: 80 < 1024"}
	if got := extractPath(err); got != "port" {
		t.Errorf("expected `port`; got %q", got)
	}
}

// TestExtractPathNested verifies extractPath traverses multiple
// /properties/<name> segments.
func TestExtractPathNested(t *testing.T) {
	t.Parallel()

	err := &stubErr{msg: "validating root: validating /properties/server: validating /properties/server/properties/listenPort: minimum: 80 < 1024"}
	if got := extractPath(err); got != "server.listenPort" {
		t.Errorf("expected `server.listenPort`; got %q", got)
	}
}

// TestExtractPathSkipsThenElse verifies extractPath skips `then` and
// `else` segments — those are schema-only positions that don't advance
// the instance path.
func TestExtractPathSkipsThenElse(t *testing.T) {
	t.Parallel()

	err := &stubErr{msg: "validating root: validating /properties/tlsConfig: validating /properties/tlsConfig/then: required: missing properties: [\"certPath\"]"}
	if got := extractPath(err); got != "tlsConfig" {
		t.Errorf("expected `tlsConfig` (then segment skipped); got %q", got)
	}
}

// TestExtractPathNilErr verifies extractPath returns empty on nil.
func TestExtractPathNilErr(t *testing.T) {
	t.Parallel()

	if got := extractPath(nil); got != "" {
		t.Errorf("nil err should produce empty path; got %q", got)
	}
}

// TestExtractPathNoValidatingPrefix verifies extractPath returns empty
// for an error message without the `validating ` marker (defensive).
func TestExtractPathNoValidatingPrefix(t *testing.T) {
	t.Parallel()

	err := &stubErr{msg: "some unrelated error"}
	if got := extractPath(err); got != "" {
		t.Errorf("unrecognised message should produce empty path; got %q", got)
	}
}

// TestNavigateMissingPath verifies navigate returns (nil, false)
// when the path does not exist in the instance.
func TestNavigateMissingPath(t *testing.T) {
	t.Parallel()

	instance := map[string]any{"foo": "bar"}
	v, ok := navigate(instance, []string{"missing"})
	if ok {
		t.Errorf("navigate should return ok=false for missing key; got v=%v", v)
	}
}

// TestNavigateThroughScalar verifies navigate returns (nil, false)
// when the path traverses through a non-map value.
func TestNavigateThroughScalar(t *testing.T) {
	t.Parallel()

	instance := map[string]any{"foo": "scalar"}
	v, ok := navigate(instance, []string{"foo", "sub"})
	if ok {
		t.Errorf("navigate through scalar should return ok=false; got v=%v", v)
	}
}

// TestIsUnderPath verifies isUnderPath's prefix-match semantics.
func TestIsUnderPath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		child, parent string
		want          bool
	}{
		{"foo", "foo", true},
		{"foo.bar", "foo", true},
		{"foo.bar.baz", "foo", true},
		{"foo.bar", "foo.b", false}, // siblings sharing prefix do NOT match
		{"foobar", "foo", false},    // strict path separator required
		{"", "foo", false},
		{"foo", "", false}, // empty parent never matches (defensive)
	}

	for _, c := range cases {
		if got := isUnderPath(c.child, c.parent); got != c.want {
			t.Errorf("isUnderPath(%q, %q) = %v; want %v", c.child, c.parent, got, c.want)
		}
	}
}

// TestFilterOutPathEmpty verifies filterOutPath with empty path
// returns nil (defensive — empty path would match everything).
func TestFilterOutPathEmpty(t *testing.T) {
	t.Parallel()

	findings := []Finding{{FieldPath: "foo"}, {FieldPath: "bar"}}
	got := filterOutPath(findings, "")
	if got != nil {
		t.Errorf("filterOutPath with empty path should return nil; got %v", got)
	}
}

// stubErr is a minimal error type for testing the path extractor.
type stubErr struct {
	msg string
}

func (e *stubErr) Error() string { return e.msg }

// TestAdapterErrorFormat verifies AdapterError's Error and Unwrap.
func TestAdapterErrorFormat(t *testing.T) {
	t.Parallel()

	ae := &AdapterError{
		Path:    "foo.bar",
		Keyword: "x-aggrega-when",
		Message: "template parse: unexpected",
		Err:     ErrExtensionEval(),
	}
	if ae.Unwrap() != ErrExtensionEval() {
		t.Errorf("Unwrap should return the sentinel; got %v", ae.Unwrap())
	}
	msg := ae.Error()
	for _, want := range []string{"foo.bar", "x-aggrega-when", "template parse"} {
		if !contains(msg, want) {
			t.Errorf("Error message missing %q; got %q", want, msg)
		}
	}
	// Path-less + keyword-less form.
	ae2 := &AdapterError{Message: "schema is nil", Err: ErrSchemaResolve()}
	msg2 := ae2.Error()
	if !contains(msg2, "schema is nil") {
		t.Errorf("Error should contain message; got %q", msg2)
	}
}

// TestEvaluateTemplateFalseLiteral verifies that "false" output parses
// as falsy.
func TestEvaluateTemplateFalseLiteral(t *testing.T) {
	t.Parallel()

	tpl := mustParseTemplate(t, `{{ if eq . "x" }}true{{ else }}false{{ end }}`)
	truthy, err := evaluateTemplate(tpl, "y", nil)
	if err != nil {
		t.Fatalf("evaluateTemplate: %v", err)
	}
	if truthy {
		t.Errorf("`false` output should parse as falsy")
	}
}

// TestEvaluateTemplateNumericTruthy verifies numeric outputs follow
// Go template boolean convention (nonzero truthy).
func TestEvaluateTemplateNumericTruthy(t *testing.T) {
	t.Parallel()

	tpl := mustParseTemplate(t, `{{ . }}`)
	cases := []struct {
		data any
		want bool
	}{
		{42, true},
		{0, false},
		{-1, true}, // nonzero
		{1.5, true},
	}
	for _, c := range cases {
		got, err := evaluateTemplate(tpl, c.data, nil)
		if err != nil {
			t.Errorf("evaluateTemplate(%v): %v", c.data, err)
			continue
		}
		if got != c.want {
			t.Errorf("evaluateTemplate(%v) = %v; want %v", c.data, got, c.want)
		}
	}
}

// TestEvaluateTemplateOtherString verifies non-empty non-boolean
// strings are truthy.
func TestEvaluateTemplateOtherString(t *testing.T) {
	t.Parallel()

	tpl := mustParseTemplate(t, `{{ . }}`)
	got, err := evaluateTemplate(tpl, "hello", nil)
	if err != nil {
		t.Fatalf("evaluateTemplate: %v", err)
	}
	if !got {
		t.Errorf("non-empty non-boolean string should be truthy")
	}
}

// TestReportSort verifies Report.sort orders findings by FieldPath,
// then Keyword, then Message — the tertiary key disambiguates equal
// FieldPath+Keyword.
func TestReportSort(t *testing.T) {
	t.Parallel()

	rpt := &Report{
		Findings: []Finding{
			{FieldPath: "b", Keyword: "k1", Message: "alpha"},
			{FieldPath: "a", Keyword: "k2", Message: "delta"},
			{FieldPath: "a", Keyword: "k1", Message: "charlie"},
			{FieldPath: "a", Keyword: "k1", Message: "bravo"},
		},
	}
	rpt.sort()

	wantOrder := []string{"bravo", "charlie", "delta", "alpha"}
	for i, f := range rpt.Findings {
		if f.Message != wantOrder[i] {
			t.Errorf("position %d: got Message=%q; want %q", i, f.Message, wantOrder[i])
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func mustParseTemplate(t *testing.T, src string) *template.Template {
	t.Helper()
	tpl, err := template.New("t").Funcs(sprigFuncs).Parse(src)
	if err != nil {
		t.Fatalf("template parse: %v", err)
	}
	return tpl
}

// TestIsUnderPathEdge: covered by TestIsUnderPath; included separately
// as a reminder that isUnderPath's defensive empty-parent case is
// intentional — an empty parent never matches because the alternative
// (empty matches all) would silently drop all findings on a root-level
// falsy when-node.

// TestLibraryErrorToFindingsNil verifies the nil-err early-return
// branches in libraryErrorToFindings* helpers.
func TestLibraryErrorToFindingsNil(t *testing.T) {
	t.Parallel()

	a := &googleAdapter{}
	rpt := &Report{}
	a.libraryErrorToFindings(rpt, nil, nil)
	if len(rpt.Findings) != 0 {
		t.Errorf("nil err should produce no findings; got %v", rpt.Findings)
	}
	a.libraryErrorToFindingsWithPathPrefix(rpt, nil, "foo", nil)
	if len(rpt.Findings) != 0 {
		t.Errorf("nil err prefix variant should produce no findings; got %v", rpt.Findings)
	}
}

// TestLibraryErrorToFindingsWithErr verifies the active path of
// libraryErrorToFindings (not just the nil-early-return).
func TestLibraryErrorToFindingsWithErr(t *testing.T) {
	t.Parallel()

	a := &googleAdapter{}
	rpt := &Report{}
	a.libraryErrorToFindings(rpt, &stubErr{msg: "validating root: validating /properties/name: pattern fail"}, nil)
	if len(rpt.Findings) != 1 {
		t.Fatalf("expected 1 finding; got %d", len(rpt.Findings))
	}
	if rpt.Findings[0].FieldPath != "name" {
		t.Errorf("FieldPath should be `name`; got %q", rpt.Findings[0].FieldPath)
	}
}

// TestLibraryErrorToFindingsWithPathPrefix verifies the path-prefix
// variant rebases sub-instance findings to the parent path.
func TestLibraryErrorToFindingsWithPathPrefixActive(t *testing.T) {
	t.Parallel()

	a := &googleAdapter{}
	rpt := &Report{}
	a.libraryErrorToFindingsWithPathPrefix(
		rpt,
		&stubErr{msg: "validating root: validating /properties/inner: minimum fail"},
		"outer",
		nil,
	)
	if len(rpt.Findings) != 1 {
		t.Fatalf("expected 1 finding; got %d", len(rpt.Findings))
	}
	if rpt.Findings[0].FieldPath != "outer.inner" {
		t.Errorf("FieldPath should be `outer.inner`; got %q", rpt.Findings[0].FieldPath)
	}

	// Empty subPath case: extracted path is empty, full path is just the prefix.
	rpt2 := &Report{}
	a.libraryErrorToFindingsWithPathPrefix(
		rpt2,
		&stubErr{msg: "validating root: required: missing properties: [\"X\"]"},
		"outer",
		nil,
	)
	if len(rpt2.Findings) != 1 {
		t.Fatalf("expected 1 finding; got %d", len(rpt2.Findings))
	}
	if rpt2.Findings[0].FieldPath != "outer" {
		t.Errorf("FieldPath should be `outer` (no subpath); got %q", rpt2.Findings[0].FieldPath)
	}
}

// TestJoinPathEmpty verifies joinPath returns "" for an empty slice.
func TestJoinPathEmpty(t *testing.T) {
	t.Parallel()

	if got := joinPath(nil); got != "" {
		t.Errorf("nil path should join to empty string; got %q", got)
	}
	if got := joinPath([]string{}); got != "" {
		t.Errorf("empty slice should join to empty string; got %q", got)
	}
}

// TestEvaluateTemplateEmptyOutput verifies empty template output
// parses as falsy.
func TestEvaluateTemplateEmptyOutput(t *testing.T) {
	t.Parallel()

	tpl := mustParseTemplate(t, ``)
	truthy, err := evaluateTemplate(tpl, "anything", nil)
	if err != nil {
		t.Fatalf("evaluateTemplate: %v", err)
	}
	if truthy {
		t.Errorf("empty output should parse as falsy")
	}
}

// TestReportSortSingleFinding verifies Report.sort is a no-op for
// fewer-than-2 findings (covers the early-return branch).
func TestReportSortSingleFinding(t *testing.T) {
	t.Parallel()

	rpt := &Report{Findings: []Finding{{FieldPath: "x"}}}
	rpt.sort()
	if len(rpt.Findings) != 1 {
		t.Errorf("single-finding sort should not change length; got %d", len(rpt.Findings))
	}

	empty := &Report{}
	empty.sort()
	if len(empty.Findings) != 0 {
		t.Errorf("empty report sort should not produce findings; got %d", len(empty.Findings))
	}

	var nilRpt *Report
	nilRpt.sort() // should not panic
}

// TestFilterOutPathKeepsSiblings verifies filterOutPath keeps findings
// whose FieldPath is not under the dropped path (covers the keep
// branch).
func TestFilterOutPathKeepsSiblings(t *testing.T) {
	t.Parallel()

	findings := []Finding{
		{FieldPath: "foo"},
		{FieldPath: "foo.bar"},
		{FieldPath: "sibling"},
		{FieldPath: "other.nested"},
	}
	got := filterOutPath(findings, "foo")
	if len(got) != 2 {
		t.Fatalf("expected 2 findings (sibling + other.nested); got %d (%v)", len(got), got)
	}
	for _, f := range got {
		if f.FieldPath == "foo" || f.FieldPath == "foo.bar" {
			t.Errorf("foo subtree should be dropped; got %v", f)
		}
	}
}
