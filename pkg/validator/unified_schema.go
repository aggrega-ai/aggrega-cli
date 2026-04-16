// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import "github.com/cognitio/aggrega-cli/pkg/types"

// BuildUnifiedSchema returns `baseUsesCueSchema & baseSecretsCueSchema & userCueSchema`
// as concatenated CUE source. Downstream consumers (guard_cue_schema.go,
// ValidatePillar) compile the joined document — CUE parses it as pairwise
// unification, and all constraints from every fragment (including `@selector`,
// `@inject`, `@required` attributes and allowlisted mechanism types) survive
// intact. Empty fragments are skipped.
func BuildUnifiedSchema(userCUE string, usesSchema map[string]types.UsesSchemaEntry) string {
	return BuildUnifiedSchemaFull(userCUE, usesSchema, nil)
}

// BuildUnifiedSchemaFull is the richer variant that also concatenates a
// generated secretsSchema base. Callers that don't care about secrets can
// still use BuildUnifiedSchema.
func BuildUnifiedSchemaFull(userCUE string, usesSchema map[string]types.UsesSchemaEntry, secretsSchema map[string]types.SecretSlot) string {
	parts := []string{
		GenerateBaseUsesCueSchema(usesSchema),
		GenerateBaseSecretsCueSchema(secretsSchema),
		userCUE,
	}
	var nonEmpty []string
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	if len(nonEmpty) == 0 {
		return ""
	}
	if len(nonEmpty) == 1 {
		return nonEmpty[0]
	}
	var out string
	for i, p := range nonEmpty {
		if i > 0 {
			out += "\n\n"
		}
		out += p
	}
	return out
}
