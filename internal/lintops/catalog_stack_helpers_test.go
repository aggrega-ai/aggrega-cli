// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// passesOf returns the whole-catalog passes rw runs, failing the test on a
// refusal: a test that means to lint a catalog lists directories that are there.
func passesOf(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, releases func() []string, flags []string) []workspaceconfig.CatalogPass {
	t.Helper()
	passes, err := rw.WholeCatalogPasses(releases, flags)
	if err != nil {
		t.Fatalf("the whole-catalog passes were refused: %v", err)
	}
	return passes
}

// workspaceStackOf returns the workspace's own catalog search stack and its named
// part, failing the test on a refusal, as passesOf does.
func workspaceStackOf(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) (dirs, named []string) {
	t.Helper()
	dirs, err := rw.ResolveCatalogDirs("", nil)
	if err != nil {
		t.Fatalf("the workspace's own stack was refused: %v", err)
	}
	named, err = rw.NamedCatalogDirs("", nil)
	if err != nil {
		t.Fatalf("the workspace's own stack was refused: %v", err)
	}
	return dirs, named
}
