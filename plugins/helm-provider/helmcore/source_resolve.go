// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

// ResolveChartRootRel locates the Helm chart root within a source listing and
// returns its path relative to the source root, so a guest can ask the host to
// pack that subtree. It is pure over the listing and touches no filesystem: the
// host enumerates the acquired source into the listing, and this decides which
// subtree is the chart.
//
// The resolution is a Loud Guardrail. A non-directory source (an archive) and a
// directory carrying Chart.yaml at its root both resolve to the root itself
// (the empty relative path). Otherwise the single top-level subdirectory
// carrying Chart.yaml is the chart root — the OCI/tgz convention where an
// extracted archive nests the chart one level down. Zero such subdirectories is
// a malformed archive and more than one is ambiguous; both halt rather than
// guess. Listing entries are sorted, so the ambiguity message and the
// single-match result are deterministic.
func ResolveChartRootRel(listing *rendercore.SourceListing) (string, error) {
	if !listing.RootIsDir {
		return "", nil
	}
	if listingHasFile(listing, "Chart.yaml") {
		return "", nil
	}
	var roots []string
	for _, e := range listing.Entries {
		if !e.IsDir || strings.Contains(e.Path, "/") {
			continue
		}
		if listingHasFile(listing, e.Path+"/Chart.yaml") {
			roots = append(roots, e.Path)
		}
	}
	switch len(roots) {
	case 0:
		return "", errors.New("no Chart.yaml at the extracted archive root or in any top-level subdirectory")
	case 1:
		return roots[0], nil
	default:
		return "", fmt.Errorf("ambiguous chart layout: top-level subdirectories %v each contain Chart.yaml; cannot choose a chart root", roots)
	}
}

// listingHasFile reports whether the listing carries a regular-file entry at the
// given root-relative path.
func listingHasFile(listing *rendercore.SourceListing, path string) bool {
	for _, e := range listing.Entries {
		if e.Path == path && !e.IsDir {
			return true
		}
	}
	return false
}
