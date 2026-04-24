// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue/cuecontext"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// GenerateBaseUsesCueSchema builds a CUE schema string from a component/feature usesSchema map.
// Each socket category becomes an optional struct in the uses pillar. If inject is allowlisted,
// only those mechanisms are allowed; if omitted, inject is forbidden (Black-Box pattern).
//
// Each socket carries two CUE attribute tags for downstream tooling (policy engines,
// inspection output): `@selector("<expr>")` with the original selector expression, and
// `@inject("env,file")` when an allowlist is present. CUE attributes are metadata —
// they don't affect validation; the selector itself is enforced by the analyzer
// (Step 4 selector matcher), not here.
func GenerateBaseUsesCueSchema(usesSchema types.OrderedMap[types.UsesSchemaEntry]) string {
	if usesSchema.Len() == 0 {
		return ""
	}

	var b strings.Builder
	// Emit as file-level `uses:` field declaration (not wrapped in extra braces)
	// so `LookupPath("uses")` resolves to the close() struct. `close()` rejects
	// socket names not declared in usesSchema.
	b.WriteString("uses: close({\n")

	for socketName, entry := range usesSchema.All() {
		// Selector attribute: always present, even if the selector string is empty —
		// the socket is still a declared topology slot. @inject attribute
		// reflects the allowlist for downstream policy tooling (Black-Box when absent).
		b.WriteString(fmt.Sprintf("\t%s?: {\n", socketName))
		b.WriteString(fmt.Sprintf("\t\t@selector(%q)\n", entry.Selector))
		if len(entry.Inject) > 0 {
			b.WriteString(fmt.Sprintf("\t\t@inject(%q)\n", strings.Join(entry.Inject, ",")))
		}
		b.WriteString("\t\t[string]: {\n")
		b.WriteString("\t\t\ttarget: string\n")

		if len(entry.Inject) > 0 {
			b.WriteString("\t\t\tinject?: {\n")
			for _, mechanism := range entry.Inject {
				b.WriteString(fmt.Sprintf("\t\t\t\t%s?: {[string]: string}\n", mechanism))
			}
			b.WriteString("\t\t\t}\n")
		}

		b.WriteString("\t\t}\n")
		b.WriteString("\t}\n")
	}

	b.WriteString("})\n")
	return b.String()
}

// GenerateBaseSecretsCueSchema builds a CUE schema string from a Component or
// Feature secretsSchema map. Each slot becomes a field in the `secrets` pillar:
//
//   - Required=true → `<name>: { ... }` (mandatory; close() rejects absence).
//   - Required=false → `<name>?: { ... }` (optional).
//   - Inject empty (Black-Box) → no `inject` field emitted, so the pillar
//     rejects any inject declared in the Aggregate (close() + absent field).
//   - Inject non-empty (Transparent) → `inject?: { <mech>?: {[string]: string} }`
//     limited to allowlisted mechanisms.
//
// @required and @inject CUE attribute tags surface slot semantics for
// downstream policy tools. Deterministic via sorted slot names.
func GenerateBaseSecretsCueSchema(secretsSchema types.OrderedMap[types.SecretSlot]) string {
	if secretsSchema.Len() == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("secrets: close({\n")

	for slotName, slot := range secretsSchema.All() {
		// Required slots omit the "?" optional marker on the slot field and
		// make `type` mandatory inside the slot — a missing slot leaves `type`
		// incomplete, so Validate(Concrete, Final) flags it. Optional slots
		// carry "?" so absence is fine.
		if slot.Required {
			b.WriteString(fmt.Sprintf("\t%s: close({\n", slotName))
		} else {
			b.WriteString(fmt.Sprintf("\t%s?: close({\n", slotName))
		}
		b.WriteString(fmt.Sprintf("\t\t@required(%q)\n", fmt.Sprintf("%t", slot.Required)))
		if slot.Required {
			b.WriteString("\t\ttype: string\n")
		} else {
			b.WriteString("\t\ttype?: string\n")
		}
		b.WriteString("\t\tvalues?: {...}\n")
		b.WriteString("\t\tuses?: {...}\n")
		if len(slot.Inject) > 0 {
			b.WriteString(fmt.Sprintf("\t\t@inject(%q)\n", strings.Join(slot.Inject, ",")))
			b.WriteString("\t\tinject?: {\n")
			for _, mechanism := range slot.Inject {
				b.WriteString(fmt.Sprintf("\t\t\t%s?: {[string]: string}\n", mechanism))
			}
			b.WriteString("\t\t}\n")
		}
		b.WriteString("\t})\n")
	}

	b.WriteString("})\n")
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
