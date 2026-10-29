// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/helmcore"
)

// TestHelmEngine_Sourced_NestedSameKindResourcesSurvive pins that two same-kind
// resources rendered from templates in distinct subdirectories — sharing one
// basename — each survive as a distinct output file. Basename keying collapsed
// them onto a single slot; identity keying keeps both.
func TestHelmEngine_Sourced_NestedSameKindResourcesSurvive(t *testing.T) {
	t.Parallel()
	files, err := helmcore.RenderLoadedChart(context.Background(), loadChart(t, "testdata/nested-samekind-chart"),
		map[string]any{}, nodeCtx("gw", "testdata/nested-samekind-chart"), helmcore.NewHermeticValuesValidator(forbidFetch(t)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 distinct rendered files, got %d: %v", len(files), sortedFileKeys(files))
	}
	var haveContour, haveEnvoy bool
	for k, v := range files {
		switch {
		case strings.Contains(string(v), "name: contour-policy"):
			haveContour = true
		case strings.Contains(string(v), "name: envoy-policy"):
			haveEnvoy = true
		default:
			t.Errorf("unexpected rendered file %q:\n%s", k, v)
		}
	}
	if !haveContour {
		t.Errorf("contour NetworkPolicy did not survive; files: %v", sortedFileKeys(files))
	}
	if !haveEnvoy {
		t.Errorf("envoy NetworkPolicy did not survive; files: %v", sortedFileKeys(files))
	}
}

// TestHelmEngine_Sourced_DistinctContentCollisionHalts pins the Loud Guardrail:
// two templates rendering resources that share one identity (kind, namespace,
// name) but differ in content halt on the output-name-collision condition rather
// than silently drop one.
func TestHelmEngine_Sourced_DistinctContentCollisionHalts(t *testing.T) {
	t.Parallel()
	_, err := helmcore.RenderLoadedChart(context.Background(), loadChart(t, "testdata/collision-chart"),
		map[string]any{}, nodeCtx("svc", "testdata/collision-chart"), helmcore.NewHermeticValuesValidator(forbidFetch(t)))
	if err == nil {
		t.Fatal("expected a collision error when two distinct resources derive one output filename")
	}
	if !errors.Is(err, plugincondition.ErrPluginOutputNameCollision()) {
		t.Fatalf("error does not carry the output-name-collision condition: %v", err)
	}
	// The halt must name both source templates and the output path — and the
	// sentence that says a RENDERED FILE is what collided, since the condition
	// names only the kind and is shared with every other collision this core
	// reports.
	msg := err.Error()
	for _, want := range []string{
		"service.yaml", "svc-service-shared.yaml",
		"two rendered resources derive the same output filename with differing content",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("collision message missing %q; got: %s", want, msg)
		}
	}
}

func sortedFileKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
