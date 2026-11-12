// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"fmt"
	"io"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// reportCatalogShadows writes, to diagOut, each entity a higher-precedence catalog
// directory overrode during a multi-directory load — the observable half of the
// first-win stack. A single-directory load records no shadows, so this is a no-op
// there. Diagnostics are written to diagOut (os.Stderr in production); it never
// changes which entity resolved.
func reportCatalogShadows(cat *catalog.Catalog, diagOut io.Writer) {
	if cat == nil || len(cat.Shadows) == 0 {
		return
	}
	for _, sh := range cat.Shadows {
		fmt.Fprintf(diagOut, "catalog: %s %q in %s is shadowed by %s (first-win precedence)\n",
			sh.Kind, sh.Identity, sh.ShadowedDir, sh.WinnerDir)
	}
}
