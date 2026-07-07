// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

// ResolveSourceSelection locates the base compose document within a source
// listing and returns its path relative to the source root, so a guest can ask
// the host to pack that single file. It is pure over the listing and touches no
// filesystem.
//
// Two input shapes are accepted, matching the acquired-source shapes the compose
// provider sees. A regular-file source root is the compose document itself and
// resolves to the empty relative path (the root). A directory source root is
// scanned for its single top-level .yaml/.yml entry — the shape a
// compose-fragment cache slot extracts to. Zero or more than one such entry is a
// Loud Guardrail halt naming the source, so an author of a multi-file fragment
// sees a clear signal rather than a silent first-wins.
func ResolveSourceSelection(listing *rendercore.SourceListing) (string, error) {
	if !listing.RootIsDir {
		return "", nil
	}
	var yamlEntries []string
	for _, e := range listing.Entries {
		if e.IsDir || strings.Contains(e.Path, "/") {
			continue
		}
		if strings.HasSuffix(e.Path, ".yaml") || strings.HasSuffix(e.Path, ".yml") {
			yamlEntries = append(yamlEntries, e.Path)
		}
	}
	switch len(yamlEntries) {
	case 0:
		return "", fmt.Errorf("compose: locate source in %q: directory contains no .yaml/.yml entries", listing.Root)
	case 1:
		return yamlEntries[0], nil
	default:
		return "", fmt.Errorf("compose: locate source in %q: directory contains %d .yaml/.yml entries; expected exactly one", listing.Root, len(yamlEntries))
	}
}
