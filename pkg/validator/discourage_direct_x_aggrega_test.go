// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestDiscourageDirectXAggregaInSchema_FiresOnSelector verifies that the
// walk emits a warn-level log entry when x-aggrega-selector is authored
// inline in schema content.
func TestDiscourageDirectXAggregaInSchema_FiresOnSelector(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	in := `uses:
  primary_db:
    type: object
    x-aggrega-selector: 'component.aggrega.ai/type=stateful-server'
`
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(in), &node); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	DiscourageDirectXAggregaInSchema("core/server/api", &node)

	out := buf.String()
	if !strings.Contains(out, "x-aggrega-selector") {
		t.Errorf("expected warning naming x-aggrega-selector; got %q", out)
	}
	if !strings.Contains(out, "core/server/api") {
		t.Errorf("expected warning naming the entity; got %q", out)
	}
	if !strings.Contains(out, "usesSchema") {
		t.Errorf("expected warning to name the recommended path (usesSchema); got %q", out)
	}
	if !strings.Contains(out, "at </") {
		t.Errorf("expected warning to include the RFC 6901 JSON Pointer (bracketed form `at <...>`); got %q", out)
	}
}

// TestDiscourageDirectXAggregaInSchema_FiresOnInject verifies the walk
// catches x-aggrega-inject usage inline.
func TestDiscourageDirectXAggregaInSchema_FiresOnInject(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	in := `secrets:
  db_password:
    x-aggrega-inject:
      username: connection.user
`
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(in), &node); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	DiscourageDirectXAggregaInSchema("core/server/api", &node)

	out := buf.String()
	if !strings.Contains(out, "x-aggrega-inject") {
		t.Errorf("expected warning naming x-aggrega-inject; got %q", out)
	}
}

// TestDiscourageDirectXAggregaInSchema_SilentOnCleanContent verifies the
// walk emits nothing when schema content carries no x-aggrega-* keys —
// the recommended authoring path (usesSchema / secretsSchema produces
// the extension keys via bridge synthesis, not via inline authoring).
func TestDiscourageDirectXAggregaInSchema_SilentOnCleanContent(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

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

	DiscourageDirectXAggregaInSchema("core/server/api", &node)

	if buf.Len() != 0 {
		t.Errorf("expected no warning on clean content; got %q", buf.String())
	}
}

// TestDiscourageDirectXAggregaInSchema_NilSafeBoundary verifies nil-node
// and zero-Kind inputs are no-ops.
func TestDiscourageDirectXAggregaInSchema_NilSafeBoundary(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	DiscourageDirectXAggregaInSchema("x", nil)
	var zero yaml.Node
	DiscourageDirectXAggregaInSchema("x", &zero)

	if buf.Len() != 0 {
		t.Errorf("expected no warning on nil/zero inputs; got %q", buf.String())
	}
}
