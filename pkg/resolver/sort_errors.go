// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import "sort"

// sortResolverErrors sorts errors by Entity then Field for deterministic output.
// The Deterministic Output Invariant requires stable ordering even when errors
// are collected from Go map iteration (which randomizes order).
func sortResolverErrors(errs []*ResolverError) {
	sort.Slice(errs, func(i, j int) bool {
		if errs[i].Entity != errs[j].Entity {
			return errs[i].Entity < errs[j].Entity
		}
		return errs[i].Field < errs[j].Field
	})
}
