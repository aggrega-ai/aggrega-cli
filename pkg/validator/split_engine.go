// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"
	"sort"
	"strings"

	"cuelang.org/go/cue/cuecontext"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// GenerateBaseUsesCueSchema builds a CUE schema string from a component/feature usesSchema map.
// Each socket category becomes an optional struct in the uses pillar. If inject is allowlisted,
// only those mechanisms are allowed; if omitted, inject is forbidden (Black-Box pattern).
func GenerateBaseUsesCueSchema(usesSchema map[string]types.UsesSchemaEntry) string {
	if len(usesSchema) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("{\n")
	b.WriteString("\tuses?: {\n")

	// Sort keys for deterministic output
	keys := make([]string, 0, len(usesSchema))
	for k := range usesSchema {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, socketName := range keys {
		entry := usesSchema[socketName]
		b.WriteString(fmt.Sprintf("\t\t%s?: {\n", socketName))
		b.WriteString("\t\t\t[string]: {\n")
		b.WriteString("\t\t\t\ttarget: string\n")

		if len(entry.Inject) > 0 {
			b.WriteString("\t\t\t\tinject?: {\n")
			for _, mechanism := range entry.Inject {
				b.WriteString(fmt.Sprintf("\t\t\t\t\t%s?: {[string]: string}\n", mechanism))
			}
			b.WriteString("\t\t\t\t}\n")
		}

		b.WriteString("\t\t\t}\n")
		b.WriteString("\t\t}\n")
	}

	b.WriteString("\t}\n")
	b.WriteString("}\n")
	return b.String()
}

// UnifySchemas unifies a base CUE schema with a user CUE schema (base & user).
// Returns translated errors if unification fails.
func UnifySchemas(baseCUE, userCUE, entity string) []*ValidationError {
	if baseCUE == "" && userCUE == "" {
		return nil
	}

	ctx := cuecontext.New()

	var baseVal = ctx.CompileString("{}")
	if baseCUE != "" {
		baseVal = ctx.CompileString(baseCUE)
		if baseVal.Err() != nil {
			return []*ValidationError{{
				Entity:     entity,
				FieldPath:  ".spec.usesSchema",
				Constraint: "generated baseUsesCueSchema failed to compile",
				Suggestion: "This is a compiler bug — report it",
				Err:        ErrUsesSchemaUnification,
			}}
		}
	}

	var userVal = ctx.CompileString("{}")
	if userCUE != "" {
		userVal = ctx.CompileString(userCUE)
		if userVal.Err() != nil {
			return []*ValidationError{{
				Entity:     entity,
				FieldPath:  ".spec.cueSchema",
				Constraint: "user cueSchema failed to compile",
				Suggestion: "Check the cueSchema syntax",
				Err:        ErrUsesSchemaUnification,
			}}
		}
	}

	unified := baseVal.Unify(userVal)
	if unified.Err() != nil {
		return translateCUEErrors(unified.Err(), entity, ErrUsesSchemaUnification)
	}

	return nil
}
