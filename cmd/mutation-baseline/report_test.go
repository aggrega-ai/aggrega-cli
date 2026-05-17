// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"go/token"
	"strings"
	"testing"
)

// TestWriteMarkdown_AllSections pins the report's sectional structure
// and the aggregation arithmetic with a small synthetic result set.
func TestWriteMarkdown_AllSections(t *testing.T) {
	t.Parallel()
	root := "/aggrega-cli"
	results := []Result{
		{
			Mutation: Mutation{
				File:        root + "/pkg/a/x.go",
				Operator:    "equality_to_inequality",
				Position:    token.Position{Line: 10, Column: 5},
				Original:    "==",
				Replacement: "!=",
			},
			Outcome: OutcomeKilled,
		},
		{
			Mutation: Mutation{
				File:        root + "/pkg/a/x.go",
				Operator:    "return_true_to_false",
				Position:    token.Position{Line: 20, Column: 3},
				Original:    "true",
				Replacement: "false",
			},
			Outcome: OutcomeSurvived,
		},
		{
			Mutation: Mutation{
				File:        root + "/pkg/b/y.go",
				Operator:    "logical_and_to_or",
				Position:    token.Position{Line: 5, Column: 7},
				Original:    "&&",
				Replacement: "||",
			},
			Outcome: OutcomeEquivalent,
		},
	}
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, results, root, "abc123", "2026-05-17"); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}
	out := buf.String()

	// Header presence.
	if !strings.Contains(out, "# Mutation Baseline") {
		t.Errorf("missing header")
	}
	if !strings.Contains(out, "2026-05-17") {
		t.Errorf("missing date")
	}
	if !strings.Contains(out, "abc123") {
		t.Errorf("missing commit")
	}

	// Per-package score table presence.
	if !strings.Contains(out, "## Per-package mutation scores") {
		t.Errorf("missing per-package section")
	}
	if !strings.Contains(out, "`pkg/a`") {
		t.Errorf("missing pkg/a row")
	}
	if !strings.Contains(out, "`pkg/b`") {
		t.Errorf("missing pkg/b row")
	}

	// pkg/a score = 1/2 = 50.0%.
	if !strings.Contains(out, "50.0%") {
		t.Errorf("missing pkg/a 50%% score; out:\n%s", out)
	}

	// Surviving mutations section.
	if !strings.Contains(out, "## Surviving mutations") {
		t.Errorf("missing survivors section")
	}
	if !strings.Contains(out, "return_true_to_false") {
		t.Errorf("missing survivor's operator name")
	}
	if !strings.Contains(out, "pkg/a/x.go:20:3") {
		t.Errorf("missing survivor's file:line:col")
	}

	// Acceptable / Actionable templates present.
	if !strings.Contains(out, "## Acceptable survivors") {
		t.Errorf("missing acceptable template")
	}
	if !strings.Contains(out, "## Actionable survivors") {
		t.Errorf("missing actionable template")
	}
}

// TestWriteMarkdown_NoSurvivors pins the all-killed message: when zero
// mutations survive, the section emits a "none" message rather than an
// empty list.
func TestWriteMarkdown_NoSurvivors(t *testing.T) {
	t.Parallel()
	root := "/aggrega-cli"
	results := []Result{
		{
			Mutation: Mutation{
				File:     root + "/pkg/a/x.go",
				Operator: "equality_to_inequality",
				Original: "==", Replacement: "!=",
			},
			Outcome: OutcomeKilled,
		},
	}
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, results, root, "abc", "2026-05-17"); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "None — every mutation was killed") {
		t.Errorf("missing none message; out:\n%s", out)
	}
}

// TestSortedKeys pins lexicographic key ordering.
func TestSortedKeys(t *testing.T) {
	t.Parallel()
	m := map[string]int{"zebra": 1, "apple": 2, "mango": 3}
	got := sortedKeys(m)
	want := []string{"apple", "mango", "zebra"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q; want %q", i, got[i], want[i])
		}
	}
	// Empty.
	if len(sortedKeys(map[string]int{})) != 0 {
		t.Errorf("empty → empty slice")
	}
}

// TestPackageRelPath_FileOutsideRoot pins the fallback when the file
// isn't under the aggregaCli root.
func TestPackageRelPath_FileOutsideRoot(t *testing.T) {
	t.Parallel()
	got := packageRelPath("/elsewhere/foo/bar.go", "/aggrega-cli")
	if got != "/elsewhere/foo/bar.go" {
		t.Errorf("outside root: got %q; want full path", got)
	}
}

// TestFindSingleSmokeMutation_HappyAndMiss pins the smoke-mutation
// finder against the smokeprobe package files.
func TestFindSingleSmokeMutation_HappyAndMiss(t *testing.T) {
	t.Parallel()
	// Use the actual smokeprobe files — stable across refactors.
	root, err := findAggregaCliRoot()
	if err != nil {
		t.Skip("cannot find aggrega-cli root from test environment")
	}
	probeFile := root + "/cmd/mutation-baseline/smokeprobe/probe.go"
	m, err := findSingleSmokeMutation(probeFile, "return_true_to_false")
	if err != nil {
		t.Fatalf("happy: %v", err)
	}
	if m.Operator != "return_true_to_false" {
		t.Errorf("got %q; want return_true_to_false", m.Operator)
	}

	// Look for an operator that doesn't appear in probe.go.
	_, err = findSingleSmokeMutation(probeFile, "logical_and_to_or")
	if err == nil {
		t.Errorf("missing operator should error")
	}
}
