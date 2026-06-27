// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"go.yaml.in/yaml/v3"
)

// renderSchemaLibraryFile renders a SchemaLibrary authored as a standalone
// file. Content is reusable JSON-Schema definitions plus optional parameterized
// templates. Each schema def carries an Aggrega-synthesized usage line — the
// `{ $ref: <Library>#/schemas/<Name> }` form a Component / Feature schema writes
// to reference it, by bare-name identity (the version comes from the per-Release
// lockfile). Templates render their $params declarations.
//
//   - levelList: schema def names, then template names.
//   - levelDefault: per-def usage line, per-template $params, labels / annotations.
//   - levelDetail: adds the full def body and full template bodies.
func renderSchemaLibraryFile(_ *workspaceconfig.ResolvedWorkspace, path string, data []byte, _ []string, opts inspectOptions) error {
	if opts.output == outputYAML {
		return emitYAMLRoundTrip(opts, path, data)
	}
	var lib types.SchemaLibrary
	if err := yaml.Unmarshal(data, &lib); err != nil {
		return fmt.Errorf("parse SchemaLibrary at %q: %w", path, err)
	}
	name := lib.Metadata.Name

	if opts.level == levelList {
		for def := range lib.Spec.Schemas.All() {
			fmt.Fprintln(opts.out, def)
		}
		for tmpl := range lib.Spec.Templates.All() {
			fmt.Fprintln(opts.out, tmpl)
		}
		return nil
	}

	renderHeader(opts, "SchemaLibrary", identifierVersion(name, lib.Metadata.Version))
	if lib.Metadata.Description != "" {
		fmt.Fprintf(opts.out, "Description: %s\n", lib.Metadata.Description)
	}

	fmt.Fprintln(opts.out, "Schemas:")
	for def, entry := range lib.Spec.Schemas.All() {
		fmt.Fprintf(opts.out, "  %s\n", def)
		fmt.Fprintf(opts.out, "    Use: { $ref: %s#/schemas/%s }\n", name, def)
		if opts.level == levelDetail {
			if err := renderYAMLNodeIndented(opts, entry.Content); err != nil {
				return err
			}
		}
	}

	if lib.Spec.Templates.Len() > 0 {
		fmt.Fprintln(opts.out, "Templates:")
		for tmpl, entry := range lib.Spec.Templates.All() {
			fmt.Fprintf(opts.out, "  %s\n", tmpl)
			if entry.Params.Len() > 0 {
				fmt.Fprintln(opts.out, "    params:")
				for pName, p := range entry.Params.All() {
					line := fmt.Sprintf("      %s: %s", pName, p.Type)
					if p.Default != nil {
						line += fmt.Sprintf(" (default: %v)", p.Default)
					}
					fmt.Fprintln(opts.out, line)
				}
			}
			if opts.level == levelDetail {
				if err := renderYAMLNodeIndented(opts, entry.Body); err != nil {
					return err
				}
			}
		}
	}

	renderLibraryMetadata(opts, lib.Metadata.Labels, lib.Metadata.Annotations)
	return nil
}
