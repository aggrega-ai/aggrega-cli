// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"testing"
)

// boundaryAssemblyEngine is a no-op engine reporting a fixed boundary-assembly
// mode, standing in for a provider whose guest self-reports the mode via
// describe(). It exercises the host's render classification from the reported
// value alone.
type boundaryAssemblyEngine struct {
	stubEngine
	mode string
}

func (b boundaryAssemblyEngine) BoundaryAssembly() string { return b.mode }

// TestRenderIsIntermediateFromReport pins the intermediate-marking rule to the
// engine's boundary-assembly self-report: a fragment-merging provider ("merge") is
// intermediate; a nesting provider ("nest") and a flat-output provider ("none")
// are not.
func TestRenderIsIntermediateFromReport(t *testing.T) {
	if !RenderIsIntermediate(boundaryAssemblyEngine{mode: "merge"}) {
		t.Error(`boundary_assembly "merge" should be intermediate`)
	}
	if RenderIsIntermediate(boundaryAssemblyEngine{mode: "nest"}) {
		t.Error(`boundary_assembly "nest" must not be intermediate`)
	}
	if RenderIsIntermediate(boundaryAssemblyEngine{mode: "none"}) {
		t.Error(`boundary_assembly "none" must not be intermediate`)
	}
}

// TestAssemblesBoundaryFromReport pins the assembler classification to the engine's
// boundary-assembly self-report: a merging ("merge") or nesting ("nest") provider
// assembles; a flat-output provider ("none") and an engine that reports no mode do
// not.
func TestAssemblesBoundaryFromReport(t *testing.T) {
	if !AssemblesBoundary(boundaryAssemblyEngine{mode: "merge"}) {
		t.Error(`boundary_assembly "merge" should assemble`)
	}
	if !AssemblesBoundary(boundaryAssemblyEngine{mode: "nest"}) {
		t.Error(`boundary_assembly "nest" should assemble`)
	}
	if AssemblesBoundary(boundaryAssemblyEngine{mode: "none"}) {
		t.Error(`boundary_assembly "none" must not assemble`)
	}
	if AssemblesBoundary(&stubEngine{}) {
		t.Error("an engine that reports no boundary-assembly mode must not assemble")
	}
}
