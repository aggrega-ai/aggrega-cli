// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// renderArtifactsLibraryFile renders an ArtifactsLibrary authored as a standalone
// file. Each bundle is shown with an Aggrega-synthesized usage line — the
// `artifacts.<name>: { $import: <Library>#/artifacts/<Entry> }` form a Recipe /
// FeatureRecipe writes to consume it. The reference uses the library's bare-name
// identity (the version comes from the per-Release lockfile; no registry host
// enters the reference).
//
//   - levelList: bundle names only.
//   - levelDefault: header, description, per-bundle usage line + file list, labels / annotations.
//   - levelDetail: adds the full Go-template body per file, verbatim.
func renderArtifactsLibraryFile(_ context.Context, _ *workspaceconfig.ResolvedWorkspace, path string, data []byte, _ []string, opts inspectOptions) error {
	if opts.output == outputYAML {
		return emitYAMLRoundTrip(opts, path, data)
	}
	var lib types.ArtifactsLibrary
	if err := yaml.Unmarshal(data, &lib); err != nil {
		return fmt.Errorf("parse ArtifactsLibrary at %q: %w", path, err)
	}
	name := lib.Metadata.Name

	if opts.level == levelList {
		for bundle := range lib.Spec.Artifacts.All() {
			fmt.Fprintln(opts.out, bundle)
		}
		return nil
	}

	renderIdentityBlock(opts, "ArtifactsLibrary", identifierVersion(name, lib.Metadata.Version), lib.Metadata.Description)

	fmt.Fprintln(opts.out, "Artifacts:")
	for bundle, entry := range lib.Spec.Artifacts.All() {
		fmt.Fprintf(opts.out, "  %s\n", bundle)
		fmt.Fprintf(opts.out, "    Use: artifacts.<name>: { $import: %s#/artifacts/%s }\n", name, bundle)
		for _, fname := range entry.Files.Keys() {
			fmt.Fprintf(opts.out, "    - %s\n", fname)
			if opts.level == levelDetail {
				body, _ := entry.Files.Get(fname)
				renderTemplateBodyIndented(opts, body)
			}
		}
	}

	renderLibraryMetadata(opts, lib.Metadata.Labels, lib.Metadata.Annotations)
	return nil
}
