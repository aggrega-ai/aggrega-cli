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
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parsersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// resolveSocketLookup returns the plugin-resolution lookup a Wasm socket uses:
// the caller-supplied override when present, else the build-time entity-fronted
// cascade (embed → colocated → local → cache → registry) — the same fallback the
// renderer's per-node pipeline uses. It is resolved only after a socket confirms
// it has at least one plugin to run, so an unconfigured socket builds no lookup
// and stays a clean no-op.
//
// The attachment's tier selects how an unpinned gate plugin resolves — and for
// the governance socket the tier is a property of the attachment's role, so each
// runner passes the tier it knows statically rather than reading it from the
// socket. A mutating attachment (a label-transform governance role) resolves
// strictly through the pinned entity-fronted cascade — its plugin shapes output
// bytes and is pinned in the lockfile. A verdict attachment (a verdict governance
// role, or the linter socket) renders an accept/reject verdict without shaping
// output, so its plugin is cached at pull but not pinned (cache-not-pin): the
// entity-fronted cascade is tried first (honoring a pin / Tier-1 / embed source),
// and an unpinned gate plugin that misses there resolves from the pull-populated
// cache by URI.
func resolveSocketLookup(inputs Inputs, catalogLookup resolver.CatalogLookup, tier workspaceconfig.SocketTier) pluginpath.Lookup {
	if inputs.PluginPathLookup != nil {
		return inputs.PluginPathLookup
	}
	base := buildEntityPluginLookup(inputs, catalogLookup)
	if tier != workspaceconfig.SocketTierVerdict {
		return base
	}
	return resolver.VerdictLookup(base, func(entityURI string) (pluginpath.ResolvedPlugin, error) {
		return resolveVerdictSocketPluginFromCache(inputs, catalogLookup, entityURI)
	})
}

// resolveVerdictSocketPluginFromCache resolves a verdict-socket (policy / linter)
// plugin from the pull-populated cache by its entity URI, with no lockfile pin.
// It reads the Plugin entity from the cache (manifest root of trust) to obtain the
// entity's authored contract and runtime limits and the .wasm layer content
// digest, then resolves the executable through the shared artifact cascade keyed
// by that manifest-derived digest — the same verified Tier-2 cache read the pinned
// path uses, so the .wasm is digest-checked exactly as a pinned plugin's is. The
// entity axis is never pinned or drift-gated; only its bytes are integrity-checked.
func resolveVerdictSocketPluginFromCache(inputs Inputs, catalogLookup resolver.CatalogLookup, entityURI string) (pluginpath.ResolvedPlugin, error) {
	sources := resolver.VerdictPluginTierSources{
		ColocatedDirs: catalogLookup.PluginColocatedDirs,
		WorkspaceRoot: inputs.WorkspaceRoot,
		CacheDir:      inputs.WorkspaceConfig.ResolvedArtifactCacheDir(),
	}
	return resolver.ResolveVerdictPluginFromCache(sources, catalogLookup.PluginFromCache, entityURI)
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
// it to dispatch one export, and the verdict runners build on it to validate the
// attachment's settings against the same descriptor before dispatching — so a
// settings check and an export dispatch never double-load a guest within one
// pass, and no runner duplicates the loader machinery.
//
// The loader carries the process-wide shared compilation cache (honoring the ctx
// persistent-cache dial), so a socket guest compiles once and every later socket
// call and concurrent request reuses the machine code. A typed socket sets
// spec.PluginType to the kind it expects, so the shared load gate rejects a guest
// of the wrong kind loudly before fn ever sees the descriptor. The governance
// socket sets the governance type, which every governance guest reports; what a
// governance plugin may emit is fixed by the role the runner dispatches it at, not
// by this type check.
func withSocketPlugin(ctx context.Context, spec wasm.PluginSpec, fn func(*wasm.Plugin, wasm.PluginDescriptor) error) error {
	loader := wasm.NewCachingLoaderFromContext(ctx, nil)
	plugin, desc, err := loader.LoadAndDescribe(ctx, spec)
	if err != nil {
		return fmt.Errorf("load: %w", err)
	}
	defer plugin.Close(ctx)
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
	refs := inputs.WorkspaceConfig.Plugins.GovernanceRefsForRole(plugintype.RoleLabelTransform)
	if len(refs) == 0 {
		return nil
	}
	lookup := resolveSocketLookup(inputs, catalogLookup, workspaceconfig.SocketTierMutating)
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
		mi, err := topology.AssembleMutatorInput(topo, catalogLookup, cc, mutatorSources(ref), attachmentSettings(ref))
		if err != nil {
			return fmt.Errorf("Step 2 (mutator): assemble input for plugin %q: %w", ref.URI, err)
		}
		payload, err := json.Marshal(mi)
		if err != nil {
			return fmt.Errorf("Step 2 (mutator): marshal input for plugin %q: %w", ref.URI, err)
		}
		spec := resolver.PluginSpec(pluginRefName(ref.URI), resolved)
		spec.PluginType = wasm.PluginTypeGovernance
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

// runPolicySocket runs the Step-4 policy socket: it marshals the
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
	refs := inputs.WorkspaceConfig.Plugins.GovernanceRefsForRole(plugintype.RoleIRVerdict)
	if len(refs) == 0 {
		return nil
	}
	if analysis == nil || analysis.PolicyInput == nil {
		return nil
	}
	lookup := resolveSocketLookup(inputs, catalogLookup, workspaceconfig.SocketTierVerdict)

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
		in.Settings = attachmentSettings(ref)
		payload, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("Step 4 (policy): marshal input for plugin %q: %w", ref.URI, err)
		}
		spec := resolver.PluginSpec(pluginRefName(ref.URI), resolved)
		spec.PluginType = wasm.PluginTypeGovernance
		out, err := invokeVerdictPlugin(ctx, spec, export, payload, in.Settings)
		if err != nil {
			return fmt.Errorf("Step 4 (policy): plugin %q: %w", ref.URI, err)
		}
		var po analyzer.PolicyOutput
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

// runRenderPolicySocket runs the render-stage policy socket after
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
func runRenderPolicySocket(ctx context.Context, inputs Inputs, catalogLookup resolver.CatalogLookup, files map[string]renderer.AssembledFile) error {
	refs := inputs.WorkspaceConfig.Plugins.GovernanceRefsForRole(plugintype.RoleRenderVerdict)
	if len(refs) == 0 {
		return nil
	}
	lookup := resolveSocketLookup(inputs, catalogLookup, workspaceconfig.SocketTierVerdict)
	artifacts, err := sortedRenderedArtifacts(ctx, inputs, catalogLookup, files)
	if err != nil {
		return fmt.Errorf("Step 5 (render policy): %w", err)
	}

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
		settings := attachmentSettings(ref)
		payload, err := json.Marshal(analyzer.RenderPolicyInput{
			Artifacts: artifacts,
			Sources:   sources,
			Settings:  settings,
		})
		if err != nil {
			return fmt.Errorf("Step 5 (render policy): marshal input for plugin %q: %w", ref.URI, err)
		}
		spec := resolver.PluginSpec(pluginRefName(ref.URI), resolved)
		spec.PluginType = wasm.PluginTypeGovernance
		out, err := invokeVerdictPlugin(ctx, spec, export, payload, settings)
		if err != nil {
			return fmt.Errorf("Step 5 (render policy): plugin %q: %w", ref.URI, err)
		}
		var po analyzer.PolicyOutput
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

// invokeVerdictPlugin loads a verdict-role governance plugin through the shared
// describe() gate, validates the attachment's settings against the plugin's
// declared settings schema, and dispatches the export. A plugin that declares a
// schema has its attachment's with.settings checked, and settings that do not
// conform halt the build loudly (wrapping ErrPolicySettingsInvalid) before the
// guest is invoked; a plugin that declares no schema accepts any settings
// (free-form pass-through). Which stage a verdict runs at is fixed by the
// attachment role — an ir-verdict runner dispatches at Step 4, a render-verdict
// runner at Step 5 — so there is no guest-declared stage to consult and the
// dispatch is unconditional. It reads a verdict and applies nothing: the host has
// no code path here that would honor a patch in the guest's output, so a verdict
// role is structurally non-mutating regardless of what the guest emits. Sharing
// withSocketPlugin means the settings check and the export dispatch read one
// loaded guest — no double-load within the pass, no duplicated loader machinery.
func invokeVerdictPlugin(ctx context.Context, spec wasm.PluginSpec, export string, payload []byte, settings map[string]any) (out []byte, err error) {
	err = withSocketPlugin(ctx, spec, func(plugin *wasm.Plugin, desc wasm.PluginDescriptor) error {
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
	return out, err
}

// policyExport returns the entry function a policy attachment dispatches to: the
// per-attachment override when set, else the socket's documented default.
func policyExport(ref workspaceconfig.PluginRef) string {
	if ref.Function != "" {
		return ref.Function
	}
	return wasm.ValidateExport
}

// attachmentSettings returns the free-form settings object the attachment
// declared, handed to the plugin verbatim, or nil when the attachment declares
// none. The host does not parse or act on it. The accessor is socket-agnostic —
// the policy and mutator sockets share this one settings projection.
func attachmentSettings(ref workspaceconfig.PluginRef) map[string]any {
	if ref.With == nil {
		return nil
	}
	return ref.With.Settings
}

// mutatorSources projects the attachment's pinned source files into the mutator
// socket's ordered source entries, sorted by base name. It deliberately diverges
// from readPolicySources: the policy socket reads live host files at compile
// time, whereas the mutator reads CONTENT pinned in the lockfile and
// reconstructed onto the ref (PinnedSources) — so a mutator build stays
// byte-identical after a post-pull change to the source file. Returns nil when
// the attachment pins no sources.
func mutatorSources(ref workspaceconfig.PluginRef) []topology.MutatorSource {
	if ref.With == nil || len(ref.With.PinnedSources) == 0 {
		return nil
	}
	sources := make([]topology.MutatorSource, 0, len(ref.With.PinnedSources))
	for _, s := range ref.With.PinnedSources {
		sources = append(sources, topology.MutatorSource{Path: s.Path, Content: s.Content})
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Path < sources[j].Path })
	return sources
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

// policyNativeFormats are the source formats the policy guest reads natively
// from an artifact's raw Content — it decodes JSON directly and parses YAML
// itself — so the host does not pre-parse them and their Tree stays empty. Every
// other format the extension table resolves is non-native: the host runs the
// matching parser and carries the parsed tree alongside the raw content, so a
// policy can reason over the structure of a format it cannot read on its own
// (HCL, for example). The set is the single home of the native/non-native
// distinction the render-policy seam applies.
var policyNativeFormats = map[string]bool{
	"json": true,
	"yaml": true,
}

// sortedRenderedArtifacts projects the render output map into the render-stage
// policy payload's ordered artifact list, sorted by path. Sorting the map keys
// into a list here is determinism-safe: this is a socket INPUT payload, not an
// emitted artifact, so the sort yields one canonical wire order for identical
// input.
//
// The parser format is selected in precedence order: the producer-declared
// format the rendering provider carried for the artifact wins; absent that, the
// path extension selects it. There is no third path — the host never inspects an
// artifact's content to guess a format. For an artifact whose selected format the
// policy guest does not read natively, the host parses the content through the
// resolved parser and carries the parsed tree on the artifact, so a policy can
// reason over the structure of a non-native format. A native format (JSON, YAML)
// and a selection that yields no format both leave the tree empty — the guest
// reads the raw content directly, and the two native-format render-policy paths
// stay byte-identical. The parser here feeds a verdict gate, so the host carries
// no reproducibility posture of its own; a core parser embedded in the binary
// resolves with no lockfile pin. A parse resolution or invocation failure on a
// non-native artifact halts loudly rather than dropping the tree silently.
func sortedRenderedArtifacts(ctx context.Context, inputs Inputs, catalogLookup resolver.CatalogLookup, files map[string]renderer.AssembledFile) ([]analyzer.RenderedArtifact, error) {
	arts := make([]analyzer.RenderedArtifact, 0, len(files))
	for path, af := range files {
		arts = append(arts, analyzer.RenderedArtifact{Path: path, Content: string(af.Content), Format: af.Format})
	}
	sort.Slice(arts, func(i, j int) bool { return arts[i].Path < arts[j].Path })

	var res parsersocket.ParserResolver
	var load parsersocket.ParserLoader
	for i := range arts {
		format, ok := parsersocket.SelectFormat(arts[i].Format, arts[i].Path)
		if !ok || policyNativeFormats[format] {
			continue
		}
		if res == nil {
			res, load = parserSeam(inputs, catalogLookup)
		}
		out, err := parsersocket.ParseArtifact(ctx, res, load, format, []byte(arts[i].Content), nil)
		if err != nil {
			return nil, fmt.Errorf("parse artifact %q as %q: %w", arts[i].Path, format, err)
		}
		arts[i].Tree = json.RawMessage(out.Tree)
	}
	return arts, nil
}

// parserResolverFunc adapts the catalog's format→parser lookup to the
// parser-socket resolver seam.
type parserResolverFunc func(format string) (types.Plugin, error)

func (f parserResolverFunc) ParserPluginForFormat(format string) (types.Plugin, error) {
	if f == nil {
		return types.Plugin{}, fmt.Errorf("no parser resolver is available: the catalog exposes no parser lookup")
	}
	return f(format)
}

// parserSeam builds the resolver and loader the render-policy stage hands to the
// parser socket. The resolver fronts the catalog's format→parser lookup; the
// loader resolves the parser entity's bytes through the same entity-fronted
// cascade the render pipeline uses (embed → colocated → local → cache → registry),
// so a core parser embedded in the binary resolves offline with no lockfile pin.
func parserSeam(inputs Inputs, catalogLookup resolver.CatalogLookup) (parsersocket.ParserResolver, parsersocket.ParserLoader) {
	res := parserResolverFunc(catalogLookup.ParserPlugin)
	cascade := buildPluginResolver(inputs, catalogLookup.PluginColocatedDirs).Resolve
	load := func(p types.Plugin) (wasm.PluginSpec, error) {
		resolved, err := cascade(p.ArtifactReference())
		if err != nil {
			return wasm.PluginSpec{}, err
		}
		resolved.Contract = resolver.PluginContractClaim(p)
		resolved.Limits = resolver.PluginRuntimeLimits(p)
		return resolver.PluginSpec(pluginRefName(p.Coordinate()), resolved), nil
	}
	return res, load
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
func policyVerdictErrors(pluginURI string, po analyzer.PolicyOutput) []analyzer.AnalysisError {
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
