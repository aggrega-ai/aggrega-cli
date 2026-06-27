// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"go.yaml.in/yaml/v3"
)

// renderComponentFile renders a Component authored as a standalone file. It
// parses into the shared types.Component (single-sourcing the entity shape with
// the catalog loader rather than a narrowed inline struct) and prints the three
// detail levels through renderComponentStructured. The --output yaml path is a
// verbatim node round-trip preserving authoring bytes.
//
// A Component file is self-contained, so the --raw / --resolved override has no
// effect here (opts.mode is read by the resolved instance kinds, not by the
// as-authored kinds); file-path inspection does not resolve the extends chain.
func renderComponentFile(_ *workspaceconfig.ResolvedWorkspace, path string, data []byte, _ []string, opts inspectOptions) error {
	if opts.output == outputYAML {
		return emitYAMLRoundTrip(opts, path, data)
	}
	var comp types.Component
	if err := yaml.Unmarshal(data, &comp); err != nil {
		return fmt.Errorf("parse Component at %q: %w", path, err)
	}
	identifier := identifierVersion(comp.Metadata.Name, comp.Metadata.Version)
	if comp.Metadata.Name == "" {
		identifier = path
	}
	return renderComponentStructured(comp, identifier, opts)
}

// renderComponentStructured prints a parsed Component at the requested detail
// level. Shared by the catalog-name path (resolved Component) and the file path
// (as-authored Component); identifier is the display name (with version, or the
// source path for an unnamed file).
//
//   - levelList: the identifier plus its usesSchema socket names.
//   - levelDefault: header, schema body, and the usesSchema sockets (selector).
//   - levelDetail: adds inject mechanisms, requiredFeatures, secretsSchema, and
//     metadata labels.
//
// All map iteration uses OrderedMap.All() for author-order determinism.
func renderComponentStructured(comp types.Component, identifier string, opts inspectOptions) error {
	if opts.level == levelList {
		fmt.Fprintln(opts.out, opts.colorize.uri(identifier))
		for socket := range comp.Spec.UsesSchema.All() {
			fmt.Fprintln(opts.out, socket)
		}
		return nil
	}

	renderHeader(opts, "Component", identifier)

	if err := renderSchemaBody(opts, "Schema:", comp.Spec.Schema); err != nil {
		return err
	}

	if comp.Spec.UsesSchema.Len() > 0 {
		fmt.Fprintln(opts.out, "Uses sockets:")
		for socket, entry := range comp.Spec.UsesSchema.All() {
			req := ""
			if entry.Required {
				req = " (required)"
			}
			fmt.Fprintf(opts.out, "  %s: selector=%q%s\n", socket, entry.Selector, req)
		}
	}

	if opts.level == levelDetail {
		if len(comp.Spec.Inject) > 0 {
			fmt.Fprintf(opts.out, "Self-inject mechanisms: %v\n", comp.Spec.Inject)
		}
		if len(comp.Spec.RequiredFeatures) > 0 {
			fmt.Fprintln(opts.out, "Required features:")
			for _, rf := range comp.Spec.RequiredFeatures {
				fmt.Fprintf(opts.out, "  - %s\n", rf)
			}
		}
		if comp.Spec.SecretsSchema.Len() > 0 {
			fmt.Fprintln(opts.out, "Secret slots:")
			for slot, entry := range comp.Spec.SecretsSchema.All() {
				req := ""
				if entry.Required {
					req = " (required)"
				}
				fmt.Fprintf(opts.out, "  %s%s\n", slot, req)
			}
		}
		if comp.Metadata.Labels.Len() > 0 {
			fmt.Fprintln(opts.out, "Labels:")
			for k, v := range comp.Metadata.Labels.All() {
				fmt.Fprintf(opts.out, "  %s: %s\n", k, v)
			}
		}
	}
	return nil
}
