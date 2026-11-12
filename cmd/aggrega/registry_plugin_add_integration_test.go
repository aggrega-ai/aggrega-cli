// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

// Package main's registry round-trip integration coverage for `aggrega plugin
// add`: the discover-and-attach command searches a real registry, resolves the
// match to one plugin identity at its latest published version, and writes the
// attachment into .aggrega-config.yaml — and a subsequent pull resolves and pins
// the just-added config-socket attachment. It reuses the config-socket
// round-trip harness (real in-process OCI registry, real Plugin-entity publish,
// real pull) so the add's write is proven against the same resolve-and-pin path
// a hand-authored attachment travels.
package main

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestIntegration_PluginAdd_ConfigSocket_RoundTrip proves the full discover →
// attach → resolve → pin path for a config-socket plugin. A real mutator Plugin
// entity is published to the in-process registry; `plugin add mutator tag-injector`
// searches, pins the latest published version, and writes a schema-valid
// plugins.governance entry; a subsequent pull resolves the attachment and pins
// both the entity and its .wasm artifact in the lockfile.
func TestIntegration_PluginAdd_ConfigSocket_RoundTrip(t *testing.T) {
	// Resolve the committed .wasm fixture BEFORE any chdir (fixture
	// resolution walks up from cwd); a clean skip only if the toolchain is absent.
	mutatorPath := wasmtest.EnsureFixtureBuilt(t, "mutator-proof-go.wasm")
	std := standardCatalogDir(t)

	host := startRegistry(t, nil)
	configSocketFixtureCatalog(t, host)

	const mutatorSocket = "governance/tag-injector"
	mutatorDigest := publishSocketPluginEntity(t, host, mutatorSocket, "1.0.0", mutatorPath, "governance", "mutate")

	// A workspace with NO plugins block; the add command writes it. auto_pull on so
	// the follow-up pull runs under the hermetic complete-lockfile posture.
	ws := writeConfigSocketWorkspace(t, host, "", true)
	t.Chdir(ws)

	rw, err := workspaceconfig.Resolve(ws, "")
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}

	var out bytes.Buffer
	if err := pluginAddRun(context.Background(), pluginAddConfig{
		Socket: "governance",
		Role:   "label-transform",
		Query:  "tag-injector",
		ws:     rw,
		Out:    &out,
	}); err != nil {
		t.Fatalf("plugin add: %v", err)
	}
	t.Logf("plugin add output: %s", out.String())

	// The add wrote a schema-valid plugins.governance entry (a green re-resolve is
	// the config-schema validation), pinned at the latest published version.
	reread, err := workspaceconfig.Resolve(ws, "")
	if err != nil {
		t.Fatalf("resolve written config: %v", err)
	}
	got := reread.Config().Plugins.Governance
	if len(got) != 1 || got[0].URI != mutatorSocket+":1.0.0" {
		t.Fatalf("plugins.governance = %+v; want one entry %q", got, mutatorSocket+":1.0.0")
	}
	if body, rerr := os.ReadFile(workspaceconfig.ConfigFilename); rerr == nil {
		t.Logf("written .aggrega-config.yaml:\n%s", body)
	}

	// A subsequent pull resolves and pins the just-added config-socket attachment
	// on both axes — the entity and its .wasm artifact.
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml", CatalogDirs: []string{std}}); err != nil {
		t.Fatalf("pull after add: %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor("release.yaml"))
	if err != nil {
		t.Fatalf("load lock: %v", err)
	}
	assertSocketPinnedInLock(t, lf, host, mutatorSocket, mutatorDigest)
}
