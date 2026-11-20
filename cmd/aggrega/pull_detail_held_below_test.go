// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// `aggrega pull --detail` names each reference the pull settles below the newest
// version held, with the constraints that hold it, and says nothing of a reference
// selected at its newest.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// pullWithDetail pulls the Release at releasePath in the workspace at root and
// returns what the pull printed to its output stream, with --detail set or not.
func pullWithDetail(t *testing.T, root, releasePath string, puller catalog.OCIPuller, detail bool) string {
	t.Helper()
	var diag, out bytes.Buffer
	err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		Detail:      detail,
		DiagOut:     &diag,
		Out:         &out,
		diag:        buildDiagOptions{quiet: true},
		puller:      puller,
	})
	if err != nil {
		t.Fatalf("pull: %v\n%s%s", err, diag.String(), out.String())
	}
	return out.String()
}

func TestPullDetailNamesHeldBelowNewest(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name   string
		root   func(t *testing.T) string
		puller func(t *testing.T) catalog.OCIPuller
	}{
		{name: "from a catalog directory",
			root: func(t *testing.T) string { return instanceVersionsWorkspace(t, "quickstart-web") }},
		{name: "from a registry",
			root:   func(t *testing.T) string { return registryInstanceVersionsWorkspace(t, "quickstart-web") },
			puller: func(t *testing.T) catalog.OCIPuller { return newVersionRegistry(t) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			root := c.root(t)
			var puller catalog.OCIPuller
			if c.puller != nil {
				puller = c.puller(t)
			}
			releasePath := filepath.Join(root, "release.yaml")

			// The pull with --detail runs first: a second pull honours the version the
			// first recorded, and a registry then offers it that one alone.
			detail := pullWithDetail(t, root, releasePath, puller, true)
			lockPath := lockfile.PathFor(releasePath)
			withDetail := mustReadFile(t, lockPath)
			if err := os.Remove(lockPath); err != nil {
				t.Fatal(err)
			}
			plain := pullWithDetail(t, root, releasePath, puller, false)
			if strings.Contains(plain, "newest held") {
				t.Errorf("a pull without --detail names a held reference:\n%s", plain)
			}
			if !bytes.Equal(stripGeneratedAt(withDetail), stripGeneratedAt(mustReadFile(t, lockPath))) {
				t.Errorf("a pull with --detail records a lockfile that differs from the one a pull without it records")
			}
			var held []string
			for _, line := range strings.Split(detail, "\n") {
				if strings.Contains(line, "newest held") {
					held = append(held, line)
				}
			}
			if len(held) != 1 {
				t.Fatalf("--detail prints %d held-reference lines, want 1 (the postgres reference sits at its newest):\n%s", len(held), detail)
			}
			for _, want := range []string{
				"Component core/compute/server ",
				"selected at 1.0.0",
				"below 1.1.0, the newest held",
				"Manifest mapping key core/compute/server:1.0.0",
				"written in manifest.yaml",
				"mapped to recipe compose/server:1.0.0",
				"whose spec.implements is core/compute/server:1.0.0",
			} {
				if !strings.Contains(held[0], want) {
					t.Errorf("the held-reference line lacks %q:\n%s", want, held[0])
				}
			}
			if strings.Contains(detail, "core/datastore/postgres is selected") {
				t.Errorf("--detail names a reference selected at its newest:\n%s", detail)
			}
		})
	}
}
