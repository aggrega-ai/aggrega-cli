// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredshape"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// renderMappingLibraryFile renders a MappingLibrary authored as a standalone
// file. Each mapping entry is shown with an Aggrega-synthesized usage line — the
// `valuesMapping: { $import: <Library>#/mappings/<Entry> }` form a Recipe writes
// to consume it. The reference uses the library's bare-name identity (the
// version comes from the per-Release lockfile; no registry host enters the
// reference).
//
//   - levelList: mapping entry names only.
//   - levelDefault: header, description, per-entry usage line, labels / annotations.
//   - levelDetail: adds the full Go-template body per entry, verbatim.
func renderMappingLibraryFile(_ context.Context, _ *workspaceconfig.ResolvedWorkspace, path string, data []byte, _ []string, opts inspectOptions) error {
	if opts.output == outputYAML {
		return emitYAMLRoundTrip(opts, path, data)
	}
	var lib types.MappingLibrary
	if err := yaml.Unmarshal(data, &lib); err != nil {
		return fmt.Errorf("parse MappingLibrary at %q: %w", path, authoredshape.DescribeYAMLDecodeErrorIn(err, data))
	}
	name := lib.Metadata.Name

	if opts.level == levelList {
		for entry := range lib.Spec.Mappings.All() {
			fmt.Fprintln(opts.out, entry)
		}
		return nil
	}

	renderIdentityBlock(opts, "MappingLibrary", identifierVersion(name, lib.Metadata.Version), lib.Metadata.Description)

	fmt.Fprintln(opts.out, "Mappings:")
	for entry, mapping := range lib.Spec.Mappings.All() {
		fmt.Fprintf(opts.out, "  %s\n", entry)
		fmt.Fprintf(opts.out, "    Use: valuesMapping: { $import: %s#/mappings/%s }\n", name, entry)
		if opts.level == levelDetail {
			renderTemplateBodyIndented(opts, mapping.Body)
		}
	}

	renderLibraryMetadata(opts, lib.Metadata.Labels, lib.Metadata.Annotations)
	return nil
}

// renderLibraryMetadata prints a library's labels and annotations in author
// order, shared by the MappingLibrary and SchemaLibrary renderers.
func renderLibraryMetadata(opts inspectOptions, labels, annotations types.OrderedMap[string]) {
	if labels.Len() > 0 {
		fmt.Fprintln(opts.out, "Labels:")
		for k, v := range labels.All() {
			fmt.Fprintf(opts.out, "  %s: %s\n", k, v)
		}
	}
	if annotations.Len() > 0 {
		fmt.Fprintln(opts.out, "Annotations:")
		for k, v := range annotations.All() {
			fmt.Fprintf(opts.out, "  %s: %s\n", k, v)
		}
	}
}
