// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// TestIntegration_CatalogAuthoredVerdictAttachmentNeverReachesARegistry drives a
// governance plugin authored in a catalog directory, with its .wasm beside it and
// attached at a verdict role, against a registry that publishes the same identity
// with a guest that rejects every build.
//
// A verdict plugin is cached rather than pinned, and the pull resolves its artifact
// so a build can run it; an entity authored in a catalog directory has that artifact
// read from this machine, never fetched, so the pull asks the registry nothing about
// the plugin. The build then runs the local guest, which accepts: had it run the
// registry's guest it would have refused.
func TestIntegration_CatalogAuthoredVerdictAttachmentNeverReachesARegistry(t *testing.T) {
	local := wasmtest.EnsureFixtureBuilt(t, "policy-allow-go.wasm")
	published := wasmtest.EnsureFixtureBuilt(t, "policy-go.wasm")
	guest, err := os.ReadFile(local)
	if err != nil {
		t.Fatalf("read the local guest: %v", err)
	}

	var log requestLog
	host := startRegistry(t, log.wrap)
	if digest := publishSocketPluginEntity(t, host, catalogVerdictAttachment.artifact, "1.0.0", published,
		"governance", catalogVerdictAttachment.export); digest == "" {
		t.Fatal("the registry-side publish reported no digest")
	}
	mark := log.len()
	if mark == 0 {
		t.Fatal("the request log recorded nothing for the publish, so it cannot show what the pull asks for")
	}

	root, releasePath, _ := catalogGovernanceAttachmentWorkspace(t, catalogVerdictAttachment, guest,
		"default_registry: \""+host+"\"\noffline: false\n")
	t.Chdir(root)
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

	if err := pullRun(context.Background(), pullConfig{ws: ws, ReleasePath: releasePath}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	pluginRepository := "/v2/plugin/" + catalogVerdictAttachment.artifact + "/"
	for _, req := range log.since(mark) {
		if strings.Contains(req, pluginRepository) {
			t.Errorf("the pull asked the registry for the catalog-authored verdict attachment's plugin: %s", req)
		}
	}
	if err := dispatchBuild(context.Background(), ws, releasePath, nil, false, io.Discard, io.Discard); err != nil {
		t.Fatalf("the build over the lockfile the pull wrote, with the local accepting guest attached: %v", err)
	}
}
