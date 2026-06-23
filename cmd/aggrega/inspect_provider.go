// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"gopkg.in/yaml.v3"
)

// renderProviderFile renders a Provider authored as a standalone file. The
// plugin URI is shown first and prominently; recipe-hosting compatibility is
// derived from the presence of workloadSchema (synthesized mode) and baseSchema
// (sourceless mode); well-known annotations render per entry, or
// `(none declared)` when the contract list is empty. The eval provider receives
// no special-casing — it inspects as a regular Provider.
//
//   - levelList: identifier plus the provider engine name.
//   - levelDefault: plugin, recipe-hosting line, well-known annotations.
//   - levelDetail: adds the full baseSchema / workloadSchema bodies.
func renderProviderFile(_ *workspaceconfig.ResolvedWorkspace, path string, data []byte, _ []string, opts inspectOptions) error {
	if opts.output == outputYAML {
		return emitYAMLRoundTrip(opts, path, data)
	}
	var prov types.Provider
	if err := yaml.Unmarshal(data, &prov); err != nil {
		return fmt.Errorf("parse Provider at %q: %w", path, err)
	}
	identifier := identifierVersion(prov.Metadata.Name, prov.Metadata.Version)

	if opts.level == levelList {
		fmt.Fprintln(opts.out, opts.colorize.uri(identifier))
		fmt.Fprintf(opts.out, "engine: %s\n", prov.Metadata.Name)
		return nil
	}

	renderHeader(opts, "Provider", identifier)

	plugin := prov.Spec.Plugin
	if plugin == "" {
		plugin = "(none declared)"
	}
	fmt.Fprintf(opts.out, "Plugin: %s\n", plugin)

	fmt.Fprintf(opts.out, "Recipe-hosting: %s\n", recipeHostingLine(prov.Spec))

	if len(prov.Spec.WellKnownAnnotations) == 0 {
		fmt.Fprintln(opts.out, "Well-known annotations: (none declared)")
	} else {
		fmt.Fprintln(opts.out, "Well-known annotations:")
		for _, wka := range prov.Spec.WellKnownAnnotations {
			fmt.Fprintf(opts.out, "  %s\n", wka.Key)
			fmt.Fprintf(opts.out, "    purpose: %s\n", wka.Purpose)
			if wka.DefaultBehavior != "" {
				fmt.Fprintf(opts.out, "    default: %s\n", wka.DefaultBehavior)
			}
		}
	}

	if opts.level == levelDetail {
		if prov.Spec.BaseSchema != "" {
			fmt.Fprintln(opts.out, "baseSchema:")
			renderTemplateBodyIndented(opts, prov.Spec.BaseSchema)
		}
		if prov.Spec.WorkloadSchema != "" {
			fmt.Fprintln(opts.out, "workloadSchema:")
			renderTemplateBodyIndented(opts, prov.Spec.WorkloadSchema)
		}
	}
	return nil
}

// recipeHostingLine derives the recipe-hosting compatibility line from the
// provider's schema declarations: synthesized mode iff workloadSchema is
// present, sourceless mode iff baseSchema is present. The actual synthesis
// capability is the bound engine's, not this field's — the line reports the
// documented contract.
func recipeHostingLine(spec types.ProviderSpec) string {
	synth := spec.WorkloadSchema != ""
	sourceless := spec.BaseSchema != ""
	switch {
	case synth && sourceless:
		return "synthesized + sourceless"
	case synth:
		return "synthesized"
	case sourceless:
		return "sourceless"
	default:
		return "sourced-only"
	}
}
