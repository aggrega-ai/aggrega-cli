// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
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
//
// When the caller declares an expected plugin type on spec.PluginType, the
// loaded descriptor's self-reported plugin_type must match it; a mismatch — a
// mutator plugin attached at the policy socket, or the reverse — fails loudly
// with a legible message rather than through the cryptic missing-export error
// the wrong export name would otherwise raise. A caller that leaves
// spec.PluginType empty declares no expectation and skips the check.
func invokeSocketPlugin(ctx context.Context, spec wasm.PluginSpec, export string, payload []byte) ([]byte, error) {
	var out []byte
	err := withSocketPlugin(ctx, spec, func(plugin *wasm.Plugin, desc wasm.PluginDescriptor) error {
		o, cerr := wasm.InvokeExport(ctx, plugin, desc, export, payload)
		if cerr != nil {
			return fmt.Errorf("call %q: %w", export, cerr)
		}
		out = o
		return nil
	})
	return out, err
}

// withSocketPlugin loads a plugin through the shared describe() gate, applies
// the plugin-type guard, invokes fn with the live plugin and its validated
// descriptor, and closes the plugin as the call unwinds. It is the single
// load+describe+guard+Close core the sockets share: invokeSocketPlugin builds on
// it to dispatch one export, and the stage-aware policy runners build on it to
// read a plugin's declared stage from the same descriptor before deciding
// whether to invoke — so a stage check and an export dispatch never double-load a
// guest within one pass, and no runner duplicates the loader machinery.
//
// The loader carries the process-wide shared compilation cache (honoring the ctx
// persistent-cache dial), so a socket guest compiles once and every later socket
// call and concurrent request reuses the machine code. When spec.PluginType is
// set, the loaded descriptor's self-reported plugin type must match it; a
// mismatch — a mutator plugin attached at the policy socket, or the reverse —
// fails loudly with a legible message rather than through the cryptic
// missing-export error the wrong export name would raise. An empty spec.PluginType
// declares no expectation and skips the check.
func withSocketPlugin(ctx context.Context, spec wasm.PluginSpec, fn func(*wasm.Plugin, wasm.PluginDescriptor) error) error {
	loader := wasm.NewCachingLoaderFromContext(ctx, nil)
	plugin, desc, err := loader.LoadAndDescribe(ctx, spec)
	if err != nil {
		return fmt.Errorf("load: %w", err)
	}
	defer plugin.Close(ctx)
	if spec.PluginType != "" && desc.PluginType != spec.PluginType {
		return fmt.Errorf("plugin declares plugin_type %q but is attached at the %q socket", desc.PluginType, spec.PluginType)
	}
	return fn(plugin, desc)
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

	for _, ref := range refs {
		export := policyExport(ref)
		resolved, err := lookup(ref.URI)
		if err != nil {
			return fmt.Errorf("Step 4 (policy): resolve plugin %q: %w", ref.URI, err)
		}
		sources, err := readPolicySources(ref)
		if err != nil {
			return fmt.Errorf("Step 4 (policy): plugin %q: %w", ref.URI, err)
		}
		// Per-ref payload: the shared graph and paths plus this attachment's own
		// sources and settings. The graph fields are shared references (a shallow
		// copy), so per-ref marshalling adds only this attachment's inputs.
		in := *analysis.PolicyInput
		in.Sources = sources
		in.Settings = policySettings(ref)
		payload, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("Step 4 (policy): marshal input for plugin %q: %w", ref.URI, err)
		}
		spec := resolver.PluginSpec(pluginRefName(ref.URI), resolved)
		spec.PluginType = wasm.PluginTypePolicyValidator
		out, ran, err := invokeStagedPolicy(ctx, spec, export, payload, wasm.StageIR, in.Settings)
		if err != nil {
			return fmt.Errorf("Step 4 (policy): plugin %q: %w", ref.URI, err)
		}
		if !ran {
			// A render-stage plugin does not run at the IR stage; the render-stage
			// pass runs it after render.
			continue
		}
		var po analyzer.PolicyValidatorOutput
		if err := json.Unmarshal(out, &po); err != nil {
			return fmt.Errorf("Step 4 (policy): plugin %q output: %w", ref.URI, err)
		}
		analysis.Errors = append(analysis.Errors, policyVerdictErrors(ref.URI, po)...)
		for _, r := range po.Results {
			switch r.Severity {
			case analyzer.SeverityWarn:
				analysis.Warnings = append(analysis.Warnings, policyWarnMessage(ref.URI, r))
			case analyzer.SeverityAudit:
				emitPolicyAudit(ref.URI, r)
			}
		}
	}
	return nil
}

// runRenderPolicySocket runs the render-stage policy-validator socket after
// RenderAll: for each configured policy plugin that declares the render stage,
// it hands the plugin the rendered artifacts (path-sorted into a canonical
// order) plus the attachment's own sources and settings, and routes the returned
// verdicts by severity. deny halts the build loudly — the runner returns a joined
// error built from the same policyVerdictErrors → ToCompilerError conversion the
// IR-stage gate uses, so the halt carries ErrPolicyViolation exactly as an
// IR-stage deny does. warn and audit print to the diagnostic sink and the build
// proceeds. An IR-stage or stage-absent plugin is skipped here (it ran at the IR
// stage). An unconfigured socket is a no-op. A resolution, load, source-read, or
// output-unmarshal failure halts the build loudly (a misconfigured attachment is
// a build fault, not a policy verdict).
func runRenderPolicySocket(ctx context.Context, inputs Inputs, catalogLookup resolver.CatalogLookup, files map[string][]byte) error {
	refs := inputs.WorkspaceConfig.Plugins.Policy
	if len(refs) == 0 {
		return nil
	}
	lookup := resolveSocketLookup(inputs, catalogLookup)
	artifacts := sortedRenderedArtifacts(files)

	var denyErrs []analyzer.AnalysisError
	for _, ref := range refs {
		export := policyExport(ref)
		resolved, err := lookup(ref.URI)
		if err != nil {
			return fmt.Errorf("Step 5 (render policy): resolve plugin %q: %w", ref.URI, err)
		}
		sources, err := readPolicySources(ref)
		if err != nil {
			return fmt.Errorf("Step 5 (render policy): plugin %q: %w", ref.URI, err)
		}
		settings := policySettings(ref)
		payload, err := json.Marshal(analyzer.RenderPolicyValidatorInput{
			Artifacts: artifacts,
			Sources:   sources,
			Settings:  settings,
		})
		if err != nil {
			return fmt.Errorf("Step 5 (render policy): marshal input for plugin %q: %w", ref.URI, err)
		}
		spec := resolver.PluginSpec(pluginRefName(ref.URI), resolved)
		spec.PluginType = wasm.PluginTypePolicyValidator
		out, ran, err := invokeStagedPolicy(ctx, spec, export, payload, wasm.StageRender, settings)
		if err != nil {
			return fmt.Errorf("Step 5 (render policy): plugin %q: %w", ref.URI, err)
		}
		if !ran {
			continue
		}
		var po analyzer.PolicyValidatorOutput
		if err := json.Unmarshal(out, &po); err != nil {
			return fmt.Errorf("Step 5 (render policy): plugin %q output: %w", ref.URI, err)
		}
		denyErrs = append(denyErrs, policyVerdictErrors(ref.URI, po)...)
		for _, r := range po.Results {
			switch r.Severity {
			case analyzer.SeverityWarn:
				fmt.Fprintf(os.Stderr, "  warning: %s\n", policyWarnMessage(ref.URI, r))
			case analyzer.SeverityAudit:
				emitPolicyAudit(ref.URI, r)
			}
		}
	}
	if len(denyErrs) == 0 {
		return nil
	}
	errs := make([]error, len(denyErrs))
	for i := range denyErrs {
		errs[i] = denyErrs[i].ToCompilerError()
	}
	return stderrors.Join(errs...)
}

// invokeStagedPolicy loads a policy plugin through the shared describe() gate,
// reads its declared stage from the same descriptor, and invokes the export only
// when that stage matches wantStage (an absent stage defaults to the IR stage).
// When the stage matches, it validates the attachment's settings against the
// plugin's declared settings schema before dispatch: a plugin that declares a
// schema has its attachment's with.settings checked, and settings that do not
// conform halt the build loudly (wrapping ErrPolicySettingsInvalid) before the
// guest is invoked; a plugin that declares no schema accepts any settings
// (free-form pass-through). Validating inside the stage-matched branch means the
// check runs exactly once, at the stage that dispatches the plugin.
//
// It returns the guest output, whether the plugin ran (ran is false when the
// stage did not match, so the caller skips it without treating the skip as an
// error), and any load, settings-validation, or invocation error. Sharing
// withSocketPlugin means the stage check, the settings check, and the export
// dispatch read one loaded guest — no double-load within the pass, no duplicated
// loader machinery.
func invokeStagedPolicy(ctx context.Context, spec wasm.PluginSpec, export string, payload []byte, wantStage string, settings map[string]any) (out []byte, ran bool, err error) {
	err = withSocketPlugin(ctx, spec, func(plugin *wasm.Plugin, desc wasm.PluginDescriptor) error {
		if effectivePolicyStage(desc.Stage) != wantStage {
			return nil
		}
		ran = true
		if serr := desc.ValidateSettings(settings); serr != nil {
			return fmt.Errorf("with.settings does not satisfy the plugin's declared settings schema: %v: %w", serr, ErrPolicySettingsInvalid)
		}
		o, cerr := wasm.InvokeExport(ctx, plugin, desc, export, payload)
		if cerr != nil {
			return fmt.Errorf("call %q: %w", export, cerr)
		}
		out = o
		return nil
	})
	return out, ran, err
}

// effectivePolicyStage resolves a descriptor's declared stage to the stage the
// runner routes it by: an empty (absent) stage defaults to the IR stage, so a
// plugin that declares no stage runs at the IR stage exactly as before the
// render stage existed.
func effectivePolicyStage(stage string) string {
	if stage == "" {
		return wasm.StageIR
	}
	return stage
}

// policyExport returns the entry function a policy attachment dispatches to: the
// per-attachment override when set, else the socket's documented default.
func policyExport(ref workspaceconfig.PluginRef) string {
	if ref.Function != "" {
		return ref.Function
	}
	return wasm.ValidateExport
}

// policySettings returns the free-form settings object the attachment declared,
// handed to the plugin verbatim, or nil when the attachment declares none. The
// host does not parse or act on it.
func policySettings(ref workspaceconfig.PluginRef) map[string]any {
	if ref.With == nil {
		return nil
	}
	return ref.With.Settings
}

// readPolicySources reads the attachment's declared source files at compile time
// into the opaque-content payload entries the socket hands the plugin. Each entry
// carries the file's base name (never an absolute host path — portability and
// determinism) and its bytes as an opaque string the host never parses; the
// result is sorted by base name so the wire is byte-identical across runs. The
// paths are the already-resolved absolute paths the config layer anchored. A read
// failure is a loud build fault (a misconfigured attachment), not a policy
// verdict. Returns nil when the attachment declares no sources.
func readPolicySources(ref workspaceconfig.PluginRef) ([]analyzer.PolicySource, error) {
	if ref.With == nil || len(ref.With.Sources) == 0 {
		return nil, nil
	}
	sources := make([]analyzer.PolicySource, 0, len(ref.With.Sources))
	for _, p := range ref.With.Sources {
		content, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read source %q: %w", p, err)
		}
		sources = append(sources, analyzer.PolicySource{
			Path:    filepath.Base(p),
			Content: string(content),
		})
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Path < sources[j].Path })
	return sources, nil
}

// sortedRenderedArtifacts projects the render output map into the render-stage
// policy payload's ordered artifact list, sorted by path. Sorting the map keys
// into a list here is determinism-safe: this is a socket INPUT payload, not an
// emitted artifact, so the sort yields one canonical wire order for identical
// input.
func sortedRenderedArtifacts(files map[string][]byte) []analyzer.RenderedArtifact {
	arts := make([]analyzer.RenderedArtifact, 0, len(files))
	for path, content := range files {
		arts = append(arts, analyzer.RenderedArtifact{Path: path, Content: string(content)})
	}
	sort.Slice(arts, func(i, j int) bool { return arts[i].Path < arts[j].Path })
	return arts
}

// policyVerdictErrors converts a policy plugin's output into one AnalysisError
// per DENY verdict, in plugin-emitted order. Only fired verdicts appear in the
// output, and only a deny verdict halts the build — warn and audit verdicts are
// handled by the socket runner as non-halting diagnostics — so a result set with
// no deny verdict adds nothing and the build proceeds. Each error carries the
// AnalysisErrorPolicy kind (so its message renders verbatim) and the
// ErrPolicyViolation sentinel (so the build error is errors.Is-recoverable), and
// its message names the rejecting plugin and the plugin-supplied verdict message.
// It is the single conversion both stages use: the IR-stage gate appends its
// result to analysis.Errors, and the render-stage runner turns its result into a
// joined ToCompilerError halt.
func policyVerdictErrors(pluginURI string, po analyzer.PolicyValidatorOutput) []analyzer.AnalysisError {
	var errs []analyzer.AnalysisError
	for _, r := range po.Results {
		if r.Severity != analyzer.SeverityDeny {
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

// policyWarnMessage formats a warn verdict for the diagnostic sink, naming the
// flagging plugin and carrying the plugin-supplied message.
func policyWarnMessage(pluginURI string, r analyzer.PolicyResult) string {
	return fmt.Sprintf("policy plugin %q warning: %s", pluginRefName(pluginURI), r.Message)
}

// emitPolicyAudit records an audit verdict to the diagnostic sink under a
// distinct "audit:" label, in plugin-emitted order, with zero effect on the
// build outcome.
func emitPolicyAudit(pluginURI string, r analyzer.PolicyResult) {
	fmt.Fprintf(os.Stderr, "  audit: policy plugin %q: %s\n", pluginRefName(pluginURI), r.Message)
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
