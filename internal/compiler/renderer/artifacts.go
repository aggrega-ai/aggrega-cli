// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Phase B.3 artifacts host-side projection per chapter 16's three-input-kinds
// model. `artifacts:` is a single OrderedMap keyed by output filename; each entry
// is either inline content (a templated string) or $import:-loaded (a map
// containing only $import: with a file path). The host runs the generic,
// invariant-forced work — the $import read, the template parse pre-check, the Go-template
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
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
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
// the template parse pre-check, the template evaluation, and the provenance-key
// computation).
//
// Implemented by the Wasm provider adapter — the single engine implementation the
// host renders through — for every shipped provider, by compile-time assertion. So
// the dispatch's own type assertion is not a capability check on what an author
// wrote, and it mirrors [WorkloadSynthesizer] in that too: which input kinds a
// provider may be handed is settled by the central input-kind gate from the
// provider's declared contract, and an engine that does not implement this
// interface produces a CATALOG_ERROR only in a state neither render path reaches.
// The delimiter accessor reports the guest's declared artifact-template
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
// entry's content, asks the rule declared over the owner it is rendering for,
// checks that the template parses, and evaluates it as a Go template
// against tmplCtx using the supplied delimiter pair. It attaches the host-computed
// build-provenance label and annotation key sets — the same key computation every
// emitting surface shares — to each entry. Format, provenance placement, path, and
// final bytes are the guest's concern via [ArtifactsEmitter.EmitArtifacts].
//
// The author-declared map key names the output, in every form. A mapping key is
// unique within one document, so the key set is the output-name set and an author
// reads the name they wrote rather than a name derived from something else.
// Each entry resolves by content-shape (per the file-import vocabulary):
//   - Mode 1 (inline): output name is the key.
//   - Mode 2 (local file $import): output name is the key; the imported file's own
//     name selects the content and does not name the output, so two entries
//     importing different files that share a base name are two distinct outputs.
//     The artifactDigests file-pin is verified.
//   - Mode 3 (library $import, `<Library>#/artifacts/<entry>`): the referenced
//     ArtifactLibrary bundle expands into one output per file, each nested under
//     the consumption key as `<key>/<filename>` — one key names many files, so
//     the key alone cannot name them and the pair identifies both the consumption
//     and the file within the bundle. The library version is lockfile-pinned, so
//     no artifactDigests file-pin applies. A host-bearing `oci://…` reference is
//     rejected (ErrHostInLibraryImport), and a bundle file whose name would not
//     stay under the consumption key is rejected
//     (ErrArtifactLibraryFileEscapesKey) before the name is composed.
//
// origin records which form the entry took so a guest that rejects one form can
// halt on it. Two outputs resolving to the same filename halt loudly with
// ErrArtifactOutputCollision — a residual class rather than the common one, since
// the naming above makes the key set the name set.
//
// cancelled is the caller's own cancellation signal, read by the traversal that
// asks the rule declared over this stage's facts about each entry, so a walk over
// a Recipe declaring many artifacts stops when the caller asks it to rather than
// running to completion and reporting afterwards. A nil signal is an
// uncancellable walk, which is what a caller assembling its own input gets.
func buildArtifactsProjection(
	cancelled constraint.Cancellation,
	tmplCtx *types.TemplateContext,
	state *PhaseBState,
	nodeCtx NodeRenderContext,
	leftDelim, rightDelim string,
) (ArtifactsProjection, error) {
	labels := BuildAggregaProvenanceLabels(nodeCtx)
	annotations := BuildAggregaProvenanceAnnotations(nodeCtx, recipeURI(state))

	var proj ArtifactsProjection
	// seen tracks every emitted output filename so a collision halts loudly rather
	// than one output silently overwriting another (a Loud Guardrail per the No
	// Ghost Infrastructure invariant).
	//
	// What reaches it is a residue, not the common case: the output name is the
	// authored key, and a mapping key is unique within one document, so the
	// derivation cannot manufacture a collision. The authoring surface bounds a
	// key to a single filename, so a key spelling the nested name another key's
	// bundle produces is not writable either. What survives is any input that
	// reached this walk without the envelope validation and the duplicate-key
	// refusal a decoded document gets.
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
		// The retained text is taken here rather than beside the authored entry,
		// because this is where an imported entry's and a bundle entry's content
		// has been resolved to the text that stands at the artifact — and it is
		// taken before evaluation, which is the state the retained value names.
		//
		// It is recorded against the owner the render's state holds, which is
		// what tells a node's own entries from those of an attachment reaching this
		// same walk under the node's identity. A render carrying no owner records
		// nothing.
		state.recordArtifactTemplateText(outputName, tmplText)
		// The pin rules, asked ahead of the pillar rules below because content that
		// has drifted from its recorded pin is content nothing verified: such an
		// entry is refused before anything reads it for any other purpose, so a
		// build meeting both faults reports the drift rather than a pillar
		// reference inside unverified bytes.
		//
		// The recorded pin was retained by the branch that resolved the
		// entry, which is the one place that knows whether the entry's form carries
		// one at all; an entry with no recorded pin leaves both rules with nothing to
		// compare, which is the ordinary answer for an inline entry and for a Recipe
		// with no pin recorded beside it.
		//
		// Two lines for the same reason the pillar readings below are two: the facts
		// are keyed by the owner the render was reached from, so an attachment's
		// entries — which flow through this walk under the HOST component's node
		// identity — are asked about through their own owner's values.
		if err := runArtifactContentPinRule(cancelled, state, outputName); err != nil {
			return err
		}
		if err := runAttachmentArtifactContentPinRule(cancelled, state, outputName); err != nil {
			return err
		}
		// The rule declared over this stage's facts, asked about the entry recorded
		// on the line above. It sits between the recording and the evaluation below
		// because that is the only span where the entry's text exists and nothing
		// has yet evaluated it — after evaluation an undeclared pillar has already
		// become either a placeholder in the output or a halt naming the first hop
		// of the expression rather than the expression the author wrote.
		//
		// It is the only pillar reading on this owner: the pre-check beside it once
		// held the same two conditions and no longer does, so a reference to an
		// undeclared pillar is refused here or not at all.
		if err := runArtifactPillarRule(cancelled, state, outputName); err != nil {
			return err
		}
		// The same question over the owner this render was reached from. This walk
		// is reached by two renders — a node's own Recipe, and the render an
		// attachment's or a boundary-scope FeatureRecipe carries — and the entry
		// belongs to exactly one of them, so the two lines are exclusive rather than
		// cumulative: a node's own state holds the node presentation and no owner,
		// an attachment's render the owner and no node presentation.
		//
		// It is a separate line rather than a wider node axis because the facts are
		// keyed differently: an attachment's entries flow through here under the HOST
		// component's node identity, so a fact keyed by the node alone would answer
		// about the host's pillars while reading the attachment's template.
		if err := runAttachmentArtifactPillarRule(cancelled, state, outputName); err != nil {
			return err
		}
		// What is left of the pre-check: the parse condition, which the declared rule
		// deliberately does not own because evaluation reports it against the same
		// entry. It runs after both pillar readings so a template naming an
		// undeclared pillar is answered by the rule that has something to say about
		// it, and before evaluation so the failure carries a parse attribution.
		if err := preCheckArtifactTemplateParses(outputName, tmplText, leftDelim, rightDelim); err != nil {
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
				cerrors.ComposeMessage(ErrArtifactOutputCollision(), []string{
					fmt.Sprintf("Recipe %q", state.RecipeName),
					fmt.Sprintf("artifact output %q emitted more than once", outputName),
				}, ""))
		}
		seen[outputName] = struct{}{}
		entry := ArtifactProjectionEntry{
			Name:        outputName,
			Origin:      origin,
			Content:     []byte(rendered),
			Labels:      labels,
			Annotations: annotations,
		}
		proj.Entries = append(proj.Entries, entry)
		// The evaluated bytes are retained here rather than beside the recorded
		// text above, because this is the point the entry becomes part of what the
		// render projects: the renders-to-nothing skip and the collision guard both
		// sit between the two, so the key set recorded here is the render's and the
		// one recorded above is the Recipe's. Retaining the same entry the provider
		// receives is what keeps the two from being separate readings of one value.
		// A node whose render carries no retention records nothing.
		state.artifacts.record(state.NodeID, entry.Name, entry.Content)
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
				cerrors.ComposeMessage(ErrHostInLibraryImport(), []string{
					fmt.Sprintf("Recipe %q", state.RecipeName),
					fmt.Sprintf("artifact %q", key),
					fmt.Sprintf("$import %q", entry.Import),
				}, ""))
		case entry.IsLibraryImport():
			// Mode 3: a bundle expands into one output per file, each nested under
			// the key the bundle was consumed at. One key names many files, so the
			// key alone cannot name them and the bundle's own filename alone does
			// not say which consumption it came from — the pair does, and nesting
			// is what keeps both halves readable in the output. The library version
			// is lockfile-pinned, so no artifactDigests file-pin applies here (that
			// check guards only mode-2 local-file imports).
			bundle, err := resolveArtifactLibraryImport(entry.Import, state.ArtifactLibraries)
			if err != nil {
				return ArtifactsProjection{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipeName},
					fmt.Sprintf("Recipe %q artifact library import %q", state.RecipeName, entry.Import))
			}
			for _, fname := range bundle.Files.Keys() {
				// The nesting above is only true of the composed name while the
				// bundle's own filename stays under the key, and the filename comes
				// from a document the consuming Recipe did not write. So it is judged
				// before it is composed rather than after: once the two halves are
				// joined, a climbing segment reads as an ordinary interior one and the
				// composed name resolves somewhere the consuming Recipe never named.
				// The question is asked of the shared containment predicate, in slash
				// arithmetic, because an output name is a logical name rather than a
				// path in the host's own spelling — a backslash is an ordinary
				// character in it on every platform.
				if !pathscope.IsLocalSlash(fname) {
					return ArtifactsProjection{}, cerrors.Wrap(ErrArtifactLibraryFileEscapesKey(),
						cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipeName},
						cerrors.ComposeMessage(ErrArtifactLibraryFileEscapesKey(), []string{
							fmt.Sprintf("Recipe %q", state.RecipeName),
							fmt.Sprintf("artifact %q", key),
							fmt.Sprintf("bundle file %q", fname),
						}, "rename the file inside the ArtifactLibrary bundle so its name stays under the artifacts key it is consumed at"))
				}
				body, _ := bundle.Files.Get(fname)
				if err := emit(key+"/"+fname, "import", body); err != nil {
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
				origin = "import"
				// The file this entry was supplied from is retained in the same
				// branch as the pin below, because this is the one branch that knows
				// the entry's resolved form names a file at all. It is the resolved
				// path, which is absolute: a finding about drifted content is about a
				// file on disk, and the entry's output name is chosen independently of
				// that file, so nothing else the finding carries can name it.
				//
				// It is retained for EVERY imported entry rather than only for one
				// carrying a recorded pin, because the two are different questions:
				// which file supplied this entry, and whether a pull recorded a digest
				// for it beside the document. Gating the path on the pin would make the
				// fact answer emptily for a Recipe with nothing recorded, whose entries
				// name a file exactly as a pinned one's do.
				state.recordArtifactImportedPath(name, entry.Import)
				// The recorded pin is retained here rather than at the emit seam,
				// because this is the one branch that knows the entry's resolved form
				// is content supplied by an imported file — the only form a pin is
				// recorded for. An inline entry is pinned by whatever pins the entity
				// it is part of, and a bundle-supplied one by the version the bundle
				// resolved at, so neither carries a file digest to compare against.
				// Retaining a pin for every entry and leaving each reader to decide
				// whether the comparison applied would put the form gate in as many
				// places as there are readers.
				//
				// A Recipe no pull has pinned has nothing recorded beside it, which
				// leaves the rules over this entry with nothing to compare and is the
				// ordinary answer rather than an approval. The pins are the ones the load
				// recorded for the document this entity was read from, resolved onto
				// the state when it was built — so an entity declared in no document
				// records nothing here for the same reason.
				if pin, ok := recipepin.MatchArtifactPin(state.RecordedPins.ArtifactDigests, key); ok {
					state.recordArtifactContentPin(name, pin.Digest)
				}
			}
			if err := emit(name, origin, tmplText); err != nil {
				return ArtifactsProjection{}, err
			}
		}
	}
	return proj, nil
}

// ArtifactOriginIndex answers, for one file a provider guest placed, which
// authoring form of the node's artifacts produced it. The guest owns the output
// path, so the projection entry a placed file came from is read back from the path
// rather than carried on it; this index is the one home for that reading, so every
// adapter on the [ArtifactsEmitter] seam attributes a file identically.
//
// The reading is exact on the whole output name and tolerant of a directory the
// guest prepended — a provider whose format needs its manifests under a
// subdirectory places them there, while the name the projection carries is
// unchanged. It is deliberately NOT a match on the name's last segment: a name
// with more than one segment has no last-segment entry to find, and two entries
// whose names end alike are indistinguishable that way.
//
// Its bound, stated because it cannot be closed from here: a file the guest
// emitted for itself, whose path ends in an entry's whole name, is attributed to
// that entry. Nothing on the placed file distinguishes the two, so a guest's own
// companion output is best named so it cannot be mistaken for an authored one.
type ArtifactOriginIndex struct {
	byName map[string]EmitOrigin
}

// NewArtifactOriginIndex indexes a projection's entries by the output name the
// host resolved for each, once per emission, so the per-file reading is a lookup
// rather than a walk.
func NewArtifactOriginIndex(proj ArtifactsProjection) ArtifactOriginIndex {
	byName := make(map[string]EmitOrigin, len(proj.Entries))
	for _, ent := range proj.Entries {
		origin := EmitOriginArtifactsInline
		if ent.Origin == "import" {
			origin = EmitOriginArtifactsImport
		}
		byName[ent.Name] = origin
	}
	return ArtifactOriginIndex{byName: byName}
}

// For reports the input-kind origin of the file a guest placed at path. A path
// claimed by no entry — a companion the guest emitted for itself — reports the
// inline tag, which is what the host reports for output it did not project.
func (ix ArtifactOriginIndex) For(path string) EmitOrigin {
	if origin, ok := ix.byName[path]; ok {
		return origin
	}
	// The scan runs forwards so the longest trailing run of segments is tried
	// first: a shorter suffix of the same path is a different, less specific name,
	// and the most specific entry naming this file is the one that produced it.
	for i := 0; i < len(path); i++ {
		if path[i] != '/' {
			continue
		}
		if origin, ok := ix.byName[path[i+1:]]; ok {
			return origin
		}
	}
	return EmitOriginArtifactsInline
}

// preCheckArtifactTemplateParses surfaces a template that does not parse as a
// CATALOG_ERROR positioned at the artifact, before the entry reaches evaluation.
//
// leftDelim/rightDelim are the same delimiter pair the eventual
// EvalTemplateWithDelims call will use, so the parser sees Aggrega's templating
// exactly as the renderer will execute it. For external-handler engines using
// non-default delimiters (helm-chart's `[[ ]]`), Helm's `{{ ... }}` blocks become
// opaque text and don't surface as parse errors.
//
// A parse failure is deliberately NOT the declared pillar rule's finding, and this
// is where the condition kept its home for that reason. Evaluation would report it
// against the same entry, so refusing it in both places would report one authoring
// fault twice; refusing it here first is what gives it the parse attribution
// against the artifact's own name rather than the evaluation wording.
//
// It is the whole of what this pre-check still owns. The pillar conditions that
// used to sit beside it — a `.workload` or `.source` reference the document
// carrying the template does not declare — belong to the rule declared over each
// owner the artifacts walk is reached by, which reports every offending
// reference in a template rather than the first and positions its finding at the
// document the reference was written in.
func preCheckArtifactTemplateParses(filename, tmplText, leftDelim, rightDelim string) error {
	if _, err := gotmpl.CollectTemplateFieldAccessesWithDelims(tmplText, leftDelim, rightDelim); err != nil {
		return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: filename}, fmt.Sprintf("artifact %q template parse", filename))
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
