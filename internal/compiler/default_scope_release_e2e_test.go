// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// TestDefaultScopeRelease_SynthesizedDefaultBuildsFlat proves the
// former-degenerate path end-to-end: a Release that declares top-level
// components and no named boundaries builds, the synthesized `default` boundary
// is honest in the IR (BoundaryPath == "default", SynthesizedBoundary == true),
// and its artifacts emit to dist/ flat — no boundaries/default/ segregation
// segment.
func TestDefaultScopeRelease_SynthesizedDefaultBuildsFlat(t *testing.T) {
	t.Parallel()
	wf := baseWorkspaceFiles()
	wf.release = `apiVersion: aggrega.ai/v1beta1
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
`
	in := writeMinimalWorkspace(t, wf)
	result, err := pipelinetest.BuildE(t, in)
	if err != nil {
		t.Fatalf("a boundary-less Release with top-level components must build; got: %v", err)
	}

	node, ok := result.IRGraph.Spec.Nodes.Get("api")
	if !ok {
		t.Fatalf("node 'api' missing; nodes=%v", result.IRGraph.Spec.Nodes.Keys())
	}
	if !node.SynthesizedBoundary {
		t.Error("IR node must mark SynthesizedBoundary=true for the synthesized default")
	}
	if node.BoundaryPath != "default" {
		t.Errorf("BoundaryPath must be the literal 'default'; got %q", node.BoundaryPath)
	}

	if len(result.Artifacts) == 0 {
		t.Fatal("expected rendered artifacts")
	}
	for p := range result.Artifacts {
		if strings.Contains(p, "boundaries/") {
			t.Errorf("synthesized default must emit flat to dist/; found segregated path %q", p)
		}
	}
}

// TestDefaultScopeRelease_MultiBoundaryBuilds proves the Level-2 multi-scope
// form end-to-end: a Release declaring two named boundaries builds, each node
// carries its own non-synthesized boundary path, and the explicit grouping is
// what the synthesized default is the single-scope shorthand for.
func TestDefaultScopeRelease_MultiBoundaryBuilds(t *testing.T) {
	t.Parallel()
	wf := baseWorkspaceFiles()
	wf.aggregate = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: corpus-app
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
        port: 8080
    worker:
      type: core/server
`
	wf.release = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: corpus-rel
  environment: dev
  version: "1.0.0"
spec:
  aggregate: corpus-app
  manifest: corpus-manifest
  boundaries:
    edge:
      components:
        api:
          infra:
            replicas: 1
    data:
      components:
        worker:
          infra:
            replicas: 1
`
	in := writeMinimalWorkspace(t, wf)
	result, err := pipelinetest.BuildE(t, in)
	if err != nil {
		t.Fatalf("a multi-boundary Release must build; got: %v", err)
	}
	want := map[string]string{"api": "edge", "worker": "data"}
	for id, path := range want {
		node, ok := result.IRGraph.Spec.Nodes.Get(id)
		if !ok {
			t.Fatalf("node %q missing; nodes=%v", id, result.IRGraph.Spec.Nodes.Keys())
		}
		if node.BoundaryPath != path {
			t.Errorf("node %q: want BoundaryPath %q; got %q", id, path, node.BoundaryPath)
		}
		if node.SynthesizedBoundary {
			t.Errorf("node %q: a declared boundary must NOT be marked synthesized", id)
		}
	}
}

// TestDefaultScopeRelease_DeclaredDefaultBoundarySegregates is the
// flag-not-name control: a boundary a user explicitly NAMES "default" (not
// synthesized) keeps the boundaries/default/ segregation and is NOT marked
// SynthesizedBoundary, even though it shares the name with the synthesized one.
func TestDefaultScopeRelease_DeclaredDefaultBoundarySegregates(t *testing.T) {
	t.Parallel()
	wf := baseWorkspaceFiles()
	wf.release = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: corpus-rel
  environment: dev
  version: "1.0.0"
spec:
  aggregate: corpus-app
  manifest: corpus-manifest
  boundaries:
    default:
      components:
        api:
          infra:
            replicas: 1
`
	in := writeMinimalWorkspace(t, wf)
	result, err := pipelinetest.BuildE(t, in)
	if err != nil {
		t.Fatalf("a Release with a declared boundary named 'default' must build; got: %v", err)
	}

	node, ok := result.IRGraph.Spec.Nodes.Get("api")
	if !ok {
		t.Fatalf("node 'api' missing; nodes=%v", result.IRGraph.Spec.Nodes.Keys())
	}
	if node.SynthesizedBoundary {
		t.Error("a user-declared boundary named 'default' must NOT be marked SynthesizedBoundary")
	}
	if node.BoundaryPath != "default" {
		t.Errorf("BoundaryPath must be 'default'; got %q", node.BoundaryPath)
	}

	segregated := false
	for p := range result.Artifacts {
		if strings.Contains(p, "boundaries/default") {
			segregated = true
			break
		}
	}
	if !segregated {
		paths := make([]string, 0, len(result.Artifacts))
		for p := range result.Artifacts {
			paths = append(paths, p)
		}
		t.Errorf("a declared boundary named 'default' must segregate to boundaries/default/...; paths=%v", paths)
	}
}
