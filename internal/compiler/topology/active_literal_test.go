// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package topology

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestActiveLiteralReadsAToggleAsFlatteningDoes holds the one reading of an
// `active` toggle to each shape it can take: omitted and written true are present,
// written false (spaces around it ignored) is absent, and a toggle that is no
// boolean literal — empty, or a parameter template not yet rendered — reads as
// present and as no literal, which flattening refuses and a reader of unrendered
// drafts keeps.
//
// 🔴 PLANTED DEFECT: drop the strings.TrimSpace in ActiveLiteral
// (internal/compiler/topology/flatten.go): the " false " row reddens, reading as no
// literal.
func TestActiveLiteralReadsAToggleAsFlatteningDoes(t *testing.T) {
	t.Parallel()
	flag := func(s string) *types.ActiveFlag {
		f := types.ActiveFlag(s)
		return &f
	}
	for _, tc := range []struct {
		name                 string
		active               *types.ActiveFlag
		wantPresent, literal bool
	}{
		{name: "omitted", active: nil, wantPresent: true, literal: true},
		{name: "written true", active: flag("true"), wantPresent: true, literal: true},
		{name: "written false", active: flag("false"), wantPresent: false, literal: true},
		{name: "written false with spaces", active: flag(" false "), wantPresent: false, literal: true},
		{name: "empty", active: flag(""), wantPresent: true, literal: false},
		{name: "a template not yet rendered", active: flag("{{ .parameters.on }}"), wantPresent: true, literal: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			present, literal := ActiveLiteral(tc.active)
			if present != tc.wantPresent || literal != tc.literal {
				t.Errorf("ActiveLiteral = (present %v, literal %v); want (%v, %v)", present, literal, tc.wantPresent,
					tc.literal)
			}
		})
	}
}
