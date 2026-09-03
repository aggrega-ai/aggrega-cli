// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchartcore

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

// ValidateSourcedChartListing checks that a source listing describes a Helm
// chart directory the helm-chart provider can vendor whole, returning a Loud
// Guardrail error naming the source when it does not. It is pure over the
// listing and touches no filesystem: the host enumerates the acquired source
// into the listing, and this validates its shape before a guest asks the host to
// pack the whole tree.
//
// The source must be a directory (a chart is a directory, never a single file or
// archive here) carrying Chart.yaml at its root. A non-directory source or a
// missing Chart.yaml halts.
func ValidateSourcedChartListing(listing *rendercore.SourceListing) error {
	if !listing.RootIsDir {
		return fmt.Errorf("helmchart sourced: source %q is not a directory", listing.Root)
	}
	if !listingHasFile(listing, chartMetadataFileName) {
		return fmt.Errorf("helmchart sourced: %q has no %s", listing.Root, chartMetadataFileName)
	}
	return nil
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
