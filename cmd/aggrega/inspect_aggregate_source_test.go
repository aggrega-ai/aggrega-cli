// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bothSpellingsAggregate authors the same parameter reference in both spellings
// in one values block, alongside values that are not references at all. Neither
// spelling can render a value — the document declares no deployment supplying
// one — so what each must print is the expression the author wrote.
const bothSpellingsAggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: spelling
  version: "1.0.0"
spec:
  parameters:
    replica_count:
      type: integer
      default: 3
    db_size:
      type: string
      default: 10Gi
  components:
    api:
      type: core/compute/api-server
      values:
        bare_ref: {{ .parameters.replica_count }}
        quoted_ref: "{{ .parameters.db_size }}"
        tight: {{.parameters.replica_count}}
        commented: {{ .parameters.db_size }}   # an authoring note
        nested:
          deep_bare: {{ .parameters.replica_count }}
          ordinary: 7
        plain: hello
`

// writeAggregateFile writes body into a fresh directory and returns the file
// path, for driving dispatchInspect against a standalone Aggregate document.
func writeAggregateFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "aggregate.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write aggregate: %v", err)
	}
	return path
}

// inspectAggregateOutput renders a standalone Aggregate file and returns what
// the command printed.
func inspectAggregateOutput(t *testing.T, path string) string {
	t.Helper()
	var buf bytes.Buffer
	dir := filepath.Dir(path)
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, []string{dir}, testInspectOptions(&buf)); err != nil {
		t.Fatalf("inspecting the Aggregate should succeed; got: %v", err)
	}
	return buf.String()
}

// TestInspectAggregate_BothReferenceSpellingsPrintTheirExpression proves the two
// spellings of a parameter reference render alike: each prints the expression
// text its author wrote, under the key its author wrote it against.
//
// An unquoted reference is well-formed YAML that reads as a mapping rather than
// a scalar, so reading the document for structure cannot preserve the
// expression's own text. Both spellings are asserted in one run because the
// quoted one is the reference the unquoted one has to match, and a run that
// checked only the unquoted spelling could not tell a repair from a regression
// that had moved the defect across.
func TestInspectAggregate_BothReferenceSpellingsPrintTheirExpression(t *testing.T) {
	t.Parallel()
	out := inspectAggregateOutput(t, writeAggregateFile(t, bothSpellingsAggregate))

	for _, want := range []string{
		"bare_ref: {{ .parameters.replica_count }}",
		`quoted_ref: {{ .parameters.db_size }}`,
		"tight: {{.parameters.replica_count}}",
		"commented: {{ .parameters.db_size }}",
		"nested.deep_bare: {{ .parameters.replica_count }}",
		"nested.ordinary: 7",
		"plain: hello",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
}

// TestInspectAggregate_ReferenceRendersNoInternalKeyOrValue pins the two ways
// the rendered line used to lose the author's text: the substituted key
// standing where the expression was, and the empty value printed beside it.
// Neither may appear, and no reference may render as a resolved value — the
// document supplies none.
func TestInspectAggregate_ReferenceRendersNoInternalKeyOrValue(t *testing.T) {
	t.Parallel()
	out := inspectAggregateOutput(t, writeAggregateFile(t, bothSpellingsAggregate))

	for _, unwanted := range []string{"template-key", "<nil>"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("output should not carry %q; got:\n%s", unwanted, out)
		}
	}
	// The declaration's default is rendered in the parameters block; no
	// reference in the values block may render as one.
	if strings.Contains(out, "bare_ref: 3") || strings.Contains(out, "quoted_ref: 10Gi") {
		t.Errorf("a reference rendered as a value; got:\n%s", out)
	}
	// The trailing comment sits outside the expression and is not part of it.
	if strings.Contains(out, "an authoring note") {
		t.Errorf("recovered text swallowed a trailing comment; got:\n%s", out)
	}
}

// complexKeyAggregate authors a mapping whose key is a sequence — legal YAML
// that no template wrote. It shares the shape a reference takes, so it pins that
// recovery is bounded to expressions rather than to every mapping the structural
// read cannot carry.
const complexKeyAggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: complex
  version: "1.0.0"
spec:
  components:
    api:
      type: core/compute/api-server
      values:
        genuine:
          ? [a, b]
          : c
`

// TestInspectAggregate_ComplexKeyIsNotReportedAsAnExpression proves a mapping
// with a genuinely complex key is left as it renders rather than being reported
// as an authored expression, so the display never invents text for a value
// nobody wrote as a reference.
func TestInspectAggregate_ComplexKeyIsNotReportedAsAnExpression(t *testing.T) {
	t.Parallel()
	out := inspectAggregateOutput(t, writeAggregateFile(t, complexKeyAggregate))

	if strings.Contains(out, "genuine: {{") {
		t.Errorf("a complex-keyed mapping was reported as an expression; got:\n%s", out)
	}
	if !strings.Contains(out, "genuine.") {
		t.Errorf("output missing the complex-keyed value's own rendering; got:\n%s", out)
	}
}
