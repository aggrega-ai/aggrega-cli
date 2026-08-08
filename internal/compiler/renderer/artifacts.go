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

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
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
// entry's content, runs the pillar pre-check, and evaluates it as a Go template
// against tmplCtx using the supplied delimiter pair. It attaches the host-computed
// build-provenance label and annotation key sets — the same key computation every
// emitting surface shares — to each entry. Format, provenance placement, path, and
// final bytes are the guest's concern via [ArtifactsEmitter.EmitArtifacts].
//
// Each entry resolves by content-shape (per the file-import vocabulary):
//   - Mode 1 (inline): output name is the author-declared map key.
//   - Mode 2 (local file $import): output name is the imported file's base name;
//     the artifactDigests file-pin is verified.
//   - Mode 3 (library $import, `<Library>#/artifacts/<entry>`): the referenced
//     ArtifactsLibrary bundle expands into one output per file, each named by the
//     bundle's authored filename (not the consumption key). The library version
//     is lockfile-pinned, so no artifactDigests file-pin applies. A host-bearing
//     `oci://…` reference is rejected (ErrHostInLibraryImport).
//
// origin records which form the entry took so a guest that rejects one form can
// halt on it. Two outputs resolving to the same filename halt loudly with
// ErrArtifactOutputCollision.
func buildArtifactsProjection(
	tmplCtx *types.TemplateContext,
	state *PhaseBState,
	nodeCtx NodeRenderContext,
	leftDelim, rightDelim string,
) (ArtifactsProjection, error) {
	labels := BuildAggregaProvenanceLabels(nodeCtx)
	annotations := BuildAggregaProvenanceAnnotations(nodeCtx, recipeURI(state))

	var proj ArtifactsProjection
	// seen tracks every emitted output filename so a collision — two bundle
	// files, or a bundle file and another artifacts entry, resolving to the same
	// output name — halts loudly rather than one output silently overwriting
	// another (a Loud Guardrail per the No Ghost Infrastructure invariant).
	//
	// Deliberately narrow, and deliberately not the only collision check: this
	// map spans one Recipe's own artifacts entries, so it sees a Recipe naming
	// one output filename twice and nothing else. Two separate producers meeting
	// on one path once their output is assembled is a different key space this
	// map never observes, guarded separately where a group's files are resolved.
	// Consolidating the two would either blind this check to the intra-Recipe
	// case or widen it to a scope it cannot see from here.
	seen := map[string]struct{}{}
	emit := func(outputName, origin, tmplText string) error {
		if err := preCheckArtifactPillars(state, outputName, tmplText, leftDelim, rightDelim); err != nil {
			return err
		}
		rendered, err := EvalTemplateWithDelims(
			fmt.Sprintf("artifacts[%s/%s]", state.RecipeName, outputName),
			tmplText, tmplCtx, leftDelim, rightDelim,
		)
		if err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: outputName}, fmt.Sprintf("artifact %q template eval", outputName))
		}
		// A presence-guarded body that renders to nothing must not emit a
		// zero-length file; skip the entry so it never reaches the guest.
		if strings.TrimSpace(rendered) == "" {
			return nil
		}
		if _, dup := seen[outputName]; dup {
			return cerrors.Wrap(ErrArtifactOutputCollision(), cerrors.CatalogError, cerrors.SourceLocation{File: outputName},
				fmt.Sprintf("Recipe %q emits artifact output %q more than once", state.RecipeName, outputName))
		}
		seen[outputName] = struct{}{}
		proj.Entries = append(proj.Entries, ArtifactProjectionEntry{
			Name:        outputName,
			Origin:      origin,
			Content:     []byte(rendered),
			Labels:      labels,
			Annotations: annotations,
		})
		return nil
	}

	for _, key := range state.Recipe.Spec.Artifacts.Keys() {
		entry, _ := state.Recipe.Spec.Artifacts.Get(key)
		switch {
		case entry.HasOCIPrefix():
			// A library is addressed by bare-name identity, never by registry
			// location; reject a host-bearing reference (mirrors workload:).
			return ArtifactsProjection{}, cerrors.Wrap(ErrHostInLibraryImport(),
				cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipeName},
				fmt.Sprintf("Recipe %q artifact %q $import %q", state.RecipeName, key, entry.Import))
		case entry.IsLibraryImport():
			// Mode 3: a bundle expands into one output per file. Each file's name
			// is the bundle's authored filename (not the consumption key); the
			// library version is lockfile-pinned, so no artifactDigests file-pin
			// applies here (that check guards only mode-2 local-file imports).
			bundle, err := resolveArtifactLibraryImport(entry.Import, state.ArtifactsLibraries)
			if err != nil {
				return ArtifactsProjection{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipeName},
					fmt.Sprintf("Recipe %q artifacts library import %q", state.RecipeName, entry.Import))
			}
			for _, fname := range bundle.Files.Keys() {
				body, _ := bundle.Files.Get(fname)
				if err := emit(fname, "import", body); err != nil {
					return ArtifactsProjection{}, err
				}
			}
		default:
			// Mode 1 (inline) / mode 2 (local file).
			tmplText, err := resolveArtifactEntryText(entry, state.RecipeName, key)
			if err != nil {
				return ArtifactsProjection{}, err
			}
			name := key
			origin := "inline"
			if entry.Import != "" {
				name = filepath.Base(entry.Import)
				origin = "import"
				// Build-verify: an imported artifact whose recipe carries a recorded
				// pin must still hash to that pin — a file edited after resolution
				// halts loudly rather than rendering unverified content. A recipe with
				// no recorded pin (never frozen) has nothing to compare and proceeds.
				if pin, ok := recipepin.MatchArtifactPin(state.Recipe.Spec.ArtifactDigests, key); ok {
					if got := digest.ContentDigest([]byte(tmplText)); got != pin.Digest {
						return ArtifactsProjection{}, cerrors.Wrap(ErrArtifactContentMismatch(),
							cerrors.CatalogError, cerrors.SourceLocation{File: entry.Import},
							fmt.Sprintf("Recipe %q artifact %q imported file %s digest %s does not match recorded pin %s",
								state.RecipeName, key, entry.Import, got, pin.Digest))
					}
				}
			}
			if err := emit(name, origin, tmplText); err != nil {
				return ArtifactsProjection{}, err
			}
		}
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
	refs, err := gotmpl.CollectTemplateFieldAccessesWithDelims(tmplText, leftDelim, rightDelim)
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
