// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import "github.com/cognitio/aggrega-cli/pkg/types"

// BuildUnifiedSchema returns `baseUsesCueSchema & userCueSchema` as concatenated
// CUE source. Downstream consumers (guard_cue_schema.go, ValidatePillar) can
// compile the joined document — CUE parses it as pairwise unification, and all
// constraints from both sides (including `@selector` attributes and allowlisted
// inject mechanism types) survive intact. An empty usesSchema means no base
// part; an empty userCUE means no user overlay.
func BuildUnifiedSchema(userCUE string, usesSchema map[string]types.UsesSchemaEntry) string {
	base := GenerateBaseUsesCueSchema(usesSchema)
	switch {
	case base == "" && userCUE == "":
		return ""
	case base == "":
		return userCUE
	case userCUE == "":
		return base
	default:
		return base + "\n\n" + userCUE
	}
}
