// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — end-to-end proof that a Release declaring one
// component in two boundaries halts, and that the diagnostic names both
// declaration sites.
//
// Before the load-time rejection this fixture BUILT, and it built into a shape
// with no single meaning: the node's output landed under the INNER boundary's
// directory (dist/inner/...) while its per-edge `infra` came from the OUTER
// declaration (DB_POOL_SIZE 50, not the inner declaration's 77). Two walks of
// one boundary tree resolved the duplicated name to different declarations.
// Rejecting the input is what makes that divergence unreachable.

package compiler_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

func TestReleaseDupBoundary_HaltsNamingBothSites(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/uses-edge-config/release-dup-boundary.yaml",
		CatalogPaths: []string{"testdata/uses-edge-config/catalog/base"},
	})
	if !errors.Is(err, resolver.ErrDuplicateBoundaryComponent()) {
		t.Fatalf("want ErrDuplicateBoundaryComponent for a component declared in two boundaries; got %v", err)
	}
	// Both declaration sites, not just the offending one: an author told only
	// "declared twice" has to hunt the tree for the other half.
	for _, want := range []string{`"cluster"`, `"cluster/inner"`, `"api"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("diagnostic must name %s; got %q", want, err.Error())
		}
	}
}
