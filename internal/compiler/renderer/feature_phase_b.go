// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"os"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// resolveValuesMappingText resolves a ValuesMapping struct to a Go template
// source string. Mode 1 (inline) returns the authored content directly;
// mode 2 (file $import:) reads the file from disk; mode 3 (library
// $import: in the bare-name identity form `<Library>#/mappings/<entry>`)
// resolves the referenced MappingLibrary entry's template fragment from
// mappingLibs (the local catalog plus any lockfile-pinned libraries bridged
// in at load), recursively when an entry body is itself a field-root
// `$import:`. A host-bearing `oci://…` $import: is rejected with
// ErrHostInLibraryImport — a library is addressed by identity, not by
// registry location. The empty struct returns "" + nil (callers treat as
// "no valuesMapping").
//
// entityKind is one of "Recipe" / "FeatureRecipe" / "Provider"; used in
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
		return "", cerrors.Wrap(ErrHostInLibraryImport, cerrors.CatalogError, cerrors.SourceLocation{File: entityName}, fmt.Sprintf("%s %q valuesMapping $import %q", entityKind, entityName, vm.Import))
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
// directly; mode 2 (file $import:) reads the file from disk. The
// envelope schema rejects oci:// prefixes at workload: — library
// imports aren't a supported mode at this site — so an oci:// value
// reaches this function only when authors bypass envelope validation
// (which production load doesn't enforce); the file read surfaces a
// generic "file not found" error in that case. The empty struct
// returns "" + nil (callers treat as "no workload declared").
func resolveWorkloadText(w types.WorkloadTemplate, entityName string) (string, error) {
	if w.IsEmpty() {
		return "", nil
	}
	if w.Inline != "" {
		return w.Inline, nil
	}
	data, err := os.ReadFile(w.Import)
	if err != nil {
		return "", cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: w.Import}, fmt.Sprintf("Recipe %q workload file import", entityName))
	}
	return string(data), nil
}

// resolveArtifactEntryText resolves an ArtifactEntry struct to its
// Go-template source string. Mode 1 (inline) returns the authored
// content directly; mode 2 (file $import:) reads the file from disk.
// The envelope schema rejects oci:// prefixes at artifacts entries
// for the same reason as workload:.
func resolveArtifactEntryText(a types.ArtifactEntry, entityName, artifactName string) (string, error) {
	if a.IsEmpty() {
		return "", nil
	}
	if a.Inline != "" {
		return a.Inline, nil
	}
	data, err := os.ReadFile(a.Import)
	if err != nil {
		return "", cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: a.Import}, fmt.Sprintf("Recipe %q artifact %q file import", entityName, artifactName))
	}
	return string(data), nil
}

// EvaluateFeatureRecipeLocals evaluates fr.Spec.Locals templates against the
// feature-scoped context and returns the resolved map. Same self-containment
// rules as Recipe locals: no .context.inject / .context.uses references.
// Iteration is in author order (fr.Spec.Locals is an OrderedMap).
func EvaluateFeatureRecipeLocals(
	fr *types.FeatureRecipe,
	instanceName string,
	values types.OrderedMap[any],
	infra types.OrderedMap[any],
	provides types.OrderedMap[string],
) (types.OrderedMap[string], error) {
	resolved := types.OrderedMap[string]{}
	if fr == nil || fr.Spec.Locals.Len() == 0 {
		return resolved, nil
	}

	lCtx := LocalsContext{
		Context:  ProvidesContextBlock{InstanceID: instanceName},
		Values:   values,
		Infra:    infra,
		Provides: provides,
	}

	for key, tmplText := range fr.Spec.Locals.All() {
		if err := CheckLocalsSelfContainment(tmplText, fr.Metadata.Name, key); err != nil {
			return types.OrderedMap[string]{}, err
		}
		val, err := EvalTemplate(fmt.Sprintf("fr-locals[%q]", key), tmplText, lCtx)
		if err != nil {
			return types.OrderedMap[string]{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Metadata.Name}, fmt.Sprintf("FeatureRecipe %q locals[%q]", fr.Metadata.Name, key))
		}
		resolved.Set(key, strings.TrimSpace(val))
	}
	return resolved, nil
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
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Metadata.Name}, fmt.Sprintf("Phase B (feature): FeatureRecipe %q valuesMapping", fr.Metadata.Name))
	}

	var values map[string]any
	if err := parse.TemplateOutput([]byte(output), &values); err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Metadata.Name}, fmt.Sprintf("Phase B (feature): FeatureRecipe %q valuesMapping output is not valid YAML", fr.Metadata.Name))
	}

	return values, nil
}

// isResourceGeneratingFR returns true if the FeatureRecipe declares a render
// input kind — source, valuesMapping, workload, or artifacts — i.e., it
// produces its own resources in Phase B. Inject-only FRs (only injectMapping)
// return false — they're already handled by Phase A and render nothing.
func isResourceGeneratingFR(fr *types.FeatureRecipe) bool {
	if fr == nil {
		return false
	}
	if fr.Spec.Source != nil {
		if s, ok := fr.Spec.Source.(string); ok && s != "" {
			return true
		}
		if _, ok := fr.Spec.Source.(string); !ok {
			// Structured (non-string) source — treat as present.
			return true
		}
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
