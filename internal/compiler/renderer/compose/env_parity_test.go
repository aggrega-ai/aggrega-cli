// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose_test

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/compose"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// TestEnvOrderParity_ComposeMatchesHelm is the cross-engine guard for the
// single-sourced "iterate the container's env in author order" mechanic: the
// compose and helm synthesizers are independent implementations, so a shared
// Workload fixture is run through both and the rendered env-key sequences are
// asserted equal to each other AND to the authored order. The two engines emit
// different env shapes (compose `environment:` map, helm `env:` list), so the
// keys are read out of the rendered YAML node tree, where document order is
// authoritative regardless of any map round-trip.
func TestEnvOrderParity_ComposeMatchesHelm(t *testing.T) {
	t.Parallel()

	authored := []string{"ZEBRA", "ALPHA", "MIDDLE", "NESTED"}

	makeWorkload := func() *types.Workload {
		w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
		c := types.ContainerSpec{Image: "app:1.0"}
		c.Variables.Set("ZEBRA", "z")
		c.Variables.Set("ALPHA", "a")
		c.Variables.Set("MIDDLE", "m")
		c.Secrets.Set("NESTED", types.SecretRef{From: "db-credentials", As: "env"})
		w.Containers.Set("main", c)
		return w
	}

	nodeCtx := renderer.NodeRenderContext{
		NodeID:       "api",
		BoundaryPath: "default",
		Provenance: renderer.ProvenanceContext{
			CompilerVersion: "test", AggregateName: "test-agg", ReleaseName: "test-rel",
			Environment: "test-env", AggregateVersion: "1.0.0",
		},
	}

	composeFiles, err := (&compose.ComposeEngine{}).SynthesizeWorkload(context.Background(), nil, makeWorkload(), nodeCtx)
	if err != nil {
		t.Fatalf("compose SynthesizeWorkload: %v", err)
	}
	helmFiles, err := (&helm.HelmEngine{}).SynthesizeWorkload(context.Background(), nil, makeWorkload(), nodeCtx)
	if err != nil {
		t.Fatalf("helm SynthesizeWorkload: %v", err)
	}

	composeOrder := composeEnvKeys(t, composeFiles[0].Body)
	helmOrder := helmEnvKeys(t, helmFiles[0].Body)

	if !equalSeq(composeOrder, authored) {
		t.Errorf("compose env order = %v, want authored %v", composeOrder, authored)
	}
	if !equalSeq(helmOrder, authored) {
		t.Errorf("helm env order = %v, want authored %v", helmOrder, authored)
	}
	if !equalSeq(composeOrder, helmOrder) {
		t.Errorf("cross-engine env order divergence:\n  compose = %v\n  helm    = %v", composeOrder, helmOrder)
	}
}

// composeEnvKeys reads the first service's environment: keys in document order.
func composeEnvKeys(t *testing.T, body []byte) []string {
	t.Helper()
	root := parseDocRoot(t, body)
	services := mapValue(root, "services")
	if services == nil || len(services.Content) < 2 {
		t.Fatalf("compose body has no services: %s", body)
	}
	firstService := services.Content[1] // [0]=key, [1]=value mapping
	env := mapValue(firstService, "environment")
	if env == nil {
		t.Fatalf("compose body has no environment: %s", body)
	}
	keys := make([]string, 0, len(env.Content)/2)
	for i := 0; i < len(env.Content); i += 2 {
		keys = append(keys, env.Content[i].Value)
	}
	return keys
}

// helmEnvKeys reads the first container's env[].name list in document order.
func helmEnvKeys(t *testing.T, body []byte) []string {
	t.Helper()
	root := parseDocRoot(t, body)
	containers := mapValue(mapValue(mapValue(mapValue(root, "spec"), "template"), "spec"), "containers")
	if containers == nil || len(containers.Content) == 0 {
		t.Fatalf("helm body has no containers: %s", body)
	}
	env := mapValue(containers.Content[0], "env")
	if env == nil {
		t.Fatalf("helm body has no env: %s", body)
	}
	keys := make([]string, 0, len(env.Content))
	for _, entry := range env.Content {
		if name := mapValue(entry, "name"); name != nil {
			keys = append(keys, name.Value)
		}
	}
	return keys
}

func parseDocRoot(t *testing.T, body []byte) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("yaml unmarshal: %v\n%s", err, body)
	}
	if len(doc.Content) == 0 {
		t.Fatalf("empty yaml document: %s", body)
	}
	return doc.Content[0]
}

// mapValue returns the value node for key in a mapping node, or nil.
func mapValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func equalSeq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
