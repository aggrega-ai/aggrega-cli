// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// normalized wraps a raw test Aggregate as a [types.NormalizedAggregate] for the
// consumers (guards, walker, name/annotation passes) that take the normalized
// type.
// [types.Normalize] is non-mutating, so the caller's agg is untouched and
// remains safe to inspect after the call.
func normalized(t *testing.T, agg *types.Aggregate) types.NormalizedAggregate {
	t.Helper()
	return types.Normalize(agg)
}
