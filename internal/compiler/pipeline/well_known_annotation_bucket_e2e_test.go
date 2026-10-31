// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end proof that a provider renders the well-known aggrega.ai/*
// annotations an Aggregate declares — on a subsystem or on a component instance,
// in the Aggregate itself or in a module it imports — with the Release's value
// winning on a key both write, through the real pipeline and the shipped helm
// guest, and that the catalog's values for the same keys decide nothing.

package pipeline_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

const wellKnownAnnotationBucketDir = "testdata/well-known-annotation-bucket/"

// TestWellKnownAnnotationsLayerTheReleaseOverTheAggregate builds a workspace in
// which aggrega.ai/namespace and aggrega.ai/image-registry are written on each of
// the four Aggregate surfaces — the Aggregate's own subsystem, its own component
// instance, a subsystem of the module it imports, and a component instance of
// that module — each with a value naming the surface. Every surface carries two
// components: one the Release leaves alone, and one whose Release entry writes the
// namespace and not the registry. The catalog Component writes both keys on every
// node, and one component sits on no annotated surface at all.
//
// Each row is one node and pins what decides placement: the namespace and the
// registry the Aggregate surface wrote, where the Release writes nothing; the
// Release's namespace beside the Aggregate's registry, where the Release writes
// only the namespace — the layering is per key; and neither a namespace nor a
// relocated image on the node no Aggregate surface annotates, because the
// catalog's values never decide one. The IR rows pin that the buckets themselves
// stay apart: the layering is the render's reading, never a merge in the graph.
//
// 🔴 PLANTED DEFECT: in RenderAnnotations.PlatformAnnotations
// (internal/compiler/renderctx/renderctx.go) drop the loop over the aggregate
// bucket and regenerate the embedded guests: every row whose Release writes
// nothing renders no namespace and an unrelocated image and fails, and every
// operated row renders its image unrelocated and fails. Fold the catalog bucket in
// beneath the aggregate one instead: the "plain" row renders from-catalog and
// fails.
func TestWellKnownAnnotationsLayerTheReleaseOverTheAggregate(t *testing.T) {
	t.Parallel()

	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  wellKnownAnnotationBucketDir + "release.yaml",
		CatalogPaths: []string{wellKnownAnnotationBucketDir + "catalog/base"},
	})
	if result == nil || result.IRGraph == nil {
		t.Fatal("the build returned no graph")
	}

	rows := []struct {
		node          string // the IR node id
		file          string // the stem of its rendered Deployment
		image         string // the image its Aggregate entry writes
		aggregateNS   string // the namespace its aggregate bucket carries; "" for none
		releaseNS     string // the namespace its release bucket carries; "" for none
		wantNamespace string // the namespace rendered; "" means none is rendered
		wantRegistry  string // the registry its image is relocated under; "" means none
	}{
		{"rsub", "rsub", "corp/rsub:v1", "from-root-subsystem", "", "from-root-subsystem", "root-subsystem.registry.example"},
		{"rsub-operated", "rsub-operated", "corp/rsub-operated:v1", "from-root-subsystem", "from-release", "from-release", "root-subsystem.registry.example"},
		{"rcomp", "rcomp", "corp/rcomp:v1", "from-root-component", "", "from-root-component", "root-component.registry.example"},
		{"rcomp-operated", "rcomp-operated", "corp/rcomp-operated:v1", "from-root-component", "from-release", "from-release", "root-component.registry.example"},
		{"mod.msub", "mod-zdmsub", "corp/msub:v1", "from-module-subsystem", "", "from-module-subsystem", "module-subsystem.registry.example"},
		{"mod.msub-operated", "mod-zdmsub-operated", "corp/msub-operated:v1", "from-module-subsystem", "from-release", "from-release", "module-subsystem.registry.example"},
		{"mod.mcomp", "mod-zdmcomp", "corp/mcomp:v1", "from-module-component", "", "from-module-component", "module-component.registry.example"},
		{"mod.mcomp-operated", "mod-zdmcomp-operated", "corp/mcomp-operated:v1", "from-module-component", "from-release", "from-release", "module-component.registry.example"},
		{"plain", "plain", "corp/plain:v1", "", "", "", ""},
	}
	for _, row := range rows {
		t.Run(row.node, func(t *testing.T) {
			t.Parallel()
			node, ok := result.IRGraph.Spec.Nodes.Get(row.node)
			if !ok {
				t.Fatalf("no IR node %q; nodes: %v", row.node, result.IRGraph.Spec.Nodes.Keys())
			}
			// The buckets stay apart in the graph, each carrying its own side's value.
			if got, _ := node.AggregateAnnotations.Get("aggrega.ai/namespace"); got != row.aggregateNS {
				t.Errorf("IR aggregate bucket namespace = %q, want %q", got, row.aggregateNS)
			}
			if got, _ := node.ReleaseAnnotations.Get("aggrega.ai/namespace"); got != row.releaseNS {
				t.Errorf("IR release bucket namespace = %q, want %q", got, row.releaseNS)
			}
			if got, _ := node.CatalogAnnotations.Get("aggrega.ai/namespace"); got != "from-catalog" {
				t.Errorf("IR catalog bucket namespace = %q, want %q", got, "from-catalog")
			}

			body := artifactEndingIn(t, result.Artifacts, "/"+row.file+"-deployment.yaml")
			if row.wantNamespace == "" {
				if strings.Contains(body, "\n  namespace:") {
					t.Errorf("a namespace was rendered though no Aggregate surface or Release writes one:\n%s", body)
				}
			} else if !strings.Contains(body, "\n  namespace: "+row.wantNamespace+"\n") {
				t.Errorf("namespace %q not rendered:\n%s", row.wantNamespace, body)
			}
			wantImage := row.image
			if row.wantRegistry != "" {
				wantImage = row.wantRegistry + "/" + row.image
			}
			if !strings.Contains(body, "image: "+wantImage+"\n") {
				t.Errorf("image %q not rendered:\n%s", wantImage, body)
			}
			for _, foreign := range []string{"from-catalog", "catalog.registry.example"} {
				if strings.Contains(body, foreign) {
					t.Errorf("a value the catalog wrote, %q, decided the render:\n%s", foreign, body)
				}
			}
		})
	}
}
