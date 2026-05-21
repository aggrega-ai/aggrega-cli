// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// detectFilenameCollisions walks state.EmittedFiles and groups by Name.
// Any name with EmittedFiles from more than one Origin produces a rule 7
// CATALOG_ERROR matching the reserved sentinel format. Within-origin
// duplicates (e.g., two artifacts: entries with the same name; engine
// emitting the same file twice) are NOT rule 7 violations — those classes
// are caught by rule 6 (load-time) or by engine-internal logic.
//
// Findings are sorted alphabetically by filename for output stability.
// Multiple cross-origin collisions in one Recipe produce one
// RecipeValidationError carrying every collision in its Detail line.
func detectFilenameCollisions(state *PhaseBState) error {
	if state == nil || len(state.EmittedFiles) == 0 {
		return nil
	}

	// Group origins per filename. Using a sorted-set deduplicates origins
	// that contributed multiple files with the same name (rare; e.g., two
	// artifacts: entries pointing to the same output filename — that case
	// is rule 6's domain and surfaces at load time, but defensive
	// deduplication here keeps the rule 7 message focused on cross-origin
	// collisions).
	originsByName := map[string]map[EmitOrigin]struct{}{}
	for _, f := range state.EmittedFiles {
		if _, ok := originsByName[f.Name]; !ok {
			originsByName[f.Name] = map[EmitOrigin]struct{}{}
		}
		originsByName[f.Name][f.Origin] = struct{}{}
	}

	type collision struct {
		filename string
		origins  []string
	}
	var collisions []collision
	for name, originSet := range originsByName {
		if len(originSet) < 2 {
			continue
		}
		// Sort origin tags alphabetically for deterministic output.
		ordered := make([]string, 0, len(originSet))
		for o := range originSet {
			ordered = append(ordered, o.String())
		}
		sort.Strings(ordered)
		collisions = append(collisions, collision{filename: name, origins: ordered})
	}

	if len(collisions) == 0 {
		return nil
	}

	// Stable order across multiple collisions: alphabetical by filename.
	sort.Slice(collisions, func(i, j int) bool {
		return collisions[i].filename < collisions[j].filename
	})

	// Build the Detail line. Each entry of the form:
	// "<filename> declared by: <origin1>, <origin2>, ...".
	var details []string
	for _, c := range collisions {
		details = append(details,
			fmt.Sprintf("%s declared by: %s", c.filename, strings.Join(c.origins, ", ")))
	}

	return &validator.RecipeValidationError{
		Path:        state.RecipePath,
		Name:        state.RecipeName,
		Version:     state.RecipeVersion,
		Rule:        "7",
		Description: "filename collision across input-kind origins at emission",
		Detail:      strings.Join(details, "; "),
		Err:         validator.ErrRecipeRule7,
	}
}
