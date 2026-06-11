// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// The capability-socket fixture mirrors the standard-library
// core/ai-tool/retrieval contract: a required usesSchema socket whose
// selector is a set-membership expression over the per-category
// capability label datastore.aggrega.ai/engine, rather than the
// operational type label. The pair proves the socket is enforced
// through the full compile pipeline, not just at the analyzer unit.

// TestCapabilitySocket_GreenBuilds: wiring the socket to a target whose
// engine label value is in the selector set builds to rendered output.
func TestCapabilitySocket_GreenBuilds(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/capability-socket/release-green.yaml",
		CatalogPaths: []string{"testdata/capability-socket/catalog"},
	})
	body, ok := result.Artifacts["dist/app/compose/docker-compose.yaml"]
	if !ok {
		paths := make([]string, 0, len(result.Artifacts))
		for p := range result.Artifacts {
			paths = append(paths, p)
		}
		t.Fatalf("expected rendered compose output; got artifacts %v", paths)
	}
	for _, want := range []string{"tool", "vdb"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("rendered output missing service %q\n--- output ---\n%s", want, body)
		}
	}
}

// TestCapabilitySocket_RedSelectorHalt: wiring the same socket to a
// target whose engine label value is outside the selector set halts the
// build at the Step-4 selector gate with the errors.Is-recoverable
// ErrSelectorViolation sentinel.
func TestCapabilitySocket_RedSelectorHalt(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/capability-socket/release-red.yaml",
		CatalogPaths: []string{"testdata/capability-socket/catalog"},
	})
	if err == nil {
		t.Fatal("want a selector-violation halt for an out-of-set engine target; got nil")
	}
	if !errors.Is(err, analyzer.ErrSelectorViolation) {
		t.Fatalf("want errors.Is(err, ErrSelectorViolation); got %v", err)
	}
	if !strings.Contains(err.Error(), "datastore.aggrega.ai/engine") {
		t.Fatalf("want the capability-axis selector in the diagnostic; got %v", err)
	}
}
