// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// The pin step over a listener takes exactly the dependency resolution's
// posture: it writes the lock beside an entity and reaches the hosts that entity's
// sources name, spending this host's credentials, so it is admitted under the
// workspace-write grant and refused under the read scope. These are driven over a
// real listener with tokens minted by the in-process authorization server, and
// every cell asserts both sides of the line — what the credential is offered and
// what a call to the tool is answered with.
//
// The subjects named do not exist in the fixture workspace, deliberately: what a
// cell separates is a call that was REFUSED from one that RAN, and a call that ran
// and reported that it found no such entity is on the admitted side of that line.

// TestThePinToolIsRefusedUnderTheReadScopeAndServedUnderWorkspaceWrite is the
// listener cell the pin step owes: read scope refused naming the scope that
// admits it, workspace-write served.
func TestThePinToolIsRefusedUnderTheReadScopeAndServedUnderWorkspaceWrite(t *testing.T) {
	f := newScopeGridFixture(t, false)
	call := map[string]any{"entity": "catalog/recipe/helm/absent/1.0.0/recipe.yaml"}

	t.Run("read scope", func(t *testing.T) {
		credential := f.grantOf(t, scopeRead)
		if offered := append(f.listed(t, credential), f.surfaced(t, credential)...); slices.Contains(offered, "pin") {
			t.Errorf("the read scope was offered the pin tool; it was offered %v", offered)
		}
		wantRefusedForWantOfScope(t, "pin", f.callTool(t, credential, "pin", call),
			scopeAdmittingInThisTest(opsurface.ReachWorkspace))
	})
	t.Run("workspace-write grant", func(t *testing.T) {
		credential := f.grantOf(t, scopeWorkspaceWrite)
		if offered := append(f.listed(t, credential), f.surfaced(t, credential)...); !slices.Contains(offered, "pin") {
			t.Errorf("the workspace-write grant was not offered the pin tool; it was offered %v", offered)
		}
		wantTheCallRan(t, "pin", f.callTool(t, credential, "pin", call))
	})
}

// TestAPullAskedToPinIsHeldToTheSamePosture drives the resolution's own argument:
// asking it to pin changes nothing about who may call it, because pinning is
// inside what the resolution already reaches.
func TestAPullAskedToPinIsHeldToTheSamePosture(t *testing.T) {
	f := newScopeGridFixture(t, false)
	call := map[string]any{"release": "release.yaml", "pin_sources": true}
	wantRefusedForWantOfScope(t, "pull", f.callTool(t, f.grantOf(t, scopeRead), "pull", call),
		scopeAdmittingInThisTest(opsurface.ReachWorkspace))
	wantTheCallRan(t, "pull", f.callTool(t, f.grantOf(t, scopeWorkspaceWrite), "pull", call))
}

// TestAPublicationAskedToPinWidensToTheWorkspace is the interaction cell: the
// publication declares the registry, and a call asking it to pin first also
// writes inside the workspace, so the registry-write grant alone is refused
// naming the workspace-write scope, and the two together are served. Without the
// widening, a caller granted only the right to publish could make this host write
// a lock and fetch from any host an entity names.
//
// The control is the same call with the pin not asked for, which the
// registry-write grant alone admits — so the refusal above is the pin's, not the
// publication's.
func TestAPublicationAskedToPinWidensToTheWorkspace(t *testing.T) {
	f := newScopeGridFixture(t, false)
	pinning := map[string]any{"entity": "component.yaml", "dry_run": true, "pin_sources": true}
	plain := map[string]any{"entity": "component.yaml", "dry_run": true}

	wantRefusedForWantOfScope(t, "publish-entity",
		f.callTool(t, f.grantOf(t, scopeRegistryWrite), "publish-entity", pinning),
		scopeAdmittingInThisTest(opsurface.ReachWorkspace))
	wantTheCallRan(t, "publish-entity",
		f.callTool(t, f.grantOf(t, scopeRegistryWrite, scopeWorkspaceWrite), "publish-entity", pinning))
	wantTheCallRan(t, "publish-entity",
		f.callTool(t, f.grantOf(t, scopeRegistryWrite), "publish-entity", plain))
}
