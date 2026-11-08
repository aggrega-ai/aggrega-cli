// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/serve"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// resolveWorkspaceExtends resolves the extends chains of cat, loaded over dirs,
// reading a parent cat does not hold where a build reads one: from the content
// cache under the pins the lockfiles of release record, or, where release is
// empty, the pins of every Release the workspace holds (serve.CatalogPassPins).
// So a base a pull fetched and pinned resolves here as it does in a build of a
// Release that reaches it, and nothing is fetched.
//
// release is the Release file as the command was given it, read against the
// working directory, so it is made absolute before the decision of which
// lockfiles to read joins a relative path to the workspace root.
func resolveWorkspaceExtends(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, cat *catalog.Catalog, release string, dirs, named []string) ([]*validator.ValidationError, error) {
	pass := workspaceconfig.CatalogPass{}
	if release != "" {
		abs, err := filepath.Abs(release)
		if err != nil {
			return nil, err
		}
		pass.Releases = []string{abs}
	}
	pins, err := serve.CatalogPassPins(rw, pass)
	if err != nil {
		return nil, err
	}
	return cat.ResolveAllExtendsWithPins(ctx, rw.Root(), rw.Config().ResolvedArtifactCacheDir(), dirs, named, pins), nil
}
