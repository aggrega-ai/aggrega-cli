// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"go.yaml.in/yaml/v3"
)

// renderFeatureFile renders a Feature authored as a standalone file. The schema
// pillar renders through the same helper as Component (the merged-schema shape
// is unified). A Feature's injection surface is its per-socket usesSchema inject
// paradigms and its featuresSchema slots; the free-form self-inject block is a
// Component concept and does not exist on a Feature. Targeting renders from
// appliesTo / appliesToAnyOf. A credential Feature (labeled
// feature.aggrega.ai/kind: secret) is marked prominently.
//
//   - levelList: identifier plus usesSchema socket names.
//   - levelDefault: schema, uses sockets, secret slots, targeting, labels.
//   - levelDetail: adds interoperability and assertions.
func renderFeatureFile(_ *workspaceconfig.ResolvedWorkspace, path string, data []byte, _ []string, opts inspectOptions) error {
	if opts.output == outputYAML {
		return emitYAMLRoundTrip(opts, path, data)
	}
	var feat types.Feature
	if err := yaml.Unmarshal(data, &feat); err != nil {
		return fmt.Errorf("parse Feature at %q: %w", path, err)
	}
	identifier := identifierVersion(feat.Metadata.Name, feat.Metadata.Version)

	if opts.level == levelList {
		fmt.Fprintln(opts.out, opts.colorize.uri(identifier))
		for socket := range feat.Spec.UsesSchema.All() {
			fmt.Fprintln(opts.out, socket)
		}
		return nil
	}

	renderHeader(opts, "Feature", identifier)

	if kind, ok := feat.Metadata.Labels.Get(types.FeatureKindLabel); ok && kind == types.FeatureKindSecret {
		fmt.Fprintln(opts.out, "Credential Feature — fills a secrets: slot")
	}

	if err := renderSchemaBody(opts, "Schema:", feat.Spec.Schema); err != nil {
		return err
	}

	if feat.Spec.UsesSchema.Len() > 0 {
		fmt.Fprintln(opts.out, "Uses sockets:")
		for socket, entry := range feat.Spec.UsesSchema.All() {
			req := ""
			if entry.Required {
				req = " (required)"
			}
			fmt.Fprintf(opts.out, "  %s: selector=%q%s\n", socket, entry.Selector, req)
			if len(entry.Inject) > 0 {
				fmt.Fprintf(opts.out, "    inject: %v\n", entry.Inject)
			}
		}
	}

	if feat.Spec.FeaturesSchema.Len() > 0 {
		fmt.Fprintln(opts.out, "Secret slots:")
		for slot, entry := range feat.Spec.FeaturesSchema.All() {
			req := ""
			if entry.Required {
				req = " (required)"
			}
			fmt.Fprintf(opts.out, "  %s%s\n", slot, req)
		}
	}

	renderTargeting(opts, feat.Spec)

	if feat.Metadata.Labels.Len() > 0 {
		fmt.Fprintln(opts.out, "Labels:")
		for k, v := range feat.Metadata.Labels.All() {
			fmt.Fprintf(opts.out, "  %s: %s\n", k, v)
		}
	}

	if opts.level == levelDetail {
		if feat.Spec.Interoperability != nil {
			io := feat.Spec.Interoperability
			fmt.Fprintln(opts.out, "Interoperability:")
			if len(io.ConflictsWith) > 0 {
				fmt.Fprintf(opts.out, "  conflictsWith: %v\n", io.ConflictsWith)
			}
			if io.ComponentRefPath != "" {
				fmt.Fprintf(opts.out, "  componentRefPath: %s\n", io.ComponentRefPath)
			}
		}
		if feat.Spec.Assertions.Len() > 0 {
			fmt.Fprintln(opts.out, "Assertions:")
			for name := range feat.Spec.Assertions.All() {
				fmt.Fprintf(opts.out, "  - %s\n", name)
			}
		}
	}
	return nil
}

// renderTargeting prints the Feature's component-targeting surface: appliesTo
// (a single AND-selector) and appliesToAnyOf (OR of selector terms, each a
// string selector or a flat label map).
func renderTargeting(opts inspectOptions, spec types.FeatureSpec) {
	if spec.AppliesTo == "" && len(spec.AppliesToAnyOf) == 0 {
		return
	}
	fmt.Fprintln(opts.out, "Targeting:")
	if spec.AppliesTo != "" {
		fmt.Fprintf(opts.out, "  appliesTo: %s\n", spec.AppliesTo)
	}
	if len(spec.AppliesToAnyOf) > 0 {
		fmt.Fprintln(opts.out, "  appliesToAnyOf:")
		for _, term := range spec.AppliesToAnyOf {
			if term.IsMap() {
				for k, v := range term.Labels.All() {
					fmt.Fprintf(opts.out, "    - %s=%s\n", k, v)
				}
			} else {
				fmt.Fprintf(opts.out, "    - %s\n", term.Selector)
			}
		}
	}
}
