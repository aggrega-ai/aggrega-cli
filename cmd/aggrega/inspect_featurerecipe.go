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

// renderFeatureRecipeFile renders a FeatureRecipe authored as a standalone
// file, parallel to the Recipe renderer. Resource generation is the artifacts:
// surface (plus optional workload synthesis) — there is no separate
// resourceGeneration field. spec.implements renders as authored;
// --show-implements resolves the bound Feature against the catalog via the
// shared lookup, and a credential FeatureRecipe (its bound Feature labeled
// feature.aggrega.ai/kind: secret) is then marked prominently.
//
//   - levelList: identifier, implements, provider, artifact count.
//   - levelDefault: implements, provider, artifacts, valuesMapping, injectMapping, executionScope.
//   - levelDetail: adds artifact bodies, workload body, provides / locals / pipeline / assertions.
func renderFeatureRecipeFile(_ context.Context, rw *workspaceconfig.ResolvedWorkspace, path string, data []byte, catalogDirs []string, opts inspectOptions) error {
	if opts.output == outputYAML {
		return emitYAMLRoundTrip(opts, path, data)
	}
	var fr types.FeatureRecipe
	if err := yaml.Unmarshal(data, &fr); err != nil {
		return fmt.Errorf("parse FeatureRecipe at %q: %w", path, err)
	}
	identifier := identifierVersion(fr.Metadata.Name, fr.Metadata.Version)

	if opts.level == levelList {
		fmt.Fprintln(opts.out, opts.colorize.uri(identifier))
		fmt.Fprintf(opts.out, "implements: %s\n", fr.Spec.Implements)
		fmt.Fprintf(opts.out, "capability: %s\n", fr.Spec.Capability)
		fmt.Fprintf(opts.out, "artifacts: %d\n", fr.Spec.Artifacts.Len())
		return nil
	}

	renderIdentityBlock(opts, "FeatureRecipe", identifier, fr.Metadata.Description)
	fmt.Fprintf(opts.out, "Implements: %s\n", opts.colorize.uri(fr.Spec.Implements))
	fmt.Fprintf(opts.out, "Capability: %s\n", fr.Spec.Capability)

	if fr.Spec.Artifacts.Len() > 0 {
		fmt.Fprintf(opts.out, "Artifacts (%d):\n", fr.Spec.Artifacts.Len())
		for name, entry := range fr.Spec.Artifacts.All() {
			fmt.Fprintf(opts.out, "  %s: %s\n", name, importableSummary(entry.Inline, entry.Import))
		}
	}
	if !fr.Spec.Workload.IsEmpty() {
		fmt.Fprintf(opts.out, "Workload: %s\n", importableSummary(fr.Spec.Workload.Inline, fr.Spec.Workload.Import))
	}

	fmt.Fprintf(opts.out, "valuesMapping: %s\n", valuesMappingSummary(fr.Spec.ValuesMapping))
	if fr.Spec.InjectMapping != "" {
		fmt.Fprintln(opts.out, "injectMapping: present")
	}
	if fr.Spec.ExecutionScope != "" {
		fmt.Fprintf(opts.out, "executionScope: %s\n", fr.Spec.ExecutionScope)
	}

	if opts.level == levelDetail {
		if fr.Spec.Artifacts.Len() > 0 {
			for name, entry := range fr.Spec.Artifacts.All() {
				if entry.Inline != "" {
					fmt.Fprintf(opts.out, "Artifact %s:\n", name)
					renderTemplateBodyIndented(opts, entry.Inline)
				}
			}
		}
		if fr.Spec.Workload.Inline != "" {
			fmt.Fprintln(opts.out, "Workload:")
			renderTemplateBodyIndented(opts, fr.Spec.Workload.Inline)
		}
		renderNamedTemplateKeys(opts, "Provides:", fr.Spec.Provides)
		renderNamedTemplateKeys(opts, "Locals:", fr.Spec.Locals)
		renderPipeline(opts, fr.Spec.Pipeline)
		renderAssertionKeys(opts, fr.Spec.Assertions)
	}

	if opts.showImplements {
		feat, err := resolveImplementsFeature(rw, catalogDirs, fr.Spec.Implements)
		if err != nil {
			return err
		}
		fmt.Fprintf(opts.out, "Implements Feature: %s\n",
			opts.colorize.uri(identifierVersion(feat.Metadata.Name, feat.Metadata.Version)))
		if kind, ok := feat.Metadata.Labels.Get(types.FeatureKindLabel); ok && kind == types.FeatureKindSecret {
			fmt.Fprintln(opts.out, "Credential FeatureRecipe — emits secret-kind injection")
		}
	}
	return nil
}
