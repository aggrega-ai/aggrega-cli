// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"os"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// importLoadError reports a `$import:` whose file the build could not acquire,
// as the load failure it is rather than as an untyped read error.
//
// It names a condition this package does not declare, and that is the point: the
// failure is not specific to which key named the path — valuesMapping, workload
// and an artifacts entry each leave the same surface unusable and give the author
// the same thing to do — so each raise path supplies the clause saying where it
// happened and leaves the identity to the package that already holds it. A
// second identity per naming key would hand a reader another code to look up for
// one failure.
//
// It composes through the condition's registered sentence rather than emitting
// surface alone, so the diagnostic opens with that sentence and carries the raise
// path's own clause after it. The boundary that renders a halt prints the
// outermost frame whose text the output does not already carry, so a message
// that did not lead with the sentence would print it a second time as a
// redundant continuation line.
//
// The cause's text travels as one of the occurrence's values rather than as a
// wrapped frame, so the operating system's own reason — which is what tells a
// missing file from an unreadable one — reaches the reader on the same line as
// the surface that named it.
//
// The advice is the raise path's rather than the condition's, which is what the
// third argument of the composition is for: the condition covers every file a
// build has to read, and a sentence about library references would be wrong at
// most of those sites. Here it is right at all of them, because every caller is
// the file arm of an `$import:`. It is [types.ImportFragmentAdvice] rather than
// a sentence of this package's own, because the pin freeze reports the same
// failure for an artifacts entry from its own raise path and the two have to say
// the same thing.
func importLoadError(path string, cause error, surface string) error {
	condition := resolver.ErrYAMLLoad()
	return cerrors.Wrap(
		condition,
		cerrors.CatalogError,
		cerrors.SourceLocation{File: path},
		cerrors.ComposeMessage(condition, []string{surface, cause.Error()}, types.ImportFragmentAdvice),
	)
}

// resolveValuesMappingText resolves a ValuesMapping struct to a Go template
// source string. Mode 1 (inline) returns the authored content directly;
// mode 2 (file $import:) reads the file from disk, at the absolute path the
// catalog resolved against the declaring entity's own directory when it
// loaded the entity; mode 3 (library
// $import: in the bare-name identity form `<Library>#/mappings/<entry>`)
// resolves the referenced MappingLibrary entry's template fragment from
// mappingLibs (the local catalog plus any lockfile-pinned libraries bridged
// in at load), recursively when an entry body is itself a field-root
// `$import:`. A host-bearing `oci://…` $import: is rejected with
// ErrHostInLibraryImport — a library is addressed by identity, not by
// registry location. The empty struct returns "" + nil (callers treat as
// "no valuesMapping").
//
// entityKind is one of "Recipe" / "FeatureRecipe"; used in
// error wrapping for diagnostic clarity. entityName is the metadata.name
// of the loaded entity.
func resolveValuesMappingText(vm types.ValuesMapping, entityName, entityKind string, mappingLibs map[string]types.MappingLibrary) (string, error) {
	if vm.IsEmpty() {
		return "", nil
	}
	if vm.Inline != "" {
		return vm.Inline, nil
	}
	if vm.HasOCIPrefix() {
		return "", cerrors.Wrap(ErrHostInLibraryImport(), cerrors.CatalogError, cerrors.SourceLocation{File: entityName},
			cerrors.ComposeMessage(ErrHostInLibraryImport(), []string{
				fmt.Sprintf("%s %q", entityKind, entityName),
				fmt.Sprintf("valuesMapping $import %q", vm.Import),
			}, ""))
	}
	if vm.IsLibraryImport() {
		text, err := resolveMappingLibraryImport(vm.Import, mappingLibs, nil)
		if err != nil {
			return "", cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: entityName}, fmt.Sprintf("%s %q valuesMapping library import %q", entityKind, entityName, vm.Import))
		}
		return text, nil
	}
	data, err := os.ReadFile(vm.Import)
	if err != nil {
		return "", importLoadError(vm.Import, err, fmt.Sprintf("%s %q valuesMapping file import", entityKind, entityName))
	}
	return string(data), nil
}

// resolveWorkloadText resolves a WorkloadTemplate struct to its Go-
// template source string. Mode 1 (inline) returns the authored content
// directly; mode 2 (file $import:) reads the file from disk, at the absolute
// path the catalog resolved against the declaring entity's own directory when
// it loaded the entity; mode 3
// (library $import: in the bare-name identity form
// `<Library>#/workloads/<entry>`) resolves the referenced WorkloadLibrary
// entry's template fragment from workloadLibs (the local catalog),
// recursively when an entry body is itself a field-root `$import:`. A
// host-bearing `oci://…` $import: is rejected with ErrHostInLibraryImport
// — a library is addressed by identity, not by registry location. The
// empty struct returns "" + nil (callers treat as "no workload declared").
func resolveWorkloadText(w types.WorkloadTemplate, entityName string, workloadLibs map[string]types.WorkloadLibrary) (string, error) {
	if w.IsEmpty() {
		return "", nil
	}
	if w.Inline != "" {
		return w.Inline, nil
	}
	if w.HasOCIPrefix() {
		return "", cerrors.Wrap(ErrHostInLibraryImport(), cerrors.CatalogError, cerrors.SourceLocation{File: entityName},
			cerrors.ComposeMessage(ErrHostInLibraryImport(), []string{
				fmt.Sprintf("Recipe %q", entityName),
				fmt.Sprintf("workload $import %q", w.Import),
			}, ""))
	}
	if w.IsLibraryImport() {
		text, err := resolveWorkloadLibraryImport(w.Import, workloadLibs, nil)
		if err != nil {
			return "", cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: entityName}, fmt.Sprintf("Recipe %q workload library import %q", entityName, w.Import))
		}
		return text, nil
	}
	data, err := os.ReadFile(w.Import)
	if err != nil {
		return "", importLoadError(w.Import, err, fmt.Sprintf("Recipe %q workload file import", entityName))
	}
	return string(data), nil
}

// resolveArtifactEntryText resolves an ArtifactEntry struct to its
// Go-template source string. Mode 1 (inline) returns the authored
// content directly; mode 2 (file $import:) reads the file from disk, at
// the absolute path the catalog resolved against the declaring entity's
// own directory when it loaded the entity. The envelope schema rejects
// oci:// prefixes at artifacts entries for the same reason as workload:.
func resolveArtifactEntryText(a types.ArtifactEntry, entityName, artifactName string) (string, error) {
	text, err := a.Text()
	if err != nil {
		return "", importLoadError(a.Import, err, fmt.Sprintf("Recipe %q artifact %q file import", entityName, artifactName))
	}
	return text, nil
}

// EvaluateFeatureRecipeLocals evaluates fr.Spec.Locals templates against the
// feature-scoped context and returns the resolved map. The context is
// LocalsContext, the same node-local type Recipe locals use, so the two surfaces
// admit and refuse the same references — identity, the attachment's own values and
// infra, and no provides pillar — without either restating the rule.
//
// The two identities the surface exposes answer two different questions, and
// neither substitutes for the other:
//
//   - .context.instance_id is the HOST COMPONENT the attachment is resolving for.
//     It means that on every surface the compiler evaluates, so an author reads one
//     denotation whichever entity's block they are writing.
//   - .attachment_name is the attachment instance being resolved — the slot it
//     fills, or the free name the author gave it. It is the route from a local to
//     the instance name, and the only one.
//
// instanceID supplies the first and instanceName the second. A base Recipe's locals
// surface refuses .attachment_name, because no attachment is being resolved there.
//
// instanceID is the identity ALREADY PUBLISHED as .context.instance_id on the host
// node's context, not the identity the graph is keyed by, and the difference is
// load-bearing: a published identity has had the composition join written out of it,
// and that encoding is injective rather than idempotent, so encoding a published
// identity again would write out the escape the first pass emitted and give one node
// two spellings. A caller holding a graph identity here encodes it once; a caller
// reading it off a context, as the one production caller does, passes it through.
//
// Iteration is in author order (fr.Spec.Locals is an OrderedMap), and an entry may
// read an earlier sibling: the evaluation routes through evaluateLocalsInOrder,
// the one home of the locals contract, which publishes the resolved-so-far prefix
// for each entry and halts on a forward or unknown sibling reference.
//
// The prefix is the FeatureRecipe's OWN block only. A base Recipe's locals are
// merged over this result AFTER it returns, so they are not resolved at the point
// an attachment's local is evaluated, and an attachment local naming a base-Recipe
// key reports the unknown-reference halt.
func EvaluateFeatureRecipeLocals(
	fr *types.FeatureRecipe,
	instanceName, instanceID string,
	values types.OrderedMap[any],
	infra types.OrderedMap[any],
) (types.OrderedMap[string], error) {
	if fr == nil {
		return types.OrderedMap[string]{}, nil
	}
	return evaluateLocalsInOrder(
		fr.Spec.Locals,
		featureRecipeLocalsScope,
		func(key string) localsRefSite {
			return localsRefSite{
				Where: fmt.Sprintf("FeatureRecipe %q locals[%q]", fr.Metadata.Name, key),
				File:  fr.Metadata.Name,
				Key:   key,
			}
		},
		func(key string) string { return fmt.Sprintf("fr-locals[%q]", key) },
		func(resolved types.OrderedMap[string]) any {
			return LocalsContext{
				Context:        ProvidesContextBlock{InstanceID: instanceID},
				Values:         values,
				Infra:          infra,
				Locals:         resolved,
				AttachmentName: instanceName,
			}
		},
	)
}

// FeatureValues is a FeatureRecipe's evaluated values dictionary together with
// the one thing about it that only the evaluation can state: whether its keys
// ARE the leaf paths the Feature declares.
//
// The fact travels with the dictionary rather than being answered again later,
// for the reason the base Recipe's own evaluation states where it records the
// same fact: reading the values mapping a second time to reach the same
// conclusion would be two readings of one declaration, free to disagree once
// either moves.
type FeatureValues struct {
	// Resolved is the values dictionary the provider engine consumes.
	Resolved map[string]any

	// KeysAreDeclaredLeaves reports that Resolved's keys are the Feature's
	// authored leaf paths. It is false where a values mapping ran, because the
	// mapping is one whole-document template and its output keys are the
	// template's own invention: a declaration names none of them.
	KeysAreDeclaredLeaves bool
}

// ExecutePhaseBForFeature evaluates a FeatureRecipe's valuesMapping and
// returns the values dictionary for the provider engine, with the one fact
// about those keys that the evaluation settles. Mirrors
// ExecutePhaseB for base Recipes but takes a *types.FeatureRecipe. The
// valuesMapping field carries one of three content-shape-dispatched
// modes (inline / file $import: / library $import:); this function
// resolves the mode and returns the templated values output.
func ExecutePhaseBForFeature(ctx *types.TemplateContext, fr *types.FeatureRecipe, mappingLibs map[string]types.MappingLibrary) (FeatureValues, error) {
	if fr == nil {
		return FeatureValues{Resolved: map[string]any{}}, nil
	}

	tmplText, err := resolveValuesMappingText(fr.Spec.ValuesMapping, fr.Metadata.Name, "FeatureRecipe", mappingLibs)
	if err != nil {
		return FeatureValues{}, err
	}

	if tmplText == "" {
		// No valuesMapping — return feature-scoped .values as-is.
		out := make(map[string]any, ctx.Values.Len())
		for k, v := range ctx.Values.All() {
			out[k] = v
		}
		// This branch is the ONLY one whose resolved keys are the authored leaf
		// paths, so it is the only one that can record the fact — the same
		// reading, at the same position, that the base Recipe's own values pass
		// makes about its own branch.
		return FeatureValues{Resolved: out, KeysAreDeclaredLeaves: true}, nil
	}

	output, err := EvalTemplate(
		fmt.Sprintf("fr-valuesMapping[%s]", fr.Metadata.Name),
		tmplText,
		ctx,
	)
	if err != nil {
		return FeatureValues{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Metadata.Name}, fmt.Sprintf("FeatureRecipe %q valuesMapping", fr.Metadata.Name))
	}

	var values map[string]any
	if err := parse.TemplateOutput([]byte(output), &values); err != nil {
		return FeatureValues{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Metadata.Name}, fmt.Sprintf("FeatureRecipe %q valuesMapping output is not valid YAML", fr.Metadata.Name))
	}

	return FeatureValues{Resolved: values}, nil
}

// featureRecipeDeclaredSensitivity assembles the sensitivity declarations one
// attachment's own render is judged against, from the attachment's own side
// rather than from the component hosting it.
//
// It is the ONE home of that assembly. Both attachment scopes copy the host's
// render context at their own site, and a context copy names no field — so an
// assembly written out at each of them would be two statements of one rule,
// agreeing on the day they were written and free to diverge afterwards.
//
// Each of the three declarations is read from whichever entity ALREADY governs
// the data it is joined against, which is what makes each join declaration to
// declaration rather than name to name:
//
//   - the leaf set, from featureType — the Feature the attachment NAMES, whose
//     schema the attachment's authored values were checked against — read
//     through the render's one memoised reader of that declaration, so this
//     answer and the withholding set's cannot differ for one type;
//   - the gate over that leaf set, from the values evaluation the caller already
//     ran, because only that evaluation knows whether a mapping invented the
//     keys;
//   - the slot set, from the Feature the FeatureRecipe IMPLEMENTS, because the
//     entries this join marks are the ones a FeatureRecipe-synthesized workload
//     authored, and that Feature's slots are the allowlist those entries were
//     checked against.
//
// 🔴 The first and the third deliberately read different entities, and they are
// equal in most deployments without being held equal by anything: which
// FeatureRecipe binds to an attachment is decided by the deployment's mapping,
// not by what the FeatureRecipe implements. Two authoring surfaces are in play —
// the attachment's values are written where the attachment is attached, and the
// workload's entries inside the FeatureRecipe — so each is joined against the
// declarations its own author wrote to.
//
// An input that carries no reader — one assembled outside RenderAll — declares
// nothing here, and a declaration set that declares nothing marks nothing. That
// is this axis's safe direction, stated on the declaration type itself.
func featureRecipeDeclaredSensitivity(fr *types.FeatureRecipe, featureType string, input RendererInput, keysAreDeclaredLeaves bool) DeclaredSensitivity {
	if fr == nil || input.declaredFeatureLeaves == nil {
		return DeclaredSensitivity{}
	}
	return DeclaredSensitivity{
		ValueLeaves:             input.declaredFeatureLeaves(featureType).Values,
		ValuesAreDeclaredLeaves: keysAreDeclaredLeaves,
		Slots:                   ir.SensitiveSlots(featureAttachmentSlots(fr, input)),
	}
}

// isResourceGeneratingFR returns true if the FeatureRecipe declares a render
// input kind — source, valuesMapping, workload, or artifacts — i.e., it
// produces its own resources in Phase B. A FeatureRecipe declaring none of them
// generates no resource of its own: an inject-only one (only injectMapping)
// contributes through Phase A, and a pure-wiring one (capability and implements
// only) has its whole effect in the uses-edge the Feature it implements carries.
//
// It answers what its name asks and nothing else. It does NOT decide whether an
// attachment takes part in the base-rendering sequence —
// participatesInRenderSequence answers that, and a pipeline-declaring
// FeatureRecipe takes part while generating no resource of its own.
func isResourceGeneratingFR(fr *types.FeatureRecipe) bool {
	if fr == nil {
		return false
	}
	if sourceValueDeclared(fr.Spec.Source) {
		return true
	}
	if !fr.Spec.ValuesMapping.IsEmpty() {
		return true
	}
	if !fr.Spec.Workload.IsEmpty() {
		return true
	}
	if fr.Spec.Artifacts.Len() > 0 {
		return true
	}
	return false
}

// participatesInRenderSequence reports whether an attachment's FeatureRecipe has
// work to do in the base-rendering attachment sequence. Two disjoint reasons put
// it there, and they are different questions:
//
//   - it declares a render input, so it generates a resource of its own;
//   - it declares a pipeline, so it rewrites documents the base Recipe or an
//     earlier attachment in the sequence already emitted.
//
// The resource predicate above answers only the first. A pipeline generates no
// resource — it patches what is already there — so widening that predicate to
// return true on a declared pipeline would make its name assert something false.
// The two questions stay separate, and this one gates admission.
//
// A FeatureRecipe declaring neither has nothing to do in the sequence and is
// excluded: an inject-only one contributes through Phase A, and a pure-wiring
// one through the uses-edge the Feature it implements carries.
func participatesInRenderSequence(fr *types.FeatureRecipe) bool {
	if fr == nil {
		return false
	}
	return isResourceGeneratingFR(fr) || len(fr.Spec.Pipeline) > 0
}
