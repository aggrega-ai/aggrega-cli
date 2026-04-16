// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"sort"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// ExtractProvidesKeys compiles a cueSchema and returns the sorted key names
// declared in its `provides:` pillar. Returns an empty slice (and no error)
// when the schema is empty or omits the provides pillar — callers use this
// to short-circuit inject-from validation.
//
// Raw CUE errors on compilation are surfaced as a single translated
// ValidationError so resolver guards can wrap them consistently.
func ExtractProvidesKeys(cueSource string) ([]string, []*ValidationError) {
	if cueSource == "" {
		return nil, nil
	}

	ctx := cuecontext.New()
	schemaVal := ctx.CompileString(cueSource)
	if err := schemaVal.Err(); err != nil {
		return nil, []*ValidationError{{
			FieldPath:  ".spec.cueSchema",
			Constraint: "cueSchema compilation failed",
			Suggestion: "Check the cueSchema syntax in the catalog entity",
			Err:        ErrSchemaValidation,
		}}
	}

	providesVal := schemaVal.LookupPath(cue.ParsePath("provides"))
	if !providesVal.Exists() {
		return nil, nil
	}

	iter, err := providesVal.Fields(cue.Optional(true), cue.Definitions(false))
	if err != nil {
		return nil, nil
	}

	var keys []string
	for iter.Next() {
		keys = append(keys, iter.Label())
	}
	sort.Strings(keys)
	return keys, nil
}
