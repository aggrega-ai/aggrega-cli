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

// renderComponentFile renders a Component authored as a standalone file. It
// parses into the shared types.Component (single-sourcing the entity shape with
// the catalog loader rather than a narrowed inline struct) and prints the three
// detail levels through renderComponentStructured. The --output yaml path is a
// verbatim node round-trip preserving authoring bytes.
//
// A Component file is self-contained, so the --raw / --resolved override has no
// effect here (opts.mode is read by the resolved instance kinds, not by the
// as-authored kinds); file-path inspection does not resolve the extends chain.
func renderComponentFile(_ context.Context, _ *workspaceconfig.ResolvedWorkspace, path string, data []byte, _ []string, opts inspectOptions) error {
	if opts.output == outputYAML {
		return emitYAMLRoundTrip(opts, path, data)
	}
	var comp types.Component
	if err := yaml.Unmarshal(data, &comp); err != nil {
		return fmt.Errorf("parse Component at %q: %w", path, authoredshape.DescribeYAMLDecodeErrorIn(err, data))
	}
	identifier := identifierVersion(comp.Metadata.Name, comp.Metadata.Version)
	if comp.Metadata.Name == "" {
		identifier = path
	}
	return renderComponentStructured(comp, identifier, comp.Metadata.Description, opts)
}

// renderComponentStructured prints a parsed Component at the requested detail
// level. Shared by the catalog-name path (resolved Component) and the file path
// (as-authored Component); identifier is the display name (with version, or the
// source path for an unnamed file).
//
//   - levelList: the identifier plus its usesSchema socket names.
//   - levelDefault: header, description, schema body, and the usesSchema
//     sockets (selector).
//   - levelDetail: adds inject mechanisms, the declared presence mandates,
//     featuresSchema, and metadata labels.
//
// identifier and description are the caller's rather than derived here, which is
// this renderer's standing contract for identity and now covers the description
// beside it. Each caller supplies them from the authority it holds: the
// catalog-name path from the entity the by-name resolution returned, the file
// path from the document it parsed. A renderer that re-derived them would be a
// second statement of the same facts, free to disagree with the resolution about
// which entity is being described.
//
// All map iteration uses OrderedMap.All() for author-order determinism.
func renderComponentStructured(comp types.Component, identifier, description string, opts inspectOptions) error {
	if opts.level == levelList {
		fmt.Fprintln(opts.out, opts.colorize.uri(identifier))
		for socket := range comp.Spec.UsesSchema.All() {
			fmt.Fprintln(opts.out, socket)
		}
		return nil
	}

	renderIdentityBlock(opts, "Component", identifier, description)

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
		if mandates := types.RequiredAttachmentTypes(comp.Metadata.Annotations); len(mandates) > 0 {
			fmt.Fprintln(opts.out, "Required attachment types:")
			for _, m := range mandates {
				fmt.Fprintf(opts.out, "  %s: %s\n", m.Label, m.Type)
			}
		}
		if comp.Spec.FeaturesSchema.Len() > 0 {
			fmt.Fprintln(opts.out, "Attachment slots:")
			for slot, entry := range comp.Spec.FeaturesSchema.All() {
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
