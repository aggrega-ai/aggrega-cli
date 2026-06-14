// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"os"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// resolveCatalogDirs computes the effective catalog search stack shared by every
// command that loads a catalog: the repeatable `--catalog` flag directories
// PREPENDED to the workspace config's `catalog_dirs`, falling back to `fallback`
// when neither is set.
//
// The list is an ordered first-win precedence stack (the loader resolves a
// duplicate identity to the earliest-listed directory and records the rest as
// shadows). Prepending the flag dirs makes `--catalog` additive and
// override-capable in one move: an explicit `--catalog ./x` is searched before
// the configured directories, shadowing them on collision, while the configured
// directories still resolve everything `./x` does not carry.
//
// `catalog_dirs_append` is already folded into `cfg.Workspace.CatalogDirs` at
// config-load time, so it needs no handling here. The fallback differs per
// surface — the current directory for the Release-scoped commands (build, pull,
// inspect), the workspace root for the whole-catalog lint — and is passed in
// rather than hardcoded so precedence has exactly one home.
func resolveCatalogDirs(flagDirs []string, cfg workspaceconfig.Config, fallback string) []string {
	dirs := make([]string, 0, len(flagDirs)+len(cfg.Workspace.CatalogDirs))
	dirs = append(dirs, flagDirs...)
	dirs = append(dirs, cfg.Workspace.CatalogDirs...)
	if len(dirs) == 0 {
		dirs = []string{fallback}
	}
	return dirs
}

// reportCatalogShadows prints, to stderr, each entity a higher-precedence catalog
// directory overrode during a multi-directory load — the observable half of the
// first-win stack. A single-directory load records no shadows, so this is a no-op
// there. Diagnostic only; it never changes which entity resolved.
func reportCatalogShadows(cat *catalog.Catalog) {
	if cat == nil || len(cat.Shadows) == 0 {
		return
	}
	for _, sh := range cat.Shadows {
		fmt.Fprintf(os.Stderr, "catalog: %s %q in %s is shadowed by %s (first-win precedence)\n",
			sh.Kind, sh.Identity, sh.ShadowedDir, sh.WinnerDir)
	}
}
