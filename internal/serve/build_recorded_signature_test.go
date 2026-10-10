// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// The daemon's build reaches the check of the signature facts a lockfile
// records, as the command line's does: one workspace whose lockfile the daemon's
// own fill derived, then its Component moved into the content cache as a registry
// fetch lays it and removed from the catalog directory. The fill recorded no
// signature for it, and the daemon's default registry is the official hub, whose
// built-in policy requires one — so the next build refuses the row. The positive
// control differs in one setting: the hub's own skip, which a pull honours and so
// the build does too.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// cacheServedComponentWorkspace returns a workspace whose lockfile matches its
// closure and whose one Component is served from the content cache.
func cacheServedComponentWorkspace(t *testing.T) (ws, releaseRel string) {
	t.Helper()
	ws, releaseRel = autoPullWorkspace(t)
	if resp, pushed := driveBuildWithRawParams(t, newFillCapableServer(t, ws),
		`{"release":"`+releaseRel+`","autoPull":true}`); resp.Error != nil {
		t.Fatalf("the fill that derives the lockfile refused: code %d: %s\nwire:\n%s", resp.Error.Code, resp.Error.Message, pushed)
	}
	authored := filepath.Join(ws, "catalog", "components", "svc.yaml")
	body, err := os.ReadFile(authored)
	if err != nil {
		t.Fatalf("read the authored Component: %v", err)
	}
	const cacheDir = ".aggrega/cache"
	if err := oci.WriteCached(ws, cacheDir, "demo/svc:1.0.0", "1.0.0", body); err != nil {
		t.Fatalf("lay the Component's cache slot: %v", err)
	}
	if err := oci.WriteCachedManifest(ws, cacheDir, "demo/svc:1.0.0", "1.0.0", componentManifestFor(t, body)); err != nil {
		t.Fatalf("lay the Component's cached manifest: %v", err)
	}
	if err := os.Remove(authored); err != nil {
		t.Fatalf("remove the authored Component: %v", err)
	}
	return ws, releaseRel
}

// TestTheDaemonBuildRefusesACacheServedRowRecordingNoSignature is the refusal on
// this surface, asserted against the condition the check raises.
func TestTheDaemonBuildRefusesACacheServedRowRecordingNoSignature(t *testing.T) {
	ws, releaseRel := cacheServedComponentWorkspace(t)

	resp, pushed := driveBuildWithRawParams(t, newFillCapableServer(t, ws), `{"release":"`+releaseRel+`"}`)
	if resp.Error == nil {
		t.Fatalf("the daemon built a cache-served row recording no signature under the hub's built-in "+
			"requirement\nwire:\n%s", pushed)
	}
	if !strings.Contains(pushed, lockfile.ErrSignatureNotRecorded().Error()) {
		t.Errorf("the refusal does not carry the not-recorded condition:\n%s", pushed)
	}
	if !strings.Contains(pushed, "demo/svc:1.0.0") {
		t.Errorf("the refusal does not name the row:\n%s", pushed)
	}
}

// TestTheDaemonBuildAdmitsTheSameRowUnderTheHubsOwnSkip is the matched positive
// control: one setting different, and the build goes ahead.
func TestTheDaemonBuildAdmitsTheSameRowUnderTheHubsOwnSkip(t *testing.T) {
	ws, releaseRel := cacheServedComponentWorkspace(t)

	srv := newFillCapableServer(t, ws)
	srv.wsConfig.Registries = append(srv.wsConfig.Registries, workspaceconfig.RegistryConfig{
		Host: workspaceconfig.HubBaselineHost, SkipSignatureVerification: true,
	})
	resp, pushed := driveBuildWithRawParams(t, srv, `{"release":"`+releaseRel+`"}`)
	if resp.Error != nil {
		t.Fatalf("the daemon refused a row the hub's own skip admits: code %d: %s\nwire:\n%s",
			resp.Error.Code, resp.Error.Message, pushed)
	}
}
