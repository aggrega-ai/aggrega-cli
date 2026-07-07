// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Phase B.3 artifacts host-side projection per chapter 16's three-input-kinds
// model. `artifacts:` is a single OrderedMap keyed by output filename; each entry
// is either inline content (a templated string) or $import:-loaded (a map
// containing only $import: with a file path). The host runs the generic,
// invariant-forced work — the $import read, the pillar pre-check, the Go-template
// evaluation against the full TemplateContext (including .workload and .source),
// and the build-provenance key computation — and projects the result to the
// provider guest, which decides format, provenance placement, output path, and
// final bytes. This file carries the host projection builder, the exported
// Kubernetes-YAML provenance placer the YAML-emitting guests call, and the shared
// provenance-splice primitives.

package renderer

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ArtifactsEmitter is the optional interface a provider engine implements to
// place a node's pre-evaluated artifact set into provider-native output. Phase
// B.3 builds an [ArtifactsProjection] — each entry already read from its source,
// evaluated as a Go template against the render context, and carrying the host-
// computed build-provenance key sets — and dispatches it here. The guest owns the
// format decision, the provenance placement, the output path, and the final
// bytes; the host runs only the generic, invariant-forced work (the $import read,
// the pillar pre-check, the template evaluation, and the provenance-key
// computation).
//
// Mirrors [WorkloadSynthesizer]: an engine that does not implement it produces a
// CATALOG_ERROR at B.3. Implemented by the Wasm provider adapter for every shipped
// provider; the delimiter accessor reports the guest's declared artifact-template
// delimiter pair so the host evaluates each entry with the pair the guest's target
// syntax needs (the default "{{"/"}}", or an alternate such as helm-chart's
// "[["/"]]").
type ArtifactsEmitter interface {
	// EmitArtifacts places the pre-evaluated artifact set into provider-native
	// output. Returns []EmittedFile (each tagged with its artifacts origin and
	// Intermediate marking by the emitter) or a render error.
	EmitArtifacts(ctx context.Context, proj ArtifactsProjection, nodeCtx NodeRenderContext) ([]EmittedFile, error)
	// ArtifactTemplateDelimiters reports the Go-template delimiter pair the host
	// evaluates each artifact entry with before projecting it — the guest's own
	// declared pair, so the guest's target syntax (e.g. Helm's "{{ ... }}") is
	// left untouched by the host's pre-templating pass.
	ArtifactTemplateDelimiters() (left, right string)
}

// buildArtifactsProjection performs the generic, invariant-forced host work for a
// node's artifacts: it walks Recipe.Spec.Artifacts in author order, resolves each
// entry's text (inline content or the $import:-loaded file the host reads per
// Invariant 2), runs the pillar pre-check, and evaluates the entry as a Go
// template against tmplCtx using the supplied delimiter pair. It attaches the
// host-computed build-provenance label and annotation key sets — the same key
// computation every emitting surface shares — to each entry. Format, provenance
// placement, path, and final bytes are the guest's concern via
// [ArtifactsEmitter.EmitArtifacts].
//
// The per-entry output name is the author-declared map key for an inline entry
// and the imported file's base name for an $import:-loaded entry; origin records
// which form the entry took so a guest that rejects one form can halt on it.
func buildArtifactsProjection(
	tmplCtx *types.TemplateContext,
	state *PhaseBState,
	nodeCtx NodeRenderContext,
	leftDelim, rightDelim string,
) (ArtifactsProjection, error) {
	labels := BuildAggregaProvenanceLabels(nodeCtx)
	annotations := BuildAggregaProvenanceAnnotations(nodeCtx, recipeURI(state))

	var proj ArtifactsProjection
	for _, key := range state.Recipe.Spec.Artifacts.Keys() {
		entry, _ := state.Recipe.Spec.Artifacts.Get(key)
		tmplText, err := resolveArtifactEntryText(entry, state.RecipeName, key)
		if err != nil {
			return ArtifactsProjection{}, err
		}
		name := key
		origin := "inline"
		if entry.Import != "" {
			name = filepath.Base(entry.Import)
			origin = "import"
		}
		if err := preCheckArtifactPillars(state, name, tmplText, leftDelim, rightDelim); err != nil {
			return ArtifactsProjection{}, err
		}
		rendered, err := EvalTemplateWithDelims(
			fmt.Sprintf("artifacts[%s/%s]", state.RecipeName, name),
			tmplText, tmplCtx, leftDelim, rightDelim,
		)
		if err != nil {
			return ArtifactsProjection{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: name}, fmt.Sprintf("artifact %q template eval", name))
		}
		proj.Entries = append(proj.Entries, ArtifactProjectionEntry{
			Name:        name,
			Origin:      origin,
			Content:     []byte(rendered),
			Labels:      labels,
			Annotations: annotations,
		})
	}
	return proj, nil
}

// preCheckArtifactPillars walks the template's parsed field-access set
// (via CollectTemplateFieldAccessesWithDelims) and short-circuits with a
// CATALOG_ERROR when the template references a pillar slot that the
// current Recipe has not declared:
//   - any `.workload.*` reference when state.ParsedWorkload == nil.
//   - any `.source.*` reference when state.Recipe.Spec.Source == nil.
//
// The pre-check names the failing expression authoritatively (the literal
// dot-path captured by the parser) and avoids depending on text/template's
// runtime nil-pointer error formatting.
//
// leftDelim/rightDelim are the same delimiter pair the eventual
// EvalTemplateWithDelims call will use, so the parser sees Aggrega's
// templating exactly as the renderer will execute it. For external-handler
// engines using non-default delimiters (helm-chart's `[[ ]]`), Helm's
// `{{ ... }}` blocks become opaque text and don't surface as parse errors
// or as field-access entries.
func preCheckArtifactPillars(state *PhaseBState, filename, tmplText, leftDelim, rightDelim string) error {
	refs, err := CollectTemplateFieldAccessesWithDelims(tmplText, leftDelim, rightDelim)
	if err != nil {
		return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: filename}, fmt.Sprintf("artifact %q template parse", filename))
	}
	workloadDeclared := state.ParsedWorkload != nil
	sourceDeclared := false
	if state.Recipe != nil {
		switch s := state.Recipe.Spec.Source.(type) {
		case nil:
			sourceDeclared = false
		case string:
			sourceDeclared = s != ""
		default:
			sourceDeclared = true
		}
	}

	for _, ref := range refs {
		if !workloadDeclared && (ref == ".workload" || strings.HasPrefix(ref, ".workload.")) {
			return cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: filename},
				fmt.Sprintf("artifact %q template references %q but Recipe declares no workload:", filename, ref),
				nil, nil)
		}
		if !sourceDeclared && (ref == ".source" || strings.HasPrefix(ref, ".source.")) {
			return cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: filename},
				fmt.Sprintf("artifact %q template references %q but Recipe declares no source:", filename, ref),
				nil, nil)
		}
	}
	return nil
}

// Deliberate second splicer — do NOT unify with the helm render-core label
// injector (helm.injectAggregaLabels / injectAggregaAnnotations). This surgical
// yaml.Node splice is the artifacts path: it preserves the user's authored field
// order and every byte outside the inserted label lines, which a Helm-authored
// artifact needs so its `{{ ... }}` template tokens survive. The render-core
// injector runs on a fully-remarshalled document with its own quoting and
// hook-weight handling for synthesized manifests. The two operate on genuinely
// different inputs and unifying them would change emitted bytes; they are left
// separate on purpose.
//
