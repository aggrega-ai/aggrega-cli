// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// nestingEngine returns a nesting assembler that names segment as the directory
// its contributors' per-node files sit under. An empty segment is the shape every
// assembler had before one could be named, so it is what the default-path proofs
// below drive.
func nestingEngine(segment string) *collisionAssemblerEngine {
	return &collisionAssemblerEngine{
		collisionEngine: collisionEngine{emit: []renderer.RenderedFile{
			{Path: "templates/deployment.yaml", Content: []byte("a: 1\n")},
		}},
		merged: map[string]renderer.AssembledFile{
			"Chart.yaml": {Content: []byte("name: collide\n")},
		},
		nest:        true,
		nestSegment: segment,
	}
}

// TestRenderAll_NestSegmentAbsent_EmitsTheUnchangedLayout is the default-path
// proof. An assembler that names no segment must place its contributors exactly
// where it placed them before the segment could be named at all, so the expected
// paths here are written as literals rather than through
// renderer.DefaultNestSegment: reading the constant would make the assertion
// follow a change to the default instead of catching it.
func TestRenderAll_NestSegmentAbsent_EmitsTheUnchangedLayout(t *testing.T) {
	t.Parallel()

	res, err := runCollisionRender([]string{"alpha", "beta"}, nestingEngine(""))
	if err != nil {
		t.Fatalf("an assembler naming no segment must render: %v", err)
	}
	got := sortedKeys(res.Files)
	want := []string{
		"boundaries/default/collide/Chart.yaml",
		"boundaries/default/collide/charts/alpha/templates/deployment.yaml",
		"boundaries/default/collide/charts/beta/templates/deployment.yaml",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("emitted paths moved\nwant:\n%s\ngot:\n%s",
			strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

// TestRenderAll_NestSegmentNamedByTheAssembler_ReachesTheEmittedPath is the
// set-path proof, and it uses a segment DIFFERENT from the default on purpose:
// an assembler naming the default's own value would emit paths indistinguishable
// from naming nothing, so such a test would pass whether or not the field was
// read at all.
//
// It also runs the two side by side, so the difference between naming a segment
// and naming none is the segment and nothing else.
func TestRenderAll_NestSegmentNamedByTheAssembler_ReachesTheEmittedPath(t *testing.T) {
	t.Parallel()

	res, err := runCollisionRender([]string{"alpha", "beta"}, nestingEngine("modules"))
	if err != nil {
		t.Fatalf("an assembler naming a segment must render: %v", err)
	}
	got := sortedKeys(res.Files)
	want := []string{
		"boundaries/default/collide/Chart.yaml",
		"boundaries/default/collide/modules/alpha/templates/deployment.yaml",
		"boundaries/default/collide/modules/beta/templates/deployment.yaml",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the named segment did not reach the emitted path\nwant:\n%s\ngot:\n%s",
			strings.Join(want, "\n"), strings.Join(got, "\n"))
	}

	defaulted, err := runCollisionRender([]string{"alpha", "beta"}, nestingEngine(""))
	if err != nil {
		t.Fatalf("the default-segment companion run must render: %v", err)
	}
	rewritten := make([]string, 0, len(defaulted.Files))
	for _, k := range sortedKeys(defaulted.Files) {
		rewritten = append(rewritten, strings.Replace(k, "/charts/", "/modules/", 1))
	}
	if strings.Join(rewritten, "\n") != strings.Join(got, "\n") {
		t.Errorf("naming a segment changed more than the segment\nnamed:\n%s\ndefaulted, segment substituted:\n%s",
			strings.Join(got, "\n"), strings.Join(rewritten, "\n"))
	}
}

// TestRenderAll_NestSegmentOutsideOneDirectoryHalts is the bound firing. The
// orchestrator composes the node's identity into the segment, so a value that is
// not a single directory name would place a node's files somewhere other than
// beneath its own position in the dispatch group. Each case below is a value that
// must be refused, and the refusal has to name the provider whose assembler
// returned it and the value itself — a halt naming neither leaves an author with
// nothing to fix.
func TestRenderAll_NestSegmentOutsideOneDirectoryHalts(t *testing.T) {
	t.Parallel()

	for _, segment := range []string{
		"a/b",             // a path rather than a segment
		"/charts",         // a leading separator
		"charts/",         // a trailing separator
		"..",              // the parent directory
		".",               // the current directory
		"../../etc",       // a traversal out of the dispatch position
		`sub\dir`,         // the other platform's separator
		"a/../../../root", // a traversal disguised inside a path
	} {
		t.Run(segment, func(t *testing.T) {
			t.Parallel()

			res, err := runCollisionRender([]string{"alpha"}, nestingEngine(segment))
			if err == nil {
				t.Fatalf("expected a halt for segment %q, got files: %v", segment, sortedKeys(res.Files))
			}
			msg := err.Error()
			if !strings.Contains(msg, `provider "collide"`) {
				t.Errorf("refusal does not name the provider\ngot: %s", msg)
			}
			// Compared against the quoted rendering rather than the raw value:
			// the diagnostic quotes what it names, so a value carrying a
			// backslash reaches the reader escaped, and asserting on the raw
			// bytes would fail on a message that names it correctly.
			if !strings.Contains(msg, fmt.Sprintf("%q", segment)) {
				t.Errorf("refusal does not name the value %q\ngot: %s", segment, msg)
			}
		})
	}
}

// TestRenderAll_NestSegmentBoundAcceptsAnOrdinaryDirectoryName is the
// counterpart that keeps the bound from being satisfiable by refusing
// everything. A halt on any of these would reject legal authoring.
func TestRenderAll_NestSegmentBoundAcceptsAnOrdinaryDirectoryName(t *testing.T) {
	t.Parallel()

	for _, segment := range []string{"charts", "modules", "stacks", "my.services", "sub_units", "v2"} {
		t.Run(segment, func(t *testing.T) {
			t.Parallel()

			res, err := runCollisionRender([]string{"alpha"}, nestingEngine(segment))
			if err != nil {
				t.Fatalf("segment %q is a single directory name and must be accepted: %v", segment, err)
			}
			want := "boundaries/default/collide/" + segment + "/alpha/templates/deployment.yaml"
			found := false
			for _, k := range sortedKeys(res.Files) {
				if k == want {
					found = true
				}
			}
			if !found {
				t.Errorf("expected %q, got: %v", want, sortedKeys(res.Files))
			}
		})
	}
}

// TestRenderAll_NestSegmentIgnoredWhenTheAssemblerDoesNotNest pins the field's
// read condition. An assembler that emits flat carries no per-node position, so
// a segment on that output changes nothing — and in particular a value the bound
// would refuse does not halt a build that never places a per-node file.
func TestRenderAll_NestSegmentIgnoredWhenTheAssemblerDoesNotNest(t *testing.T) {
	t.Parallel()

	engine := nestingEngine("../escape")
	engine.nest = false

	res, err := runCollisionRender([]string{"alpha"}, engine)
	if err != nil {
		t.Fatalf("a flat assembler must render whatever segment it carries: %v", err)
	}
	for _, k := range sortedKeys(res.Files) {
		if strings.Contains(k, "escape") {
			t.Fatalf("a flat assembler's segment reached an emitted path: %v", sortedKeys(res.Files))
		}
	}
}
