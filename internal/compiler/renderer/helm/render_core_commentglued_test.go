// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
)

// TestHelmEngine_Sourced_CommentGluedSeparator pins the comment-glued separator
// tolerance end-to-end: a chart whose template chomps whitespace with {{- -}} so
// a comment is glued directly onto the "---" document separator ("---# a
// comment") renders each document as its own output file, keyed by resource
// identity and carrying injected provenance labels, exactly as a whitespace-
// padded separator would.
func TestHelmEngine_Sourced_CommentGluedSeparator(t *testing.T) {
	t.Parallel()
	const (
		wantConfig   = "cg-configmap-rbac-config.yaml"
		wantApprover = "cg-clusterrole-cert-approver.yaml"
		wantReviewer = "cg-clusterrole-cert-reviewer.yaml"
	)

	files, err := helm.RenderLoadedChart(context.Background(), loadChart(t, "testdata/comment-glued-doc-chart"),
		map[string]any{}, nodeCtx("cg", "testdata/comment-glued-doc-chart"), helm.NewHermeticValuesValidator(forbidFetch(t)))
	if err != nil {
		t.Fatalf("a comment-glued document separator must split, not halt: %v", err)
	}

	for _, k := range sortedFileKeys(files) {
		t.Logf("=== %s ===\n%s", k, files[k])
	}

	if len(files) != 3 {
		t.Fatalf("expected 3 output files; got %d: %v", len(files), sortedFileKeys(files))
	}

	config, ok := files[wantConfig]
	if !ok {
		t.Fatalf("no ConfigMap output at %q; files: %v", wantConfig, sortedFileKeys(files))
	}
	if !strings.Contains(string(config), "key: value") {
		t.Errorf("ConfigMap content missing; got:\n%s", config)
	}

	approver, ok := files[wantApprover]
	if !ok {
		t.Fatalf("no approver ClusterRole output at %q; files: %v", wantApprover, sortedFileKeys(files))
	}
	for _, want := range []string{"kind: ClusterRole", "name: cert-approver", "approve"} {
		if !strings.Contains(string(approver), want) {
			t.Errorf("approver ClusterRole missing %q; got:\n%s", want, approver)
		}
	}
	// The document downstream of the glued separator is labeled through the same
	// identity path as any other rendered resource.
	if !strings.Contains(string(approver), "aggrega.ai/component-id: cg") {
		t.Errorf("approver ClusterRole carries no injected provenance label; got:\n%s", approver)
	}

	reviewer, ok := files[wantReviewer]
	if !ok {
		t.Fatalf("no reviewer ClusterRole output at %q; files: %v", wantReviewer, sortedFileKeys(files))
	}
	for _, want := range []string{"kind: ClusterRole", "name: cert-reviewer", "certificaterequests"} {
		if !strings.Contains(string(reviewer), want) {
			t.Errorf("reviewer ClusterRole missing %q; got:\n%s", want, reviewer)
		}
	}
}
