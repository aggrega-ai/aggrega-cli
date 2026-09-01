// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"os"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

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
		return "", cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: vm.Import}, fmt.Sprintf("%s %q valuesMapping file import", entityKind, entityName))
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
		return "", cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: w.Import}, fmt.Sprintf("Recipe %q workload file import", entityName))
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
		return "", cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: a.Import}, fmt.Sprintf("Recipe %q artifact %q file import", entityName, artifactName))
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

// ExecutePhaseBForFeature evaluates a FeatureRecipe's valuesMapping and
// returns the values dictionary for the provider engine. Mirrors
// ExecutePhaseB for base Recipes but takes a *types.FeatureRecipe. The
// valuesMapping field carries one of three content-shape-dispatched
// modes (inline / file $import: / library $import:); this function
// resolves the mode and returns the templated values output.
func ExecutePhaseBForFeature(ctx *types.TemplateContext, fr *types.FeatureRecipe, mappingLibs map[string]types.MappingLibrary) (map[string]any, error) {
	if fr == nil {
		return map[string]any{}, nil
	}

	tmplText, err := resolveValuesMappingText(fr.Spec.ValuesMapping, fr.Metadata.Name, "FeatureRecipe", mappingLibs)
	if err != nil {
		return nil, err
	}

	if tmplText == "" {
		// No valuesMapping — return feature-scoped .values as-is.
		out := make(map[string]any, ctx.Values.Len())
		for k, v := range ctx.Values.All() {
			out[k] = v
		}
		return out, nil
	}

	output, err := EvalTemplate(
		fmt.Sprintf("fr-valuesMapping[%s]", fr.Metadata.Name),
		tmplText,
		ctx,
	)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Metadata.Name}, fmt.Sprintf("FeatureRecipe %q valuesMapping", fr.Metadata.Name))
	}

	var values map[string]any
	if err := parse.TemplateOutput([]byte(output), &values); err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Metadata.Name}, fmt.Sprintf("FeatureRecipe %q valuesMapping output is not valid YAML", fr.Metadata.Name))
	}

	return values, nil
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
