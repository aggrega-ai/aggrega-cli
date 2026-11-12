// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validateops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestValidateRecipeEntity_ImplementsPin covers the single-entity Recipe
// validation's lookup of the Component a Recipe implements, through the real
// catalog loader. An implements whose version pin is malformed is reported as that
// malformation rather than as a Component the catalog does not hold; a well-formed
// pin the catalog does not hold keeps the not-found refusal; a pin the catalog
// satisfies passes the lookup, which is the control — whatever the render tiers
// after it report, it is not the lookup's refusal.
func TestValidateRecipeEntity_ImplementsPin(t *testing.T) {
	t.Parallel()
	catDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(catDir, "api-server.yaml"), []byte(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/api-server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values: {}
`), 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}

	for _, tc := range []struct {
		name          string
		implements    string
		wantMalformed bool
		wantNotFound  bool
	}{
		{"a pin the catalog satisfies", "core/api-server:1.0.0", false, false},
		{"a caret pin whose range does not parse", "core/api-server:^garbage", true, false},
		{"an exact pin that is not a semantic version", "core/api-server:1.x", true, false},
		{"a well-formed pin the catalog does not hold", "core/api-server:9.9.9", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			recipePath := filepath.Join(t.TempDir(), "recipe.yaml")
			data := []byte("apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: probe-recipe\n" +
				"  version: 1.0.0\nspec:\n  capability: probe\n  implements: " + tc.implements + "\n" +
				"  artifacts:\n    probe.txt: \"probe\"\n")
			if err := os.WriteFile(recipePath, data, 0o644); err != nil {
				t.Fatalf("write recipe: %v", err)
			}
			var res EntityResult
			err := validateRecipeEntity(context.Background(), &res, t.TempDir(), t.TempDir(), []string{catDir}, nil, recipePath, data, false, nil)

			malformed := errors.Is(err, types.ErrMalformedVersionPin())
			notFound := err != nil && strings.Contains(err.Error(), "not found in catalog")
			if malformed != tc.wantMalformed {
				t.Errorf("err = %v; malformed-pin condition present = %v, want %v", err, malformed, tc.wantMalformed)
			}
			if notFound != tc.wantNotFound {
				t.Errorf("err = %v; not-found refusal present = %v, want %v", err, notFound, tc.wantNotFound)
			}
			if tc.wantMalformed && !strings.Contains(err.Error(), `reference "`+tc.implements+`"`) {
				t.Errorf("err = %v; the refusal must name the implements reference", err)
			}
		})
	}
}
