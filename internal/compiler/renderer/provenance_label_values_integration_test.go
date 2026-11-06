// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// kubernetesLabelValue is the Kubernetes label-value rule, written here once and
// independently of the code under test so the assertion is an oracle rather than
// a second reading of the implementation: empty, or at most 63 characters drawn
// from [A-Za-z0-9_.-] that open and close on a letter or digit. The length half
// is checked beside the pattern.
var kubernetesLabelValue = regexp.MustCompile(`^(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?$`)

func isKubernetesLabelValue(v string) bool {
	return len(v) <= 63 && kubernetesLabelValue.MatchString(v)
}

// TestIntegration_ASlashNamedAggregateRendersKubernetesValidLabels renders a Helm
// target through the embedded helm guest for an Aggregate named `imported/flow`,
// deployed to an environment named `eu/prod` by a development build of the
// compiler. Every one of those values carries a character a label value refuses.
//
// It asserts that every label value on every emitted object — the object's own
// labels, a pod template's labels and a selector's matchLabels — satisfies the
// Kubernetes label-value rule, so the cluster accepts the objects; and that each
// object carrying the provenance labels carries the verbatim name, environment
// and compiler version as annotations under the same keys, so the exact values
// survive the rewrite.
func TestIntegration_ASlashNamedAggregateRendersKubernetesValidLabels(t *testing.T) {
	t.Parallel()
	const (
		aggregateName   = "imported/flow"
		environment     = "eu/prod"
		compilerVersion = "(devel)"
	)
	nodeLabels := types.OrderedMap[string]{}
	nodeLabels.Set("component.aggrega.ai/type", "server")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server", Labels: nodeLabels, BoundaryPath: "prod/eks", ResolvedVersion: "1.0.0"})

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: aggregateName},
			Spec: types.IRSpec{
				Nodes:      nodes,
				Provenance: types.IRProvenance{CompilerVersion: compilerVersion, AggregateVersion: "1.0.0"},
			},
		},
		FeatureOrder: ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: aggregateName}},
			Release:       &types.Release{Metadata: types.ReleaseMetadata{Name: "prod", Environment: environment}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
	}
	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "r"},
				Spec: types.RecipeSpec{Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: app:latest
    ports:
      web: 8080
`}},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	paths := make([]string, 0, len(result.Files))
	for path := range result.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	wantAnnotations := map[string]string{
		"aggrega.ai/aggregate-name":   aggregateName,
		"aggrega.ai/environment":      environment,
		"aggrega.ai/compiler-version": compilerVersion,
	}
	labelSets, stamped := 0, 0
	for _, path := range paths {
		if !strings.HasSuffix(path, ".yaml") {
			continue
		}
		dec := yaml.NewDecoder(bytes.NewReader(result.Files[path].Content))
		for {
			var doc map[string]any
			if err := dec.Decode(&doc); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				t.Fatalf("%s: emitted YAML does not parse: %v", path, err)
			}
			if doc == nil {
				continue
			}
			walkLabelSets(doc, "", func(where string, set map[string]any) {
				labelSets++
				for k, v := range set {
					s, _ := v.(string)
					if !isKubernetesLabelValue(s) {
						t.Errorf("%s %s: label %s = %q is not a Kubernetes label value", path, where, k, s)
					}
				}
			})

			meta, _ := doc["metadata"].(map[string]any)
			objLabels, _ := meta["labels"].(map[string]any)
			if _, ok := objLabels["aggrega.ai/aggregate-name"]; !ok {
				continue
			}
			stamped++
			if got := objLabels["aggrega.ai/aggregate-name"]; got != "imported-flow" {
				t.Errorf("%s: label aggrega.ai/aggregate-name = %v; want imported-flow", path, got)
			}
			annotations, _ := meta["annotations"].(map[string]any)
			for k, want := range wantAnnotations {
				if got := annotations[k]; got != want {
					t.Errorf("%s: annotation %s = %v; want the verbatim %q", path, k, got, want)
				}
			}
		}
	}
	// Positive controls: a render that emitted no label set, or no object
	// carrying the provenance, would pass every assertion above vacuously.
	if labelSets == 0 {
		t.Fatal("no label set was found in the rendered output")
	}
	if stamped == 0 {
		t.Fatal("no rendered object carries the aggrega.ai/aggregate-name label")
	}
}

// walkLabelSets reports every label map in a decoded Kubernetes document: the
// labels under any metadata block, the object's own and a pod template's alike,
// and every selector's matchLabels.
func walkLabelSets(node any, where string, visit func(where string, set map[string]any)) {
	switch n := node.(type) {
	case map[string]any:
		keys := make([]string, 0, len(n))
		for k := range n {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			child := where + "." + k
			if set, ok := n[k].(map[string]any); ok {
				if k == "matchLabels" || (k == "labels" && strings.HasSuffix(where, ".metadata")) {
					visit(child, set)
					continue
				}
			}
			walkLabelSets(n[k], child, visit)
		}
	case []any:
		for _, item := range n {
			walkLabelSets(item, where+"[]", visit)
		}
	}
}
