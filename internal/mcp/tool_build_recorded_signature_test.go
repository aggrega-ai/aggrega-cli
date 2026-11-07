// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The build tool reaches the check of the signature facts a lockfile records, as
// the command line's build does: the hermetic fixture is resolved through the pull
// tool, so its lockfile matches its closure, and one of its Components is then
// moved into the content cache as a registry fetch lays it and removed from the
// catalog directory. The resolution recorded no signature for it, and the
// workspace's default registry is the official hub, whose built-in policy requires
// one — so the build refuses the row. The positive control differs in one setting:
// the hub's own skip, which a pull honours and so the build does too.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const recordedSignatureComponent = "core/compute/api-server"

// moveComponentIntoTheCache lays the Component's cache slot — its content and the
// manifest a registry serves for it — at the version the lockfile pins, and
// removes the authored copy, so the build can read it from the cache alone.
func moveComponentIntoTheCache(t *testing.T, root string) {
	t.Helper()
	authored := filepath.Join(root, "catalog", "base", "components", "api-server.yaml")
	body, err := os.ReadFile(authored)
	if err != nil {
		t.Fatalf("read the authored Component: %v", err)
	}
	ctx := context.Background()
	store := memory.New()
	if _, err := oci.Push(ctx, store, "seed", oci.MediaTypeComponent, body, nil); err != nil {
		t.Fatalf("seed push: %v", err)
	}
	pulled, err := oci.Pull(ctx, store, "seed")
	if err != nil {
		t.Fatalf("seed pull: %v", err)
	}
	const cacheDir = ".aggrega/cache"
	slot := recordedSignatureComponent + ":1.0.0"
	if err := oci.WriteCached(root, cacheDir, "Component", slot, "1.0.0", body); err != nil {
		t.Fatalf("lay the Component's cache slot: %v", err)
	}
	if err := oci.WriteCachedManifest(root, cacheDir, "Component", slot, "1.0.0", pulled.ManifestBytes); err != nil {
		t.Fatalf("lay the Component's cached manifest: %v", err)
	}
	if err := os.Remove(authored); err != nil {
		t.Fatalf("remove the authored Component: %v", err)
	}
}

// TestTheBuildToolRefusesACacheServedRowRecordingNoSignature is the refusal on this
// surface, pinned to the condition the check raises.
func TestTheBuildToolRefusesACacheServedRowRecordingNoSignature(t *testing.T) {
	s, root, dirs, releasePath := pullWorkspace(t)
	resolveTheWorkspace(t, s, root, dirs, releasePath)
	moveComponentIntoTheCache(t, root)

	res := callBuild(t, s, root, dirs, releasePath)
	if !res.IsError {
		t.Fatalf("the build tool built a cache-served row recording no signature under the hub's built-in "+
			"requirement: %s", res.Content[0].Text)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, lockfile.ErrSignatureNotRecorded().Error()) {
		t.Errorf("the refusal does not carry the not-recorded condition:\n%s", text)
	}
	if !strings.Contains(text, recordedSignatureComponent) {
		t.Errorf("the refusal does not name the row:\n%s", text)
	}
}

// TestTheBuildToolAdmitsTheSameRowUnderTheHubsOwnSkip is the matched positive
// control.
func TestTheBuildToolAdmitsTheSameRowUnderTheHubsOwnSkip(t *testing.T) {
	s, root, dirs, releasePath := pullWorkspace(t)
	resolveTheWorkspace(t, s, root, dirs, releasePath)
	moveComponentIntoTheCache(t, root)
	cfg := "workspace: {}\noffline: true\nregistries:\n  - host: " + workspaceconfig.HubBaselineHost +
		"\n    skip_signature_verification: true\n"
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write the workspace config: %v", err)
	}

	res := callBuild(t, s, root, dirs, releasePath)
	if res.IsError {
		t.Fatalf("the build tool refused a row the hub's own skip admits: %s", res.Content[0].Text)
	}
	if len(unmarshalBuildPayload(t, res).Files) == 0 {
		t.Fatal("the admitted build returned no rendered files")
	}
}
