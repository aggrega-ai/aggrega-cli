// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"context"
	"strings"
	"testing"

	"helm.sh/helm/v4/pkg/chart/common"
	commonutil "helm.sh/helm/v4/pkg/chart/common/util"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/engine"
)

// TestSplitRenderedDocuments_CommentGluedSeparator pins that a rendered stream
// whose "---" document separator is glued directly to a comment with no
// separating whitespace ("---# a comment", as whitespace-chomping template
// control produces) splits into its individual documents in author order. The
// fixture's template renders three documents joined by such glued separators;
// the raw engine stream is asserted to actually carry the glued form so the test
// exercises the tolerated separator, not a whitespace-padded one.
func TestSplitRenderedDocuments_CommentGluedSeparator(t *testing.T) {
	t.Parallel()

	ch, err := loader.Load("testdata/comment-glued-doc-chart")
	if err != nil {
		t.Fatalf("load chart: %v", err)
	}
	vals, err := commonutil.ToRenderValues(ch, map[string]any{}, common.ReleaseOptions{Name: "cg", Namespace: "default"}, common.DefaultCapabilities)
	if err != nil {
		t.Fatalf("render values: %v", err)
	}
	rendered, err := engine.Engine{}.RenderWithContext(context.Background(), ch, vals)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	var stream string
	for path, content := range rendered {
		if strings.HasSuffix(path, "templates/rbac.yaml") {
			stream = content
		}
	}
	if stream == "" {
		t.Fatalf("no rendered rbac.yaml; got %v", rendered)
	}
	t.Logf("raw rendered stream:\n%s", stream)

	// Ground the fixture: the raw stream must carry the glued "---#" form that
	// the yaml decoder rejects when fed the stream directly.
	if !strings.Contains(stream, "---# Permission") {
		t.Fatalf("fixture did not produce a comment-glued separator; raw stream:\n%s", stream)
	}

	docs, err := splitRenderedDocuments([]byte(stream), 2)
	if err != nil {
		t.Fatalf("splitRenderedDocuments on a comment-glued stream must not error: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("expected 3 documents split from the glued stream; got %d", len(docs))
	}

	type id struct{ kind, name string }
	got := []id{}
	for _, d := range docs {
		got = append(got, id{d.kind, d.name})
	}
	want := []id{
		{"ConfigMap", "rbac-config"},
		{"ClusterRole", "cert-approver"},
		{"ClusterRole", "cert-reviewer"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("document %d: got kind=%q name=%q; want kind=%q name=%q", i, got[i].kind, got[i].name, want[i].kind, want[i].name)
		}
	}
}
