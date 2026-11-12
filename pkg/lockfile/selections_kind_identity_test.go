// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lockfile_test

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestASocketEntityIsAPluginRowAndNoOtherKindSharingItsCoordinate holds the set a
// build reconstructs the workspace-socket entities from to the rows that pin a
// Plugin. A provider binding names provider/helm; the lockfile also pins a Recipe
// and a Component whose references read like that coordinate. Only the Plugin row
// is a socket's entity: the other two are entities of their own kinds, and a build
// reconstructing them as plugins would load a Recipe or a Component as a Plugin.
func TestASocketEntityIsAPluginRowAndNoOtherKindSharingItsCoordinate(t *testing.T) {
	t.Parallel()
	lf := &lockfile.Lockfile{
		Providers: []lockfile.ProviderBinding{{Capability: "helm", Plugin: "provider/helm"}},
		Entities: []lockfile.Entity{
			{Kind: closure.KindComponent, URI: "provider/helm:2.0.0"},
			{Kind: closure.KindPlugin, URI: "provider/helm:1.0.0"},
			{Kind: closure.KindRecipe, URI: "provider/helm:3.0.0"},
		},
	}
	got := lockfile.WorkspaceSocketEntityURIs(lf)
	want := []string{"provider/helm:1.0.0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("WorkspaceSocketEntityURIs = %v; want only the Plugin row, %v", got, want)
	}
}
