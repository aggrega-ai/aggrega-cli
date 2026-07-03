// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

package compose

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ProjectComposeSource reads the base compose file from the local source at
// compile time and packs it into a SourceProjection the host hands a provider
// Wasm guest, so the guest reconstructs the source in memory and touches no
// filesystem. It resolves the base compose file the same way the host sourced
// path does (resolveComposeSourcePath), reads it, and returns a one-file
// SourceProjection keyed by the file's base name. This is what the host does at
// compile time; the render core (renderSourcedCore) then renders identically
// whether the source was read from a path (host) or from these bytes (guest).
func ProjectComposeSource(composePath string) (*renderer.SourceProjection, error) {
	resolvedPath, err := resolveComposeSourcePath(composePath)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: composePath}, fmt.Sprintf("compose: locate source in %q", composePath))
	}
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: resolvedPath}, fmt.Sprintf("compose: project source from %q", resolvedPath))
	}

	return &renderer.SourceProjection{
		Files: []renderer.SourceFile{
			{Path: filepath.Base(resolvedPath), Content: data},
		},
	}, nil
}
