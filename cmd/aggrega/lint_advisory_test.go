// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// A Component whose schema declares the same property ("port") in both pillars —
// a schema-pillar overlap, which is advisory (a lint warning), not a validation
// error, so the catalog still loads and validates clean.
const lintOverlapComponentYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/overlap-srv
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port:
        type: integer
    infra:
      port:
        type: integer
`

// TestLintCatalogWide_SurfacesOverlapAdvisory pins that aggrega lint now prints
// the whole-catalog schema-pillar-overlap advisory it computes — as a notice:
// line, informational (lint still exits 0; overlap never escalates the result).
func TestLintCatalogWide_SurfacesOverlapAdvisory(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "overlap.yaml"), lintOverlapComponentYAML)

	var buf bytes.Buffer
	if err := validateCatalogWide(t.Context(), "", "", []string{dir}, nil, nil, &buf, io.Discard); err != nil {
		t.Fatalf("overlap is advisory; lint must still pass (exit 0): %v", err)
	}
	out := buf.String()
	for _, want := range []string{"notice:", "core/overlap-srv", "port"} {
		if !strings.Contains(out, want) {
			t.Errorf("advisory output missing %q; got diagnostics:\n%s", want, out)
		}
	}
}

// TestLintCatalogWide_CleanCatalogNoAdvisory is the non-vacuity companion: a
// catalog with no overlap emits no notice and still passes.
func TestLintCatalogWide_CleanCatalogNoAdvisory(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "clean.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/clean-srv
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)

	var buf bytes.Buffer
	if err := validateCatalogWide(t.Context(), "", "", []string{dir}, nil, nil, &buf, io.Discard); err != nil {
		t.Fatalf("clean catalog must pass: %v", err)
	}
	if strings.Contains(buf.String(), "notice:") {
		t.Errorf("clean catalog must emit no notice; got diagnostics:\n%s", buf.String())
	}
}

// TestLintCatalogWide_AdvisoryDeterministicOrder pins stable advisory ordering:
// two overlap-tripping entities produce byte-identical advisory output across
// runs, in sorted order — lintWarnings is accumulated over sorted-key walks.
func TestLintCatalogWide_AdvisoryDeterministicOrder(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "z.yaml"), strings.Replace(lintOverlapComponentYAML, "core/overlap-srv", "core/zzz-srv", 1))
	writeFixture(t, filepath.Join(dir, "a.yaml"), strings.Replace(lintOverlapComponentYAML, "core/overlap-srv", "core/aaa-srv", 1))

	var buf1, buf2 bytes.Buffer
	_ = validateCatalogWide(t.Context(), "", "", []string{dir}, nil, nil, &buf1, io.Discard)
	_ = validateCatalogWide(t.Context(), "", "", []string{dir}, nil, nil, &buf2, io.Discard)
	out1, out2 := buf1.String(), buf2.String()
	if out1 != out2 {
		t.Errorf("advisory output not deterministic across runs:\n--run1--\n%s\n--run2--\n%s", out1, out2)
	}
	if ia, iz := strings.Index(out1, "core/aaa-srv"), strings.Index(out1, "core/zzz-srv"); ia < 0 || iz < 0 || ia > iz {
		t.Errorf("expected both overlap notices in sorted order (aaa before zzz); got:\n%s", out1)
	}
}
