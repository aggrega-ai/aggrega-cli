// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// findingsText joins the findings' sentences one per line, the shape a reader
// meets them in.
func findingsText(findings []XAggregaFinding) string {
	var b strings.Builder
	for _, f := range findings {
		b.WriteString(f.Message())
		b.WriteString("\n")
	}
	return b.String()
}

// TestDiscourageDirectXAggregaInSchema_FiresOnSelector verifies that the
// walk returns a finding when x-aggrega-selector is authored inline in
// schema content.
func TestDiscourageDirectXAggregaInSchema_FiresOnSelector(t *testing.T) {
	in := `uses:
  primary_db:
    type: object
    x-aggrega-selector: 'component.aggrega.ai/type=stateful-server'
`
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(in), &node); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	findings := DiscourageDirectXAggregaInSchema("core/compute/api", &node)
	if len(findings) != 1 {
		t.Fatalf("expected one finding, got %d: %+v", len(findings), findings)
	}
	out := findingsText(findings)
	if !strings.Contains(out, "x-aggrega-selector") {
		t.Errorf("expected warning naming x-aggrega-selector; got %q", out)
	}
	if !strings.Contains(out, "core/compute/api") {
		t.Errorf("expected warning naming the entity; got %q", out)
	}
	if !strings.Contains(out, "usesSchema") {
		t.Errorf("expected warning to name the recommended path (usesSchema); got %q", out)
	}
	if !strings.Contains(out, "at </uses/primary_db/x-aggrega-selector>") {
		t.Errorf("expected warning to include the RFC 6901 JSON Pointer (bracketed form `at <...>`); got %q", out)
	}
}

// TestDiscourageDirectXAggregaInSchema_FiresOnInject verifies the walk
// catches x-aggrega-inject usage inline.
func TestDiscourageDirectXAggregaInSchema_FiresOnInject(t *testing.T) {
	in := `features:
  db_password:
    x-aggrega-inject:
      username: connection.user
`
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(in), &node); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	out := findingsText(DiscourageDirectXAggregaInSchema("core/compute/api", &node))
	if !strings.Contains(out, "x-aggrega-inject") {
		t.Errorf("expected warning naming x-aggrega-inject; got %q", out)
	}
}

// TestDiscourageDirectXAggregaInSchema_SilentOnCleanContent verifies the
// walk finds nothing when schema content carries no x-aggrega-* keys —
// the recommended authoring path (usesSchema / featuresSchema produces
// the extension keys via bridge synthesis, not via inline authoring).
func TestDiscourageDirectXAggregaInSchema_SilentOnCleanContent(t *testing.T) {
	in := `values:
  port:
    type: integer
    minimum: 1024
    maximum: 65535
provides:
  host:
    type: string
`
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(in), &node); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if findings := DiscourageDirectXAggregaInSchema("core/compute/api", &node); len(findings) != 0 {
		t.Errorf("expected no finding on clean content; got %+v", findings)
	}
}

// TestDiscourageDirectXAggregaInSchema_NilSafeBoundary verifies nil-node
// and zero-Kind inputs are no-ops.
func TestDiscourageDirectXAggregaInSchema_NilSafeBoundary(t *testing.T) {
	var zero yaml.Node
	if findings := DiscourageDirectXAggregaInSchema("x", nil); len(findings) != 0 {
		t.Errorf("expected no finding on a nil input; got %+v", findings)
	}
	if findings := DiscourageDirectXAggregaInSchema("x", &zero); len(findings) != 0 {
		t.Errorf("expected no finding on a zero input; got %+v", findings)
	}
}

// TestDiscourageDirectXAggregaInSchema_WritesNothing holds the check to
// reporting through its return value alone: the process's log and its
// standard error stay empty while it finds something, so whoever reads the
// finding decides where it is shown and how often.
func TestDiscourageDirectXAggregaInSchema_WritesNothing(t *testing.T) {
	var logBuf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(prev)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	saved := os.Stderr
	os.Stderr = w

	var node yaml.Node
	if err := yaml.Unmarshal([]byte("values:\n  x-aggrega-probe: planted\n"), &node); err != nil {
		os.Stderr = saved
		t.Fatalf("unmarshal: %v", err)
	}
	findings := DiscourageDirectXAggregaInSchema("core/datastore/mariadb", &node)

	os.Stderr = saved
	_ = w.Close()
	var errBuf bytes.Buffer
	_, _ = errBuf.ReadFrom(r)
	_ = r.Close()

	if len(findings) != 1 {
		t.Fatalf("expected one finding, got %+v", findings)
	}
	if logBuf.Len() != 0 {
		t.Errorf("the check wrote to the log: %q", logBuf.String())
	}
	if errBuf.Len() != 0 {
		t.Errorf("the check wrote to standard error: %q", errBuf.String())
	}
}
