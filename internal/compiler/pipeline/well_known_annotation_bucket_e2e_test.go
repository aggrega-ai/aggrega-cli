// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end proof that a provider renders the well-known aggrega.ai/*
// annotations from the Release alone, through the real pipeline and the shipped
// helm guest, while the Aggregate and catalog buckets reach it carrying the same
// keys.

package pipeline_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

const wellKnownAnnotationBucketDir = "testdata/well-known-annotation-bucket/"

// TestWellKnownAnnotationsComeFromTheReleaseBucket builds a workspace in which
// the Aggregate, the catalog Component and — for one of the two nodes — the
// Release all write aggrega.ai/namespace and aggrega.ai/image-registry, each with
// a value naming the side that wrote it.
//
// Every bucket reaches the guest, which the IR rows pin: each node's graph carries
// the Aggregate's and the catalog's values in their own buckets. The rendered rows
// pin what decides placement: the node whose Release writes the keys renders into
// the Release's namespace under the Release's registry, and the node whose Release
// writes none renders with no namespace and its image unrelocated — the Aggregate
// and catalog values, present in the context, decide nothing. A catalog value can
// therefore never override the operator's Release, and a node is never placed
// somewhere the compiler's own reading of its namespace, which is the Release's,
// does not look for it.
//
// 🔴 PLANTED DEFECT: in WellKnownAnnotationSet (internal/compiler/rendercore/
// annotations.go) fold the catalog bucket in after the release bucket: the
// operated row renders from-catalog and catalog.registry.example and fails, and
// the unoperated row renders a namespace and fails.
func TestWellKnownAnnotationsComeFromTheReleaseBucket(t *testing.T) {
	t.Parallel()

	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  wellKnownAnnotationBucketDir + "release.yaml",
		CatalogPaths: []string{wellKnownAnnotationBucketDir + "catalog/base"},
	})
	if result == nil || result.IRGraph == nil {
		t.Fatal("the build returned no graph")
	}

	rows := []struct {
		node          string
		wantNamespace string // "" means no namespace is rendered
		wantImage     string
	}{
		{"operated", "from-release", "release.registry.example/corp/operated:v1"},
		{"unoperated", "", "corp/unoperated:v1"},
	}
	for _, row := range rows {
		t.Run(row.node, func(t *testing.T) {
			t.Parallel()
			node, ok := result.IRGraph.Spec.Nodes.Get(row.node)
			if !ok {
				t.Fatalf("no IR node %q; nodes: %v", row.node, result.IRGraph.Spec.Nodes.Keys())
			}
			// The context is full: the other two buckets carry the same keys.
			if got, _ := node.AggregateAnnotations.Get("aggrega.ai/namespace"); got != "from-aggregate" {
				t.Errorf("IR aggregate bucket namespace = %q, want %q", got, "from-aggregate")
			}
			if got, _ := node.CatalogAnnotations.Get("aggrega.ai/namespace"); got != "from-catalog" {
				t.Errorf("IR catalog bucket namespace = %q, want %q", got, "from-catalog")
			}

			body := artifactEndingIn(t, result.Artifacts, "/"+row.node+"-deployment.yaml")
			if row.wantNamespace == "" {
				if strings.Contains(body, "\n  namespace:") {
					t.Errorf("a namespace was rendered though the Release writes none:\n%s", body)
				}
			} else if !strings.Contains(body, "\n  namespace: "+row.wantNamespace+"\n") {
				t.Errorf("namespace %q not rendered:\n%s", row.wantNamespace, body)
			}
			if !strings.Contains(body, "image: "+row.wantImage+"\n") {
				t.Errorf("image %q not rendered:\n%s", row.wantImage, body)
			}
			for _, foreign := range []string{"from-aggregate", "from-catalog", "aggregate.registry.example", "catalog.registry.example"} {
				if strings.Contains(body, foreign) {
					t.Errorf("a value written outside the Release, %q, decided the render:\n%s", foreign, body)
				}
			}
		})
	}
}
