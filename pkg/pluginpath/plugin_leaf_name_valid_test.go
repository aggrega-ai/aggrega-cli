// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pluginpath

import (
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegmenttest"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestTheCoLocatedGuestFileIsNamedSoEveryPlatformAcceptsIt derives the file the
// co-located tier reads a guest from, for the reference a Plugin entity resolves
// to — its type and name joined — over names carrying what a file name cannot or
// what a platform reserves, and holds each to the one validity predicate. The
// reference always carries both parts, which is what keeps the name's stem from
// ever being a device name on its own.
func TestTheCoLocatedGuestFileIsNamedSoEveryPlatformAcceptsIt(t *testing.T) {
	t.Parallel()
	const dir = "/catalog/plugin/provider/x/1.0.0"
	for _, p := range []struct{ pluginType, name string }{
		{"provider", "helm"},
		{"governance", "cel"},
		{"provider", "con"},
		{"provider", "NUL"},
		{"provider", "aux.txt"},
		{"cli", "a:b"},
		{"cli", "x."},
		{"cli", ""},
		{"", "con"},
		{"Provider", "Helm"},
	} {
		ref := types.PluginCoordinate(p.pluginType, p.name)
		pathsegmenttest.RequireValidBeneath(t, dir, filepath.Join(dir, PluginLeaf(ref)))
	}
}
