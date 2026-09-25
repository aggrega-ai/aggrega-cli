// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// catalogVerdictAttachment is the governance plugin the fixtures below attach at the
// verdict role that reads the build graph, with its entity authored in the
// workspace's own catalog directory.
var catalogVerdictAttachment = catalogGovernanceAttachment{
	artifact: "governance/gate",
	export:   "verdict_ir",
	role:     "ir-verdict",
}

// TestPullReadsACatalogAuthoredVerdictAttachmentFromThisMachine drives the real pull
// and build over a governance plugin authored in a catalog directory and attached at
// a verdict role, in an offline workspace.
//
// A verdict plugin is cached rather than pinned, and the pull resolves its artifact
// so the build can run it. For an entity authored in a catalog directory that artifact
// is read from this machine, as every such entity's is: with the module beside the
// entity the pull completes and records no plugin row, and the build runs the gate.
// With no module on any local tier the pull refuses with the condition an absent
// artifact raises, naming the entity, the file and the directory, and writes nothing.
func TestPullReadsACatalogAuthoredVerdictAttachmentFromThisMachine(t *testing.T) {
	t.Run("the module sits beside the entity", func(t *testing.T) {
		guest, err := os.ReadFile(wasmtest.EnsureFixtureBuilt(t, "policy-allow-go.wasm"))
		if err != nil {
			t.Fatalf("read the guest fixture: %v", err)
		}
		root, releasePath, _ := catalogGovernanceAttachmentWorkspace(t, catalogVerdictAttachment, guest, "offline: true\n")
		t.Chdir(root)
		ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

		if err := pullRun(context.Background(), pullConfig{ws: ws, ReleasePath: releasePath}); err != nil {
			t.Fatalf("the pull refused a verdict attachment whose module sits beside its catalog entity: %v", err)
		}
		lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
		if err != nil {
			t.Fatalf("load the written lockfile: %v", err)
		}
		for _, p := range lf.Plugins {
			if p.URI == catalogVerdictAttachment.artifact {
				t.Errorf("the lockfile pins the verdict attachment's artifact %+v; a verdict plugin is never pinned", p)
			}
		}
		if err := dispatchBuild(context.Background(), ws, releasePath, nil, false, io.Discard, io.Discard); err != nil {
			t.Fatalf("the build over the lockfile the pull wrote: %v", err)
		}
	})

	t.Run("no local tier holds the module", func(t *testing.T) {
		root, releasePath, modulePath := catalogGovernanceAttachmentWorkspace(t, catalogVerdictAttachment, nil, "offline: true\n")
		t.Chdir(root)
		ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

		err := pullRun(context.Background(), pullConfig{ws: ws, ReleasePath: releasePath})
		if !errors.Is(err, pluginpath.ErrPluginArtifactNotFound()) {
			t.Fatalf("the pull must refuse with the absent-artifact condition; got %v", err)
		}
		for _, want := range []string{
			catalogVerdictAttachment.artifact + ":1.0.0",
			filepath.Base(modulePath),
			filepath.Dir(modulePath),
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %q:\n%v", want, err)
			}
		}
		if _, statErr := os.Stat(lockfile.PathFor(releasePath)); !errors.Is(statErr, fs.ErrNotExist) {
			t.Errorf("the refused pull wrote a lockfile (stat: %v); a refusal writes nothing", statErr)
		}
	})
}
