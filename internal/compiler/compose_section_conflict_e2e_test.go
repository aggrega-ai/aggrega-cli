// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package compiler_test — end-to-end proof that two ordinary components
// declaring one top-level compose volume name halt a real build when their
// declarations differ, and produce a single entry when they agree.
//
// The unit-level coverage of the same rule sits with the assembler. This file
// exists because that coverage cannot make the claim a product claim: the
// assembler is one function, and what an author experiences is a build that
// stops. Both cases run through the same pipeline a real build runs.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

const composeSectionConflictPath = "dist/cluster/compose/docker-compose.yaml"

// TestComposeSectionConflict_TwoOrdinaryComponentsHaltTheBuild covers the
// general case rather than the attachment-shaped one the defect was found
// through. Neither contributor is a Feature: both are components the developer
// declared in the Aggregate, and each carries an artifacts: fragment declaring
// `shared-data` with a different body.
func TestComposeSectionConflict_TwoOrdinaryComponentsHaltTheBuild(t *testing.T) {
	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/compose-section-conflict/release-conflict.yaml",
		CatalogPaths: []string{"testdata/compose-section-conflict/catalog/base"},
	})
	if err == nil {
		t.Fatal("the build accepted two components disagreeing about one volume name; want a halt")
	}

	// The assertion reads the flattened cause chain rather than err.Error(),
	// because a categorised frame keeps its cause out of Error() and surfaces it
	// only through Unwrap — so the outermost text here is the dispatch wrapper
	// and the guest's own diagnostic sits beneath it. CauseChain is the project's
	// flattener for exactly this single-line case; walking the chain by hand here
	// would be a second answer to the same question.
	chain := cerrors.CauseChain(err)
	for _, want := range []string{
		"volume",
		`"shared-data"`,
		`node="alpha"`,
		`node="beta"`,
	} {
		if !strings.Contains(chain, want) {
			t.Errorf("the halt does not name %s:\n%s", want, chain)
		}
	}
}

// TestComposeSectionConflict_AgreeingComponentsBuild is the separate proof that
// the rule is conflict-only end to end: the same two-contributor shape, with
// the two bodies identical, builds and emits one entry. A check that refused
// every shared name would fail here.
func TestComposeSectionConflict_AgreeingComponentsBuild(t *testing.T) {
	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/compose-section-conflict/release-agree.yaml",
		CatalogPaths: []string{"testdata/compose-section-conflict/catalog/base"},
	})
	body, ok := result.Artifacts[composeSectionConflictPath]
	if !ok {
		paths := make([]string, 0, len(result.Artifacts))
		for p := range result.Artifacts {
			paths = append(paths, p)
		}
		t.Fatalf("expected the merged compose file at %q; got %v", composeSectionConflictPath, paths)
	}
	assembled := string(body)
	if got := strings.Count(assembled, "shared-data:"); got != 1 {
		t.Errorf("volume emitted %d times, want exactly one entry:\n%s", got, assembled)
	}
	for _, want := range []string{"driver: local", "device: /srv/shared"} {
		if !strings.Contains(assembled, want) {
			t.Errorf("the merged entry lost %q:\n%s", want, assembled)
		}
	}
}
