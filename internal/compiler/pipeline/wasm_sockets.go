// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// resolveSocketLookup returns the plugin-resolution lookup the Step-2 and
// Step-4 Wasm sockets use: the caller-supplied override when present, else the
// build-time entity-fronted cascade (embed → sidecar → local → cache →
// registry) — the same fallback the renderer's per-node pipeline uses. It is
// resolved only after a socket confirms it has at least one plugin to run, so
// an unconfigured socket builds no lookup and stays a clean no-op.
func resolveSocketLookup(inputs Inputs, catalogLookup resolver.CatalogLookup) pluginpath.Lookup {
	if inputs.PluginPathLookup != nil {
		return inputs.PluginPathLookup
	}
	return buildEntityPluginLookup(inputs, catalogLookup)
}

// invokeSocketPlugin loads a plugin through the shared describe() gate, invokes
// the named export against payload, and returns the guest's output bytes. It
// mirrors the publishing-linter dispatch: a per-invocation loader, describe()
// validation at load, and a Close that fires as the call unwinds so a socket
// leaks no Wazero instance. The describe() gate refuses a plugin that lacks the
// export or declares a descriptor the host rejects.
func invokeSocketPlugin(ctx context.Context, spec wasm.PluginSpec, export string, payload []byte) ([]byte, error) {
	loader := &wasm.Loader{}
	plugin, desc, err := loader.LoadAndDescribe(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("load: %w", err)
	}
	defer plugin.Close(ctx)
	out, err := wasm.InvokeExport(ctx, plugin, desc, export, payload)
	if err != nil {
		return nil, fmt.Errorf("call %q: %w", export, err)
	}
	return out, nil
}

// runMutatorSocket runs the Step-2 mutator socket: for each configured mutator
// plugin, in order, it assembles the labels-only MutatorInput from the topology
// result, dispatches the plugin's mutate export (or a per-attachment override),
// and applies the returned JSON Patches to the node-label surface. It runs
// between topology construction and IR build so the auto-tagged labels flow into
// the IR. An unconfigured socket is a no-op (returns nil before any resolution),
// which keeps a build with no mutator attachment byte-identical to a build
// without the socket. A resolution, load, output-unmarshal, or patch-confinement
// failure halts the build loudly.
func runMutatorSocket(ctx context.Context, inputs Inputs, catalogLookup resolver.CatalogLookup, topo *topology.TopologyResult) error {
	refs := inputs.WorkspaceConfig.Plugins.Mutator
	if len(refs) == 0 {
		return nil
	}
	lookup := resolveSocketLookup(inputs, catalogLookup)
	cc := mutatorCompilationContext(inputs.Release)

	for _, ref := range refs {
		export := ref.Function
		if export == "" {
			export = wasm.MutateExport
		}
		resolved, err := lookup(ref.URI)
		if err != nil {
			return fmt.Errorf("Step 2 (mutator): resolve plugin %q: %w", ref.URI, err)
		}
		payload, err := json.Marshal(topology.AssembleMutatorInput(topo, catalogLookup, cc))
		if err != nil {
			return fmt.Errorf("Step 2 (mutator): marshal input for plugin %q: %w", ref.URI, err)
		}
		spec := resolver.PluginSpec(pluginRefName(ref.URI), resolved)
		spec.PluginType = wasm.PluginTypeMutator
		out, err := invokeSocketPlugin(ctx, spec, export, payload)
		if err != nil {
			return fmt.Errorf("Step 2 (mutator): plugin %q: %w", ref.URI, err)
		}
		var mo topology.MutatorOutput
		if err := json.Unmarshal(out, &mo); err != nil {
			return fmt.Errorf("Step 2 (mutator): plugin %q output: %w", ref.URI, err)
		}
		if err := topology.ApplyMutatorPatches(topo, mo); err != nil {
			return fmt.Errorf("Step 2 (mutator): plugin %q: %w", ref.URI, err)
		}
	}
	return nil
}

// runPolicySocket runs the Step-4 policy-validator socket: it marshals the
// analyzer's assembled PolicyInput (the full IR graph plus precalculated paths)
// once, dispatches each configured policy plugin's validate export (or a
// per-attachment override), and appends one AnalysisError per failed verdict to
// analysis.Errors. Appending (rather than returning a function-level error)
// routes a policy rejection through the same gate selector and appliesTo
// violations use: RunPipeline joins Errors into a build-halting error, while the
// Tier-4 validation path surfaces them as diagnostics — so a policy rejection
// halts a build without breaking daemon responsiveness. Policy never mutates.
// An unconfigured socket is a no-op. A resolution, load, or output-unmarshal
// failure halts the build loudly (a misconfigured plugin is a build fault, not a
// policy verdict).
func runPolicySocket(ctx context.Context, inputs Inputs, catalogLookup resolver.CatalogLookup, analysis *analyzer.AnalysisResult) error {
	refs := inputs.WorkspaceConfig.Plugins.Policy
	if len(refs) == 0 {
		return nil
	}
	if analysis == nil || analysis.PolicyInput == nil {
		return nil
	}
	lookup := resolveSocketLookup(inputs, catalogLookup)
	payload, err := json.Marshal(analysis.PolicyInput)
	if err != nil {
		return fmt.Errorf("Step 4 (policy): marshal input: %w", err)
	}

	for _, ref := range refs {
		export := ref.Function
		if export == "" {
			export = wasm.ValidateExport
		}
		resolved, err := lookup(ref.URI)
		if err != nil {
			return fmt.Errorf("Step 4 (policy): resolve plugin %q: %w", ref.URI, err)
		}
		spec := resolver.PluginSpec(pluginRefName(ref.URI), resolved)
		spec.PluginType = wasm.PluginTypePolicyValidator
		out, err := invokeSocketPlugin(ctx, spec, export, payload)
		if err != nil {
			return fmt.Errorf("Step 4 (policy): plugin %q: %w", ref.URI, err)
		}
		var po analyzer.PolicyValidatorOutput
		if err := json.Unmarshal(out, &po); err != nil {
			return fmt.Errorf("Step 4 (policy): plugin %q output: %w", ref.URI, err)
		}
		analysis.Errors = append(analysis.Errors, policyVerdictErrors(ref.URI, po)...)
	}
	return nil
}

// policyVerdictErrors converts a policy plugin's output into one AnalysisError
// per failed verdict, in plugin-emitted order. A passing verdict adds nothing.
// Each error carries the AnalysisErrorPolicy kind (so its message renders
// verbatim) and the ErrPolicyViolation sentinel (so the build error is
// errors.Is-recoverable), and its message names the rejecting plugin and the
// plugin-supplied rule message.
func policyVerdictErrors(pluginURI string, po analyzer.PolicyValidatorOutput) []analyzer.AnalysisError {
	var errs []analyzer.AnalysisError
	for _, r := range po.Results {
		if r.Pass {
			continue
		}
		errs = append(errs, analyzer.AnalysisError{
			Kind:     analyzer.AnalysisErrorPolicy,
			Message:  fmt.Sprintf("policy plugin %q rejected the build: %s", pluginRefName(pluginURI), r.Message),
			Sentinel: analyzer.ErrPolicyViolation,
		})
	}
	return errs
}

// mutatorCompilationContext builds the release-level context the mutator reads
// when deciding which rules apply. Both fields — the release name and the
// environment — come from the Release metadata.
func mutatorCompilationContext(rel *types.Release) topology.CompilationContext {
	if rel == nil {
		return topology.CompilationContext{}
	}
	return topology.CompilationContext{
		ReleaseName: rel.Metadata.Name,
		Environment: rel.Metadata.Environment,
	}
}

// pluginRefName derives a short diagnostic label from a plugin URI — the final
// path segment, or the whole URI when it carries no separator. It feeds the
// loader's program-name label and the policy diagnostic; it is a display label,
// not a resolution key.
func pluginRefName(uri string) string {
	if i := strings.LastIndexAny(uri, "/:"); i >= 0 && i < len(uri)-1 {
		return uri[i+1:]
	}
	return uri
}
