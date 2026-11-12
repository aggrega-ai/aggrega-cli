// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"io"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// validateCatalogWide validates the catalog under dirs as the single pass of a
// workspace naming no Release is validated (validateWorkspacePass), with pins as
// the pins that pass reads, for a test that holds the directories and the pins
// itself rather than a resolved workspace. named is the part of dirs somebody
// named, as the resolver returned it.
func validateCatalogWide(ctx context.Context, wsRoot, cacheDir string, dirs, named []string, pins catalog.Pins, diagOut, out io.Writer) error {
	pass := workspaceconfig.CatalogPass{Dirs: dirs, Named: named}
	return validateWorkspacePass(ctx, wsRoot, cacheDir, pass,
		func(workspaceconfig.CatalogPass) (catalog.Pins, error) { return pins, nil }, diagOut, out)
}
