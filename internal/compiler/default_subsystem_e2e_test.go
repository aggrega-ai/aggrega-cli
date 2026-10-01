// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// TestDefaultSubsystem_NodeIdentityPreserved is the load-bearing proof for the
// Aggregate default-subsystem normalization: folding a
// root-authored component into the synthesized `default` subsystem must NOT
// change its node identity. The node ID is `qualifiedID(prefix, name)` — not
// subsystem-scoped — so the root component "api" keeps node ID "api" (never
// "default.api"); only SubsystemPath gains "default" and the
// synthesized-provenance marker flips on. A green suite proves only
// non-regression; this test deliberately pins identity-preservation + the
// honest-IR marker.
func TestDefaultSubsystem_NodeIdentityPreserved(t *testing.T) {
	t.Parallel()
	in := writeMinimalWorkspace(t, baseWorkspaceFiles()) // baseAggregate authors a root component "api"
	result, err := pipelinetest.BuildE(t, in)
	if err != nil {
		t.Fatalf("base workspace (root component) must build under normalization; got: %v", err)
	}

	// Node identity preserved: the root component keeps its bare ID, not a
	// subsystem-scoped one.
	node, ok := result.IRGraph.Spec.Nodes.Get("api")
	if !ok {
		t.Fatalf("node 'api' must keep its identity (not 'default.api'); nodes=%v", result.IRGraph.Spec.Nodes.Keys())
	}
	for _, k := range result.IRGraph.Spec.Nodes.Keys() {
		if strings.Contains(k, "default") {
			t.Errorf("no node ID may be subsystem-scoped by the synthesized default; found %q", k)
		}
	}

	// Honest IR: the synthesized default subsystem is literal + provenance-marked.
	if node.SubsystemPath != "default" {
		t.Errorf("SubsystemPath must be the literal 'default'; got %q", node.SubsystemPath)
	}
	if !node.SynthesizedSubsystem {
		t.Error("IR node must mark SynthesizedSubsystem=true for the synthesized default")
	}
}

// TestDefaultSubsystem_MultiSubsystemBuilds proves the Level-2 multi-scope form
// end-to-end: an Aggregate declaring two named subsystems builds, and each node
// carries its own non-synthesized subsystem path in the IR — the explicit
// grouping the single-scope default is the shorthand for.
func TestDefaultSubsystem_MultiSubsystemBuilds(t *testing.T) {
	t.Parallel()
	wf := baseWorkspaceFiles()
	wf.aggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: corpus-app
  version: "1.0.0"
spec:
  subsystems:
    edge:
      components:
        api:
          type: core/server
          values:
            port: 8080
    data:
      components:
        worker:
          type: core/server
`
	wf.release = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: corpus-rel
  environment: dev
  version: "1.0.0"
spec:
  aggregate: corpus-app
  manifest: corpus-manifest
  components:
    api:
      infra:
        replicas: 1
    worker:
      infra:
        replicas: 1
`
	in := writeMinimalWorkspace(t, wf)
	result, err := pipelinetest.BuildE(t, in)
	if err != nil {
		t.Fatalf("a multi-subsystem Aggregate must build; got: %v", err)
	}
	want := map[string]string{"api": "edge", "worker": "data"}
	for id, path := range want {
		node, ok := result.IRGraph.Spec.Nodes.Get(id)
		if !ok {
			t.Fatalf("node %q missing; nodes=%v", id, result.IRGraph.Spec.Nodes.Keys())
		}
		if node.SubsystemPath != path {
			t.Errorf("node %q: want SubsystemPath %q; got %q", id, path, node.SubsystemPath)
		}
		if node.SynthesizedSubsystem {
			t.Errorf("node %q: a declared subsystem must NOT be marked synthesized", id)
		}
	}
}

// TestDefaultSubsystem_DeclaredDefaultNotSynthesized is the flag-not-name
// control: a subsystem a user explicitly NAMES "default" carries
// SynthesizedSubsystem=false even though SubsystemPath is "default".
func TestDefaultSubsystem_DeclaredDefaultNotSynthesized(t *testing.T) {
	t.Parallel()
	wf := baseWorkspaceFiles()
	wf.aggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: corpus-app
  version: "1.0.0"
spec:
  subsystems:
    default:
      components:
        api:
          type: core/server
          values:
            port: 8080
`
	in := writeMinimalWorkspace(t, wf)
	result, err := pipelinetest.BuildE(t, in)
	if err != nil {
		t.Fatalf("an Aggregate with a user-declared 'default' subsystem must build; got: %v", err)
	}
	node, ok := result.IRGraph.Spec.Nodes.Get("api")
	if !ok {
		t.Fatalf("node 'api' missing; nodes=%v", result.IRGraph.Spec.Nodes.Keys())
	}
	if node.SubsystemPath != "default" {
		t.Errorf("SubsystemPath must be 'default'; got %q", node.SubsystemPath)
	}
	if node.SynthesizedSubsystem {
		t.Error("a user-declared subsystem named 'default' must NOT be marked SynthesizedSubsystem")
	}
}
