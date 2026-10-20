// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// RecipeTemplateLibraries is the library set a Recipe's imported template fields
// resolve against: the MappingLibrary, WorkloadLibrary and ArtifactLibrary
// entities a catalog holds, each keyed by its bare library name, which is how the
// render's own import resolution reads them. A nil map holds no library, so an
// import naming one does not resolve.
type RecipeTemplateLibraries struct {
	Mappings  map[string]types.MappingLibrary
	Workloads map[string]types.WorkloadLibrary
	Artifacts map[string]types.ArtifactLibrary
}

// RecipeTemplateReads returns every field access the read census reports
// across every template a Recipe's own render evaluates, imported content
// included.
//
// # The set
//
// It is the templates the render evaluates against the node's context, and each
// is read from the home that already decides it rather than enumerated here: the
// inline fields [RecipeTemplateFields] presents; the values mapping and the
// workload a Recipe imports instead, which [RecipeImportFields] names and the
// render's own resolvers read — a file, or a library entry with every field-root
// import inside it followed; each artifacts entry a Recipe imports, from a file or
// as a library bundle whose every file is a template; and the locals, provides and
// assertions entries and the source's templates, presented by
// [recipeBlockTemplates]. So an import is read as what the render will evaluate,
// not as the line that names it.
//
// An artifacts body is read with the delimiter pair the node's provider applies
// to artifacts, which the caller supplies; every other field is read with the
// usual pair, as the render reads it. Read with the wrong pair, a body would
// either report references its author never made or not parse at all.
//
// # The reading, and which way it errs
//
// The census is [gotmpl.CollectTemplateFieldAccessesWithDelims], the walk that
// over-collects on purpose: a read inside a rebound body, the receiver of a
// chained expression and the argument a template variable is bound from are all
// reported. That is the direction a caller asking "does anything here read X"
// needs, because a read this walk missed would be a read the caller is told does
// not happen. The paths are reported as that census spells them — dot-qualified,
// a `$`-anchored read under the spelling its plain form has — and in its order
// within each template; the templates come in the order the set above lists
// them, and the result is a list rather than a set, so a path read twice appears
// twice.
//
// A template that does not parse, and an import that does not resolve, stop the
// reading with an error naming the field: what such a field would read cannot be
// known, so no list stands for the Recipe. The parse uses the function set the
// render uses, so a template parsing here parses there.
//
// It evaluates nothing and reads no value: only the text of each template, and,
// for an import, the file or library entry the render would read it from.
func RecipeTemplateReads(recipe *types.Recipe, libs RecipeTemplateLibraries, artifactLeft, artifactRight string) ([]string, error) {
	if recipe == nil {
		return nil, nil
	}
	var reads []string
	collect := func(label, text, left, right string) error {
		if text == "" {
			return nil
		}
		found, err := gotmpl.CollectTemplateFieldAccessesWithDelims(text, left, right)
		if err != nil {
			return fmt.Errorf("template %q parse: %w", label, err)
		}
		reads = append(reads, found...)
		return nil
	}

	for _, field := range RecipeTemplateFields(recipe) {
		left, right := "{{", "}}"
		if RecipeTemplateFieldReadsWithArtifactDelimiters(field.Label) {
			left, right = artifactLeft, artifactRight
		}
		if err := collect(field.Label, field.Text, left, right); err != nil {
			return nil, err
		}
	}

	name := recipe.Metadata.Name
	for _, field := range RecipeImportFields(recipe) {
		var text string
		var err error
		switch field {
		case recipeFieldValuesMapping:
			text, err = resolveValuesMappingText(recipe.Spec.ValuesMapping, name, "Recipe", libs.Mappings)
		case recipeFieldWorkload:
			text, err = resolveWorkloadText(recipe.Spec.Workload, name, libs.Workloads)
		}
		if err != nil {
			return nil, err
		}
		if err := collect(field, text, "{{", "}}"); err != nil {
			return nil, err
		}
	}

	// An inline artifacts entry is one of the fields above; only an imported one
	// is read here, in the two forms the artifacts walk resolves it from. The
	// boundary block takes the same forms and is walked by the same evaluation, so
	// its imported entries are read the same way, after the Recipe's own.
	for _, block := range []struct {
		labelPrefix string
		entries     types.OrderedMap[types.ArtifactEntry]
	}{
		{artifactsLabelPrefix, recipe.Spec.Artifacts},
		{boundaryArtifactsLabelPrefix, recipe.Spec.Boundary.Artifacts},
	} {
		for _, key := range block.entries.Keys() {
			entry, _ := block.entries.Get(key)
			if entry.Import == "" {
				continue
			}
			label := block.labelPrefix + key + "]"
			if entry.IsLibraryImport() {
				bundle, err := resolveArtifactLibraryImport(entry.Import, libs.Artifacts)
				if err != nil {
					return nil, err
				}
				for _, fname := range bundle.Files.Keys() {
					body, _ := bundle.Files.Get(fname)
					if err := collect(label+"/"+fname, body, artifactLeft, artifactRight); err != nil {
						return nil, err
					}
				}
				continue
			}
			text, err := resolveArtifactEntryText(entry, name, key)
			if err != nil {
				return nil, err
			}
			if err := collect(label, text, artifactLeft, artifactRight); err != nil {
				return nil, err
			}
		}
	}

	var blockErr error
	recipeBlockTemplates(recipe, func(label, text string) bool {
		blockErr = collect(label, text, "{{", "}}")
		return blockErr == nil
	})
	if blockErr != nil {
		return nil, blockErr
	}
	return reads, nil
}

// recipeBlockTemplates presents the template texts a Recipe writes outside the
// fields [RecipeTemplateFields] enumerates: each locals entry, each provides entry
// and each assertion, in the document's own order within each block and in that
// order between them, then every template the source carries, walked by
// mapRecipeSourceTemplates, which is the one home of which parts of a source
// carry one.
//
// It is one home because two readings walk exactly this set beside the
// enumerated fields — the unread-key reading and [RecipeTemplateReads] — and a
// block one of them walked and the other did not would be a template one reading
// counts and the other silently misses.
//
// visit returning false stops the walk, and the walk then returns false; it
// returns true when every text was visited. The source's structured form is a Go
// map, so the order among its fields is Go's own.
func recipeBlockTemplates(recipe *types.Recipe, visit func(label, text string) bool) bool {
	blocks := []struct {
		name    string
		entries types.OrderedMap[string]
	}{
		{"locals", recipe.Spec.Locals},
		{"provides", recipe.Spec.Provides},
		{"assertions", recipe.Spec.Assertions},
	}
	for _, block := range blocks {
		for key, text := range block.entries.All() {
			if !visit(block.name+"["+key+"]", text) {
				return false
			}
		}
	}
	visited := true
	_, _ = mapRecipeSourceTemplates(recipe.Spec.Source, func(field, text string) (string, error) {
		if visited && !visit(sourceFieldLabel(field), text) {
			visited = false
		}
		return text, nil
	})
	return visited
}
