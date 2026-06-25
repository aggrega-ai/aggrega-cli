// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// pipelineResult is the outcome of running one node's pipeline through the
// shared executor: the rewritten own emitted set (PATCH applied to its
// documents, EMIT side effects appended as EmitOriginPipeline files), the
// host-document modifications keyed by their original emitted path (a /host
// PATCH rewrite or a /host EMIT addition — empty when the step has no host),
// and the warnings raised along the way (validate-only notice, soft-failed
// optional steps, non-error guest diagnostics).
type pipelineResult struct {
	Own         []EmittedFile
	HostPatched map[string][]byte
	Warnings    []string
}

// pipelineFile pairs an emitted file with the segmentation of its bytes into
// discrete YAML documents. segs is nil for a file whose bytes are not
// parseable structured YAML (an opaque/plain-text/binary EMIT target): such a
// file passes through verbatim and can never be a PATCH target — a patch
// addressing one of its (absent) tokens halts as a not-structured target.
type pipelineFile struct {
	file pipelineFile0
	segs []Segment
}

// pipelineFile0 is the EmittedFile fields the executor carries through the
// fold-back: the original name, body, intermediate flag, and origin. Held by
// value so the working copy never aliases the caller's slice.
type pipelineFile0 struct {
	Name         string
	Body         []byte
	Origin       EmitOrigin
	Intermediate bool
}

// runNodePipeline executes a node's pipeline steps over the keyed document set
// built from its emitted documents, the single executor shared by every
// pipeline site — a Recipe (own only), a component-scope FeatureRecipe (own +
// host), and a boundary-scope FeatureRecipe (own only). The scope-shape
// difference lives entirely in the caller's arguments: own carries the node's
// own emitted documents; host carries the attached host component's documents
// (nil for a Recipe or a boundary-scope FeatureRecipe).
//
// It segments each emitted file into discrete keyed documents, builds the
// scope-rooted DocumentSet, binds a StepInvoker over the shared plugin-path
// resolver and the describe() gate, calls the pipeline walk with the
// renderer's EvalTemplate as the template seam, and folds the walk's result
// back: a patched document is re-serialized into its file byte-faithfully via
// ApplyBack, an own-scope EMIT becomes a new EmitOriginPipeline file, a
// host-scope modification is keyed by the host document's path for the
// orchestrator's host-doc write-back conduit.
//
// hostIsAssembler reports whether the host component's provider is an
// assembler (its documents are not discrete until assembly). A PATCH against a
// pre-assembly fragment — an own-scope document from an Intermediate emitted
// file, or any host-scope document when hostIsAssembler — halts with
// ErrPipelinePatchPreAssembly; the post-assembly phase re-runs such a PATCH.
// EMIT is additive and runs per-node for every provider.
//
// lookup nil disables execution: the steps are validated by their presence but
// not run, and a validate-only warning is returned (the non-regression path).
func runNodePipeline(
	ctx context.Context,
	steps []types.PipelineStep,
	own []EmittedFile,
	host []EmittedFile,
	hostIsAssembler bool,
	renderCtx renderctx.RenderContext,
	withCtx *types.TemplateContext,
	yamlIndent int,
	lookup pluginpath.Lookup,
	nodeID string,
) (pipelineResult, error) {
	res := pipelineResult{Own: own}
	if len(steps) == 0 {
		return res, nil
	}
	if lookup == nil {
		res.Warnings = append(res.Warnings,
			fmt.Sprintf("node %q: pipeline (%d steps) validated but not executed (no plugin path resolver configured)", nodeID, len(steps)))
		return res, nil
	}

	// Build the StepInvoker over the shared resolver + describe() gate, then
	// delegate to the executor body. The public function owns only invoker
	// construction (and the empty-steps / nil-lookup short-circuits above); the
	// executor body is single-sourced in runNodePipelineWithInvoker, which a
	// test exercises with a canned invoker.
	return runNodePipelineWithInvoker(ctx, steps, own, host, hostIsAssembler, renderCtx, withCtx, yamlIndent, newStepInvoker(lookup), nodeID)
}

// runNodePipelineWithInvoker is the executor body shared by the production path
// (a real describe()-gated invoker) and tests (a canned invoker). It builds the
// keyed document set, walks the steps through invoke, and folds the result back
// — the one place the segmenter, walk, and apply-back are wired together. The
// public runNodePipeline is the only production caller; it differs solely in
// constructing the invoker from a plugin-path lookup. invoke must be non-nil.
func runNodePipelineWithInvoker(
	ctx context.Context,
	steps []types.PipelineStep,
	own []EmittedFile,
	host []EmittedFile,
	hostIsAssembler bool,
	renderCtx renderctx.RenderContext,
	withCtx *types.TemplateContext,
	yamlIndent int,
	invoke frp.StepInvoker,
	nodeID string,
) (pipelineResult, error) {
	res := pipelineResult{Own: own}

	// 1. Build the keyed document set from the emitted files via the segmenter.
	ownFiles, ownDocs, err := buildScopeDocs(own)
	if err != nil {
		return pipelineResult{}, wrapPipelineErr(err, nodeID)
	}
	hostFiles, hostDocs, err := buildScopeDocs(host)
	if err != nil {
		return pipelineResult{}, wrapPipelineErr(err, nodeID)
	}
	docs := frp.DocumentSet{Own: ownDocs, Host: hostDocs}

	// Entry-time snapshot of each document's bytes, so fold-back re-serializes
	// only the documents a step actually changed (byte-faithful apply-back).
	ownBefore := snapshotDocBytes(ownDocs)
	hostBefore := snapshotDocBytes(hostDocs)

	// Token → un-segmentable file: a patch addressing such a token is a
	// PATCH against a non-structured target and halts loudly.
	ownNotStructured := unstructuredTokens(own, ownFiles)

	// 3-4. Walk the steps. EvalTemplate is the injected template seam.
	walkRes, werr := frp.Walk(ctx, docs, steps, invoke, withCtx, renderCtx, EvalTemplate)
	if werr != nil {
		return pipelineResult{}, mapWalkError(werr, nodeID, len(host) > 0)
	}

	// 5. Fold the walk result back into emitted files.
	// 5a. Pre-assembly fragment guard (scope-symmetric): a PATCH that changed
	//     an own-scope document of an Intermediate file, or any host-scope
	//     document when the host provider is an assembler, halts — the target
	//     is a pre-assembly fragment.
	if step, ok := changedFragmentOwn(walkRes.Documents.Own, ownBefore, ownFiles); ok {
		return pipelineResult{}, pipelinePreAssemblyErr(nodeID, step, "own")
	}
	if hostIsAssembler {
		if tok, ok := firstChangedToken(walkRes.Documents.Host, hostBefore); ok {
			return pipelineResult{}, pipelinePreAssemblyErr(nodeID, tok, "host")
		}
	}

	// 5b. Apply PATCHes back into own files byte-faithfully.
	ownOut, err := foldPatchesBack(ownFiles, walkRes.Documents.Own, ownBefore, ownNotStructured, yamlIndent, nodeID)
	if err != nil {
		return pipelineResult{}, err
	}

	// 5c. Apply PATCHes back into host files; surface them keyed by path.
	hostPatched := map[string][]byte{}
	if len(host) > 0 {
		hostOut, err := foldPatchesBack(hostFiles, walkRes.Documents.Host, hostBefore, nil, yamlIndent, nodeID)
		if err != nil {
			return pipelineResult{}, err
		}
		for _, f := range hostOut {
			if before, ok := lookupFile(hostFiles, f.Name); ok && !bytes.Equal(before.file.Body, f.Body) {
				hostPatched[f.Name] = f.Body
			}
		}
	}

	// 5d. EMIT side effects → new files, in step+effect order.
	for i, se := range walkRes.SideEffects {
		content, perr := sideEffectBytes(se, nodeID, i)
		if perr != nil {
			return pipelineResult{}, perr
		}
		name := sideEffectName(se, i)
		switch se.Scope {
		case frp.DocumentScopeHost:
			if len(host) == 0 {
				return pipelineResult{}, pipelineHostScopeErr(nodeID, name)
			}
			hostPatched[name] = content
		default: // own (empty scope defaults to own per the wire contract)
			ownOut = append(ownOut, EmittedFile{
				Name:   name,
				Origin: EmitOriginPipeline,
				Body:   content,
			})
		}
	}

	res.Own = ownOut
	res.HostPatched = hostPatched
	for _, d := range walkRes.Warnings {
		res.Warnings = append(res.Warnings, fmt.Sprintf("node %q: pipeline: %s", nodeID, d.Message))
	}
	return res, nil
}

// buildPipelineRenderCtx assembles the render-time context a pipeline guest
// receives for a node: the fulfilled parameters (deterministically ordered),
// the three annotation buckets partitioned by authoring side, and the
// boundary metadata. It is the wire projection the FRP step input carries;
// the host hands it whole and the guest reads it without the host
// interpreting it.
func buildPipelineRenderCtx(node types.IRNode, bi *resolver.BoundaryInfra, parameters map[string]any) renderctx.RenderContext {
	rc := renderctx.RenderContext{
		Annotations: renderctx.RenderAnnotations{
			Aggregate: node.AggregateAnnotations,
			Release:   node.ReleaseAnnotations,
			Catalog:   node.CatalogAnnotations,
		},
		Boundary: renderctx.BoundaryContext{
			Name:     boundaryLeafName(node.BoundaryPath),
			Path:     splitBoundaryPath(node.BoundaryPath),
			SyncWave: node.SyncWave,
		},
	}
	rc.Parameters = orderedParameters(parameters)
	if bi != nil {
		rc.Boundary.Directory = bi.Directory
		rc.Boundary.SubDirectory = bi.SubDirectory
		rc.Boundary.Annotations = bi.Annotations
	}
	return rc
}

// orderedParameters projects a parameter map into a deterministically-ordered
// OrderedMap (sorted by key) so the wire payload is byte-identical across runs
// — the source map carries no author order to preserve.
func orderedParameters(params map[string]any) types.OrderedMap[any] {
	var om types.OrderedMap[any]
	if len(params) == 0 {
		return om
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		om.Set(k, params[k])
	}
	return om
}

// boundaryLeafName returns the last segment of a slash-separated boundary
// path — the boundary's authored name.
func boundaryLeafName(bp string) string {
	if bp == "" {
		return ""
	}
	if i := strings.LastIndex(bp, "/"); i >= 0 {
		return bp[i+1:]
	}
	return bp
}

// splitBoundaryPath splits a slash-separated boundary path into its segments,
// returning nil for an empty path.
func splitBoundaryPath(bp string) []string {
	if bp == "" {
		return nil
	}
	return strings.Split(bp, "/")
}

// newStepInvoker binds the pipeline's per-step guest dispatch over the shared
// plugin-path resolver and the describe() gate: resolve the step's plugin URI
// to a .wasm path, load it through LoadAndDescribe (which validates the
// descriptor), invoke the step's named export, and close the plugin. It
// mirrors the publishing-linter's dispatch precedent; loading per step is
// acceptable (batch amortisation is a follow-on).
func newStepInvoker(lookup pluginpath.Lookup) frp.StepInvoker {
	return func(ctx context.Context, step types.PipelineStep, payload []byte) ([]byte, error) {
		path, err := lookup(step.Plugin)
		if err != nil {
			return nil, err
		}
		loader := &wasm.Loader{}
		plugin, desc, err := loader.LoadAndDescribe(ctx, wasm.PluginSpec{Name: step.Plugin, Path: path})
		if err != nil {
			return nil, err
		}
		defer plugin.Close(ctx)
		return wasm.InvokeExport(ctx, plugin, desc, step.Function, payload)
	}
}

// buildScopeDocs segments each emitted file into discrete documents and keys
// each by its opaque positional token into an OrderedMap[json.RawMessage], the
// scope root the walk operates on. Each document's YAML body is converted to
// JSON (key order preserved via OrderedMap) so the guest and apply engine see
// a JSON document; the original bytes and segments are retained so fold-back
// can re-serialize a patched document into its file byte-faithfully.
//
// A file whose bytes are not parseable structured YAML is recorded with nil
// segments and contributes no documents to the set — it can only ever be an
// EMIT sibling, never a PATCH target.
func buildScopeDocs(files []EmittedFile) ([]pipelineFile, types.OrderedMap[json.RawMessage], error) {
	out := make([]pipelineFile, 0, len(files))
	var docs types.OrderedMap[json.RawMessage]
	for _, f := range files {
		pf := pipelineFile{file: pipelineFile0{Name: f.Name, Body: f.Body, Origin: f.Origin, Intermediate: f.Intermediate}}
		segs, err := Split(f.Name, f.Body)
		if err != nil {
			// Not parseable structured YAML: retain verbatim, no documents.
			// Only an actual PATCH addressing its token halts later.
			pf.segs = nil
			out = append(out, pf)
			continue
		}
		pf.segs = segs
		for _, sg := range segs {
			body := f.Body[sg.bodyStart:sg.bodyEnd]
			raw, cerr := yamlBodyToJSON(body)
			if cerr != nil {
				// A segment whose body fails JSON conversion is not an
				// addressable structured document; treat the file as
				// not-structured (verbatim pass-through, never a PATCH target).
				pf.segs = nil
				break
			}
			docs.Set(sg.Token, raw)
		}
		out = append(out, pf)
	}
	return out, docs, nil
}

// unstructuredTokens returns the set of tokens that belong to a file the
// segmenter could not parse as structured YAML, so a returned PATCH addressing
// one of them is reported as a not-structured target rather than an
// absent-target. A file with nil segs after buildScopeDocs contributed no
// tokens to the set, so the walk would already report absent-target; this map
// lets the integration upgrade that to the precise not-structured diagnostic.
func unstructuredTokens(files []EmittedFile, pfs []pipelineFile) map[string]bool {
	out := map[string]bool{}
	for _, pf := range pfs {
		if pf.segs == nil {
			out[pf.file.Name] = true
		}
	}
	return out
}

// foldPatchesBack rebuilds each file from its segments, re-serializing only the
// documents whose bytes changed during the walk. For a changed token the
// patched JSON is converted back to YAML at the configured indent and spliced
// into the file via ApplyBack (leading/trailing affixes preserved verbatim);
// an unchanged token's document is re-joined from its original byte-range, so
// an unpatched sibling in a patched file stays byte-identical.
func foldPatchesBack(
	pfs []pipelineFile,
	after types.OrderedMap[json.RawMessage],
	before map[string][]byte,
	notStructured map[string]bool,
	yamlIndent int,
	nodeID string,
) ([]EmittedFile, error) {
	out := make([]EmittedFile, 0, len(pfs))
	for _, pf := range pfs {
		if pf.segs == nil {
			// Verbatim pass-through. A patch could only have addressed it if a
			// token mapped here, which it cannot (no tokens contributed), so
			// nothing to fold.
			out = append(out, EmittedFile{Name: pf.file.Name, Origin: pf.file.Origin, Body: pf.file.Body, Intermediate: pf.file.Intermediate})
			continue
		}
		patched := map[string][]byte{}
		for _, sg := range pf.segs {
			newRaw, ok := after.Get(sg.Token)
			if !ok {
				continue
			}
			if oldRaw, had := before[sg.Token]; had && jsonEqual(oldRaw, newRaw) {
				continue
			}
			if notStructured[pf.file.Name] {
				return nil, pipelineNotStructuredErr(nodeID, sg.Token)
			}
			yamlBody, err := jsonToYAMLBody(newRaw, yamlIndent)
			if err != nil {
				return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
					fmt.Sprintf("node %q: pipeline: re-serialize patched document %q", nodeID, sg.Token))
			}
			patched[sg.Token] = yamlBody
		}
		body := pf.file.Body
		if len(patched) > 0 {
			body = ApplyBack(pf.file.Body, pf.segs, patched)
		}
		out = append(out, EmittedFile{Name: pf.file.Name, Origin: pf.file.Origin, Body: body, Intermediate: pf.file.Intermediate})
	}
	return out, nil
}

// changedFragmentOwn reports the first own-scope token whose document changed
// and belongs to an Intermediate (pre-assembly fragment) file. A PATCH against
// such a fragment halts: the assembler has not produced discrete documents yet.
func changedFragmentOwn(after types.OrderedMap[json.RawMessage], before map[string][]byte, pfs []pipelineFile) (string, bool) {
	for _, pf := range pfs {
		if !pf.file.Intermediate || pf.segs == nil {
			continue
		}
		for _, sg := range pf.segs {
			newRaw, ok := after.Get(sg.Token)
			if !ok {
				continue
			}
			if oldRaw, had := before[sg.Token]; had && !jsonEqual(oldRaw, newRaw) {
				return sg.Token, true
			}
		}
	}
	return "", false
}

// firstChangedToken returns the first token in after whose bytes differ from
// before — a changed (patched) document. A new token (present in after, absent
// in before) is an EMIT, not a PATCH, and is not reported here.
func firstChangedToken(after types.OrderedMap[json.RawMessage], before map[string][]byte) (string, bool) {
	for k, v := range after.All() {
		if oldRaw, had := before[k]; had && !jsonEqual(oldRaw, v) {
			return k, true
		}
	}
	return "", false
}

// snapshotDocBytes copies each document's bytes so fold-back can compare a
// post-walk document against its entry-time bytes and re-serialize only the
// ones that changed.
func snapshotDocBytes(docs types.OrderedMap[json.RawMessage]) map[string][]byte {
	out := map[string][]byte{}
	for k, v := range docs.All() {
		out[k] = append([]byte(nil), v...)
	}
	return out
}

// yamlBodyToJSON converts one YAML document body to a JSON RawMessage,
// preserving object key order through OrderedMap so the wire payload is
// deterministic.
func yamlBodyToJSON(body []byte) (json.RawMessage, error) {
	var om types.OrderedMap[any]
	if err := parse.TemplateOutput(body, &om); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(om)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// jsonToYAMLBody converts a patched JSON document back to a YAML body at the
// configured indent, preserving object key order through OrderedMap. The
// trailing newline yaml.v3 appends is retained so the spliced body sits flush
// against the segment's trailing affix.
func jsonToYAMLBody(raw json.RawMessage, indent int) ([]byte, error) {
	var om types.OrderedMap[any]
	if err := parse.IRPayload(raw, &om); err != nil {
		return nil, err
	}
	return MarshalYAMLIndent(om, indent)
}

// jsonEqual reports whether two JSON documents are byte-identical. The walk
// re-marshals a patched document through the apply engine's OrderedMap, so an
// unchanged document round-trips to the same bytes; a byte compare therefore
// reliably distinguishes patched from untouched.
func jsonEqual(a, b []byte) bool { return bytes.Equal(a, b) }

// lookupFile returns the pipelineFile for a given emitted-file name.
func lookupFile(pfs []pipelineFile, name string) (pipelineFile, bool) {
	for _, pf := range pfs {
		if pf.file.Name == name {
			return pf, true
		}
	}
	return pipelineFile{}, false
}

// sideEffectBytes extracts the bytes an EMIT side effect contributes: the file
// content for emit_file, the CRD document for emit_crd. An unrecognised kind
// halts loudly rather than silently dropping.
func sideEffectBytes(se frp.SideEffect, nodeID string, idx int) ([]byte, error) {
	switch se.Kind {
	case "emit_file":
		if se.File == nil {
			return nil, cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
				fmt.Sprintf("node %q: pipeline: emit_file side effect %d has no file", nodeID, idx), nil, nil)
		}
		return []byte(se.File.Content), nil
	case "emit_crd":
		return se.CRD, nil
	default:
		return nil, cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("node %q: pipeline: unknown side-effect kind %q", nodeID, se.Kind), nil, nil)
	}
}

// sideEffectName returns the emitted-file path an EMIT contributes: the
// declared file path for emit_file, a deterministic derived name for emit_crd.
func sideEffectName(se frp.SideEffect, idx int) string {
	if se.Kind == "emit_file" && se.File != nil && se.File.Path != "" {
		return se.File.Path
	}
	return fmt.Sprintf("pipeline-emit-%d.yaml", idx)
}

// mapWalkError promotes the pipeline walk's local error causes to the
// renderer's build-halting Loud Guardrails. An absent patch target becomes
// ErrPipelineTargetNotEmitted; a /host operation from a hostless step becomes
// ErrPipelineHostScopeUnavailable; any other walk failure is wrapped as a
// catalog error naming the node.
func mapWalkError(err error, nodeID string, hasHost bool) error {
	if errors.Is(err, frp.ErrPipelineTargetAbsent) {
		// A boundary-scope / hostless step that addressed /host surfaces here as
		// an absent target on the empty Host root; report the precise host-scope
		// diagnostic when the step had no host component. Chain the walk's cause
		// (two %w verbs) so both the renderer sentinel and the walk's
		// ErrPipelineTargetAbsent stay matchable via errors.Is.
		if !hasHost && mentionsHostScope(err) {
			inner := fmt.Errorf("%w: %w", ErrPipelineHostScopeUnavailable, err)
			return cerrors.Wrap(inner, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
				fmt.Sprintf("node %q: %s", nodeID, ErrPipelineHostScopeUnavailable.Error()))
		}
		inner := fmt.Errorf("%w: %w", ErrPipelineTargetNotEmitted, err)
		return cerrors.Wrap(inner, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("node %q: %s", nodeID, ErrPipelineTargetNotEmitted.Error()))
	}
	return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
		fmt.Sprintf("node %q: pipeline step failed", nodeID))
}

// mentionsHostScope reports whether an absent-target error addressed the host
// scope. The walk wraps the offending scope into the message; matching on it
// distinguishes a /host op from a hostless step from a genuine own-scope
// absent target.
func mentionsHostScope(err error) bool {
	return err != nil && strings.Contains(err.Error(), `scope "host"`)
}

func wrapPipelineErr(err error, nodeID string) error {
	return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
		fmt.Sprintf("node %q: pipeline document set construction", nodeID))
}

func pipelineNotStructuredErr(nodeID, token string) error {
	return cerrors.Wrap(ErrPipelinePatchTargetNotStructured, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
		fmt.Sprintf("node %q: %s: document %q", nodeID, ErrPipelinePatchTargetNotStructured.Error(), token))
}

func pipelinePreAssemblyErr(nodeID, token, scope string) error {
	return cerrors.Wrap(ErrPipelinePatchPreAssembly, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
		fmt.Sprintf("node %q: %s (%s-scope document %q is rendered by an assembler provider; the post-assembly phase re-runs this PATCH once the documents are discrete)",
			nodeID, ErrPipelinePatchPreAssembly.Error(), scope, token))
}

func pipelineHostScopeErr(nodeID, name string) error {
	return cerrors.Wrap(ErrPipelineHostScopeUnavailable, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
		fmt.Sprintf("node %q: %s (emit %q)", nodeID, ErrPipelineHostScopeUnavailable.Error(), name))
}
