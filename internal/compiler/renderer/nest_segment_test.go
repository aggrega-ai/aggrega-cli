// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// nestingEngine returns a nesting provider that names segment as the directory
// its contributors' per-node files sit under. An empty segment is a provider that
// names none, which is what the no-directory proofs below drive.
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

// TestRenderAll_NestSegmentAbsent_PlacesContributorsAtTheDispatchPosition is the
// no-declaration proof. A provider that names no directory gets none: each
// contributor sits directly at the dispatch position, under its own name and
// nothing else.
//
// The host invents no directory here, and that is the substance rather than an
// omission. Any name it could supply would be some target's word sitting in the
// provider-agnostic core — which is exactly what a provider-declared directory
// exists to remove — and the contributors are already separated by their own
// names without one. The paths are written as literals so the assertion catches a
// re-introduced default rather than following it.
func TestRenderAll_NestSegmentAbsent_PlacesContributorsAtTheDispatchPosition(t *testing.T) {
	t.Parallel()

	res, err := runCollisionRender([]string{"alpha", "beta"}, nestingEngine(""))
	if err != nil {
		t.Fatalf("a provider naming no segment must render: %v", err)
	}
	got := sortedKeys(res.Files)
	want := []string{
		"boundaries/default/collide/Chart.yaml",
		"boundaries/default/collide/alpha/templates/deployment.yaml",
		"boundaries/default/collide/beta/templates/deployment.yaml",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("emitted paths moved\nwant:\n%s\ngot:\n%s",
			strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

// TestRenderAll_NestSegmentNamedByTheProvider_ReachesTheEmittedPath is the
// declared-path proof.
//
// It runs the declared and the undeclared shapes side by side, so the difference
// between naming a directory and naming none is that directory and nothing else:
// the second run's paths, with the directory spliced back in, must equal the
// first's exactly.
func TestRenderAll_NestSegmentNamedByTheProvider_ReachesTheEmittedPath(t *testing.T) {
	t.Parallel()

	res, err := runCollisionRender([]string{"alpha", "beta"}, nestingEngine("modules"))
	if err != nil {
		t.Fatalf("a provider naming a segment must render: %v", err)
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

	undeclared, err := runCollisionRender([]string{"alpha", "beta"}, nestingEngine(""))
	if err != nil {
		t.Fatalf("the no-segment companion run must render: %v", err)
	}
	rewritten := make([]string, 0, len(undeclared.Files))
	for _, k := range sortedKeys(undeclared.Files) {
		// Only a contributor path gains the directory; the assembled document sits
		// at the dispatch position in both runs, which the prefix test keeps out.
		if strings.HasSuffix(k, "/templates/deployment.yaml") {
			k = strings.Replace(k, "/collide/", "/collide/modules/", 1)
		}
		rewritten = append(rewritten, k)
	}
	if strings.Join(rewritten, "\n") != strings.Join(got, "\n") {
		t.Errorf("naming a segment changed more than the segment\nnamed:\n%s\nundeclared, segment spliced in:\n%s",
			strings.Join(got, "\n"), strings.Join(rewritten, "\n"))
	}
}

// TestRenderAll_NestSegmentOutsideOneDirectoryHalts is the bound firing. The
// orchestrator composes the node's identity into the segment, so a value that is
// not a single directory name would place a node's files somewhere other than
// beneath its own position in the dispatch group. Each case below is a value that
// must be refused, and the refusal has to name the provider that declared it and
// the value itself — a halt naming neither leaves an author with nothing to fix.
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

// TestRenderAll_NestSegmentIgnoredWhenTheProviderDoesNotNest pins the
// declaration's read condition. A provider that folds its contributors into one
// document gives none of them a position of its own, so a directory declared for
// those positions changes nothing — and in particular a value the bound would
// refuse does not halt a build that never places a per-node file.
func TestRenderAll_NestSegmentIgnoredWhenTheProviderDoesNotNest(t *testing.T) {
	t.Parallel()

	engine := nestingEngine("../escape")
	engine.nest = false

	res, err := runCollisionRender([]string{"alpha"}, engine)
	if err != nil {
		t.Fatalf("a folding provider must render whatever segment it declares: %v", err)
	}
	for _, k := range sortedKeys(res.Files) {
		if strings.Contains(k, "escape") {
			t.Fatalf("a folding provider's segment reached an emitted path: %v", sortedKeys(res.Files))
		}
	}
}
