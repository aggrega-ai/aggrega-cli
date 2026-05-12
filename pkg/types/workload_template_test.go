// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestWorkloadTemplate_RoundTripInline pins mode 1.
func TestWorkloadTemplate_RoundTripInline(t *testing.T) {
	t.Parallel()
	in := `workload: |
  apiVersion: aggrega.ai/v1alpha1
  kind: Workload
  containers:
    main:
      image: app:1.0
`
	type wrap struct {
		Workload WorkloadTemplate `yaml:"workload"`
	}
	var w wrap
	if err := yaml.Unmarshal([]byte(in), &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if w.Workload.Inline == "" {
		t.Fatal("expected Inline populated")
	}
	if w.Workload.Import != "" {
		t.Errorf("expected Import empty, got %q", w.Workload.Import)
	}
}

// TestWorkloadTemplate_RoundTripFileImport pins mode 2.
func TestWorkloadTemplate_RoundTripFileImport(t *testing.T) {
	t.Parallel()
	in := `workload:
  $import: ./workload.yaml
`
	type wrap struct {
		Workload WorkloadTemplate `yaml:"workload"`
	}
	var w wrap
	if err := yaml.Unmarshal([]byte(in), &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if w.Workload.Import != "./workload.yaml" {
		t.Errorf("expected Import = './workload.yaml'; got %q", w.Workload.Import)
	}
	out, err := yaml.Marshal(&w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "$import: ./workload.yaml") {
		t.Errorf("round-trip lost $import; got:\n%s", out)
	}
}

// TestWorkloadTemplate_RejectsMapWithoutImport pins the shape error.
func TestWorkloadTemplate_RejectsMapWithoutImport(t *testing.T) {
	t.Parallel()
	in := `workload:
  someKey: value
`
	type wrap struct {
		Workload WorkloadTemplate `yaml:"workload"`
	}
	var w wrap
	err := yaml.Unmarshal([]byte(in), &w)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrWorkloadTemplateShape) {
		t.Errorf("expected ErrWorkloadTemplateShape; got %v", err)
	}
}

// TestWorkloadTemplate_OmitemptyElides pins zero-value elision.
func TestWorkloadTemplate_OmitemptyElides(t *testing.T) {
	t.Parallel()
	type wrap struct {
		Name     string           `yaml:"name"`
		Workload WorkloadTemplate `yaml:"workload,omitempty"`
	}
	w := wrap{Name: "norkload"}
	out, err := yaml.Marshal(&w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "workload") {
		t.Errorf("expected workload key elided; got:\n%s", out)
	}
}
