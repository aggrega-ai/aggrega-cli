// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pullops

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestDiscoveryRenderIsConservedWhenItStops proves the discovery hands back the
// result of the render it performed rather than dropping it, and that a result
// from a run which stopped cannot be read as a finished one.
//
// The stopped path is the one this package drives on its own: a completed render
// needs a resolvable Release and a provider guest, which the proofs at the
// command layer supply. It is also the path on which conserving a result is
// easiest to get wrong, because a partial payload handed over as though it were
// whole is exactly what the verdict accessor exists to prevent. So the assertion
// has two halves — the result is there, and it refuses to yield a payload.
//
// The first check is the positive control. Were the discovery to complete here,
// every assertion after it would be about a path this fixture never created, and
// the test would pass while proving nothing.
func TestDiscoveryRenderIsConservedWhenItStops(t *testing.T) {
	t.Parallel()

	const cacheDir = ".aggrega/cache"
	ws := t.TempDir()
	view := catalog.NewLazyLookup(ws, cacheDir, nil, nil)

	_, render, err := discoverExternalInputs(
		context.Background(),
		Config{ReleasePath: "no-such-release.yaml"},
		workspaceconfig.Config{},
		ws,
		cacheDir,
		view,
		&lockfile.Lockfile{Version: 1},
		nil,
	)

	if err == nil {
		t.Fatal("the discovery completed, so this fixture never exercised a render that stopped " +
			"and the assertions below would be about a path it did not create")
	}
	if render == nil {
		t.Fatal("the render's result was dropped on the path that stopped, so a caller holding " +
			"the error alone cannot report what the render found before it stopped")
	}
	if payload := render.PipelineOutcome().Completed(); payload != nil {
		t.Errorf("a render that stopped reported a completed payload, so a caller reading it "+
			"through the verdict cannot tell a partial render from a finished one: got %#v", payload)
	}
}
